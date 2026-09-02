package health

import (
	"context"
	"time"
	"unicode/utf8"
)

type Importance string

const (
	Required      Importance = "required"
	Degrading     Importance = "degrading"
	Informational Importance = "informational"
	Disabled      Importance = "disabled"
)

func (this Importance) Known() bool {
	switch this {
	case Required, Degrading, Informational, Disabled:
		return true
	}
	return false
}

type Status string

const (
	StatusLive     Status = "live"
	StatusReady    Status = "ready"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

type State string

const (
	StatePassing  State = "passing"
	StateFailing  State = "failing"
	StateDisabled State = "disabled"
)

type Probe interface {
	Check(ctx context.Context) error
}

type ProbeFunc func(ctx context.Context) error

func (this ProbeFunc) Check(ctx context.Context) error { return this(ctx) }

// Importance is the composition root's to set, not the checker's: the same
// Redis ping is required in the session service and degrading in the report
// exporter, and only the program being assembled knows which. See [[D-091]].
type Contribution struct {
	Name string

	Code string

	Importance Importance

	Timeout time.Duration

	Probe Probe
}

const MaxMessageBytes = 256

// Report is the public projection: a status and the stable codes of the checks
// that produced it, and nothing an unauthenticated caller could use to map the
// deployment. A contribution with no Code moves the status without naming
// itself.
type Report struct {
	Status Status `json:"status"`

	Codes []string `json:"codes,omitempty"`
}

type Detail struct {
	Status Status `json:"status"`

	ObservedAt time.Time `json:"observedAt"`

	Checks []CheckDetail `json:"checks"`
}

type CheckDetail struct {
	Name string `json:"name"`

	Code string `json:"code,omitempty"`

	Importance Importance `json:"importance"`

	State State `json:"state"`

	Message string `json:"message,omitempty"`

	Took time.Duration `json:"took"`
}

func truncate(message string) string {
	if len(message) <= MaxMessageBytes {
		return message
	}
	cut := MaxMessageBytes
	for cut > 0 && !utf8.RuneStart(message[cut]) {
		cut--
	}
	return message[:cut]
}
