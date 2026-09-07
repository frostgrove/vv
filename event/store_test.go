package event

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"
)

func seamTypes() map[reflect.Type]bool {
	return map[reflect.Type]bool{
		reflect.TypeOf((*context.Context)(nil)).Elem(): true,
		reflect.TypeOf((*error)(nil)).Elem():           true,
		reflect.TypeOf(Stream{}):                       true,
		reflect.TypeOf(Version(0)):                     true,
		reflect.TypeOf(Cursor("")):                     true,
		reflect.TypeOf(AppendRequest{}):                true,
		reflect.TypeOf([]Envelope(nil)):                true,
		reflect.TypeOf(Authority{}):                    true,
		reflect.TypeOf(Backing{}):                      true,
		reflect.TypeOf(Capabilities{}):                 true,
		reflect.TypeOf(Limits{}):                       true,
	}
}

func forbiddenVerbs() []string {
	return []string{
		"Update", "Delete", "Remove", "Patch", "Rewrite", "Overwrite", "Truncate", "Purge",
		"Query", "Filter", "Search", "Sort", "Where", "Find", "Count",
		"Begin", "Commit", "Rollback", "Savepoint",
	}
}

func seamComplaints(seam reflect.Type) []string {
	allowed := seamTypes()
	var complaints []string
	for index := range seam.NumMethod() {
		method := seam.Method(index)
		for _, verb := range forbiddenVerbs() {
			if strings.Contains(method.Name, verb) {
				complaints = append(complaints, method.Name+" names "+verb+
					", which either rewrites a recorded fact or queries history as a collection")
			}
		}
		for argument := range method.Type.NumIn() {
			if !allowed[method.Type.In(argument)] {
				complaints = append(complaints, method.Name+" takes a "+method.Type.In(argument).String()+
					", which is not one of the stream, version, cursor, byte and capability values the seam is written in")
			}
		}
		for result := range method.Type.NumOut() {
			answered := method.Type.Out(result)
			if !allowed[answered] {
				complaints = append(complaints, method.Name+" answers a "+answered.String()+
					", which is not one of the values the seam is written in")
			}
			if answered.Kind() == reflect.Interface && answered != reflect.TypeOf((*error)(nil)).Elem() {
				complaints = append(complaints, method.Name+" answers the interface "+answered.String()+
					", so a caller could ask a store for a capability instead of the store answering itself")
			}
		}
	}
	return complaints
}

type mutatingLog interface {
	DeleteStream(context.Context, Stream) error
}

type typedLog interface {
	Append(context.Context, Stream, func(any) any) error
}

func exportedInterfaces(t *testing.T) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("event/ could not be listed, so its exported interfaces cannot be inventoried: %v", err)
	}
	found := map[string][]string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		if err != nil {
			t.Fatalf("event/%s could not be parsed: %v", entry.Name(), err)
		}
		ast.Inspect(source, func(node ast.Node) bool {
			declared, isType := node.(*ast.TypeSpec)
			if !isType || !declared.Name.IsExported() {
				return true
			}
			shape, isInterface := declared.Type.(*ast.InterfaceType)
			if !isInterface {
				return true
			}
			names := []string{}
			for _, member := range shape.Methods.List {
				for _, name := range member.Names {
					names = append(names, name.Name)
				}
			}
			found[declared.Name.Name] = names
			return true
		})
	}
	return found
}

type declaredValue struct {
	name   string
	kind   reflect.Type
	fields map[string]string
}

func declaredValues() []declaredValue {
	return []declaredValue{
		{"Stream", reflect.TypeOf(Stream{}), map[string]string{
			"Family": "string", "Key": "event.Key"}},
		{"Capabilities", reflect.TypeOf(Capabilities{}), map[string]string{
			"Transactions": "event.Support", "Persistence": "event.Support",
			"MonotoneVisibility": "event.Support", "SharedBacking": "event.Support"}},
		{"Limits", reflect.TypeOf(Limits{}), map[string]string{
			"MaxPayload": "int", "MaxBatch": "int", "MaxKey": "int", "StreamPage": "int", "MaxRead": "int"}},
		{"Record", reflect.TypeOf(Record{}), map[string]string{
			"Type": "string", "Revision": "int", "Payload": "[]uint8"}},
		{"AppendRequest", reflect.TypeOf(AppendRequest{}), map[string]string{
			"Stream": "event.Stream", "Expected": "event.Version", "Records": "[]event.Record"}},
		{"Envelope", reflect.TypeOf(Envelope{}), map[string]string{
			"Stream": "event.Stream", "Version": "event.Version", "Position": "event.Position",
			"Type": "string", "Revision": "int", "Payload": "[]uint8", "RecordedAt": "time.Time"}},
	}
}

