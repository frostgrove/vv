package event

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
)

type joinedCause struct {
	visits   *int
	children []error
}

func (this joinedCause) Error() string { return "a shard failed" }

func (this joinedCause) Unwrap() []error {
	*this.visits++
	return this.children
}

func balancedJoin(visits *int, depth int) error {
	if depth == 0 {
		return errors.New("a row failed")
	}
	shared := balancedJoin(visits, depth-1)
	return joinedCause{visits: visits, children: []error{shared, shared}}
}

func leftDeepJoin(visits *int, length int) error {
	built := error(errors.New("the first row failed"))
	for range length {
		built = joinedCause{visits: visits, children: []error{built}}
	}
	return built
}

type cyclicCause struct {
	next error
}

func (this *cyclicCause) Error() string { return "a chain that loops" }

func (this *cyclicCause) Unwrap() error { return this.next }

type foreignQuestion struct {
	name string
	ask  func(error)
}

func foreignQuestions() []foreignQuestion {
	return []foreignQuestion{
		{"matches", func(chain error) { matches(chain, ErrBackend) }},
		{"an append door", func(chain error) { refuseAppend(chain) }},
		{"a read door", func(chain error) { refuseRead(chain) }},
		{"CauseOf", func(chain error) { CauseOf(chain) }},
		{"a retryable question", func(chain error) { retryable(chain) }},
		{"a classified failure rendered through errs.AsFault", func(chain error) {
			errs.AsFault(refuseRead(Failure(NotWritten, chain)))
		}},
	}
}

const walksPerDoor = 5

func TestAJoinedCauseCannotOutlastTheWalksBudget(t *testing.T) {
	t.Run("the work is bounded by the budget and not by the depth", func(t *testing.T) {
		shallowVisits, deepVisits := 0, 0
		shallow := balancedJoin(&shallowVisits, 18)
		deep := balancedJoin(&deepVisits, 22)
		for _, question := range foreignQuestions() {
			shallowVisits, deepVisits = 0, 0
			question.ask(shallow)
			question.ask(deep)
			for _, measured := range []struct {
				depth  int
				visits int
			}{{18, shallowVisits}, {22, deepVisits}} {
				if measured.visits > causeHops*walksPerDoor {
					t.Fatalf("%s visited %d nodes of a depth-%d join tree, against a budget of %d spent over at most %d walks — so the budget is spent per path and the request goroutine stops inside the kernel",
						question.name, measured.visits, measured.depth, causeHops, walksPerDoor)
				}
			}
			if deepVisits-shallowVisits > causeHops {
				t.Fatalf("%s cost %d visits at depth 18 and %d at depth 22, so the work still follows the depth",
					question.name, shallowVisits, deepVisits)
			}
		}
	})

	t.Run("an ordinary chain is walked whole", func(t *testing.T) {
		visits := 0
		chain := leftDeepJoin(&visits, 11)
		if matches(chain, ErrBackend) {
			t.Fatal("a chain that carries no sentinel of this vocabulary answered for one")
		}
		if visits != 11 {
			t.Fatalf("a chain of 11 joined errors was walked %d nodes deep, so the budget is clamping chains a store may legitimately build", visits)
		}
		visits = 0
		target := errors.New("the row that failed")
		if !matches(joinedCause{visits: &visits, children: []error{errors.New("another row"), target}}, target) {
			t.Fatal("a joined chain's second branch is unreachable, so the bounded walk answers less than errors.Is would")
		}
	})

	t.Run("a chain that loops still answers", func(t *testing.T) {
		looping := &cyclicCause{}
		looping.next = looping
		for _, question := range foreignQuestions() {
			answered := make(chan struct{})
			go func() {
				defer close(answered)
				question.ask(looping)
			}()
			select {
			case <-answered:
			case <-time.After(10 * time.Second):
				t.Fatalf("%s never returned over a chain that loops, and the caller's transaction is open while it does not", question.name)
			}
		}
	})

	t.Run("a cause the kernel cannot read to the end is not promoted to a wrap", func(t *testing.T) {
		for _, depth := range []struct {
			name     string
			layers   int
			promoted bool
		}{
			{"a cause the kernel read to the end", 8, true},
			{"a cause deeper than the budget", causeHops * 2, false},
		} {
			carrying := error(crud.ErrForbidden)
			bearing := error(errs.Forbidden().Fault())
			for layer := range depth.layers {
				carrying = fmt.Errorf("layer %d: %w", layer, carrying)
				bearing = fmt.Errorf("layer %d: %w", layer, bearing)
			}
			if got := errors.Is(refuseAppend(Failure(Refused, carrying)), crud.ErrForbidden); got != depth.promoted {
				t.Fatalf("%s answers errors.Is for the forbidden class as %v", depth.name, got)
			}
			if _, got := errs.AsFault(refuseAppend(Failure(Refused, bearing))); got != depth.promoted {
				t.Fatalf("%s answers errors.As for its fault as %v, and the two traversals disagreeing is what lets a status be set by a cause the kernel would not vouch for",
					depth.name, got)
			}
		}
	})
}

type wrappingCause struct {
	inner error
}

func (this wrappingCause) Error() string { return "the store lost its connection" }

func (this wrappingCause) Unwrap() error { return this.inner }

type permissiveMatcher struct{}

