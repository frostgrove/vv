package crudhttp

import (
	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/port"
)

// Sanitize clears what a client is not allowed to choose on create: a
// database-generated key, and every column declared `generated`.
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
