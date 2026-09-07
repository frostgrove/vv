package event

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
)

type namedSentinel struct {
	name  string
	class string
	err   error
}

func declaredVocabulary() []namedSentinel {
	return []namedSentinel{
		{"ErrDeclaration", "declaration", ErrDeclaration},
		{"ErrSealed", "declaration", ErrSealed},
		{"ErrCodecType", "declaration", ErrCodecType},

		{"ErrFamily", "wiring", ErrFamily},
		{"ErrWrongStore", "wiring", ErrWrongStore},
		{"ErrWrongStream", "wiring", ErrWrongStream},
		{"ErrNoTransaction", "wiring", ErrNoTransaction},
		{"ErrNoTransactionBinding", "wiring", ErrNoTransactionBinding},
		{"ErrAmbientNotTransaction", "wiring", ErrAmbientNotTransaction},
		{"ErrTransactionMismatch", "wiring", ErrTransactionMismatch},
		{"ErrCursor", "wiring", ErrCursor},

		{"ErrKey", "request", ErrKey},
		{"ErrEncode", "request", ErrEncode},
		{"ErrSample", "request", ErrSample},
		{"ErrTooLarge", "request", ErrTooLarge},

		{"ErrUnknownType", "history", ErrUnknownType},
		{"ErrRevision", "history", ErrRevision},
		{"ErrPayload", "history", ErrPayload},
		{"ErrUpcast", "history", ErrUpcast},

		{"ErrConflict", "write", ErrConflict},
		{"ErrUncertain", "write", ErrUncertain},

		{"ErrBackend", "store", ErrBackend},
		{"ErrClosed", "store", ErrClosed},
		{"ErrRefused", "store", ErrRefused},
	}
}

func intraClassWraps() map[[2]string]bool {
	return map[[2]string]bool{
		{"ErrSealed", "ErrDeclaration"}:    true,
		{"ErrCodecType", "ErrDeclaration"}: true,
	}
}

type refusalDoor struct {
	name      string
	refuse    func(error) error
	byDefault error
}

func doors() []refusalDoor {
	return []refusalDoor{
		{"an append", refuseAppend, ErrUncertain},
		{"a read", refuseRead, ErrBackend},
	}
}

type classifiedOutcome struct {
	name    string
	outcome Outcome
	mapped  error
}

func classifiedOutcomes() []classifiedOutcome {
	return []classifiedOutcome{
		{"Unclassified", Unclassified, nil},
		{"Conflict", Conflict, ErrConflict},
		{"NotWritten", NotWritten, ErrBackend},
		{"Unconfirmed", Unconfirmed, ErrUncertain},
		{"Closed", Closed, ErrClosed},
		{"BadCursor", BadCursor, ErrCursor},
		{"Refused", Refused, ErrRefused},
	}
}

func (this classifiedOutcome) at(door refusalDoor) error {
	if this.mapped != nil {
		return this.mapped
	}
	return door.byDefault
}

func namesDeclaredInErrorsFile(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "errors.go", nil, 0)
	if err != nil {
		t.Fatalf("event/errors.go could not be parsed, so the declared sentinel set cannot be read: %v", err)
	}
	var names []string
	for _, declaration := range file.Decls {
		general, isVar := declaration.(*ast.GenDecl)
		if !isVar || general.Tok != token.VAR {
			continue
		}
		for _, spec := range general.Specs {
			for _, name := range spec.(*ast.ValueSpec).Names {
				if strings.HasPrefix(name.Name, "Err") {
					names = append(names, name.Name)
				}
			}
		}
	}
	return names
}

func namesListedInTheGate(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "errors.go", nil, 0)
	if err != nil {
		t.Fatalf("event/errors.go could not be parsed, so the gate's sentinel list cannot be read: %v", err)
	}
	var names []string
	for _, declaration := range file.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if !isFunction || function.Name.Name != "vocabulary" {
			continue
		}
		ast.Inspect(function, func(node ast.Node) bool {
			if identifier, isIdentifier := node.(*ast.Ident); isIdentifier && strings.HasPrefix(identifier.Name, "Err") {
				names = append(names, identifier.Name)
			}
			return true
		})
	}
	return names
}

func countByName(names []string) map[string]int {
	counted := make(map[string]int, len(names))
	for _, name := range names {
		counted[name]++
	}
	return counted
}

