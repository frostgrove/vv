package port

import (
	"reflect"
	"unsafe"

	"github.com/frostgrove/vv/crud"
)

func Sanitize[M any](meta *crud.Meta, m *M, allowClientID bool) error {
	if meta.PK.Auto && !allowClientID {
		if err := meta.SetID(m, reflect.Zero(meta.PK.Type).Interface()); err != nil {
			return err
		}
	}
	return ClearWriteProtected(meta, m)
}

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

func ClearWriteProtected[M any](meta *crud.Meta, m *M) error {
	base := reflect.ValueOf(m).UnsafePointer()
	for _, f := range meta.Fields {
		if f.Generated || f.Version || f.ServerOwned || f == meta.Tombstone {
			reflect.NewAt(f.Type, unsafe.Add(base, f.Offset)).Elem().SetZero()
		}
	}
	return nil
}
