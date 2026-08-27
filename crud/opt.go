package crud

import "github.com/frostgrove/vv/utils"

// Opt is kept as a compatibility alias. New code should import
// github.com/frostgrove/vv/utils and use utils.Opt: the three-state value is a
// general model and transport primitive, not a CRUD-specific one.
//
// Deprecated: use utils.Opt.
type Opt[T any] = utils.Opt[T]

// Optional is the shared public optional-value protocol.
//
// Deprecated: use utils.Optional.
type Optional = utils.Optional

// Set returns an Opt holding v.
// Deprecated: use utils.Set.
func Set[T any](v T) Opt[T] { return utils.Set(v) }

// Null returns an explicitly null Opt.
// Deprecated: use utils.Null.
func Null[T any]() Opt[T] { return utils.Null[T]() }

// Undefined returns an undefined Opt.
// Deprecated: use utils.Undefined.
func Undefined[T any]() Opt[T] { return utils.Undefined[T]() }

// FromPtr converts nil to null and a non-nil pointer to set.
// Deprecated: use utils.FromPtr.
func FromPtr[T any](p *T) Opt[T] { return utils.FromPtr(p) }