func TestTheRefusalVocabularyIsAPartition(t *testing.T) {
	table := declaredVocabulary()

	t.Run("the gate reads the sentinel set that is declared", func(t *testing.T) {
		expected := make(map[string]int, len(table))
		for _, entry := range table {
			expected[entry.name]++
		}
		for _, source := range []struct {
			what  string
			names []string
		}{
			{"declared in event/errors.go", namesDeclaredInErrorsFile(t)},
			{"listed in vocabulary(), which is the cross-class gate", namesListedInTheGate(t)},
		} {
			found := countByName(source.names)
			for name, want := range expected {
				if found[name] != want {
					t.Fatalf("%s appears %d times %s and %d times in this test's table, so the partition gate and the vocabulary have drifted apart",
						name, found[name], source.what, want)
				}
			}
			for name, count := range found {
				if expected[name] != count {
					t.Fatalf("%s appears %d times %s and is not in this test's table, so a sentinel is being pair-tested against nothing",
						name, count, source.what)
				}
			}
		}

		gated := vocabulary()
		if len(gated) != len(table) {
			t.Fatalf("vocabulary() returns %d sentinels and %d are declared, so at least one sentinel's cause can reach it across a class",
				len(gated), len(table))
		}
		seen := make(map[error]string, len(table))
		for _, entry := range table {
			seen[entry.err] = entry.name
		}
		for _, gate := range gated {
			if _, known := seen[gate]; !known {
				t.Fatalf("vocabulary() returns %q, which is not one of the declared sentinels this test pairs", gate)
			}
			delete(seen, gate)
		}
		for _, missed := range seen {
			t.Fatalf("%s is declared and is not returned by vocabulary(), so a store's cause carrying it reaches it through a refusal of another class", missed)
		}
	})

	t.Run("every ordered pair answers exactly the closed intra-class list", func(t *testing.T) {
		edges := intraClassWraps()
		pairs := 0
		for _, left := range table {
			if !errors.Is(left.err, left.err) {
				t.Fatalf("%s does not match itself", left.name)
			}
			for _, right := range table {
				if left.name == right.name {
					continue
				}
				pairs++
				want := edges[[2]string{left.name, right.name}]
				if got := errors.Is(left.err, right.err); got != want {
					if want {
						t.Fatalf("%s no longer reaches %s, and %s's own use case needs the general sentinel to catch the specific one",
							left.name, right.name, left.name)
					}
					t.Fatalf("%s (%s class) reaches %s (%s class), so a caller matching one class is answered by another",
						left.name, left.class, right.name, right.class)
				}
			}
		}
		if pairs != len(table)*(len(table)-1) {
			t.Fatalf("the pairwise sweep walked %d ordered pairs and the vocabulary has %d", pairs, len(table)*(len(table)-1))
		}
	})

	t.Run("a refusal built from a store's cause matches its own sentinel and no other", func(t *testing.T) {
		matched := 0
		refusals := 0
		for _, door := range doors() {
			for _, outcome := range classifiedOutcomes() {
				for _, cause := range table {
					refusals++
					refused := door.refuse(Failure(outcome.outcome, cause.err))
					want := outcome.at(door)
					for _, target := range table {
						got := errors.Is(refused, target.err)
						if got {
							matched++
						}
						if got != (target.err == want) {
							t.Fatalf("%s failure at %s door over cause %s answers errors.Is(%s) as %v, and the store's own cause is what carried it there",
								outcome.name, door.name, cause.name, target.name, got)
						}
					}
				}
			}
		}
		if matched != refusals {
			t.Fatalf("%d refusals produced %d matches over the vocabulary, and a partition gives exactly one each", refusals, matched)
		}
	})

	t.Run("a refusal the kernel builds itself matches only its own sentinel", func(t *testing.T) {
		for _, cause := range table {
			for _, built := range []struct {
				name     string
				refusal  error
				sentinel error
			}{
				{"an upcaster's refusal", upcastRefusal(cause.err), ErrUpcast},
				{"a transaction question's refusal", refuseTransaction(cause.err), ErrAmbientNotTransaction},
			} {
				for _, target := range table {
					if got := errors.Is(built.refusal, target.err); got != (target.err == built.sentinel) {
						t.Fatalf("%s over cause %s answers errors.Is(%s) as %v", built.name, cause.name, target.name, got)
					}
				}
			}
		}
		for _, target := range table {
			if got := errors.Is(tooLarge("the batch", 2, 1), target.err); got != (target.err == ErrTooLarge) {
				t.Fatalf("an over-a-bound refusal answers errors.Is(%s) as %v", target.name, got)
			}
		}
	})

	t.Run("a decorator's own error stays matchable when it co-carries a sentinel", func(t *testing.T) {
		quota := errors.New("tenant over quota")
		for _, carried := range []struct {
			name     string
			refusal  error
			sentinel error
		}{
			{"a policy refusal", refuseAppend(Failure(Refused, fmt.Errorf("%w (quota %w)", quota, ErrTooLarge))), ErrTooLarge},
			{"an upcaster's refusal", upcastRefusal(fmt.Errorf("%w: %w", quota, ErrRevision)), ErrRevision},
		} {
			if !errors.Is(carried.refusal, quota) {
				t.Fatalf("%s lost the error its own author wrote, so nobody can match what the decorator or the upcaster said", carried.name)
			}
			if errors.Is(carried.refusal, carried.sentinel) {
				t.Fatalf("%s reaches %v through its cause, which is the cross-class door the gate exists to close", carried.name, carried.sentinel)
			}
		}
		if !errors.Is(upcastRefusal(quota), quota) {
			t.Fatal("an upcaster's error is unreachable even on its own, so the co-wrap case above proves nothing")
		}
	})

	t.Run("a cause the framework already maps still arrives", func(t *testing.T) {
		policy := refuseAppend(Failure(Refused, fmt.Errorf("tenant over quota: %w", crud.ErrForbidden)))
		if !errors.Is(policy, crud.ErrForbidden) {
			t.Fatal("a policy refusal that wrapped the forbidden class does not carry it, so a decorator has no way to state its own status")
		}
		if !errors.Is(policy, ErrRefused) {
			t.Fatal("a policy refusal is not ErrRefused")
		}
		fault := errs.Forbidden().Code(errs.CodeForbidden).Message("the tenant is over its quota").Fault()
		found, ok := errs.AsFault(refuseAppend(Failure(Refused, fault)))
		if !ok || found.Kind != errs.KindForbidden {
			t.Fatalf("a policy refusal carrying a fault answers errs.AsFault as %v, so the transport cannot read the status the policy chose", ok)
		}
	})
}

