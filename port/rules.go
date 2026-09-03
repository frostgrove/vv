package port

import "github.com/frostgrove/vv/crud/query"

type Rules struct {
	Query *query.Config

	QueryVariants map[string]*query.Config
	QuerySelector QuerySelector
	ReadOnly      bool
	AllowClientID bool
	MaxBulk       int

	// Expose names the operations mounted, and is empty for all of them.
	// ReadOnly is the same switch for the commonest case; setting both is a
	// declaration that says two things, and RefuseContradictions refuses it.
	Expose Operations

	MaxBody int
}

const DefaultMaxBulk = 1024

func (this *Rules) BulkCap() int {
	if this == nil {
		return DefaultMaxBulk
	}
	if this.MaxBulk > 0 {
		return this.MaxBulk
	}
	return DefaultMaxBulk
}

func (this *Rules) Mounted() Operations {
	if this == nil {
		return AllOperations
	}
	if this.Expose != 0 {
		return this.Expose
	}
	if this.ReadOnly {
		return Reads
	}
	return AllOperations
}

func (this *Rules) RefuseContradictions(who string) {
	if this == nil {
		return
	}
	if this.ReadOnly && this.Expose != 0 {
		panic(who + ": ReadOnly and Expose name different sets of operations (" +
			Reads.String() + " against " + this.Expose.String() + ") — state the set once")
	}
}

func (this *Rules) Service() []ServiceOption {
	if this == nil {
		return nil
	}
	var out []ServiceOption
	if this.QuerySelector != nil || this.QueryVariants != nil {
		out = append(out, WithQueryFor(this.Query, this.QueryVariants, this.QuerySelector))
	} else if this.Query != nil {
		out = append(out, WithQuery(this.Query))
	}
	if this.AllowClientID {
		out = append(out, AllowClientID())
	}
	return out
}

func (this *Rules) RefuseServiceOptions(who string) {
	if this == nil {
		return
	}
	switch {
	case this.Query != nil || this.QuerySelector != nil || this.QueryVariants != nil:
		panic(who + ": WithQuery configures the service, which is already built — pass port.WithQuery to it instead")
	case this.AllowClientID:
		panic(who + ": AllowClientID configures the service, which is already built — pass port.AllowClientID to it instead")
	}
}
