package event

import (
	"go/token"
	"go/types"
	"slices"
	"testing"
)

// Which types render an identity, asked of the type and never of the spelling.
// Four are named — the two identities and the two numbers — and everything else
// is reached from them: a struct carrying a `Key`, a map keyed by one, a slice
// of bytes, a pointer to any of those.
var identitiesARefusalMayNotRender = []string{"Key", "Cursor", "Version", "Position"}

// What the four names are matched against is a spelling, and the test below is
// what ties that spelling to the vocabulary: renaming `Cursor` in the kernel
// would otherwise leave this file and its fixture agreeing with each other while
// the cursor stopped being covered. The two exemptions are the identity-shaped
// types that are not identities, and each says which.
var identitiesNamedForSomethingElse = map[string]string{
	"Outcome": "a store's classification of its own failure, which a refusal names because it is a class",
	"Support": "a capability's tri-state answer, which no request and no history carries",
}

func identityNamed(held types.Type) string {
	if pointer, isPointer := held.Underlying().(*types.Pointer); isPointer {
		held = pointer.Elem()
	}
	named, isNamed := held.(*types.Named)
	if !isNamed {
		return ""
	}
	if name := named.Obj().Name(); slices.Contains(identitiesARefusalMayNotRender, name) {
		return name
	}
	return ""
}

func TestEveryIdentityTheChecksNameIsOneTheVocabularyDeclaresAndEveryOneItDeclaresIsNamed(t *testing.T) {
	scope := typedSources(t, ".").pkg.Scope()
	declaredAs := func(name string) (*types.Basic, bool) {
		declared, isType := scope.Lookup(name).(*types.TypeName)
		if !isType || declared.IsAlias() {
			return nil, false
		}
		basic, isBasic := declared.Type().Underlying().(*types.Basic)
		return basic, isBasic
	}

	for _, name := range identitiesARefusalMayNotRender {
		if _, declared := declaredAs(name); !declared {
			t.Errorf("a refusal is refused %s and the vocabulary declares no such type, so the check names something this package renamed or dropped and covers nothing in its place", name)
		}
	}
	for name, because := range identitiesNamedForSomethingElse {
		if _, declared := declaredAs(name); !declared {
			t.Errorf("%s is written down here as %s and the vocabulary no longer declares it, so an exemption outlived what it exempted", name, because)
		}
	}
	for _, name := range scope.Names() {
		basic, declared := declaredAs(name)
		if !declared || !scope.Lookup(name).Exported() || basic.Info()&(types.IsString|types.IsUnsigned) == 0 {
			continue
		}
		if slices.Contains(identitiesARefusalMayNotRender, name) || identitiesNamedForSomethingElse[name] != "" {
			continue
		}
		t.Errorf("the vocabulary declares %s as text or as a count and nothing here says whether a refusal may render it, so a fifth identity travels in a message and no check sees it", name)
	}
}

// The value a message renders, judged whole: what it declares about its own
// rendering is not consulted, because a message that renders a value renders
// every part of it.
func carriedIdentity(held types.Type) string {
	seen := map[types.Type]bool{}
	for held != nil && !seen[held] {
		seen[held] = true
		if name := identityNamed(held); name != "" {
			return name
		}
		pointer, isPointer := held.Underlying().(*types.Pointer)
		if !isPointer {
			return identityInside(held, seen)
		}
		held = pointer.Elem()
	}
	return ""
}

func identityInside(held types.Type, seen map[types.Type]bool) string {
	switch shape := held.Underlying().(type) {
	case *types.Slice:
		if basic, isBasic := shape.Elem().Underlying().(*types.Basic); isBasic && basic.Kind() == types.Byte {
			return "payload"
		}
		return identityOfAPart(shape.Elem(), seen)
	case *types.Array:
		return identityOfAPart(shape.Elem(), seen)
	case *types.Map:
		if name := identityOfAPart(shape.Key(), seen); name != "" {
			return name
		}
		return identityOfAPart(shape.Elem(), seen)
	case *types.Chan:
		return identityOfAPart(shape.Elem(), seen)
	case *types.Struct:
		for index := range shape.NumFields() {
			if name := identityOfAPart(shape.Field(index).Type(), seen); name != "" {
				return name
			}
		}
	}
	return ""
}

// A part of a larger value. A type that declares its own rendering decides what
// its parts become, so the walk stops at one — which is how a `Stream` carrying
// a `Key` renders a family and the key never travels.
func identityOfAPart(held types.Type, seen map[types.Type]bool) string {
	if held == nil || seen[held] {
		return ""
	}
	seen[held] = true
	if pointer, isPointer := held.Underlying().(*types.Pointer); isPointer {
		return identityOfAPart(pointer.Elem(), seen)
	}
	if name := identityNamed(held); name != "" {
		return name
	}
	if rendersItself(held) {
		return ""
	}
	return identityInside(held, seen)
}

func rendersItself(held types.Type) bool {
	return types.Implements(held, stringerRendering()) || types.Implements(held, errorRendering())
}

func stringerRendering() *types.Interface {
	result := types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Typ[types.String]))
	method := types.NewFunc(token.NoPos, nil, "String", types.NewSignatureType(nil, nil, nil, nil, result, false))
	return types.NewInterfaceType([]*types.Func{method}, nil).Complete()
}

func errorRendering() *types.Interface {
	return types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
}
