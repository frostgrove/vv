package authjwt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/frostgrove/vv/auth"
)

type Parser[C any] struct {
	key     KeySource
	options []jwt.ParserOption
}

type Option func(*settings)

type settings struct {
	issuer      string
	audience    []string
	anyIssuer   bool
	anyAudience bool
	noExpiry    bool
	leeway      time.Duration
}

func Issuer(iss string) Option {
	return func(s *settings) { s.issuer = iss }
}

func Audience(aud ...string) Option {
	return func(s *settings) { s.audience = append(s.audience, aud...) }
}

func Leeway(d time.Duration) Option {
	return func(s *settings) { s.leeway = d }
}

func AllowAnyIssuer() Option {
	return func(s *settings) { s.anyIssuer = true }
}

func AllowAnyAudience() Option {
	return func(s *settings) { s.anyAudience = true }
}

func AllowNoExpiry() Option {
	return func(s *settings) { s.noExpiry = true }
}

func New[C any](k KeySource, options ...Option) *Parser[C] {
	if !k.valid() {
		panic("authjwt: New needs a KeySource that carries both a key and the methods it verifies")
	}
	var s settings
	for _, o := range options {
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

	po := []jwt.ParserOption{jwt.WithValidMethods(k.methods), jwt.WithJSONNumber()}
	if !s.noExpiry {
		po = append(po, jwt.WithExpirationRequired())
	}
	if s.leeway > 0 {
		po = append(po, jwt.WithLeeway(s.leeway))
	}
	if s.issuer != "" {
		po = append(po, jwt.WithIssuer(s.issuer))
	}
	if len(s.audience) > 0 {
		po = append(po, jwt.WithAllAudiences(s.audience...))
	}
	return &Parser[C]{key: k, options: po}
}

func (this *Parser[C]) Warm(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if this.key.warm == nil {
		return nil
	}
	return this.key.warm(ctx)
}

func (this *Parser[C]) Parse(ctx context.Context, token string) (C, error) {
	var zero C
	if token == "" {
		return zero, auth.Unauthenticated("no token presented")
	}

	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(token, claims, this.keyfuncFor(ctx), this.options...); err != nil {
		if errors.Is(err, ErrKeySourceUnavailable) {
			return zero, err
		}
		if ctx != nil {
			if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
				return zero, ctxErr
			}
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return zero, err
		}
		return zero, auth.Unauthenticatedf("token rejected: %v", err)
	}

	out, err := decode[C](claims)
	if err != nil {
		return zero, auth.Unauthenticatedf("token payload does not fit the claims type: %v", err)
	}
	return out, nil
}

func (this *Parser[C]) keyfuncFor(ctx context.Context) jwt.Keyfunc {
	if ctx == nil {
		ctx = context.Background()
	}
	return func(t *jwt.Token) (any, error) { return this.key.keyfunc(ctx, t) }
}

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