func (permissiveMatcher) Error() string { return "an error that answers for anything" }

func (permissiveMatcher) As(any) bool { return true }

type panickingMatcher struct{}

func (panickingMatcher) Error() string { return "an error whose matchers panic" }

func (panickingMatcher) Is(error) bool { panic("a matcher a store wrote wrong") }

func (panickingMatcher) As(any) bool { panic("a matcher a store wrote wrong") }

type rows []string

func (this rows) Error() string { return "several rows failed" }

type answersForRows struct{}

func (answersForRows) Error() string { return "the shard that failed" }

func (answersForRows) Is(target error) bool {
	_, asked := target.(rows)
	return asked
}

func TestANilOrLyingCauseNeverPanicsTheKernel(t *testing.T) {
	unpopulated := wrappingCause{inner: (*errs.Fault)(nil)}
	lying := wrappingCause{inner: permissiveMatcher{}}

	t.Run("a fault a store never populated is not a fault", func(t *testing.T) {
		if retryable(unpopulated) {
			t.Fatal("an unpopulated fault was read as a retryable classification")
		}
		if _, found := errs.AsFault(refuseRead(Failure(NotWritten, unpopulated))); found {
			t.Fatal("a refusal exposed a fault its cause never carried")
		}
	})

	t.Run("a matcher that answers for a value it never set decides nothing", func(t *testing.T) {
		for _, door := range doors() {
			if !errors.Is(door.refuse(lying), door.byDefault) {
				t.Fatalf("a lying matcher at %s door is not the door's fail-safe default", door.name)
			}
		}
		if retryable(lying) {
			t.Fatal("a lying matcher was read as a retryable classification")
		}
		if CauseOf(lying) != nil {
			t.Fatal("a lying matcher was read as one of this subsystem's own refusals")
		}
	})

	t.Run("a typed nil of the kernel's own types decides nothing", func(t *testing.T) {
		for _, door := range doors() {
			if !errors.Is(door.refuse(wrappingCause{inner: (*failure)(nil)}), door.byDefault) {
				t.Fatalf("a nil classification at %s door was read as a classification", door.name)
			}
		}
		if CauseOf(wrappingCause{inner: (*refusal)(nil)}) != nil {
			t.Fatal("a nil refusal was read as a refusal")
		}
	})

	t.Run("a matcher that panics is that party's defect and not the kernel's", func(t *testing.T) {
		panicking := panickingMatcher{}
		if _, found := errs.AsFault(refuseAppend(Failure(Refused, panicking))); found {
			t.Fatal("a policy refusal answered errs.AsFault from a cause whose As method only panics")
		}
		for _, door := range doors() {
			if !errors.Is(door.refuse(panicking), door.byDefault) {
				t.Fatalf("a cause whose matchers panic at %s door is not the door's fail-safe default", door.name)
			}
		}
		if retryable(panicking) {
			t.Fatal("a cause whose matchers panic was read as a retryable classification")
		}
		if CauseOf(refuseRead(Failure(Refused, panicking))) != error(panicking) {
			t.Fatal("a cause whose matchers panic did not survive as a cause")
		}
	})

	t.Run("a cause nothing can compare does not end the walk", func(t *testing.T) {
		uncomparable := rows{"the first row", "the second row"}
		refused := refuseAppend(Failure(Refused, errors.Join(uncomparable, answersForRows{})))
		if !errors.Is(refused, uncomparable) {
			t.Fatal("a chain whose first branch is an error nothing can compare answers for nothing behind it, so a decorator's own error stops being matchable the moment a store joins one shard failure it cannot compare")
		}
		if !errors.Is(refuseAppend(Failure(Refused, answersForRows{})), uncomparable) {
			t.Fatal("the matcher that answers for this type is not found on its own, so the case above proves nothing about the walk continuing")
		}
		for _, door := range doors() {
			if !errors.Is(door.refuse(uncomparable), door.byDefault) {
				t.Fatalf("an error nothing can compare at %s door is not the door's fail-safe default", door.name)
			}
		}
		if CauseOf(refuseRead(Failure(NotWritten, uncomparable))) == nil {
			t.Fatal("an error nothing can compare did not survive as a cause")
		}
		if errors.Is(refuseAppend(Failure(Refused, uncomparable)), rows{"a third row"}) {
			t.Fatal("an error nothing can compare answered for a second value of its own type")
		}
	})

	t.Run("the values these guards refuse are still found when they are real", func(t *testing.T) {
		if !retryable(wrappingCause{inner: errs.Retryable().Code(errs.CodeDeadlock).Fault()}) {
			t.Fatal("a populated retryable fault is no longer found, so the nil guard refuses everything")
		}
		if !retryable(asOnlyFault{fault: errs.Retryable().Code(errs.CodeDeadlock).Fault()}) {
			t.Fatal("a fault reachable only through an As method is no longer found")
		}
		if !errors.Is(refuseAppend(wrappingCause{inner: Failure(Conflict, nil)}), ErrConflict) {
			t.Fatal("a classification a decorator wrapped is no longer found, so the guard cost a store its own classification")
		}
		cause := errors.New("the connection was reset")
		if CauseOf(refuseRead(Failure(NotWritten, cause))) != cause {
			t.Fatal("a real refusal no longer answers CauseOf")
		}
	})
}
