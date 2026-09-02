package wire

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/frostgrove/vv/crud"
)

func CoversPatch[U, P any](except ...string) error {
	update, err := shapeOf[U]()
	if err != nil {
		return err
	}
	patch, err := shapeOf[P]()
	if err != nil {
		return err
	}
	problems := compare(update, patch, except)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the patch body %s does not agree with the update DTO %s: %s — regenerate it, or declare the exclusion",
		typeName[P](), typeName[U](), strings.Join(problems, "; "))
}

func MustCoverPatch[U, P any](except ...string) {
	if err := CoversPatch[U, P](except...); err != nil {
		panic(err)
	}
}

func CoversCreate[M, In any](except ...string) error {
	schema, err := crud.SchemaOf[M]()
	if err != nil {
		return err
	}
	insertable := shape{}
	for _, column := range schema.Insert {
		insertable[column.Name] = column.Type
	}
	body, err := shapeOf[In]()
	if err != nil {
		return err
	}
	problems := compare(insertable, body, except)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the create body %s does not agree with %s: %s — regenerate it, or declare the exclusion",
		typeName[In](), schema.Name, strings.Join(problems, "; "))
}

func MustCoverCreate[M, In any](except ...string) {
	if err := CoversCreate[M, In](except...); err != nil {
		panic(err)
	}
}

func CoversResponse[M, R any](except ...string) error {
	schema, err := crud.SchemaOf[M]()
	if err != nil {
		return err
	}
	model := shape{}
	for _, column := range schema.Fields {
		model[column.Name] = column.Type
	}
	response, err := shapeOf[R]()
	if err != nil {
		return err
	}
	problems := compare(model, response, except)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("the response body %s does not agree with %s: %s — regenerate it, or declare the exclusion",
		typeName[R](), schema.Name, strings.Join(problems, "; "))
}

func MustCoverResponse[M, R any](except ...string) {
	if err := CoversResponse[M, R](except...); err != nil {
		panic(err)
	}
}

type shape map[string]reflect.Type

func shapeOf[T any]() (shape, error) {
	var zero T
	structure := reflect.TypeOf(&zero).Elem()
	if structure.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s is not a struct, so it carries no wire fields", structure.String())
	}
	out := shape{}
	for _, item := range reflect.VisibleFields(structure) {
		if item.Anonymous || !item.IsExported() {
			continue
		}
		out[item.Name] = item.Type
	}
	return out, nil
}

func compare(source, public shape, except []string) []string {
	skip := make(map[string]bool, len(except))
	for _, name := range except {
		skip[name] = true
	}

	var missing, unknown, mismatched, stale []string
	for name := range source {
		if _, carried := public[name]; !carried && !skip[name] {
			missing = append(missing, name)
		}
	}
	for name, public := range public {
		owned, exists := source[name]
		if !exists {
			unknown = append(unknown, name)
			continue
		}
		if owned != public {
			mismatched = append(mismatched, fmt.Sprintf("%s carries %s where %s is expected", name, public, owned))
		}
	}
	for name := range skip {
		if _, exists := source[name]; !exists {
			stale = append(stale, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	sort.Strings(mismatched)
	sort.Strings(stale)

	var problems []string
	if len(missing) > 0 {
		problems = append(problems, "no field for "+strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		problems = append(problems, "a field for "+strings.Join(unknown, ", ")+", which the model does not carry")
	}
	if len(mismatched) > 0 {
		problems = append(problems, strings.Join(mismatched, ", "))
	}
	if len(stale) > 0 {
		problems = append(problems, "an exclusion for "+strings.Join(stale, ", ")+", which the model does not carry")
	}
	return problems
}

func typeName[T any]() string {
	var zero T
	return reflect.TypeOf(&zero).Elem().String()
}
