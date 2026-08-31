package vvdb

import (
	"fmt"
	"io"
	"strings"
)

type redactedError struct {
	operation string
	cause     error
}

func (this *redactedError) Error() string    { return this.operation + ": details redacted" }
func (this *redactedError) Unwrap() error    { return this.cause }
func (this *redactedError) GoString() string { return this.Error() }
func (this *redactedError) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, this.Error())
}

func RedactError(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	operation = strings.TrimSpace(operation)
	if operation == "" {
		operation = "vvdb operation failed"
	}
	return &redactedError{operation: operation, cause: cause}
}
