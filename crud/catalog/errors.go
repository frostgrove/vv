package catalog

import "errors"

var (
	ErrUncomparableHandle = errors.New("catalog: the datasource handle cannot be compared, so a catalog keyed on it could never be found again")

	ErrUnknownDialect = errors.New("catalog: no introspection for this dialect")

	ErrIntrospection = errors.New("catalog: the schema could not be read")
)
