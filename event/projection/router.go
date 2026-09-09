package projection

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/frostgrove/vv/event"
)

// What to do with an envelope of a family this router covers no route of. A type
// of a family it DOES route and no route claims is never skipped under either:
// it is ErrUnrouted, and that is the forgotten registration.
type Foreign uint8

const (
	SkipForeign Foreign = iota
	RefuseForeign
)

type routeKey struct{ family, name string }

// The coverage is inferred from the routes rather than configured, so it cannot
// drift from them: every On declares its fact's family, and Ignore declares
// nothing — a family this router routes nothing of stays foreign, which is the
// ordinary case for a global log carrying every aggregate's events.
//
// There is no bound on how many routes it holds. It keeps one closure per On and
// one entry per Ignore, so its size is the source code of the program that built
// it, and a limit here would be a limit on how many event types an application
// may model.
type Router struct {
	foreign Foreign

	mutex    sync.Mutex
	routes   map[routeKey]func(context.Context, event.Envelope) error
	ignored  map[routeKey]struct{}
	families map[string]struct{}

	sealed  atomic.Bool
	skipped atomic.Uint64
}

var _ Handler = (*Router)(nil)

func NewRouter(foreign Foreign) *Router {
	return &Router{
		foreign:  foreign,
		routes:   map[routeKey]func(context.Context, event.Envelope) error{},
		ignored:  map[routeKey]struct{}{},
		families: map[string]struct{}{},
	}
}

func On[S, ID, E any](router *Router, fact *event.Fact[S, ID, E], apply func(context.Context, E, event.Envelope) error) {
	if err := TryOn(router, fact, apply); err != nil {
		panic(err)
	}
}

func TryOn[S, ID, E any](router *Router, fact *event.Fact[S, ID, E], apply func(context.Context, E, event.Envelope) error) error {
	if router == nil {
		return fmt.Errorf("%w: a route is declared on a router and this call names none", event.ErrDeclaration)
	}
	if fact == nil {
		return fmt.Errorf("%w: a route is declared for a fact and this call names none", event.ErrDeclaration)
	}
	if apply == nil {
		return fmt.Errorf("%w: %q declares no applier", event.ErrDeclaration, fact.Name())
	}
	route := func(ctx context.Context, envelope event.Envelope) error {
		value, err := fact.Read(envelope)
		if err != nil {
			return err
		}
		return apply(ctx, value, envelope)
	}
	return router.declare(fact.Family(), fact.Name(), route)
}

// A covered family's types this projection deliberately does not want. By name
// rather than by fact, so a type this build declares no fact for can be named
// too — and so a renamed wire type leaves the declaration stale in the safe
// direction: the old name stays ignored and the new one halts.
func Ignore(router *Router, family string, types ...string) {
	if err := TryIgnore(router, family, types...); err != nil {
		panic(err)
	}
}

func TryIgnore(router *Router, family string, types ...string) error {
	if router == nil {
		return fmt.Errorf("%w: a type is ignored on a router and this call names none", event.ErrDeclaration)
	}
	if broken := refusedName(family); broken != "" {
		return fmt.Errorf("%w: a stream family %s", event.ErrDeclaration, broken)
	}
	for _, name := range types {
		if broken := refusedName(name); broken != "" {
			return fmt.Errorf("%w: a wire type name on the family %q %s", event.ErrDeclaration, family, broken)
		}
	}
	return router.ignore(family, types)
}

func (this *Router) declare(family, name string, route func(context.Context, event.Envelope) error) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.sealed.Load() {
		return fmt.Errorf("%w: %q on the family %q arrived after this router had applied a page, and a route a projection has already run without is not one it can be given now", event.ErrDeclaration, name, family)
	}
	if err := this.unclaimed(family, name); err != nil {
		return err
	}
	this.routes[routeKey{family: family, name: name}] = route
	this.families[family] = struct{}{}
	return nil
}

