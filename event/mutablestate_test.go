package event

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// What a package-level value *is* decides this, not how it is spelled: `make`,
// `new`, a composite literal and a bare declaration all produce a map, and a
// check that reads two of those four spellings passes the other two. So the
// kind is taken from the type, which also settles the shape no syntax reaches —
// a name handed to a function that writes through its parameter. Such a name is
// reported where it is declared, because a package-level map, slice, channel or
// pointer is state every request shares whether or not this package is the one
// that writes it.
//
// A named type that declares a pointer-receiver method is the third shape, and
// the one no reading of the declaration alone reveals: `var held registry` is an
// ordinary struct until `func (this *registry) add(…)` exists, and then every
// caller of `add` writes the package's own state. It is reported where it is
// declared, naming the method.
//
// The twenty-four sentinels and the reflected type tokens are interfaces, and
// the `*Aggregate` and `*Fact` idiom lives in an application rather than here,
// so nothing in the tree has to be exempted by name. An interface is judged by
// what it can hold: one that names no method holds anything, and one that names
// methods is read through what was written into it, so `var held any =
// map[string]int{}` is a finding and an `error` sentinel is not. A value of an
// ordinary kind that is assigned to, incremented or deleted from is the last
// arm.
func TestNoPackageLevelStateIsEverMutated(t *testing.T) {
	t.Run("nothing the extension declares at package level is ever written", func(t *testing.T) {
		directories := extensionDirectories(t)
		if len(directories) < 3 {
			t.Fatalf("%d directories of the extension were walked, so at least one package was not read", len(directories))
		}
		declared := 0
		for _, directory := range directories {
			names, complaints := packageLevelState(typedSources(t, directory))
			if len(names) == 0 {
				t.Fatalf("%s declares nothing at package level, so this walked the wrong files rather than reading a package that declares only sentinels", directory)
			}
			declared += len(names)
			for _, complaint := range complaints {
				t.Error(complaint)
			}
		}
		if declared < 24 {
			t.Fatalf("%d package-level names were read out of the extension and the refusal vocabulary alone declares 24, so this walked the wrong files", declared)
		}
	})

	t.Run("a value every copy writes through is reported however it is spelled, and a sentinel is not", func(t *testing.T) {
		_, complaints := packageLevelState(typedSources(t, writeFixture(t, mutableFixture)))

		reported := strings.Join(complaints, "\n")
		for _, expected := range []string{
			"registry", "guard", "counter", "seen", "pool", "tally",
			"kept", "anything", "rendered",
		} {
			if !strings.Contains(reported, expected) {
				t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing:\n%s", expected, reported)
			}
		}
		for _, permitted := range []string{"sentinel", "typeToken", "frozen"} {
			if strings.Contains(reported, permitted) {
				t.Fatalf("the fixture's %s was reported, and a sentinel, a reflected type token and a value nothing writes through are the idiom this repository is written in:\n%s", permitted, reported)
			}
		}
	})
}

func packageLevelState(typed typedPackage) (map[types.Object]bool, []string) {
	declared := map[types.Object]bool{}
	var complaints []string

	scope := typed.pkg.Scope()
	for _, name := range scope.Names() {
		held, isVariable := scope.Lookup(name).(*types.Var)
		if !isVariable {
			continue
		}
		declared[held] = true
		for _, kind := range []string{heldKind(held.Type()), writtenThroughItsOwnMethod(held.Type())} {
			if kind == "" {
				continue
			}
			complaints = append(complaints, typed.fileset.Position(held.Pos()).String()+": "+name+" "+kind+
				", and a package-level value every request shares is state no composition root chose")
		}
	}
	complaints = append(complaints, mutationsOf(typed, declared)...)
	return declared, append(complaints, interfacesHolding(typed, declared)...)
}

func writtenThroughItsOwnMethod(held types.Type) string {
	named, isNamed := held.(*types.Named)
	if !isNamed {
		return ""
	}
	for index := range named.NumMethods() {
		method := named.Method(index)
		receiver := method.Signature().Recv()
		if receiver == nil {
			continue
		}
		if _, isPointer := receiver.Type().(*types.Pointer); isPointer {
			return "declares " + method.Name() + ", which takes a pointer receiver and writes through it"
		}
	}
	return ""
}

