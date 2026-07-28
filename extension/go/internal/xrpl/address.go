package xrpl

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // XRPL account ids are defined in terms of RIPEMD-160.
)

// XRPL's base58 alphabet. Note it is *not* the Bitcoin ordering — decoding an
// XRPL address with a Bitcoin alphabet silently produces the wrong bytes.
const xrplAlphabet = "rpshnaf39wBUDNEGHJKLM4PQRST7VWXYZ2bcdeCg65jkm8oFqi1tuvAxyz"

// Version byte prefixing a classic account address, which is what makes an
// encoded XRPL address start with 'r'.
const accountIDVersion byte = 0x00

var (
	ErrInvalidAddress  = errors.New("xrpl: address is not a valid classic r-address")
	ErrBadChecksum     = errors.New("xrpl: address checksum mismatch")
	errAlphabetInvalid = errors.New("xrpl: address contains a character outside the XRPL base58 alphabet")
)

var base58Index = func() map[byte]int {
	m := make(map[byte]int, len(xrplAlphabet))
	for i := 0; i < len(xrplAlphabet); i++ {
		m[xrplAlphabet[i]] = i
	}
	return m
}()

// AccountIDFromPublicKey derives the 20-byte account id from a 33-byte
// compressed secp256k1 public key: RIPEMD160(SHA256(pubkey)).
func AccountIDFromPublicKey(pubKey []byte) ([]byte, error) {
	if len(pubKey) != 33 {
		return nil, ErrBadPublicKey
	}
	sha := sha256.Sum256(pubKey)
	r := ripemd160.New()
	if _, err := r.Write(sha[:]); err != nil {
		return nil, fmt.Errorf("xrpl: ripemd160: %w", err)
	}
	return r.Sum(nil), nil
}

// EncodeClassicAddress renders a 20-byte account id as an r-address.
func EncodeClassicAddress(accountID []byte) (string, error) {
	if len(accountID) != 20 {
		return "", ErrBadAccountID
	}
	payload := make([]byte, 0, 25)
	payload = append(payload, accountIDVersion)
	payload = append(payload, accountID...)
	payload = append(payload, checksum(payload)...)
	return base58Encode(payload), nil
}

// DecodeClassicAddress recovers the 20-byte account id from an r-address and
// verifies its checksum.
func DecodeClassicAddress(address string) ([]byte, error) {
	raw, err := base58Decode(address)
	if err != nil {
		return nil, err
	}
	if len(raw) != 25 || raw[0] != accountIDVersion {
		return nil, ErrInvalidAddress
	}
	if !bytes.Equal(checksum(raw[:21]), raw[21:]) {
		return nil, ErrBadChecksum
	}
	return raw[1:21], nil
}

// ValidateClassicAddress reports whether an address is a well-formed r-address.
//
// The enclave calls this on the destination from a decrypted instruction before
// signing anything: an unchecked destination is how funds get sent into a
// black hole that no FDC proof will ever match.
func ValidateClassicAddress(address string) error {
	_, err := DecodeClassicAddress(address)
	return err
}

func checksum(b []byte) []byte {
	first := sha256.Sum256(b)
	second := sha256.Sum256(first[:])
	return second[:4]
}

func base58Encode(input []byte) string {
	num := new(big.Int).SetBytes(input)
	radix := big.NewInt(58)
	mod := new(big.Int)

	var out []byte
	for num.Sign() > 0 {
		num.DivMod(num, radix, mod)
		out = append(out, xrplAlphabet[mod.Int64()])
	}

	// Leading zero bytes are significant and encode as the alphabet's first rune.
	for _, b := range input {
		if b != 0 {
			break
		}
		out = append(out, xrplAlphabet[0])
	}

	// The loop above built the string backwards.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func base58Decode(input string) ([]byte, error) {
	if input == "" {
		return nil, ErrInvalidAddress
	}
	num := big.NewInt(0)
	radix := big.NewInt(58)
	for i := 0; i < len(input); i++ {
		idx, ok := base58Index[input[i]]
		if !ok {
			return nil, errAlphabetInvalid
		}
		num.Mul(num, radix)
		num.Add(num, big.NewInt(int64(idx)))
	}

	decoded := num.Bytes()

	// Restore the leading zero bytes that base58 represents as its first rune.
	var leading int
	for leading < len(input) && input[leading] == xrplAlphabet[0] {
		leading++
	}
	out := make([]byte, leading+len(decoded))
	copy(out[leading:], decoded)
	return out, nil
}
