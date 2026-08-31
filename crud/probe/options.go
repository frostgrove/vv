package probe

import (
	"context"
	"time"

	"github.com/frostgrove/vv/crud"
)

const (
	DefaultMaxConstraints = 16

	DefaultMaxRows = 50

	DefaultTimeout = 250 * time.Millisecond

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

type Option func(*config)

func WithSavepoints() Option { return func(c *config) { c.savepoints = true } }

func WithScope(fn func(context.Context) (crud.Predicate, error)) Option {
	return func(c *config) { c.scope = fn }
}

func WithValues() Option { return func(c *config) { c.values = true } }

func CodeOnly() Option { return func(c *config) { c.codeOnly = true } }

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

func WithMaxConstraints(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxConstraints = n
		}
	}
}

func WithMaxRows(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxRows = n
		}
	}
}

func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.timeout = d
		}
	}
}

func WithMaxSavepoints(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxSavepoints = n
		}
	}
}
