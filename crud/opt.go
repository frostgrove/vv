package crud

import "github.com/frostgrove/vv/utils"

type Opt[T any] = utils.Opt[T]

type Optional = utils.Optional

func Set[T any](v T) Opt[T] { return utils.Set(v) }

func Null[T any]() Opt[T] { return utils.Null[T]() }

func Undefined[T any]() Opt[T] { return utils.Undefined[T]() }

func FromPtr[T any](p *T) Opt[T] { return utils.FromPtr(p) }
