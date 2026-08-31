package crudhttp

import (
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/port"
)

func Sanitize[M any](meta *crud.Meta, m *M, allowClientID bool) error {
	return port.Sanitize(meta, m, allowClientID)
}

func ClearGenerated[M any](meta *crud.Meta, m *M) error {
	return port.ClearGenerated(meta, m)
}

func ClearWriteProtected[M any](meta *crud.Meta, m *M) error {
	return port.ClearWriteProtected(meta, m)
}
