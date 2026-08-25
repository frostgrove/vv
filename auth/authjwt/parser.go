package authjwt

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/shardit-io/vv/auth"
)

// A Parser verifies a token and decodes its payload into C.
//
// C is whatever struct the issuer's tokens deserve. It carries no constraint —
// no embedded type of ours, none of golang-jwt's — because a consumer who wants
// a JWT parser should not have to change their claims type to get one. What
// that costs is one map-to-JSON-to-struct hop after verification, which is
// where the freedom comes from: the registered claims are validated by
// golang-jwt against the map, and only then is the payload shaped into C.
type Parser[C any] struct {
	key  KeySource
	opts []jwt.ParserOption
}

// An Option configures a [Parser].
type Option func(*settings)

type settings struct {
	issuer      string
	audience    []string
	anyIssuer   bool
	anyAudience bool
	noExpiry    bool
	leeway      time.Duration
}

// Issuer requires the iss claim to be exactly this.
func Issuer(iss string) Option {
	return func(s *settings) { s.issuer = iss }
}

// Audience requires the aud claim to contain every one of these.
func Audience(aud ...string) Option {
	return func(s *settings) { s.audience = append(s.audience, aud...) }
}

// Leeway is how much clock skew is tolerated on exp, nbf and iat. It defaults
// to none, which is the strict reading and the right default for two clocks
// that are both synchronised; thirty seconds is the usual production setting
// for two that may not be.
func Leeway(d time.Duration) Option {
	return func(s *settings) { s.leeway = d }
}

// AllowAnyIssuer accepts a token whatever its iss says, and is how a deployment
// that genuinely has no single issuer says so out loud. See [New].
func AllowAnyIssuer() Option {
	return func(s *settings) { s.anyIssuer = true }
}

// AllowAnyAudience accepts a token whatever its aud says.
//
// Read [New] before reaching for it. An unaudienced token is replayable against
// every other service that trusts the same issuer, so this is safe only where
// there is no other such service — and that is a fact about a deployment that
// changes the day somebody adds one.
func AllowAnyAudience() Option {
	return func(s *settings) { s.anyAudience = true }
}

// AllowNoExpiry accepts a token that carries no exp claim.
//
// Such a token is a credential that never stops working: one leak is permanent,
// and revoking it means rotating the signing key for everybody. It exists for
// an issuer that cannot be changed, and naming it is the point.
func AllowNoExpiry() Option {
	return func(s *settings) { s.noExpiry = true }
}

// New builds the parser.
//
// It panics rather than returning an error, and on three things: a key source
// that verifies nothing, an issuer that is neither named nor waived, and an
// audience that is neither named nor waived. All three are misconfigurations
// whose consequence is a parser that over-trusts, and a process that does not
// start is a far better outcome than a request that is quietly accepted
// ([[D-021]]). Every one of them is fixed by adding one line at the call site.
func New[C any](k KeySource, opts ...Option) *Parser[C] {
	if !k.valid() {
		panic("authjwt: New needs a KeySource that carries both a key and the methods it verifies")
	}
	var s settings
	for _, o := range opts {
		if o != nil {
			o(&s)
		}
	}
	if s.issuer == "" && !s.anyIssuer {
		panic("authjwt: New needs Issuer(...), or AllowAnyIssuer() to say the issuer is deliberately unchecked")
	}
	if len(s.audience) == 0 && !s.anyAudience {
		panic("authjwt: New needs Audience(...), or AllowAnyAudience() to say the token is deliberately replayable across services")
	}

	// WithValidMethods is what pins the algorithm to the key rather than to the
	// token's own header. Without it a token can nominate its own verification
	// scheme, which is the whole of the alg=none and key-confusion families.
	po := []jwt.ParserOption{jwt.WithValidMethods(k.methods)}
	if !s.noExpiry {
		po = append(po, jwt.WithExpirationRequired())
	}
	if s.leeway > 0 {
		po = append(po, jwt.WithLeeway(s.leeway))
	}
	if s.issuer != "" {
		po = append(po, jwt.WithIssuer(s.issuer))
	}
	for _, a := range s.audience {
		po = append(po, jwt.WithAudience(a))
	}
	return &Parser[C]{key: k, opts: po}
}

// Parse verifies the token and answers the claims.
//
// Every failure is one answer. A bad signature, an expired token, a wrong
// audience and a malformed header are indistinguishable to the caller, and the
// reason travels in the wrapped error where nothing renders it ([[D-056]]).
// Reporting which check failed tells whoever is probing exactly what to change
// next.
//
// The context is taken but not consulted here. It is what a [KeySource] built
// by [JWKS] uses to fetch and to time out, and taking it on every parser keeps
// the signature the same whichever key source is wired.
func (p *Parser[C]) Parse(ctx context.Context, token string) (C, error) {
	var zero C
	if token == "" {
		return zero, auth.Unauthenticated("no token presented")
	}

	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(token, claims, p.keyfuncFor(ctx), p.opts...); err != nil {
		return zero, auth.Unauthenticatedf("token rejected: %v", err)
	}

	out, err := decode[C](claims)
	if err != nil {
		return zero, auth.Unauthenticatedf("token payload does not fit the claims type: %v", err)
	}
	return out, nil
}

// keyfuncFor adapts this package's context-carrying [Keyfunc] to the signature
// golang-jwt calls. The context is closed over per request rather than stored
// on the parser, which two goroutines share.
func (p *Parser[C]) keyfuncFor(ctx context.Context) jwt.Keyfunc {
	if ctx == nil {
		ctx = context.Background()
	}
	return func(t *jwt.Token) (any, error) { return p.key.keyfunc(ctx, t) }
}

// decode reshapes the verified claim map into the caller's type.
//
// UseNumber keeps an integer an integer. Without it every number in the payload
// arrives as a float64, and a tenant id read out of Attr then compiles into
// `WHERE tenant_id = 7e+00` — which some engines accept, some refuse, and none
// of them should have been asked.
func decode[C any](claims jwt.MapClaims) (C, error) {
	var out C
	b, err := json.Marshal(claims)
	if err != nil {
		return out, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}
