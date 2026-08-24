package crudhttp

import (
	"reflect"
	"unsafe"

	"github.com/shardit-io/qq/crud"
)

// Sanitize clears what a client is not allowed to choose on create: a
// database-generated key, and every column declared `generated`. Without it a
// client picks its own id and forges a server-side timestamp by putting them in
// the create body.
func Sanitize[M any](meta *crud.Meta, m *M, allowClientID bool) error {
	if meta.PK.Auto && !allowClientID {
		if err := meta.SetID(m, reflect.Zero(meta.PK.Type).Interface()); err != nil {
			return err
		}
	}
	return ClearGenerated(meta, m)
}

// ClearGenerated zeroes every `generated` column by offset.
func ClearGenerated[M any](meta *crud.Meta, m *M) error {
	if !meta.HasGen {
		return nil
	}
	base := reflect.ValueOf(m).UnsafePointer()
	for _, f := range meta.Fields {
		if f.Generated {
			reflect.NewAt(f.Type, unsafe.Add(base, f.Offset)).Elem().SetZero()
		}
	}
	return nil
}
