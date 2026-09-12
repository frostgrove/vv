package event

import (
	"context"
	"errors"
	"go/ast"
	"go/types"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
)

// A refusal names the rule that was broken and the classification it belongs
// to. It never names the data: a key is a customer's identifier, a payload is
// the fact itself, and a version, a position and a cursor are the three numbers
// an operator's log would let anybody replay a history from. The table test
// beside this one reads what a sentinel renders; this reads what the source
// asks a message to render, which is where the next one is written.
//
// What is forbidden is decided from the argument's **type**, never from how the
// argument is spelled: `stream.Key.String()`, `string(stream.Key)` and a `%v`
// on an envelope all render a key and none of them is called one. Four types
// are named — Key, Cursor, Version, Position — and every other refusal falls
// out of them: a value whose type reaches one of the four, or reaches a byte
// slice, renders it too. A type that declares its own String or Error decides
// its own rendering and is trusted, which is what lets `Stream` through — it
// carries a key and renders only the family, behind the kernel's text rule.
//
// A message is derived rather than listed: any call that returns an error and
// takes a `string` is one, so `errors.New`, `fmt.Errorf` and any refusal helper
// this package grows are read without being named, while an append that carries
// a key and a decode that carries a payload are not. `string` means the type
// and not the underlying kind, because a `Key` is an identity spelled as text
// and passing one to a gate is not wording a message about it. A string built
// into a local one statement earlier is followed back to what was written into
// it, so `detail := string(key)` and a `fmt.Sprintf` over an identity are read
// where they are wrapped; a string reaching a message through a struct field, a
// package-level value or another function's return is not.
func TestNoRefusalRendersAnIdentityAPayloadAKeyOrACursor(t *testing.T) {
	t.Run("no message a linked package builds renders a key, a cursor, a payload, a version or a position", func(t *testing.T) {
		var read messages
		for _, directory := range linkedDirectories(t) {
			counted, complaints := renderedValues(typedSources(t, directory))
			read.add(counted)
			for _, complaint := range complaints {
				t.Error(complaint)
			}
		}
		if read.total < 60 {
			t.Fatalf("%d messages were read out of the packages a program links, so this walked the wrong files", read.total)
		}
		if read.wrapping == 0 || read.bare == 0 {
			t.Fatalf("%d messages wrap a class and %d are built from text alone, and both kinds are written here — a walk that reads one kind proves nothing about the other", read.wrapping, read.bare)
		}
		if read.delegated == 0 {
			t.Fatal("no message renders a value that carries an identity through its own String, so trusting that method permits nothing and the control this check rests on is not in the tree")
		}
	})

	t.Run("a message that renders one is reported however it is spelled, and a declared identifier is not", func(t *testing.T) {
		_, complaints := renderedValues(typedSources(t, writeFixture(t, renderingFixture)))
		reported := strings.Join(complaints, "\n")

		for _, escape := range []string{
			"renderedByItsOwnMethod", "renderedByConversion", "renderedWholesale",
			"renderedAsBytes", "renderedThroughAHelper", "renderedAsAField",
			"renderedThroughAnIndexedVerb", "renderedThroughAFlaggedIndexedVerb",
			"renderedThroughAStarredWidth", "renderedThroughALocalConversion",
			"renderedThroughALocalSprintf", "renderedThroughALocalConcatenation",
			"renderedThroughALocalPayload",
		} {
			if !strings.Contains(reported, escape) {
				t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing:\n%s", escape, reported)
			}
		}
		for _, permitted := range []string{
			"permittedFamily", "permittedCount", "permittedTypeName", "permittedBound",
			"permittedLocalCount", "permittedLocalAppendedToItself",
		} {
			if strings.Contains(reported, permitted) {
				t.Fatalf("the fixture's %s was reported, and a family, a count, a wire type name and a published bound are what a refusal is allowed to say:\n%s", permitted, reported)
			}
		}
	})
}

