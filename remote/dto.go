package remote

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/frostgrove/vv/crud"
)

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
			continue
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
