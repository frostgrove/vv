package event

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func sealingReaders(t *testing.T) map[string]func(accountDeclaration) {
	t.Helper()
	return map[string]func(accountDeclaration){
		"Bind": func(declared accountDeclaration) {
			if _, err := Bind(Open(newRecordingStore(t)), declared.aggregate); err != nil {
				t.Fatalf("the binding that seals the declaration was refused: %v", err)
			}
		},
		"Aggregate.Family": func(declared accountDeclaration) { _ = declared.aggregate.Family() },
		"Aggregate.Key": func(declared accountDeclaration) {
			_, _ = declared.aggregate.Key(accountID{tenant: "acme", number: "A-17"})
		},
		"Aggregate.Fold": func(declared accountDeclaration) {
			_, _ = declared.aggregate.Fold(accountID{tenant: "acme", number: "A-17"}, account{})
		},
		"Fact.New": func(declared accountDeclaration) {
			_ = declared.opened.New(accountID{tenant: "acme", number: "A-17"}, opened{Owner: "acme"})
		},
		"Fact.RoundTrip": func(declared accountDeclaration) {
			_, _ = declared.opened.RoundTrip(opened{Owner: "acme"})
		},
	}
}

func sealCallers(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("the package this test polices cannot be read: %v", err)
	}
	positions := token.NewFileSet()
	callers := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(positions, name, nil, 0)
		if err != nil {
			t.Fatalf("%s does not parse: %v", name, err)
		}
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction {
				continue
			}
			ast.Inspect(function, func(node ast.Node) bool {
				call, isCall := node.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector && selector.Sel.Name == "seal" {
					callers = append(callers, sealedFrom(function))
				}
				return true
			})
		}
	}
	sort.Strings(callers)
	return slices.Compact(callers)
}

func sealEnumeration(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "seal.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("event/seal.go does not parse, so the enumeration a reader of the library sees cannot be read: %v", err)
	}
	named := []string{}
	for _, declaration := range file.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if !isFunction || function.Name.Name != "seal" || function.Doc == nil {
			continue
		}
		for _, line := range strings.Split(function.Doc.Text(), "\n") {
			if strings.HasPrefix(line, "\t") {
				named = append(named, strings.Fields(line)[0])
			}
		}
	}
	sort.Strings(named)
	return named
}

// Bind is a function and the other five are methods, so the name a caller of
// this test writes is the one a reader of the library would: Bind, and
// Aggregate.Fold.
func sealedFrom(function *ast.FuncDecl) string {
	if receiver := receiverTypeOf(function); receiver != "" {
		return receiver + "." + function.Name.Name
	}
	return function.Name.Name
}

func receiverTypeOf(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return ""
	}
	named := ""
	ast.Inspect(function.Recv.List[0].Type, func(node ast.Node) bool {
		if identifier, is := node.(*ast.Ident); is && named == "" {
			named = identifier.Name
		}
		return true
	})
	return named
}

func TestTheSealRefusesALateFact(t *testing.T) {
	late := func(t *testing.T, aggregate *Aggregate[account, accountID]) error {
		t.Helper()
		_, err := TryDeclare(aggregate, "accounts.late", From(JSON[closed]()),
			func(this account, _ closed) account { return this })
		return err
	}

	t.Run("every reader of the declaration seals it", func(t *testing.T) {
		for name, read := range sealingReaders(t) {
			declared := declareAccounts(t)
			if err := late(t, declared.aggregate); err != nil {
				t.Fatalf("%s: a fact declared before any reader ran was refused, so the control cannot tell a seal from a broken declaration: %v", name, err)
			}
			sealed := declareAccounts(t)
			read(sealed)
			err := late(t, sealed.aggregate)
			if !errors.Is(err, ErrSealed) || !errors.Is(err, ErrDeclaration) {
				t.Fatalf("%s read the declaration and a later fact was still accepted (%v), so the type table is mutable while it is being read", name, err)
			}
			panicked := recoverDeclaration(t, "a fact declared after "+name+" read the aggregate", func() {
				Declare(sealed.aggregate, "accounts.later", From(JSON[closed]()),
					func(this account, _ closed) account { return this })
			})
			if !errors.Is(panicked, ErrSealed) {
				t.Fatalf("%s: the late declaration panicked with %v rather than the seal", name, panicked)
			}
		}
	})

	t.Run("a fact declared as the first reader runs is accepted or sealed and never both", func(t *testing.T) {
		for range 200 {
			declared := declareAccounts(t)
			var released, ran sync.WaitGroup
			released.Add(1)
			ran.Add(3)
			var arrived error
			for range 2 {
				go func() {
					defer ran.Done()
					released.Wait()
					_ = declared.aggregate.Family()
				}()
			}
			go func() {
				defer ran.Done()
				released.Wait()
				arrived = late(t, declared.aggregate)
			}()
			released.Done()
			ran.Wait()

			_, held := declared.aggregate.facts["accounts.late"]
			switch {
			case arrived == nil && !held:
				t.Fatal("a fact was accepted and is not in the table it was accepted into")
			case arrived != nil && held:
				t.Fatalf("a fact refused with %v is in the table anyway, so the seal and the declaration disagree about one write", arrived)
			case arrived != nil && !errors.Is(arrived, ErrSealed):
				t.Fatalf("a fact declared as the first reader ran answered %v, which is neither the acceptance nor the seal", arrived)
			}
			if err := late(t, declared.aggregate); !errors.Is(err, ErrSealed) {
				t.Fatalf("the declaration was read and a later fact answered %v", err)
			}
		}
	})

	t.Run("the readers that seal are exhaustive and enumerated", func(t *testing.T) {
		want := []string{}
		for name := range sealingReaders(t) {
			want = append(want, name)
		}
		sort.Strings(want)
		if got := sealCallers(t); !slices.Equal(got, want) {
			t.Fatalf("the declaration is sealed from %v and this test drives %v; a reader that seals without a case here, or a case with no reader, is what leaves the table mutable while it is read",
				got, want)
		}
		if enumerated := sealEnumeration(t); !slices.Equal(enumerated, want) {
			t.Fatalf("event/seal.go tells a reader of the library that %v seal the declaration and %v do; the enumeration is the only artefact a consumer sees and nothing but this compares it to the code",
				enumerated, want)
		}
	})
}