// The sibling above reads what the source asks a message to render; this drives
// the paths a bounded read and a digest added and reads what came out. Each row
// names what it handed in, because what a refusal may not carry is the caller's
// own data and not a digit: a bound that says "a prefix begins at version 1"
// names the rule, and "96 bytes against a bound of 64" names two published
// numbers, while a 5, a 9, a key and a payload are the values that would let an
// operator's log replay a history.
func TestNoRefusalOfABoundedReadNamesAVersion(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	untouched := accountID{tenant: "acme", number: "A-99"}
	stream := accountsAt("acme", "A-17")
	ctx := context.Background()
	secret := "a-payload-fragment-nobody-may-see"
	identity := []string{"A-17", "acme/A-17", "acme"}

	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)
	plantCredits(t, repo, declared, acme, 5)
	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the token the digest rows below are taken over was refused with %v", err)
	}
	_, elsewhere, err := repo.Load(ctx, untouched)
	if err != nil {
		t.Fatalf("the token the crossed-stream row needs was refused with %v", err)
	}

	narrow := newRecordingStore(t)
	narrow.limits.MaxKey = 8
	narrow.limits.MaxPayload = 64
	cramped, crampedFacts := bindAccounts(t, narrow)

	disordered := newRecordingStore(t)
	outOfOrder, outOfOrderFacts := bindAccounts(t, disordered)
	plantCredits(t, outOfOrder, outOfOrderFacts, acme, 2)
	disordered.page = func(page []Envelope) []Envelope {
		if len(page) != 2 {
			return page
		}
		return []Envelope{page[1], page[0]}
	}

	unreadable := newRecordingStore(t)
	unknown, _ := bindAccounts(t, unreadable)
	unreadable.history(stream, Record{Type: "accounts.retired", Revision: 1, Payload: []byte(`{"Reason":"` + secret + `"}`)})

	stateAt := func(repo *Repo[account, accountID], id accountID, version Version) error {
		_, err := repo.StateAt(ctx, id, version)
		return err
	}
	digest := func(at At[account], changes ...Change[account]) error {
		_, err := repo.Digest(at, changes...)
		return err
	}

	for _, one := range []struct {
		what     string
		refused  error
		want     error
		unspoken []string
	}{
		{"a bound of version zero", stateAt(repo, acme, 0), ErrVersion, append([]string{"0"}, identity...)},
		{"a bound past the end of the stream", stateAt(repo, acme, 9), ErrVersion, append([]string{"9", "5"}, identity...)},
		{"a bound on a stream nothing was ever appended to", stateAt(repo, untouched, 1), ErrVersion, append([]string{"A-99", "acme/A-99", "acme"}, identity...)},
		{"a bound on an identity that renders no legal stream key",
			stateAt(cramped, accountID{tenant: "acme", number: "A-17-and-far-too-long"}, 3), ErrKey,
			[]string{"A-17-and-far-too-long", "acme", "3"}},
		{"a bound over a store that answers a page out of order", stateAt(outOfOrder, acme, 2), ErrBackend, append([]string{"2"}, identity...)},
		{"a bound over a fact this declaration cannot read", stateAt(unknown, acme, 1), ErrUnknownType, append([]string{secret}, identity...)},
		{"a digest over a token no load minted", (digest(At[account]{}, declared.credited.New(acme, creditedV2{Minor: 5, Reason: "one"}))), ErrKey, identity},
		{"a digest over a change decided for another stream",
			(digest(at, declared.credited.New(untouched, creditedV2{Minor: 5, Reason: secret}))), ErrWrongStream,
			append([]string{secret, "A-99"}, identity...)},
		{"a digest over a token minted for another stream",
			(digest(elsewhere, declared.credited.New(acme, creditedV2{Minor: 5, Reason: secret}))), ErrWrongStream,
			append([]string{secret, "A-99"}, identity...)},
		{"a digest over an encoded payload past the bound the store published",
			func() error {
				_, err := cramped.Digest(At[account]{stream: Stream{Family: "accounts.account", Key: Compose("a", "b")}},
					crampedFacts.credited.New(accountID{tenant: "a", number: "b"}, creditedV2{Minor: 5, Reason: secret + strings.Repeat("f", 64)}))
				return err
			}(), ErrTooLarge, []string{secret}},
	} {
		if !errors.Is(one.refused, one.want) {
			t.Fatalf("%s answered %v where the rule it broke is %v, so the row below reads a message nobody published", one.what, one.refused, one.want)
		}
		matched := 0
		for _, sentinel := range vocabulary() {
			if errors.Is(one.refused, sentinel) {
				matched++
			}
		}
		if matched != 1 {
			t.Fatalf("%s answers %d of the published sentinels, and a refusal belongs to exactly one class", one.what, matched)
		}
		for _, named := range one.unspoken {
			escaped := strconv.Quote(named)
			if strings.Contains(one.refused.Error(), named) || strings.Contains(one.refused.Error(), escaped[1:len(escaped)-1]) {
				t.Fatalf("%s rendered %q into %q, and a refusal names the rule that was broken and never the data that broke it", one.what, named, one.refused)
			}
		}
	}

	if !errors.Is(stateAt(repo, acme, 0), crud.ErrBadRequest) || !errors.Is(stateAt(repo, acme, 9), crud.ErrBadRequest) {
		t.Fatal("a version a caller asked for and the stream does not hold does not carry the request class, so an undeclared wrap renders it as a 500 over data only the caller can correct")
	}
	if _, err := repo.StateAt(ctx, acme, 5); err != nil {
		t.Fatalf("the bound the stream does hold was refused with %v, so every row above passes against a call that refuses everything", err)
	}
}

