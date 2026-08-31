package crudhttp

import (
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/port"
)

// Sanitize clears what a client is not allowed to choose on create: a
// database-generated key and every generated/version/server-owned lifecycle
// field.
//
// The rule moved to port with the service that applies it; this is the
// compatibility hop, kept because an application that writes its own create
// route calls it ([[D-045]]).
func Sanitize[M any](meta *crud.Meta, m *M, allowClientID bool) error {
	return port.Sanitize(meta, m, allowClientID)
}

// ClearGenerated zeroes every `generated` column by offset. The compatibility
// hop over port.ClearGenerated.
func ClearGenerated[M any](meta *crud.Meta, m *M) error {
	return port.ClearGenerated(meta, m)
}

// ClearWriteProtected clears generated, server-owned and lifecycle-owned
// fields. It is the compatibility hop over port.ClearWriteProtected.
func ClearWriteProtected[M any](meta *crud.Meta, m *M) error {
	return port.ClearWriteProtected(meta, m)
}
