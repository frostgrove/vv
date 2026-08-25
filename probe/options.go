package probe

import (
	"context"
	"time"

	"github.com/shardit-io/vv/crud"
)

// The caps, as numbers. A cap without a number is not a cap — see the package
// doc for what each number is measured against.
const (
	// DefaultMaxConstraints bounds the terms in one probe statement.
	DefaultMaxConstraints = 16
	// DefaultMaxRows bounds the rows of a batch one probe covers.
	DefaultMaxRows = 50
	// DefaultTimeout bounds the probe statement, and nothing else. The write it
	// explains has already failed and the client is waiting for the answer.
	DefaultTimeout = 250 * time.Millisecond
	// DefaultMaxSavepoints bounds the savepoints claimed against one
	// transaction: half of PostgreSQL's 64-entry subxid cliff.
	DefaultMaxSavepoints = 32
)

type config struct {
	savepoints     bool
	scope          func(context.Context) (crud.Predicate, error)
	values         bool
	codeOnly       bool
	skip           map[string]bool
	maxConstraints int
	maxRows        int
	timeout        time.Duration
	maxSavepoints  int
}

func defaults() config {
	return config{
		maxConstraints: DefaultMaxConstraints,
		maxRows:        DefaultMaxRows,
		timeout:        DefaultTimeout,
		maxSavepoints:  DefaultMaxSavepoints,
	}
}

// An Option adjusts what [Full] does.
type Option func(*config)

// WithSavepoints lets the probe run inside a transaction on an engine that
// poisons one. It costs two extra statements per write — a SAVEPOINT before and
// a RELEASE after every success, failures included — and it is the only part of
// this package that touches the happy path, which is why it is opt-in.
//
// It applies only to a transaction vv opened. A foreign transaction is never
// given a savepoint, whatever this says.
func WithSavepoints() Option { return func(c *config) { c.savepoints = true } }

// WithScope narrows the probe's unique terms with the predicate a security
// policy narrows reads with, so the probe does not confirm the existence of a
// row the caller could not have read ([[D-008]]).
//
// It takes the function shape security.Policy.Scope already has. Writes carry no
// transport scope, so the predicate has to come from the policy rather than from
// crud.WithScope.
//
// The narrowing reaches this repository's own table and nothing else, because a
// predicate over the model's own fields is the only one that compiles. A
// foreign-key term reads the parent and a restrict term reads the child; use
// [Skip] where that matters.
func WithScope(fn func(context.Context) (crud.Predicate, error)) Option {
	return func(c *config) { c.scope = fn }
}

// WithValues puts the offending value into the violation's Params, where a
// message template can reach it as {value}.
//
// Off by default. "this email is already taken" tells a client what to fix;
// "test@example.com is already taken" tells whoever posted the form that the
// address is registered, which is the oracle the default declines to widen.
func WithValues() Option { return func(c *config) { c.values = true } }

// CodeOnly drops the path and keeps the code, for an endpoint where naming the
// field is already too much. It applies to what the probe adds and to the path
// the probe would have filled in on the driver's own violation.
func CodeOnly() Option { return func(c *config) { c.codeOnly = true } }

// Skip takes constraints out by name. The dangerous case for the oracle is a
// unique key over a column the caller cannot see, and it is nameable.
//
// A name the catalog does not have is a declaration-time refusal, not a silent
// no-op: a renamed constraint would otherwise turn the control off on the deploy
// that renamed it.
func Skip(names ...string) Option {
	return func(c *config) {
		if c.skip == nil {
			c.skip = map[string]bool{}
		}
		for _, n := range names {
			c.skip[n] = true
		}
	}
}

// WithMaxConstraints replaces [DefaultMaxConstraints]. A value below one leaves
// the default alone rather than turning the probe into a no-op that reports
// itself as partial forever.
func WithMaxConstraints(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxConstraints = n
		}
	}
}

// WithMaxRows replaces [DefaultMaxRows].
func WithMaxRows(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxRows = n
		}
	}
}

// WithTimeout replaces [DefaultTimeout]. It bounds the probe statement only.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithMaxSavepoints replaces [DefaultMaxSavepoints], counted per transaction
// rather than per repository.
func WithMaxSavepoints(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxSavepoints = n
		}
	}
}
