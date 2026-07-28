// Package xrpl implements a deliberately minimal XRP Ledger binary codec.
//
// It can serialize exactly one thing: a native-XRP `Payment` carrying a single
// memo. That restriction is the security property, not a limitation. The enclave
// holds a treasury key, so the dangerous failure mode is not "the codec is
// incomplete" — it is "the codec is general enough to be talked into signing
// something else". There is no field table to extend, no transaction-type
// parameter, and no path from a decrypted instruction to an arbitrary object:
// `Payment.Serialize` writes a fixed sequence of fields and nothing else.
//
// Consequently a `TrustSet`, an `AccountSet` that could rekey the account, an
// `EscrowFinish`, or a multi-currency payment simply cannot be expressed by this
// package, whatever an attacker puts in the payload.
//
// Reference: https://xrpl.org/serialization.html
package xrpl

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Field ids, precomputed from XRPL's (type code, field code) pairs.
//
// The encoding rule: with both codes below 16 the id is one byte,
// `(type << 4) | field`. When the field code reaches 16 the low nibble is left
// zero and the field code follows in a second byte. Every field below is written
// out explicitly rather than derived at run time, so the wire format is readable
// here and cannot shift under a table edit.
var (
	fieldTransactionType   = []byte{0x12}       // UInt16,    type 1,  field 2
	fieldFlags             = []byte{0x22}       // UInt32,    type 2,  field 2
	fieldSequence          = []byte{0x24}       // UInt32,    type 2,  field 4
	fieldDestinationTag    = []byte{0x2E}       // UInt32,    type 2,  field 14
	fieldLastLedgerSeq     = []byte{0x20, 0x1B} // UInt32,    type 2,  field 27
	fieldAmount            = []byte{0x61}       // Amount,    type 6,  field 1
	fieldFee               = []byte{0x68}       // Amount,    type 6,  field 8
	fieldSigningPubKey     = []byte{0x73}       // Blob,      type 7,  field 3
	fieldTxnSignature      = []byte{0x74}       // Blob,      type 7,  field 4
	fieldMemoType          = []byte{0x7C}       // Blob,      type 7,  field 12
	fieldMemoData          = []byte{0x7D}       // Blob,      type 7,  field 13
	fieldAccount           = []byte{0x81}       // AccountID, type 8,  field 1
	fieldDestination       = []byte{0x83}       // AccountID, type 8,  field 3
	fieldMemos             = []byte{0xF9}       // STArray,   type 15, field 9
	fieldMemo              = []byte{0xEA}       // STObject,  type 14, field 10
	fieldObjectEndMarker   = []byte{0xE1}       // STObject,  type 14, field 1
	fieldArrayEndMarker    = []byte{0xF1}       // STArray,   type 15, field 1
)

// Transaction type code for Payment.
const txTypePayment uint16 = 0

// Hash prefixes. XRPL domain-separates every hash it computes; using the wrong
// prefix yields a signature that verifies over the wrong bytes.
var (
	// "STX\0" — the payload a single signer signs.
	prefixTransactionSig = []byte{0x53, 0x54, 0x58, 0x00}
	// "TXN\0" — the payload whose hash is the transaction id.
	prefixTransactionID = []byte{0x54, 0x58, 0x4E, 0x00}
)

// MaxMemoLength caps the memo the enclave will embed. BridgeSafe memos are a
// fixed 36 bytes ("BSF1" + 32-byte reference); the cap exists so a malformed
// instruction cannot inflate the transaction.
const MaxMemoLength = 64

// MaxDrops is the total XRP supply in drops — the largest legal native amount.
const MaxDrops uint64 = 100_000_000_000_000_000

var (
	ErrAmountTooLarge  = errors.New("xrpl: amount exceeds total XRP supply")
	ErrAmountZero      = errors.New("xrpl: amount must be positive")
	ErrMemoTooLong     = fmt.Errorf("xrpl: memo exceeds %d bytes", MaxMemoLength)
	ErrBadAccountID    = errors.New("xrpl: account id must be 20 bytes")
	ErrBadPublicKey    = errors.New("xrpl: signing public key must be 33 bytes")
)

// writeVL writes XRPL's variable-length prefix followed by the payload.
//
// Lengths up to 192 use one byte. BridgeSafe never emits anything longer than a
// 33-byte key or a 64-byte memo, so the multi-byte forms are rejected rather
// than implemented — an unreachable branch in signing code is a liability.
func writeVL(buf []byte, payload []byte) ([]byte, error) {
	n := len(payload)
	if n > 192 {
		return nil, fmt.Errorf("xrpl: variable-length field of %d bytes exceeds the 192-byte forms this codec supports", n)
	}
	buf = append(buf, byte(n))
	return append(buf, payload...), nil
}

func writeUint16(buf []byte, field []byte, v uint16) []byte {
	buf = append(buf, field...)
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	return append(buf, b[:]...)
}

func writeUint32(buf []byte, field []byte, v uint32) []byte {
	buf = append(buf, field...)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return append(buf, b[:]...)
}

// writeXRPAmount writes a native XRP amount.
//
// Native amounts are 8 bytes with bit 63 clear (marking "this is XRP, not an
// issued currency") and bit 62 set (marking a positive value). Because bit 63
// stays clear by construction, this function cannot emit an issued-currency
// amount, so the enclave cannot be steered into moving a token it was never
// meant to touch.
func writeXRPAmount(buf []byte, field []byte, drops uint64) ([]byte, error) {
	if drops == 0 {
		return nil, ErrAmountZero
	}
	if drops > MaxDrops {
		return nil, ErrAmountTooLarge
	}
	buf = append(buf, field...)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], drops|0x4000000000000000)
	return append(buf, b[:]...), nil
}

func writeAccountID(buf []byte, field []byte, id []byte) ([]byte, error) {
	if len(id) != 20 {
		return nil, ErrBadAccountID
	}
	buf = append(buf, field...)
	return writeVL(buf, id)
}

func writeBlob(buf []byte, field []byte, v []byte) ([]byte, error) {
	buf = append(buf, field...)
	return writeVL(buf, v)
}
