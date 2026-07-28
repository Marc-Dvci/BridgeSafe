package bridgesafe

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Chain is a small Coston2 client: read logs, call views, send transactions.
type Chain struct {
	client  *ethclient.Client
	chainID *big.Int
	key     *ecdsa.PrivateKey
	from    common.Address
}

// Dial connects to an EVM RPC and prepares a signer.
//
// The key may be nil for read-only use — the FDC worker's proof fetching and the
// UI backend both need chain reads without any ability to send.
func Dial(ctx context.Context, rpcURL, privateKeyHex string) (*Chain, error) {
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", rpcURL, err)
	}
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading chain id: %w", err)
	}

	c := &Chain{client: client, chainID: chainID}
	if privateKeyHex != "" {
		key, err := crypto.HexToECDSA(trim0x(privateKeyHex))
		if err != nil {
			return nil, fmt.Errorf("parsing private key: %w", err)
		}
		c.key = key
		c.from = crypto.PubkeyToAddress(key.PublicKey)
	}
	return c, nil
}

// ChainID returns the connected chain's id.
func (c *Chain) ChainID() *big.Int { return c.chainID }

// From returns the sender address, or the zero address in read-only mode.
func (c *Chain) From() common.Address { return c.from }

// Client exposes the underlying ethclient for callers needing more.
func (c *Chain) Client() *ethclient.Client { return c.client }

// Close releases the connection.
func (c *Chain) Close() { c.client.Close() }

// Call performs a read-only contract call and unpacks the result.
func (c *Chain) Call(ctx context.Context, parsed abi.ABI, to common.Address, method string, args ...any) ([]any, error) {
	data, err := parsed.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("packing %s: %w", method, err)
	}
	out, err := c.client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", method, err)
	}
	return parsed.Unpack(method, out)
}

// Send submits a transaction and waits for its receipt.
//
// It returns an error when the transaction reverts, rather than a receipt the
// caller might forget to inspect. A relayer that treats a reverted broadcast
// report as success would silently desynchronise from the chain.
func (c *Chain) Send(ctx context.Context, parsed abi.ABI, to common.Address, value *big.Int, method string, args ...any) (*types.Receipt, error) {
	if c.key == nil {
		return nil, fmt.Errorf("chain client is read-only: no signing key configured")
	}

	data, err := parsed.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("packing %s: %w", method, err)
	}

	nonce, err := c.client.PendingNonceAt(ctx, c.from)
	if err != nil {
		return nil, fmt.Errorf("reading nonce: %w", err)
	}
	gasPrice, err := c.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("suggesting gas price: %w", err)
	}
	if value == nil {
		value = big.NewInt(0)
	}

	gas, err := c.client.EstimateGas(ctx, ethereum.CallMsg{
		From: c.from, To: &to, Value: value, Data: data,
	})
	if err != nil {
		// Estimation failing almost always means the call would revert. Surface it
		// now with the method name attached, instead of sending a doomed
		// transaction and paying for it.
		return nil, fmt.Errorf("estimating gas for %s (the call would revert): %w", method, err)
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &to,
		Value:    value,
		Gas:      gas + gas/5, // 20% headroom
		GasPrice: gasPrice,
		Data:     data,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(c.chainID), c.key)
	if err != nil {
		return nil, fmt.Errorf("signing %s: %w", method, err)
	}
	if err := c.client.SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("sending %s: %w", method, err)
	}

	receipt, err := c.waitForReceipt(ctx, signed.Hash())
	if err != nil {
		return nil, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return receipt, fmt.Errorf("%s reverted (tx %s)", method, signed.Hash().Hex())
	}
	return receipt, nil
}

func (c *Chain) waitForReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		receipt, err := c.client.TransactionReceipt(ctx, hash)
		if err == nil {
			return receipt, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for receipt of %s: %w", hash.Hex(), ctx.Err())
		case <-ticker.C:
		}
	}
}

// FilterLogs fetches historical logs for one event.
func (c *Chain) FilterLogs(ctx context.Context, address common.Address, topic common.Hash, from, to *big.Int) ([]types.Log, error) {
	return c.client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: from,
		ToBlock:   to,
		Addresses: []common.Address{address},
		Topics:    [][]common.Hash{{topic}},
	})
}

// BlockNumber returns the current head.
func (c *Chain) BlockNumber(ctx context.Context) (uint64, error) {
	return c.client.BlockNumber(ctx)
}

// BlockTimestamp returns a block's timestamp, needed to derive an FDC voting round.
func (c *Chain) BlockTimestamp(ctx context.Context, number uint64) (uint64, error) {
	header, err := c.client.HeaderByNumber(ctx, new(big.Int).SetUint64(number))
	if err != nil {
		return 0, fmt.Errorf("reading block %d: %w", number, err)
	}
	return header.Time, nil
}

func trim0x(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}
