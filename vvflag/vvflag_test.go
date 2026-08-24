package vvflag

import (
	"errors"
	"testing"
)

type Port int // a named type, which a type switch would not recognise

func TestAbsentIsNotTheSameAnswerAsMalformed(t *testing.T) {
	// The defect this pins: the original returned (value, false) for both, and
	// not even the same value — absent gave the default, malformed gave zero.
	// So --port=abc started a server on port 0 and the caller could not tell.
	absent, err := Parse([]string{"--other", "1"}, "port", 8080)
	if !errors.Is(err, ErrAbsent) {
		t.Fatalf("a missing flag should report ErrAbsent, got %v", err)
	}
	if absent != 8080 {
		t.Fatalf("a missing flag should hand back the default, got %d", absent)
	}

	bad, err := Parse([]string{"--port=abc"}, "port", 8080)
	if err == nil {
		t.Fatal("--port=abc parsed as a number, which it is not")
	}
	if errors.Is(err, ErrAbsent) {
		t.Fatal("a malformed flag is being reported as a missing one")
	}
	if bad != 8080 {
		t.Fatalf("a malformed flag should not hand back a zero value, got %d", bad)
	}
}

func TestANamedTypeIsSupported(t *testing.T) {
	// The original switched on the dynamic type, so Port(8080) matched no case
	// and fell to default — silently unsupported, for every user-defined type.
	got, err := Parse([]string{"--port", "9090"}, "port", Port(8080))
	if err != nil {
		t.Fatalf("a named integer type should parse: %v", err)
	}
	if got != Port(9090) {
		t.Fatalf("port = %d, want 9090", got)
	}
}

func TestANegativeValueIsReachable(t *testing.T) {
	// The original refused to read the next argument when it began with a dash,
	// so --offset -1 was reported as absent rather than as -1.
	got, err := Parse([]string{"--offset", "-1"}, "offset", 0)
	if err != nil {
		t.Fatalf("--offset -1: %v", err)
	}
	if got != -1 {
		t.Fatalf("offset = %d, want -1", got)
	}
}

func TestTheEqualsFormAndTheSpaceFormAgree(t *testing.T) {
	for _, args := range [][]string{{"--name=ann"}, {"--name", "ann"}} {
		got, err := Parse(args, "name", "")
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got != "ann" {
			t.Fatalf("%v gave %q, want \"ann\"", args, got)
		}
	}
}

func TestABoolFlagStandsAlone(t *testing.T) {
	on, err := Parse([]string{"--verbose"}, "verbose", false)
	if err != nil || !on {
		t.Fatalf("--verbose should be true, got %v (%v)", on, err)
	}
	off, err := Parse([]string{"--verbose=false"}, "verbose", true)
	if err != nil || off {
		t.Fatalf("--verbose=false should be false, got %v (%v)", off, err)
	}
	// A bool flag must not swallow the next argument.
	if v, err := Parse([]string{"--verbose", "positional"}, "verbose", false); err != nil || !v {
		t.Fatalf("--verbose consumed the argument after it: %v (%v)", v, err)
	}
}

func TestDoubleDashEndsTheFlags(t *testing.T) {
	_, err := Parse([]string{"--", "--port", "9090"}, "port", 8080)
	if !errors.Is(err, ErrAbsent) {
		t.Fatalf("a flag after -- is positional, not a flag: %v", err)
	}
}

func TestAFlagWithNothingAfterItIsAbsentRatherThanEmpty(t *testing.T) {
	_, err := Parse([]string{"--name"}, "name", "fallback")
	if !errors.Is(err, ErrAbsent) {
		t.Fatalf("a value-taking flag with no value should be absent, got %v", err)
	}
}

func TestOrFoldsAbsenceIntoTheDefault(t *testing.T) {
	got, err := Or([]string{}, "port", 8080)
	if err != nil || got != 8080 {
		t.Fatalf("Or on a missing flag should be (8080, nil), got (%d, %v)", got, err)
	}
	// …but not malformedness.
	if _, err := Or([]string{"--port=abc"}, "port", 8080); err == nil {
		t.Fatal("Or swallowed a malformed value; only absence is folded away")
	}
}

func TestParseReadsTheArgumentsItIsGiven(t *testing.T) {
	// The control: the original read os.Args directly, so this test could not
	// exist without mutating process-global state. If Parse ever goes back to
	// os.Args, this fails.
	if _, err := Parse([]string{"--port", "1"}, "port", 0); err != nil {
		t.Fatalf("Parse should read its argument slice: %v", err)
	}
}

func TestAnUnsupportedKindIsAnError(t *testing.T) {
	type conf struct{ A int }
	if _, err := Parse([]string{"--c", "{}"}, "c", conf{}); err == nil {
		t.Fatal("a struct is not something a flag can be coerced into")
	}
}
