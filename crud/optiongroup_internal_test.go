package crud

import (
	"reflect"
	"testing"
)

func TestEveryQueryOptionIsClassifiedByEveryVerb(t *testing.T) {
	groups := map[string]OptionGroup{
		"a read":            ReadOptions,
		"a write":           MutationOptions,
		"an aggregate read": AggregateOptions,
		"a preload":         PreloadOptions,
	}

	options := reflect.TypeOf(Options{})
	for i := range options.NumField() {
		field := options.Field(i).Name
		if _, ok := optionSpellings[field]; !ok {
			t.Errorf("Options.%s has no caller-facing spelling, so a refusal would blame a name nobody wrote", field)
		}
		for verb, group := range groups {
			if group.allow[field] || group.reason(field) != "" {
				continue
			}
			t.Errorf("Options.%s is neither honoured by %s nor refused in its own words there", field, verb)
		}
	}
}

func TestAnOptionIsEitherHonouredOrRefusedButNeverBoth(t *testing.T) {
	for verb, group := range map[string]OptionGroup{
		"a read":            ReadOptions,
		"a write":           MutationOptions,
		"an aggregate read": AggregateOptions,
		"a preload":         PreloadOptions,
	} {
		for field := range group.allow {
			if group.reason(field) != "" {
				t.Errorf("%s both honours Options.%s and explains why it cannot", verb, field)
			}
		}
	}
}