func TestTheSeamsValuesAreDefinedTypesWithTheDeclaredFields(t *testing.T) {
	t.Run("every value the seam is written in carries the fields the contract names and no others", func(t *testing.T) {
		for _, declared := range declaredValues() {
			carried := map[string]bool{}
			for index := range declared.kind.NumField() {
				field := declared.kind.Field(index)
				want, named := declared.fields[field.Name]
				if !named {
					t.Fatalf("%s carries %s, which the contract does not name, so a store is written against a field the kernel never promised it",
						declared.name, field.Name)
				}
				if got := field.Type.String(); got != want {
					t.Fatalf("%s.%s is a %s where the contract names a %s, so every store that converts it to its own column converts the wrong thing",
						declared.name, field.Name, got, want)
				}
				carried[field.Name] = true
			}
			for name := range declared.fields {
				if !carried[name] {
					t.Fatalf("%s no longer carries %s, so what a store was told to write is not what it is handed", declared.name, name)
				}
			}
		}
	})

	t.Run("the four identity types are types of their own and count upward", func(t *testing.T) {
		for _, minted := range []struct {
			name string
			kind reflect.Type
			want reflect.Kind
		}{
			{"Key", reflect.TypeOf(Key("")), reflect.String},
			{"Cursor", reflect.TypeOf(Cursor("")), reflect.String},
			{"Version", reflect.TypeOf(Version(0)), reflect.Uint64},
			{"Position", reflect.TypeOf(Position(0)), reflect.Uint64},
		} {
			if minted.kind.Name() != minted.name {
				t.Fatalf("%s is another name for %s rather than a type of its own, so a cursor is accepted where a key was meant and the compiler says nothing",
					minted.name, minted.kind.Name())
			}
			if minted.kind.Kind() != minted.want {
				t.Fatalf("%s is a %s where the contract names a %s, and a version or a position that can go negative is a column a store writes signed",
					minted.name, minted.kind.Kind(), minted.want)
			}
		}
	})
}

func TestTheStoreSeamHasEightMethodsAndNoneMutatesOrQueries(t *testing.T) {
	store := reflect.TypeOf((*Store)(nil)).Elem()
	log := reflect.TypeOf((*Log)(nil)).Elem()

	t.Run("the seam is eight required methods and no more", func(t *testing.T) {
		expected := map[string]bool{
			"Capabilities": true, "Limits": true, "Backing": true, "ReadAll": true,
			"Transaction": true, "ReadStream": true, "Append": true, "Close": true,
		}
		if store.NumMethod() != len(expected) {
			t.Fatalf("a store answers %d methods and the contract names %d, and a method that is not required is one a decorator can be walked past",
				store.NumMethod(), len(expected))
		}
		for index := range store.NumMethod() {
			if !expected[store.Method(index).Name] {
				t.Fatalf("a store answers %s, which the contract does not name", store.Method(index).Name)
			}
			delete(expected, store.Method(index).Name)
		}
		for missing := range expected {
			t.Fatalf("a store no longer answers %s", missing)
		}
		if !store.Implements(log) && !reflect.PointerTo(store).Implements(log) {
			for index := range log.NumMethod() {
				if _, carried := store.MethodByName(log.Method(index).Name); !carried {
					t.Fatalf("a store does not answer %s, which the read-only half of the seam requires", log.Method(index).Name)
				}
			}
		}
	})

	t.Run("no method rewrites a fact, queries history or controls a transaction", func(t *testing.T) {
		for _, seam := range []struct {
			name string
			kind reflect.Type
		}{{"Store", store}, {"Log", log}} {
			for _, complaint := range seamComplaints(seam.kind) {
				t.Fatalf("%s: %s", seam.name, complaint)
			}
		}
		for _, probe := range []struct {
			name string
			kind reflect.Type
		}{
			{"an interface with a delete", reflect.TypeOf((*mutatingLog)(nil)).Elem()},
			{"an interface carrying an application value", reflect.TypeOf((*typedLog)(nil)).Elem()},
		} {
			if len(seamComplaints(probe.kind)) == 0 {
				t.Fatalf("%s passed the inventory, so the inventory proves nothing about Store and Log", probe.name)
			}
		}
	})

	t.Run("no exported interface in the package names a mutation or a query", func(t *testing.T) {
		declared := exportedInterfaces(t)
		for _, required := range []string{"Store", "Log"} {
			if _, seen := declared[required]; !seen {
				t.Fatalf("the source walk did not find %s, so it is reading the wrong files", required)
			}
		}
		for name, methods := range declared {
			for _, method := range methods {
				for _, verb := range forbiddenVerbs() {
					if strings.Contains(method, verb) {
						t.Fatalf("the exported interface %s declares %s, and event history is not a collection anything queries or rewrites", name, method)
					}
				}
			}
		}
	})
}
