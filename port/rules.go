package port

import "github.com/shardit-io/vv/crud/query"

// Rules are the parts of a handler's configuration that say nothing about a
// transport: what a client may ask for, what it may not choose, and how much of
// it may arrive at once.
//
// It lives here rather than beside an HTTP binding because nothing in it is
// HTTP, and a copy in crudhttp would be a package the gRPC module had to import
// to share it ([[D-045]], [[D-058]]).
//
// It is embedded in each binding's own options struct, beside the fields that do
// mention a request type — the error handler, the presenter, the scope and the
// two before-hooks, which cannot be shared because their signatures are the
// framework's. Everything here could be, and was not: each binding carried its
// own copy of these fields and the two methods below, and a rule added to one of
// them was a rule the others silently did not have.
//
// **Four of the five reach every transport; MaxBody reaches the three HTTP
// bindings only.** gRPC bounds a message at the server with its own
// MaxRecvMsgSize, before a handler runs and at a number the operator set, so
// crudgrpc embeds this struct, offers no MaxBody option and never reads the
// field ([[D-063]], and [[FL-013]] carries the row). The field stays here rather
// than moving to an HTTP-side struct because splitting it out would re-create
// the per-binding copies this type exists to end — three copies instead of one
// field the fourth transport ignores.
//
// The option *constructors* stay in the bindings. Each binding's Option is its
// own type — three parameters, so the *constructor* infers them ([[D-045]]);
// each option still spells all three, because Go infers from a function's own
// arguments and an option's arguments name none of them —
// and a shared constructor could not return one.
type Rules struct {
	Query         *query.Config
	ReadOnly      bool
	AllowClientID bool
	MaxBulk       int
	// MaxBody is honoured by the HTTP bindings only; see the note above.
	MaxBody int
}

// DefaultMaxBulk is how many ids one bulk delete carries when nobody said.
//
// Non-zero, and that is the whole point. It used to be zero meaning unlimited,
// so a bulk delete's cardinality was bounded only by the request body — and every
// id becomes a bound parameter, which PostgreSQL refuses past 65535 for the whole
// statement. The honest 400 then arrived from the driver, as a 500, after the
// statement was built.
//
// Generous, because this is the ceiling that stops a statement no engine will
// accept rather than a page size, and it matches query.Config's own list cap.
const DefaultMaxBulk = 1024

// BulkCap is how many ids this handler's bulk delete accepts. Zero or less in
// the field means [DefaultMaxBulk]; there is no spelling for "unlimited".
//
// A method and not a defaulted field, so the four transports cannot disagree
// about what an unset MaxBulk means — which is how they came to agree it meant
// no cap at all.
func (r Rules) BulkCap() int {
	if r.MaxBulk > 0 {
		return r.MaxBulk
	}
	return DefaultMaxBulk
}

// Service translates the rules that belong to the service rather than to the
// transport into the options the default service takes.
func (r Rules) Service() []ServiceOption {
	var out []ServiceOption
	if r.Query != nil {
		out = append(out, WithQuery(r.Query))
	}
	if r.AllowClientID {
		out = append(out, AllowClientID())
	}
	return out
}

// RefuseServiceOptions panics when a service-shaped option was handed to a
// constructor that was given a finished service.
//
// A panic and not a silent no-op, named after the option so the message is the
// fix. Serving means the rules are the service's; an ignored WithQuery would
// leave an API accepting everything while its author believed it was bounded,
// and that is exactly the failure [[D-021]] says must happen at start-up.
//
// who is the constructor's qualified name, so the message names the call site
// in the caller's own vocabulary rather than this package's.
func (r Rules) RefuseServiceOptions(who string) {
	switch {
	case r.Query != nil:
		panic(who + ": WithQuery configures the service, which is already built — pass port.WithQuery to it instead")
	case r.AllowClientID:
		panic(who + ": AllowClientID configures the service, which is already built — pass port.AllowClientID to it instead")
	}
}