type asOnlyFault struct {
	fault *errs.Fault
}

func (this asOnlyFault) Error() string { return "a driver failure" }

func (this asOnlyFault) As(target any) bool {
	holder, ok := target.(**errs.Fault)
	if !ok {
		return false
	}
	*holder = this.fault
	return true
}

type answersByIs struct {
	answered error
}

func (this answersByIs) Error() string { return "a driver failure" }

func (this answersByIs) Is(target error) bool { return target == this.answered }

func TestADeclaredWrapIsReachableByErrorsAsAndACauseIsNot(t *testing.T) {
	t.Run("every declared class wrap is reachable and was not replaced", func(t *testing.T) {
		for _, member := range []namedSentinel{
			{"ErrKey", "request", ErrKey},
			{"ErrEncode", "request", ErrEncode},
			{"ErrSample", "request", ErrSample},
			{"ErrTooLarge", "request", ErrTooLarge},
		} {
			if !errors.Is(member.err, crud.ErrBadRequest) {
				t.Fatalf("%s declares no bad-request class, so a request that carried unusable data is answered as though the server broke", member.name)
			}
		}
		for _, control := range []namedSentinel{
			{"ErrCursor", "wiring", ErrCursor},
			{"ErrPayload", "history", ErrPayload},
			{"ErrBackend", "store", ErrBackend},
		} {
			if errors.Is(control.err, crud.ErrBadRequest) {
				t.Fatalf("%s is in the %s class and declares the request class's wrap, so the class no longer decides the status", control.name, control.class)
			}
		}
		if !errors.Is(ErrConflict, crud.ErrConflict) {
			t.Fatal("a conflict does not carry the framework's conflict class")
		}

		retryableCause := fmt.Errorf("the pool is exhausted: %w", crud.ErrUnavailable)
		if !errors.Is(refuseAppend(Failure(NotWritten, retryableCause)), crud.ErrUnavailable) {
			t.Fatal("a store that classified its failure retryable does not reach the retryable class, so a caller that could retry does not")
		}
		if errors.Is(refuseAppend(Failure(NotWritten, errors.New("the column is missing"))), crud.ErrUnavailable) {
			t.Fatal("a store failure that is not retryable reaches the retryable class, so every backend failure would be retried")
		}
		if !errors.Is(refuseAppend(Failure(NotWritten, asOnlyFault{fault: errs.Retryable().Code(errs.CodeDeadlock).Fault()})), crud.ErrUnavailable) {
			t.Fatal("a retryable fault reachable only through an As method is invisible, so the framework has two answers to whether a chain carries a fault")
		}

		oversized := tooLarge("the payload", 2<<20, 1<<20)
		fault, ok := errs.AsFault(oversized)
		if !ok {
			t.Fatal("an over-a-bound refusal hides its fault from errs.AsFault, so a transport renders it as an internal failure")
		}
		if fault.Kind != errs.KindTooLarge {
			t.Fatalf("an over-a-bound refusal carries the %v kind", fault.Kind)
		}
		if !strings.Contains(fault.Message, "1048576") || !strings.Contains(fault.Message, "2097152") {
			t.Fatalf("the fault names neither the bound nor the count: %q", fault.Message)
		}
	})

	t.Run("a class a cause answers for only through an Is method is still found", func(t *testing.T) {
		if !errors.Is(refuseAppend(Failure(NotWritten, answersByIs{answered: crud.ErrUnavailable})), crud.ErrUnavailable) {
			t.Fatal("a driver error that answers the retryable class through its own Is method does not reach that class, so the kernel has two answers to whether a chain carries one and a caller who could have retried is told the server broke")
		}
		if errors.Is(refuseAppend(Failure(NotWritten, answersByIs{answered: crud.ErrConflict})), crud.ErrUnavailable) {
			t.Fatal("a driver error that answers for another class reaches the retryable one, so the case above proves nothing")
		}
	})

	t.Run("a store's cause is reachable by neither traversal", func(t *testing.T) {
		for _, door := range doors() {
			for _, outcome := range classifiedOutcomes() {
				if outcome.outcome == Refused {
					continue
				}
				cause := fmt.Errorf("the pool is exhausted: %w", crud.ErrUnavailable)
				refused := door.refuse(Failure(outcome.outcome, cause))
				if outcome.at(door) == ErrBackend {
					continue
				}
				if errors.Is(refused, crud.ErrUnavailable) {
					t.Fatalf("a %s failure at %s door reaches the retryable class through its cause, so retrying it repeats a write nobody confirmed",
						outcome.name, door.name)
				}
				if _, found := errs.AsFault(door.refuse(Failure(outcome.outcome, errs.NotFound().Fault()))); found {
					t.Fatalf("a %s failure at %s door exposes its cause's fault, so a store's incidental error sets the caller's status",
						outcome.name, door.name)
				}
				if got := CauseOf(refused); !errors.Is(got, crud.ErrUnavailable) {
					t.Fatalf("a %s failure at %s door lost its cause, and CauseOf is the one reader that is meant to have it",
						outcome.name, door.name)
				}
			}
		}
	})

	t.Run("a transaction question's error is a cause and not a wrap", func(t *testing.T) {
		reported := fmt.Errorf("this executor is not a transaction: %w", crud.ErrUnavailable)
		refused := refuseTransaction(reported)
		if !errors.Is(refused, ErrAmbientNotTransaction) {
			t.Fatal("what a store answered about the ambient transaction is not the wiring refusal")
		}
		if errors.Is(refused, crud.ErrUnavailable) {
			t.Fatal("a store's answer about the ambient transaction sets the refusal's transport status")
		}
		if CauseOf(refused) != reported {
			t.Fatal("the store's own answer is unreachable even through CauseOf")
		}
	})

	t.Run("the two rows whose wrap is their cause answer both traversals", func(t *testing.T) {
		application := errors.New("this revision names a shape this reader cannot build")
		if !errors.Is(upcastRefusal(application), application) {
			t.Fatal("an upcaster's own error does not reach the caller, and matching it is the only thing an upcaster's refusal is for")
		}
		if _, ok := errs.AsFault(upcastRefusal(errs.Forbidden().Fault())); !ok {
			t.Fatal("an upcaster's fault is unreadable by errors.As")
		}
		if _, ok := errs.AsFault(refuseRead(Failure(Refused, errs.Forbidden().Fault()))); !ok {
			t.Fatal("a policy refusal's fault is unreadable by errors.As")
		}
	})

	t.Run("CauseOf answers only for a refusal", func(t *testing.T) {
		if CauseOf(nil) != nil {
			t.Fatal("CauseOf invented a cause for no error at all")
		}
		if CauseOf(errors.New("somebody else's error")) != nil {
			t.Fatal("CauseOf answered for an error this subsystem did not build")
		}
		cause := errors.New("the connection was reset")
		if CauseOf(fmt.Errorf("while loading: %w", refuseRead(Failure(NotWritten, cause)))) != cause {
			t.Fatal("CauseOf cannot see through a caller's own wrapping, so an operator's log line loses the store's reason")
		}
	})
}