// Two passes, so a list whose fourth name is already declared leaves the first
// three undeclared rather than half of the declaration standing.
func (this *Router) ignore(family string, types []string) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.sealed.Load() {
		return fmt.Errorf("%w: the family %q ignored a type after this router had applied a page", event.ErrDeclaration, family)
	}
	for _, name := range types {
		if err := this.unclaimed(family, name); err != nil {
			return err
		}
	}
	for _, name := range types {
		this.ignored[routeKey{family: family, name: name}] = struct{}{}
	}
	return nil
}

func (this *Router) unclaimed(family, name string) error {
	key := routeKey{family: family, name: name}
	if _, routed := this.routes[key]; routed {
		return fmt.Errorf("%w: the family %q already routes %q", event.ErrDeclaration, family, name)
	}
	if _, ignored := this.ignored[key]; ignored {
		return fmt.Errorf("%w: the family %q already ignores %q", event.ErrDeclaration, family, name)
	}
	return nil
}

// Sealing on the first Apply is the aggregate's own idiom, and it is what makes
// the maps safe to read here without a lock: they are written only under the
// mutex and only while unsealed, the seal is taken under that same mutex, and a
// registration arriving afterwards is refused rather than racing a page.
func (this *Router) Apply(ctx context.Context, batch Batch) error {
	this.seal()
	for _, envelope := range batch.Envelopes {
		key := routeKey{family: envelope.Stream.Family, name: envelope.Type}
		route, routed := this.routes[key]
		switch {
		case routed:
			if err := route(ctx, envelope); err != nil {
				return err
			}
		case this.claims(key):
			continue
		default:
			if err := this.foreignTo(envelope, key); err != nil {
				return err
			}
		}
	}
	return nil
}

func (this *Router) claims(key routeKey) bool {
	_, ignored := this.ignored[key]
	return ignored
}

func (this *Router) foreignTo(envelope event.Envelope, key routeKey) error {
	if _, covered := this.families[key.family]; covered || this.foreign == RefuseForeign {
		return this.unrouted(envelope)
	}
	this.skipped.Add(1)
	return nil
}

// The type name is a store's own data and is held to the kernel's identifier
// rule before it is rendered, exactly as the reading seam holds it: a name
// carrying a bracket or a control character would otherwise close the field this
// refusal opened and write a second one after it.
func (this *Router) unrouted(envelope event.Envelope) error {
	if broken := refusedName(envelope.Type); broken != "" {
		return fmt.Errorf("%w: %s recorded a type name that %s", ErrUnrouted, envelope.Stream, broken)
	}
	return fmt.Errorf("%w: %q on %s", ErrUnrouted, envelope.Type, envelope.Stream)
}

func (this *Router) seal() {
	if this.sealed.Load() {
		return
	}
	this.mutex.Lock()
	this.sealed.Store(true)
	this.mutex.Unlock()
}

// Envelopes skipped as foreign. There is no second counter, because inside a
// covered family this router skips nothing.
func (this *Router) Skipped() uint64 { return this.skipped.Load() }

const (
	nameEmpty     = "is empty"
	nameOverCap   = "is longer than its limit"
	nameNotUTF8   = "is not valid UTF-8"
	nameControl   = "contains a control character"
	nameBracketed = "contains a bracket"
)

// The kernel's identifier rule for a stream family and a wire type name —
// non-empty, within event.MaxNameBytes, valid UTF-8, no control character and
// neither of the two brackets a rendered field is framed with. It is restated
// here because the kernel applies it through declaration doors that mint an
// aggregate or a fact, and a router validating one name may not mint either; the
// agreement between the two is a test rather than a hope. It answers one of a
// closed set of phrases, because a refusal names the rule that was broken and
// never the text that broke it.
func refusedName(value string) string {
	if value == "" {
		return nameEmpty
	}
	if len(value) > event.MaxNameBytes {
		return nameOverCap
	}
	for index := 0; index < len(value); {
		decoded, size := utf8.DecodeRuneInString(value[index:])
		if decoded == utf8.RuneError && size <= 1 {
			return nameNotUTF8
		}
		if unicode.IsControl(decoded) {
			return nameControl
		}
		index += size
	}
	if strings.ContainsAny(value, "[]") {
		return nameBracketed
	}
	return ""
}
