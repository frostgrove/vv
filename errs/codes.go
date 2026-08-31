package errs

import (
	"errors"
	"fmt"
)

var ErrCodeRedeclared = errors.New("errs: the code is already declared with a different kind")

type CodeDef struct {
	Kind    Kind
	Message string
}

type Codes struct {
	defs map[Code]CodeDef
}

func NewCodes() *Codes { return &Codes{} }

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
		{CodeConflict, KindConflict, "the request conflicts with the current state"},

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

		{CodeTooLarge, KindTooLarge, "the request body is too large"},

		{CodeNotFound, KindNotFound, "not found"},
		{CodeMethodNotAllowed, KindMethodNotAllowed, "this path does not answer that method"},
		{CodeForbidden, KindForbidden, "not allowed"},
		{CodeUnauthenticated, KindUnauthorized, "authentication is required"},

		{CodeDeadlock, KindRetryable, "the request could not be completed; try again"},
		{CodeSerializationFailure, KindRetryable, "the request could not be completed; try again"},
		{CodeLockTimeout, KindRetryable, "the request could not be completed; try again"},
		{CodeTransactionAborted, KindRetryable, "the request could not be completed; try again"},
		{CodeUnavailable, KindRetryable, "the request could not be completed; try again"},

		{CodeSchemaNotReady, KindInternal, ""},
		{CodeInternal, KindInternal, ""},
	} {
		if err := c.Add(d.code, d.kind, d.message); err != nil {
			panic(err)
		}
	}
	return c
}

func (this *Codes) Add(code Code, kind Kind, message string) error {
	if this.defs == nil {
		this.defs = map[Code]CodeDef{}
	}
	if have, ok := this.defs[code]; ok {
		if have.Kind != kind {
			return fmt.Errorf("errs: %q is %s, not %s: %w", code, have.Kind, kind, ErrCodeRedeclared)
		}
		return nil
	}
	this.defs[code] = CodeDef{Kind: kind, Message: message}
	return nil
}

func (this *Codes) KindOf(code Code) (Kind, bool) {
	if this == nil {
		return KindInternal, false
	}
	d, ok := this.defs[code]
	return d.Kind, ok
}

func (this *Codes) MessageFor(code Code) (string, bool) {
	if this == nil {
		return "", false
	}
	d, ok := this.defs[code]
	if !ok || d.Message == "" {
		return "", false
	}
	return d.Message, true
}
