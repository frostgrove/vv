package port

import (
	"reflect"
	"unsafe"

	"github.com/frostgrove/vv/crud"
)

// Sanitize clears what a client is not allowed to choose on create: a
// database-generated key and every column owned by the database or a dedicated
// repository lifecycle operation. Without it a client can pick its own id or
// hand forged generated/version/tombstone state to hooks and access-control
// policy even when the eventual SQL statement omits that state.
func Sanitize[M any](meta *crud.Meta, m *M, allowClientID bool) error {
	if meta.PK.Auto && !allowClientID {
		if err := meta.SetID(m, reflect.Zero(meta.PK.Type).Interface()); err != nil {
			return err
		}
	}
	return ClearWriteProtected(meta, m)
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

// ClearWriteProtected zeroes every field a generic create/replace body does not
// own. Generated columns are database-owned; version, serverowned columns and the
// blueprint-local tombstone used by explicit sqlrepo.SoftDelete declarations
// are repository-owned. The latter comparison is intentional: an external
// model cannot always carry vv tags, but its explicit blueprint must have the
// same wire boundary as a tagged model.
func ClearWriteProtected[M any](meta *crud.Meta, m *M) error {
	base := reflect.ValueOf(m).UnsafePointer()
	for _, f := range meta.Fields {
		if f.Generated || f.Version || f.ServerOwned || f == meta.Tombstone {
			reflect.NewAt(f.Type, unsafe.Add(base, f.Offset)).Elem().SetZero()
		}
	}
	return nil
}
