package bridgesafe

import (
	"fmt"
	"reflect"
)

// CopyStruct moves the anonymous struct go-ethereum's ABI decoder produces into
// a named type.
//
// The decoder returns a generated struct whose fields match by position but
// whose type is not ours, so a direct assertion fails. Copying field by field
// keeps call sites readable and fails loudly if the shapes ever diverge.
func CopyStruct(src any, dst any) error {
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
		sf, df := sv.Field(i), dv.Field(i)
		if !df.CanSet() {
			return fmt.Errorf("field %d is not settable", i)
		}
		if !sf.Type().AssignableTo(df.Type()) {
			return fmt.Errorf("field %d: %s is not assignable to %s", i, sf.Type(), df.Type())
		}
		df.Set(sf)
	}
	return nil
}