func TestAContextCauseNeverTravelsThroughARefusal(t *testing.T) {
	cancellations := []struct {
		name string
		err  error
	}{
		{"context.Canceled", context.Canceled},
		{"context.DeadlineExceeded", context.DeadlineExceeded},
	}

	t.Run("a bare cancellation travels as itself", func(t *testing.T) {
		for _, door := range doors() {
			for _, cancellation := range cancellations {
				refused := door.refuse(cancellation.err)
				if refused != cancellation.err {
					t.Fatalf("%s at %s door came back as %v rather than as itself", cancellation.name, door.name, refused)
				}
				if errors.Is(refused, ErrUncertain) || errors.Is(refused, ErrBackend) {
					t.Fatalf("%s at %s door also answers for a refusal of this vocabulary", cancellation.name, door.name)
				}
			}
		}
	})

	t.Run("a store's text around a cancellation does not travel", func(t *testing.T) {
		key, version, cursor := "acme/A-17", 9, "eyJwIjo0Mn0"
		reported := fmt.Errorf("read %s at v%d from cursor %s: %w", key, version, cursor, context.DeadlineExceeded)
		for _, door := range doors() {
			refused := door.refuse(reported)
			for _, secret := range []string{key, "v9", cursor} {
				if strings.Contains(refused.Error(), secret) {
					t.Fatalf("a cancelled operation at %s door rendered %q, which puts %q into the caller's error and the operator's log",
						door.name, refused.Error(), secret)
				}
			}
			if !errors.Is(refused, context.DeadlineExceeded) {
				t.Fatalf("a cancelled operation at %s door lost its cancellation identity, so a client disconnect reads as a backend failure",
					door.name)
			}
		}
	})

	t.Run("a cancellation a cause answers for only through an Is method travels the same way", func(t *testing.T) {
		for _, door := range doors() {
			for _, cancellation := range cancellations {
				byIs := answersByIs{answered: cancellation.err}
				if refused := door.refuse(byIs); refused != cancellation.err {
					t.Fatalf("%s answered for through a driver's own Is method came back from %s door as %v rather than as itself, so a client disconnect reads as a backend failure",
						cancellation.name, door.name, refused)
				}
				classified := door.refuse(Failure(Unconfirmed, byIs))
				if !errors.Is(classified, ErrUncertain) {
					t.Fatalf("an unconfirmed append at %s door over a cause that answers for %s is not uncertain", door.name, cancellation.name)
				}
				if errors.Is(classified, cancellation.err) {
					t.Fatalf("an unconfirmed append at %s door answers for %s through its cause's Is method, and a caller that branches on cancellation first would abandon a write nobody confirmed",
						door.name, cancellation.name)
				}
			}
		}
	})

	t.Run("a classified cancellation is the outcome's refusal and not a cancellation", func(t *testing.T) {
		for _, door := range doors() {
			for _, outcome := range classifiedOutcomes() {
				for _, cancellation := range cancellations {
					refused := door.refuse(Failure(outcome.outcome, cancellation.err))
					if !errors.Is(refused, outcome.at(door)) {
						t.Fatalf("a %s failure at %s door whose cause was %s is not %v", outcome.name, door.name, cancellation.name, outcome.at(door))
					}
					for _, target := range cancellations {
						if errors.Is(refused, target.err) {
							t.Fatalf("a %s failure at %s door answers errors.Is(%s), and a caller that branches on cancellation first would take that branch over an unconfirmed write",
								outcome.name, door.name, target.name)
						}
					}
				}
			}
		}
	})

	t.Run("no refusal in the vocabulary answers for a cancellation", func(t *testing.T) {
		for _, cancellation := range cancellations {
			for _, built := range []struct {
				name    string
				refusal error
			}{
				{"an upcaster's refusal", upcastRefusal(cancellation.err)},
				{"a transaction question's refusal", refuseTransaction(cancellation.err)},
				{"a policy refusal", refuseAppend(Failure(Refused, cancellation.err))},
			} {
				if errors.Is(built.refusal, cancellation.err) {
					t.Fatalf("%s over %s answers for the cancellation", built.name, cancellation.name)
				}
			}
		}
		if !errors.Is(refuseAppend(Failure(Refused, crud.ErrForbidden)), crud.ErrForbidden) {
			t.Fatal("a refusal answers false for every target, so the cancellation cases above prove nothing")
		}
	})
}
