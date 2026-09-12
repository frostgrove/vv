package receipt_test

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/receipt"
)

func TestAKeyRendersNothingOfItsValue(t *testing.T) {
	const carried = "req-7f3a-victor-quebec"
	key := keyed(t, carried)

	logged := &strings.Builder{}
	slog.New(slog.NewTextHandler(logged, nil)).Info("the operation was refused", "key", key)

	for _, one := range []struct {
		what     string
		rendered string
	}{
		{"String", key.String()},
		{"a %v", fmt.Sprintf("%v", key)},
		{"a %s", fmt.Sprintf("%s", key)},
		{"a wrapped refusal", fmt.Errorf("%w: the key %v", receipt.ErrSpec, key).Error()},
		{"a log line", logged.String()},
	} {
		if !strings.Contains(one.rendered, "[operation key]") {
			t.Errorf("%s rendered %q where a key renders [operation key]", one.what, one.rendered)
		}
		if strings.Contains(one.rendered, carried) {
			t.Errorf("%s rendered %q, and the value of a request identity reaches no message", one.what, one.rendered)
		}
	}

	if key.Value() != carried {
		t.Errorf("Value answered %q where a ledger binds the value it was given", key.Value())
	}
	if key.Zero() {
		t.Error("a key NewKey answered reports itself as the zero value")
	}
	if !(receipt.Key{}).Zero() {
		t.Error("the zero Key does not report itself as one, and every door refuses it by asking")
	}

	// A Key that were a defined string type would compare equal to an untyped
	// constant at every door it crosses, which is the comparison a caller writes
	// instead of minting one through the rule.
	if kind := reflect.TypeFor[receipt.Key]().Kind(); kind != reflect.Struct {
		t.Errorf("Key is a %s, so a bare string compares to one and NewKey's rule is optional", kind)
	}

	rejected := []struct {
		what string
		raw  string
	}{
		{"an empty key", ""},
		{"a key over the bound", strings.Repeat("k", event.MaxNameBytes+1)},
		{"a key that is not valid UTF-8", "req-\xff\xfe-quebec"},
		{"a key carrying a control character", "req-7f3a\x00victor"},
		{"a key carrying a bracket", "req-[operation key]-victor"},
	}
	for _, one := range rejected {
		built, err := receipt.NewKey(one.raw)
		if !errors.Is(err, receipt.ErrSpec) {
			t.Fatalf("%s answered %v where the rule it broke is ErrSpec", one.what, err)
		}
		if !built.Zero() {
			t.Fatalf("%s answered a key that is not the zero value, so a caller that ignored the error holds one", one.what)
		}
		for _, other := range rejected {
			if other.raw == "" || !strings.Contains(err.Error(), other.raw) {
				continue
			}
			t.Fatalf("%s rendered the text it refused into %q, and a refusal names the rule that was broken and never the data that broke it", one.what, err)
		}
	}

	if len(rejected) != 5 {
		t.Fatalf("%d inputs were refused, and the kernel's identifier rule has five clauses", len(rejected))
	}

	t.Run("the control: five clauses, not one catch-all firing five times", func(t *testing.T) {
		named := map[string]string{}
		for _, one := range rejected {
			_, err := receipt.NewKey(one.raw)
			if earlier, said := named[err.Error()]; said {
				t.Fatalf("%s and %s were refused with the same sentence, so one clause is answering for two and the arm above would pass with four of the five gone", one.what, earlier)
			}
			named[err.Error()] = one.what
		}
		if _, err := receipt.NewKey(strings.Repeat("k", event.MaxNameBytes)); err != nil {
			t.Fatalf("a key exactly at the bound was refused with %v, so the bound is off by one and the arm above measures a different rule", err)
		}
	})
}