type messages struct{ total, wrapping, bare, delegated int }

func (this *messages) add(other messages) {
	this.total += other.total
	this.wrapping += other.wrapping
	this.bare += other.bare
	this.delegated += other.delegated
}

func renderedValues(typed typedPackage) (messages, []string) {
	var counted messages
	var complaints []string

	for _, file := range typed.files {
		for _, declaration := range file.Decls {
			within := declaredAs(declaration)
			written := localText(typed.info, declaration)
			ast.Inspect(declaration, func(node ast.Node) bool {
				call, isCall := node.(*ast.CallExpr)
				if !isCall || !buildsAMessage(typed.info, call) {
					return true
				}
				counted.total++
				verbs, readable := verbsPerArgument(call)
				if !readable {
					complaints = append(complaints, typed.at(call)+" in "+within+
						": the format string numbers or stars its verbs, and which argument each verb renders cannot be read here — write it plainly")
					return true
				}
				counted.count(verbs)
				for index, argument := range call.Args {
					verb := byte(0)
					if index < len(verbs) {
						verb = verbs[index]
					}
					flow := textFlow{info: typed.info, locals: written, seen: map[types.Object]bool{}}
					found, delegated := judgeRendering(typed, flow, within, argument, verb)
					complaints = append(complaints, found...)
					counted.delegated += delegated
				}
				return true
			})
		}
	}
	return counted, complaints
}

// A message built one statement at a time is one message. The locals a
// declaration writes text into are collected first, so an argument that is
// nothing but the name of one is judged as what was written into it — which is
// the shape `detail := string(key)` takes and the shape a driver refusal in the
// next store will be written in.
func localText(info *types.Info, declaration ast.Decl) map[types.Object][]ast.Expr {
	written := map[types.Object][]ast.Expr{}
	remember := func(name *ast.Ident, value ast.Expr) {
		held, isVariable := declaredBy(info, name).(*types.Var)
		if !isVariable || held.Pkg() == nil || held.Parent() == nil || held.Parent() == held.Pkg().Scope() {
			return
		}
		if basic, isBasic := held.Type().(*types.Basic); !isBasic || basic.Info()&types.IsString == 0 {
			return
		}
		written[held] = append(written[held], value)
	}
	ast.Inspect(declaration, func(node ast.Node) bool {
		switch found := node.(type) {
		case *ast.AssignStmt:
			if len(found.Lhs) != len(found.Rhs) {
				return true
			}
			for index, target := range found.Lhs {
				if name, isName := target.(*ast.Ident); isName {
					remember(name, found.Rhs[index])
				}
			}
		case *ast.ValueSpec:
			if len(found.Names) != len(found.Values) {
				return true
			}
			for index, name := range found.Names {
				remember(name, found.Values[index])
			}
		}
		return true
	})
	return written
}

func declaredBy(info *types.Info, name *ast.Ident) types.Object {
	if held := info.Defs[name]; held != nil {
		return held
	}
	return info.Uses[name]
}

func (this *messages) count(verbs []byte) {
	wrapping, rendering := false, false
	for _, verb := range verbs {
		switch verb {
		case 0:
		case 'w':
			wrapping = true
		default:
			rendering = true
		}
	}
	if wrapping {
		this.wrapping++
	}
	if !wrapping && !rendering {
		this.bare++
	}
}

