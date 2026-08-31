package crudsql

import "github.com/frostgrove/vv/crud/sqlfault"

func (this Executor) conflict(err error) error { return sqlfault.Wrap(this.faults, err) }
