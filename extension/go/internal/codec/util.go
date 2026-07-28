package codec

import (
	"fmt"
	"reflect"

	"golang.org/x/crypto/sha3"
)

// Keccak256Hash is the hash Solidity's `keccak256` computes.
func Keccak256Hash(data []byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Keccak256String hashes a UTF-8 string the way `keccak256(bytes(s))` does.
//
// Used for the XRPL address hashes the contract compares against FDC's
// `sourceAddressHash` / `receivingAddressHash`. FDC hashes the address in its
// original case, without normalisation, so this must not lowercase.
func Keccak256String(s string) [32]byte {
	return Keccak256Hash([]byte(s))
}

// copyStruct moves the anonymous struct go-ethereum's ABI decoder produces into
// one of our named types.
//
// The decoder returns a generated struct whose fields match by position and name
// but whose type is not ours, so a direct assertion fails. Copying field by field
// keeps the named types in pkg/types readable instead of forcing every call site
// to handle an anonymous shape.
func copyStruct(src any, dst any) error {
	sv := reflect.ValueOf(src)
	dv := reflect.ValueOf(dst)
	if dv.Kind() != reflect.Ptr || dv.IsNil() {
		return fmt.Errorf("destination must be a non-nil pointer, got %T", dst)
	}
	dv = dv.Elem()
	if sv.Kind() != reflect.Struct {
		return fmt.Errorf("source is %s, want struct", sv.Kind())
	}
	if sv.NumField() != dv.NumField() {
		return fmt.Errorf("field count mismatch: source has %d, destination has %d", sv.NumField(), dv.NumField())
	}
	for i := 0; i < sv.NumField(); i++ {
		sf := sv.Field(i)
		df := dv.Field(i)
		if !df.CanSet() {
			return fmt.Errorf("field %d of destination is not settable", i)
		}
		if !sf.Type().AssignableTo(df.Type()) {
			return fmt.Errorf("field %d: %s is not assignable to %s", i, sf.Type(), df.Type())
		}
		df.Set(sf)
	}
	return nil
}
