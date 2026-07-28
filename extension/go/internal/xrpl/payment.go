package xrpl

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"strings"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// Payment is the only transaction shape this package can produce.
//
// Every field is explicit and typed. There is no map, no "extra fields" escape
// hatch, and no way to change the transaction type — so a decrypted instruction
// can influence the amount, the destination and the memo, and nothing else.
type Payment struct {
	// Account is the sending treasury's 20-byte account id.
	Account []byte
	// Destination is the payee's 20-byte account id.
	Destination []byte
	// AmountDrops is the native XRP amount, in drops.
	AmountDrops uint64
	// FeeDrops is the transaction fee, in drops.
	FeeDrops uint64
	// Sequence is the sending account's next sequence number.
	Sequence uint32
	// LastLedgerSequence bounds how long the transaction can be included. This is
	// what makes a BridgeSafe request's expiry enforceable on XRPL rather than
	// merely asserted on Flare.
	LastLedgerSequence uint32
	// DestinationTag is written only when HasDestinationTag is set.
	DestinationTag    uint32
	HasDestinationTag bool
	// Memo is the BridgeSafe reference bound to the originating request.
	Memo []byte
	// SigningPubKey is the 33-byte compressed secp256k1 public key.
	SigningPubKey []byte
}

// Validate checks every field the codec depends on.
func (p *Payment) Validate() error {
	if len(p.Account) != 20 {
		return fmt.Errorf("account: %w", ErrBadAccountID)
	}
	if len(p.Destination) != 20 {
		return fmt.Errorf("destination: %w", ErrBadAccountID)
	}
	if p.AmountDrops == 0 {
		return ErrAmountZero
	}
	if p.AmountDrops > MaxDrops {
		return ErrAmountTooLarge
	}
	if p.FeeDrops == 0 || p.FeeDrops > MaxDrops {
		return fmt.Errorf("xrpl: fee of %d drops is out of range", p.FeeDrops)
	}
	if len(p.Memo) > MaxMemoLength {
		return ErrMemoTooLong
	}
	if len(p.SigningPubKey) != 33 {
		return ErrBadPublicKey
	}
	if p.LastLedgerSequence == 0 {
		return fmt.Errorf("xrpl: LastLedgerSequence must be set so the payment cannot be included indefinitely")
	}
	return nil
}

// serialize writes the canonical binary form.
//
// Fields must appear in ascending (type code, field code) order — the ledger
// rejects any other ordering, and a signature computed over a mis-ordered blob
// verifies against nothing. The sequence below is that order.
func (p *Payment) serialize(signature []byte) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	buf := make([]byte, 0, 256)
	var err error

	buf = writeUint16(buf, fieldTransactionType, txTypePayment)
	buf = writeUint32(buf, fieldFlags, 0)
	buf = writeUint32(buf, fieldSequence, p.Sequence)
	if p.HasDestinationTag {
		buf = writeUint32(buf, fieldDestinationTag, p.DestinationTag)
	}
	buf = writeUint32(buf, fieldLastLedgerSeq, p.LastLedgerSequence)

	if buf, err = writeXRPAmount(buf, fieldAmount, p.AmountDrops); err != nil {
		return nil, err
	}
	if buf, err = writeXRPAmount(buf, fieldFee, p.FeeDrops); err != nil {
		return nil, err
	}
	if buf, err = writeBlob(buf, fieldSigningPubKey, p.SigningPubKey); err != nil {
		return nil, err
	}
	// Absent while computing the signing payload, present in the final blob.
	if signature != nil {
		if buf, err = writeBlob(buf, fieldTxnSignature, signature); err != nil {
			return nil, err
		}
	}
	if buf, err = writeAccountID(buf, fieldAccount, p.Account); err != nil {
		return nil, err
	}
	if buf, err = writeAccountID(buf, fieldDestination, p.Destination); err != nil {
		return nil, err
	}

	if len(p.Memo) > 0 {
		buf = append(buf, fieldMemos...)
		buf = append(buf, fieldMemo...)
		if buf, err = writeBlob(buf, fieldMemoType, []byte(memoTypeBridgeSafe)); err != nil {
			return nil, err
		}
		if buf, err = writeBlob(buf, fieldMemoData, p.Memo); err != nil {
			return nil, err
		}
		buf = append(buf, fieldObjectEndMarker...)
		buf = append(buf, fieldArrayEndMarker...)
	}

	return buf, nil
}

// memoTypeBridgeSafe labels the memo so the payment is self-describing in an
// explorer. FDC surfaces only MemoData, so this value is presentational.
const memoTypeBridgeSafe = "BridgeSafe"

// SigningPayload returns the bytes a single signer signs: the transaction
// without its signature, under the "STX\0" prefix.
func (p *Payment) SigningPayload() ([]byte, error) {
	body, err := p.serialize(nil)
	if err != nil {
		return nil, err
	}
	return append(append([]byte{}, prefixTransactionSig...), body...), nil
}

// Signed carries everything the relayer and the contract need after signing.
type Signed struct {
	// TxBlob is the hex-encoded signed transaction, ready for XRPL `submit`.
	TxBlob string
	// TxID is the transaction id the ledger will assign, known before submission
	// because it is the hash of the signed blob.
	TxID string
	// Signature is the DER-encoded ECDSA signature.
	Signature []byte
}

// Sign produces the signed transaction blob and its resulting transaction id.
//
// XRPL secp256k1 signatures are DER-encoded with a low-S value, which is a
// different encoding from the 65-byte `[r||s||v]` form used for the enclave's
// Flare-side `ActionResult` signature. Conflating the two silently produces a
// transaction the ledger rejects, so the two signing paths are kept apart:
// this function is the only place XRPL signing happens.
func (p *Payment) Sign(key *secp256k1.PrivateKey) (*Signed, error) {
	if key == nil {
		return nil, fmt.Errorf("xrpl: no signing key")
	}
	if got := key.PubKey().SerializeCompressed(); len(p.SigningPubKey) == 0 {
		p.SigningPubKey = got
	} else if hex.EncodeToString(got) != hex.EncodeToString(p.SigningPubKey) {
		return nil, fmt.Errorf("xrpl: SigningPubKey does not belong to the signing key")
	}

	payload, err := p.SigningPayload()
	if err != nil {
		return nil, err
	}

	// ecdsa.Sign already enforces the low-S rule XRPL requires.
	sig := ecdsa.Sign(key, sha512Half(payload))
	der := sig.Serialize()

	blob, err := p.serialize(der)
	if err != nil {
		return nil, err
	}

	idPayload := append(append([]byte{}, prefixTransactionID...), blob...)
	txID := sha512Half(idPayload)

	return &Signed{
		TxBlob:    strings.ToUpper(hex.EncodeToString(blob)),
		TxID:      strings.ToUpper(hex.EncodeToString(txID)),
		Signature: der,
	}, nil
}

// sha512Half is XRPL's hash: the first 32 bytes of SHA-512.
func sha512Half(b []byte) []byte {
	sum := sha512.Sum512(b)
	return sum[:32]
}