func declaredAs(declaration ast.Decl) string {
	switch held := declaration.(type) {
	case *ast.FuncDecl:
		return held.Name.Name
	case *ast.GenDecl:
		for _, spec := range held.Specs {
			if value, isValue := spec.(*ast.ValueSpec); isValue && len(value.Names) > 0 {
				return value.Names[0].Name
			}
		}
	}
	return "the declaration"
}

// A message is what a call returning an error and taking text builds. A call
// that returns an error and takes no text forwards a failure rather than
// wording one, so an append that carries a key and a decode that carries a
// payload are not read here.
func buildsAMessage(info *types.Info, call *ast.CallExpr) bool {
	result := info.TypeOf(call)
	if result == nil {
		return false
	}
	if _, isTuple := result.(*types.Tuple); isTuple {
		return false
	}
	if !types.Implements(result, errorRendering()) {
		return false
	}
	for _, argument := range call.Args {
		held := info.TypeOf(argument)
		if held == nil {
			continue
		}
		if basic, isBasic := held.(*types.Basic); isBasic && basic.Info()&types.IsString != 0 {
			return true
		}
	}
	return false
}

func judgeRendering(typed typedPackage, flow textFlow, within string, argument ast.Expr, verb byte) ([]string, int) {
	if verb == 'T' {
		return nil, 0
	}
	var complaints []string
	delegated := 0
	refuse := func(node ast.Node, what string) {
		complaints = append(complaints, typed.at(node)+" in "+within+": "+what+
			" is rendered into a refusal, and a refusal names the rule that was broken and never the data that broke it")
	}
	for _, source := range flow.from(argument) {
		held := typed.info.TypeOf(source.expr)
		if held == nil {
			continue
		}
		if name := identityNamed(held); name != "" {
			refuse(source.expr, "a "+name)
			continue
		}
		if source.derived {
			continue
		}
		carried := carriedIdentity(held)
		if rendersItself(held) {
			if carried != "" {
				delegated++
			}
			continue
		}
		if carried != "" {
			refuse(source.expr, "a value carrying a "+carried)
		}
	}
	return complaints, delegated
}

// A value a message renders is judged whole. A value a message renders
// something *derived from* — a receiver, an argument of a helper — is judged
// only for being an identity itself, because the function in between chose what
// it rendered.
type renderedSource struct {
	expr    ast.Expr
	derived bool
}

type textFlow struct {
	info   *types.Info
	locals map[types.Object][]ast.Expr
	seen   map[types.Object]bool
}

func (this textFlow) from(argument ast.Expr) []renderedSource {
	switch held := argument.(type) {
	case *ast.ParenExpr:
		return this.from(held.X)
	case *ast.BinaryExpr:
		return append(this.from(held.X), this.from(held.Y)...)
	case *ast.CallExpr:
		return this.fromCall(held)
	case *ast.Ident:
		if written := this.writtenInto(held); written != nil {
			var sources []renderedSource
			for _, value := range written {
				sources = append(sources, this.from(value)...)
			}
			return sources
		}
	}
	return []renderedSource{{expr: argument}}
}

func (this textFlow) writtenInto(name *ast.Ident) []ast.Expr {
	held := this.info.Uses[name]
	if held == nil || this.seen[held] || len(this.locals[held]) == 0 {
		return nil
	}
	this.seen[held] = true
	return this.locals[held]
}

func (this textFlow) fromCall(call *ast.CallExpr) []renderedSource {
	if this.info.Types[call.Fun].IsType() {
		var sources []renderedSource
		for _, inner := range call.Args {
			sources = append(sources, this.from(inner)...)
		}
		return sources
	}
	if name, isName := call.Fun.(*ast.Ident); isName {
		if builtin, isBuiltin := this.info.Uses[name].(*types.Builtin); isBuiltin && (builtin.Name() == "len" || builtin.Name() == "cap") {
			return nil
		}
	}
	sources := []renderedSource{{expr: call}}
	if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector {
		if chosen := this.info.Selections[selector]; chosen != nil && chosen.Kind() == types.MethodVal {
			sources = append(sources, renderedSource{expr: selector.X, derived: true})
		}
	}
	for _, inner := range call.Args {
		sources = append(sources, renderedSource{expr: inner, derived: true})
	}
	return sources
}
