package main

import (
	"math/big"
	"testing"

	"bridgesafe-extension/internal/codec"
	"bridgesafe-extension/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
)

// TestSealRoundTripsThroughGoEthereumEcies proves a sealed payload is readable
// by exactly the scheme the TEE node uses to decrypt.
//
// This is the check that matters for this service: its only reason to exist is
// ECIES compatibility, and getting the curve, KDF or cipher wrong would produce
// ciphertext that fails inside the enclave with no useful error.
func TestSealRoundTripsThroughGoEthereumEcies(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generating enclave key: %v", err)
	}
	pub := crypto.FromECDSAPub(&key.PublicKey) // 65-byte uncompressed

	plaintext, err := codec.EncodePaymentInstruction(types.PaymentInstruction{
		ChainId:     big.NewInt(114),
		Controller:  common.HexToAddress("0x00000000000000000000000000000000000c0dE1"),
		TreasuryId:  big.NewInt(1),
		RequestId:   big.NewInt(42),
		Destination: "rPT1Sjq2YGrBMTttX4GZHjKu9dyfzbpAYe",
		AmountDrops: big.NewInt(25_000_000),
		Reference:   "contractor invoice",
	})
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	ciphertext, err := sealToEnclave(pub, plaintext)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}
	if len(ciphertext) <= len(plaintext) {
		t.Error("ciphertext is not longer than plaintext; ECIES adds an ephemeral key and MAC")
	}

	// Decrypt the way the TEE node does.
	recovered, err := ecies.ImportECDSA(key).Decrypt(ciphertext, nil, nil)
	if err != nil {
		t.Fatalf("the enclave could not decrypt this payload: %v", err)
	}

	pi, err := codec.DecodePaymentInstruction(recovered)
	if err != nil {
		t.Fatalf("decoding decrypted payload: %v", err)
	}
	if pi.Destination != "rPT1Sjq2YGrBMTttX4GZHjKu9dyfzbpAYe" {
		t.Errorf("destination: got %s", pi.Destination)
	}
	if pi.AmountDrops.Cmp(big.NewInt(25_000_000)) != 0 {
		t.Errorf("amount: got %s", pi.AmountDrops)
	}
	if pi.RequestId.Cmp(big.NewInt(42)) != 0 {
		t.Errorf("request id: got %s", pi.RequestId)
	}
}

func TestSealAcceptsCompressedAndUncompressedKeys(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	uncompressed := crypto.FromECDSAPub(&key.PublicKey)
	compressed := crypto.CompressPubkey(&key.PublicKey)
	raw := uncompressed[1:] // some proxies report X||Y without the 0x04 tag

	for name, form := range map[string][]byte{
		"uncompressed": uncompressed,
		"compressed":   compressed,
		"raw X||Y":     raw,
	} {
		ct, err := sealToEnclave(form, []byte("payment terms"))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		got, err := ecies.ImportECDSA(key).Decrypt(ct, nil, nil)
		if err != nil {
			t.Errorf("%s: enclave could not decrypt: %v", name, err)
			continue
		}
		if string(got) != "payment terms" {
			t.Errorf("%s: round trip corrupted the payload", name)
		}
	}
}

func TestSealRejectsGarbageKey(t *testing.T) {
	if _, err := sealToEnclave([]byte{1, 2, 3}, []byte("x")); err == nil {
		t.Error("sealed to a key that is not a valid curve point")
	}
}
