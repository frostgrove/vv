package event

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAnOutcomeOutsideTheVocabularyNormalises(t *testing.T) {
	cause := errors.New("the driver reported 40001")

	t.Run("an outcome outside the seven becomes unclassified before it travels", func(t *testing.T) {
		for value := range 256 {
			held, isFailure := Failure(Outcome(value), cause).(*failure)
			if !isFailure {
				t.Fatalf("a failure classified %d is not a classification at all", value)
			}
			want := Outcome(value)
			if value > int(Refused) {
				want = Unclassified
			}
			if held.outcome != want {
				t.Fatalf("a failure classified %d holds the classification %d, so a value a store computed travels unnormalised and the map has a row it cannot reach",
					value, uint8(held.outcome))
			}
		}
	})

	t.Run("a classification renders a phrase and never the store's number", func(t *testing.T) {
		phrases := map[string]int{}
		for value := range 256 {
			rendered := Outcome(value).String()
			if rendered == "" {
				t.Fatalf("the classification %d renders nothing", value)
			}
			if strings.ContainsAny(rendered, "0123456789") {
				t.Fatalf("the classification %d renders %q, and a store's number is data", value, rendered)
			}
			if value > int(Refused) && rendered != Unclassified.String() {
				t.Fatalf("the classification %d renders %q rather than the unclassified phrase", value, rendered)
			}
			if value <= int(Refused) {
				phrases[rendered]++
			}
		}
		if len(phrases) != int(Refused)+1 {
			t.Fatalf("the seven classifications render %d distinct phrases, so two of them read as one", len(phrases))
		}
	})

	t.Run("every classification maps the same way at both doors", func(t *testing.T) {
		for _, door := range doors() {
			for _, outcome := range classifiedOutcomes() {
				refused := door.refuse(Failure(outcome.outcome, cause))
				if !errors.Is(refused, outcome.at(door)) {
					t.Fatalf("a %s failure at %s door is %v and the map says %v", outcome.name, door.name, refused, outcome.at(door))
				}
			}
		}
		if errors.Is(refuseAppend(Failure(Unclassified, cause)), ErrBackend) {
			t.Fatal("an unclassified append failure reads as the store having failed, and the caller is then told a write certainly did not land")
		}
		if errors.Is(refuseRead(Failure(Unclassified, cause)), ErrUncertain) {
			t.Fatal("an unclassified read failure reads as an unconfirmed write, and a read has written nothing to be uncertain about")
		}
	})

	t.Run("a door that was handed no failure refuses nothing", func(t *testing.T) {
		for _, door := range doors() {
			if refused := door.refuse(nil); refused != nil {
				t.Fatalf("%s door turned an operation that worked into %v, so every successful append and every successful read is returned as an error", door.name, refused)
			}
		}
	})

	t.Run("a classification that reached the map unnormalised takes the door's default", func(t *testing.T) {
		for _, door := range doors() {
			refused := door.refuse(&failure{outcome: Outcome(200), cause: cause})
			if !errors.Is(refused, door.byDefault) {
				t.Fatalf("a classification outside the seven at %s door is %v, and the fail-safe default is what a value compiled against a later vocabulary must take",
					door.name, refused)
			}
		}
	})

	t.Run("a classification a decorator wrapped is still the store's", func(t *testing.T) {
		for _, door := range doors() {
			for _, outcome := range classifiedOutcomes() {
				forwarded := fmt.Errorf("the quota decorator forwarded this: %w", Failure(outcome.outcome, cause))
				if !errors.Is(door.refuse(forwarded), outcome.at(door)) {
					t.Fatalf("a %s failure a decorator wrapped is not %v at %s door, so a store loses its classification to the decorator over it",
						outcome.name, outcome.at(door), door.name)
				}
			}
		}
	})

	t.Run("a classification renders no part of the cause it carries", func(t *testing.T) {
		loud := errors.New(`pq: password authentication failed for user "admin"`)
		for _, outcome := range classifiedOutcomes() {
			rendered := Failure(outcome.outcome, loud).Error()
			if strings.Contains(rendered, "password") {
				t.Fatalf("a %s failure renders %q, so a decorator that logs what it is forwarding prints the driver's text", outcome.name, rendered)
			}
			if !strings.Contains(rendered, outcome.outcome.String()) {
				t.Fatalf("a %s failure renders %q and does not name its own classification", outcome.name, rendered)
			}
		}
	})
}