// The claim the seal must survive: one *Aggregate per aggregate type is shared
// by every request in the process, so the third of these is the control for the
// second. Without a lock-free fast path the two differ by 5x, and reading the
// second alone reports that a mutex under every decision costs nothing.
func mintOpened(family string) (*Fact[account, accountID, opened], accountID) {
	aggregate := Define[account](family, accountKey)
	return Declare(aggregate, "accounts.opened", From(JSON[opened]()), openAccount),
		accountID{tenant: "acme", number: "A-17"}
}

func BenchmarkAChangeIsMintedSerially(b *testing.B) {
	declared, id := mintOpened("accounts.serial")
	for b.Loop() {
		_ = declared.New(id, opened{Owner: "acme"})
	}
}

func BenchmarkChangesAreMintedOnOneAggregate(b *testing.B) {
	declared, id := mintOpened("accounts.shared")
	b.RunParallel(func(each *testing.PB) {
		for each.Next() {
			_ = declared.New(id, opened{Owner: "acme"})
		}
	})
}

func BenchmarkChangesAreMintedOnDistinctAggregates(b *testing.B) {
	declared := make([]*Fact[account, accountID, opened], 64)
	var id accountID
	for index := range declared {
		declared[index], id = mintOpened(fmt.Sprintf("accounts.distinct.%d", index))
	}
	var taken atomic.Uint64
	b.RunParallel(func(each *testing.PB) {
		mine := declared[(taken.Add(1)-1)%uint64(len(declared))]
		for each.Next() {
			_ = mine.New(id, opened{Owner: "acme"})
		}
	})
}

// One *Aggregate per aggregate type is shared by every request in the process,
// and the codec, the identity mapper and the fold it retains are called from all
// of them at once. The race detector is the reader of this test; the assertions
// are what say each answer was the caller's own and not a value another
// goroutine was halfway through producing.
func TestOneDeclarationIsDecidedAndFoldedFromManyGoroutines(t *testing.T) {
	declared := declareAccounts(t)
	var released, ran sync.WaitGroup
	released.Add(1)
	for worker := range 8 {
		ran.Add(1)
		go func() {
			defer ran.Done()
			released.Wait()
			id := accountID{tenant: "acme", number: fmt.Sprintf("A-%d", worker)}
			stream := Stream{Family: "accounts.account", Key: Compose(id.tenant, id.number)}
			for range 50 {
				change := declared.credited.New(id, creditedV2{Minor: int64(worker), Reason: "deposit"})
				if change.Err() != nil {
					t.Errorf("worker %d minted a change carrying %v", worker, change.Err())
					return
				}
				if change.Stream() != stream {
					t.Errorf("worker %d minted a change for %v rather than for its own identity", worker, change.Stream())
					return
				}
				state, err := declared.aggregate.Fold(id, account{}, change)
				if err != nil {
					t.Errorf("worker %d was refused: %v", worker, err)
					return
				}
				if state.Balance != int64(worker) || !state.Applied["deposit"] {
					t.Errorf("worker %d folded to %+v rather than to the state its own decision describes", worker, state)
					return
				}
				if key, err := declared.aggregate.Key(id); err != nil || key != stream.Key {
					t.Errorf("worker %d rendered the key %q (%v)", worker, key, err)
					return
				}
				if _, err := declared.opened.RoundTrip(opened{Owner: "acme"}); err != nil {
					t.Errorf("worker %d was refused by the round trip: %v", worker, err)
					return
				}
			}
		}()
	}
	released.Done()
	ran.Wait()
}
