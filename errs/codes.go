package errs

import (
	"errors"
	"fmt"
)

// ErrCodeRedeclared reports a second declaration of a code with a different
// [Kind]. It is returned rather than panicked: the wiring decides whether a
// clash is fatal, and in this library the answer is to fail at start-up rather
// than at request time ([[D-021]]).
var ErrCodeRedeclared = errors.New("errs: the code is already declared with a different kind")

// A CodeDef is what a code was declared to mean: its transport class and the
// message a renderer falls back to when no catalogue entry matched.
type CodeDef struct {
	Kind    Kind
	Message string
}

// Codes is the declared vocabulary of one wiring — the codes a process
// recognises, each with its [Kind] and its default message.
//
// It is a value and not a package-level table. Two libraries in one binary may
// each declare "too_long", and with a global registry the one that happens to
// be linked first would decide the other's status code; with a go.work joining
// five modules an init() in the wrong one is invisible besides. So a Codes is
// built where the rest of the application is built, and a component that needs
// it is handed it.
//
// The zero value is usable. A nil *Codes reads as empty rather than panicking,
// so a component whose codes were never wired degrades to "no code is known"
// instead of taking the process down at the first error.
//
// # The finer vocabulary stops at the corpus
//
// The captured corpus in errs/sqlerr speaks four words this vocabulary does not:
// primary_key, not_null, missing_default and bad_type have no [Code] here, and
// deliberately. The parsers in errs/sqlerr coarsen them — primary_key to
// [CodeUnique], not_null and missing_default to [CodeRequired], bad_type to
// [CodeInvalidFormat] — because a public code that says which index was hit is
// a hair away from naming the constraint ([[D-044]]).
//
// The coarsening is not a function anywhere. It is those four words having no
// Code to reach, and TestEveryClassifiableCorpusCaseGetsTheCodeItsClassNames is
// what holds it: the set of corpus classes with no declared code must be
// exactly those four, so a fifth fine word fails rather than passing quietly.
type Codes struct {
	defs map[Code]CodeDef
}

// NewCodes returns an empty vocabulary, for a consumer who wants only its own.
func NewCodes() *Codes { return &Codes{} }

// StandardCodes returns the standard vocabulary: every code this package
// declares, with the kind ROADMAP-errors.md §2 gives it and a default message.
//
// [CodeInternal] carries no message. That is how "500, and no message" holds by
// construction rather than by a case in a renderer somebody may edit
// ([[D-015]]).
func StandardCodes() *Codes {
	c := NewCodes()
	for _, d := range []struct {
		code    Code
		kind    Kind
		message string
	}{
		{CodeUnique, KindConflict, "this value is already taken"},
		{CodeNotUnique, KindConflict, "more than one record matches"},
		{CodeForeignKey, KindConflict, "the record this refers to does not exist"},
		{CodeRestrict, KindConflict, "this record is still referred to"},
		{CodeStaleVersion, KindConflict, "the record was changed by someone else"},
		{CodeExclusion, KindConflict, "this value overlaps one that is already there"},

		{CodeRequired, KindValidation, "this field is required"},
		{CodeCheck, KindValidation, "this value is not allowed"},
		{CodeTooLong, KindValidation, "this value is too long"},
		{CodeOutOfRange, KindValidation, "this value is out of range"},
		{CodeInvalidFormat, KindValidation, "this value is not in the expected format"},
		{CodeInvalidEnum, KindValidation, "this value is not one of the allowed ones"},

		{CodeMalformedBody, KindBadRequest, "the request body could not be read"},
		{CodeInvalidID, KindBadRequest, "the identifier could not be read"},
		{CodeUnknownField, KindBadRequest, "the request names a field that does not exist"},
		{CodeBadQuery, KindBadRequest, "the query could not be read"},

		{CodeNotFound, KindNotFound, "not found"},
		{CodeForbidden, KindForbidden, "not allowed"},
		{CodeUnauthenticated, KindUnauthorized, "authentication is required"},

		{CodeDeadlock, KindRetryable, "the request could not be completed; try again"},
		{CodeSerializationFailure, KindRetryable, "the request could not be completed; try again"},
		{CodeLockTimeout, KindRetryable, "the request could not be completed; try again"},
		{CodeTransactionAborted, KindRetryable, "the request could not be completed; try again"},
		{CodeUnavailable, KindRetryable, "the request could not be completed; try again"},

		{CodeInternal, KindInternal, ""},
	} {
		// Every entry above is distinct, so Add cannot refuse; a typo that made
		// two of them collide would show up as a start-up failure in the wiring
		// rather than as a silently overwritten kind.
		if err := c.Add(d.code, d.kind, d.message); err != nil {
			panic(err)
		}
	}
	return c
}

// Add declares a code with its kind and its default message.
//
// Declaring the same code twice with the same kind is allowed and the first
// message wins — a second library naming a code the application already knows
// is not an error, and overriding the text is the [MessageSource]'s job, not
// this one's. Declaring it with a different kind returns an error wrapping
// [ErrCodeRedeclared] and leaves the existing declaration untouched: a caller
// told its declaration was refused must not then find the table changed.
func (c *Codes) Add(code Code, kind Kind, message string) error {
	if c.defs == nil {
		c.defs = map[Code]CodeDef{}
	}
	if have, ok := c.defs[code]; ok {
		if have.Kind != kind {
			return fmt.Errorf("errs: %q is %s, not %s: %w", code, have.Kind, kind, ErrCodeRedeclared)
		}
		return nil
	}
	c.defs[code] = CodeDef{Kind: kind, Message: message}
	return nil
}

// KindOf answers the transport class of a declared code.
func (c *Codes) KindOf(code Code) (Kind, bool) {
	if c == nil {
		return KindInternal, false
	}
	d, ok := c.defs[code]
	return d.Kind, ok
}

// MessageFor answers the code's default message. A declared code with no
// message answers ("", false), which is [CodeInternal]'s whole behaviour.
func (c *Codes) MessageFor(code Code) (string, bool) {
	if c == nil {
		return "", false
	}
	d, ok := c.defs[code]
	if !ok || d.Message == "" {
		return "", false
	}
	return d.Message, true
}
