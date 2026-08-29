package remote

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/frostgrove/vv/crud"
)

// checkPatchable refuses an update DTO that cannot be put on a wire.
//
// A utils.Opt has three states and encoding/json has two, and the bridge between
// them is the `omitzero` tag: an undefined Opt marshals to null, and only
// omitzero keeps the key out of the document altogether. Without it every field
// the caller did not set arrives as an explicit null, and the far side writes
// SQL NULL to all of them — a PATCH of one column that empties the row, with a
// 200 on it and nothing in the response to say so ([[UC-003]]).
//
// So it is a start-up failure, at the moment sqlrepo.Define's failures happen and
// for the same reason: no request can recover from it, and by the time one has
// been made the damage is in the database. cmd/vv already writes the tag on
// every generated DTO, so this only ever fires on one written by hand.
//
// The model is not checked, and the difference is real rather than an
// oversight. Save writes a whole row either way — an undefined Opt and an
// explicit null both reach the database as NULL on an insert, and a replace is
// a replace — so there is nothing there for the tag to protect.
func checkPatchable(u reflect.Type) error {
	if u.Kind() == reflect.Pointer {
		u = u.Elem()
	}
	if u.Kind() != reflect.Struct {
		return nil
	}
	for i := range u.NumField() {
		f := u.Field(i)
		if !f.IsExported() || crud.OptElem(f.Type) == nil {
			continue
		}
		name, options, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" && options == "" {
			continue // never sent, so never wrong
		}
		if !hasOpt(options, "omitzero") {
			return fmt.Errorf(
				"remote: %s.%s is a crud.Opt with no `omitzero` in its json tag, "+
					"so a patch that leaves it undefined would arrive as an explicit null "+
					"and empty the column; regenerate the DTO with cmd/vv, or add the tag",
				u.Name(), f.Name)
		}
	}
	return nil
}

func hasOpt(tag, want string) bool {
	for opt := range strings.SplitSeq(tag, ",") {
		if opt == want {
			return true
		}
	}
	return false
}