// An interface that names methods can only be judged by what was put into it,
// and that is written at the declaration rather than carried by the type. A
// sentinel built by `errors.New` holds an `error` and nothing more can be said
// about it; a `map` assigned to an `any` is the map it always was.
func interfacesHolding(typed typedPackage, declared map[types.Object]bool) []string {
	var complaints []string
	for _, file := range typed.files {
		for _, declaration := range file.Decls {
			general, isGeneral := declaration.(*ast.GenDecl)
			if !isGeneral || general.Tok != token.VAR {
				continue
			}
			for _, spec := range general.Specs {
				value, isValue := spec.(*ast.ValueSpec)
				if !isValue || len(value.Names) != len(value.Values) {
					continue
				}
				for index, name := range value.Names {
					held := typed.info.Defs[name]
					if held == nil || !declared[held] {
						continue
					}
					if _, isInterface := held.Type().Underlying().(*types.Interface); !isInterface {
						continue
					}
					if kind := heldKind(typed.info.TypeOf(value.Values[index])); kind != "" {
						complaints = append(complaints, typed.at(name)+": "+name.Name+" is declared as an interface and holds a value that "+kind+
							", and a package-level value every request shares is state no composition root chose")
					}
				}
			}
		}
	}
	return complaints
}

func heldKind(held types.Type) string {
	return writtenThrough(held, map[types.Type]bool{})
}

func writtenThrough(held types.Type, seen map[types.Type]bool) string {
	if held == nil || seen[held] {
		return ""
	}
	seen[held] = true
	if named, isNamed := held.(*types.Named); isNamed {
		if declared := named.Obj().Pkg(); declared != nil && (declared.Path() == "sync" || declared.Path() == "sync/atomic") {
			return "holds a synchronisation type, which exists to be written"
		}
	}
	switch shape := held.Underlying().(type) {
	case *types.Interface:
		if shape.NumMethods() == 0 {
			return "is an interface that names no method, so it holds anything and anything it holds may be written through"
		}
	case *types.Map:
		return "is a map, which every copy of it writes through"
	case *types.Slice:
		return "is a slice, which every copy of it writes through"
	case *types.Chan:
		return "is a channel, which every copy of it writes through"
	case *types.Pointer:
		return "is a pointer, and every holder of it writes through the value behind it"
	case *types.Array:
		return writtenThrough(shape.Elem(), seen)
	case *types.Struct:
		for index := range shape.NumFields() {
			if kind := writtenThrough(shape.Field(index).Type(), seen); kind != "" {
				return kind
			}
		}
	}
	return ""
}

func mutationsOf(typed typedPackage, declared map[types.Object]bool) []string {
	var complaints []string
	report := func(node ast.Node, name, what string) {
		complaints = append(complaints, typed.at(node)+": "+name+" "+what)
	}
	for _, file := range typed.files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch found := node.(type) {
			case *ast.AssignStmt:
				for _, target := range found.Lhs {
					if written := rootObject(typed.info, target); declared[written] {
						report(found, written.Name(), "is assigned to outside its own declaration")
					}
				}
			case *ast.IncDecStmt:
				if written := rootObject(typed.info, found.X); declared[written] {
					report(found, written.Name(), "is incremented or decremented")
				}
			case *ast.CallExpr:
				called, isName := found.Fun.(*ast.Ident)
				if !isName || called.Name != "delete" || len(found.Args) == 0 {
					return true
				}
				if written := rootObject(typed.info, found.Args[0]); declared[written] {
					report(found, written.Name(), "is deleted from")
				}
			}
			return true
		})
	}
	return complaints
}

func rootObject(info *types.Info, target ast.Expr) types.Object {
	for {
		switch held := target.(type) {
		case *ast.Ident:
			return info.Uses[held]
		case *ast.IndexExpr:
			target = held.X
		case *ast.SelectorExpr:
			target = held.X
		case *ast.StarExpr:
			target = held.X
		case *ast.ParenExpr:
			target = held.X
		default:
			return nil
		}
	}
}
