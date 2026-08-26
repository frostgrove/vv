// Package apikey authenticates a caller by a shared secret it presents
// verbatim.
//
//	authn := apikey.New(apikey.Static(map[string]auth.Principal{
//	    "k-batch-1": auth.Claims{Sub: "batch", Permissions: []auth.Permission{"article:read"}},
//	}))
//
// It is the second implementation of [auth.Authenticator], and it is here
// rather than in a module of its own because it imports nothing outside the
// standard library — a second `go get` bought for no dependency is a cost with
// nothing on the other side of it ([[D-033]]).
//
// # What a Store is for
//
// A real deployment does not hold keys in a map. It holds a hash of each key,
// looks the hash up, and revokes by deleting a row. [Store] is that seam, and
// [Static] is the small case: tests, a single batch job, a service with three
// keys in its configuration.
//
// Whatever implements it must compare in constant time. [Static] does; a Store
// that indexes a database by the hash of the presented key does too, because
// the hash is what travels.
package apikey

import (
	"context"
	"crypto/subtle"

	"github.com/frostgrove/vv/auth"
)

// A Store resolves a presented key to a caller.
//
// The three results separate the two failures that must not be confused. A key
// nobody issued is (nil, false, nil) and becomes a 401. A lookup that could not
// be performed — the database is down — is (nil, false, err), and that error
// travels on unchanged, so it renders as the 500 it is. Collapsing the second
// into the first would answer "your key is wrong" to every caller during an
// outage, and the callers would rotate their keys.
type Store interface {
	Lookup(ctx context.Context, key string) (auth.Principal, bool, error)
}

// StoreFunc adapts a function to [Store].
type StoreFunc func(ctx context.Context, key string) (auth.Principal, bool, error)

// Lookup implements [Store].
func (f StoreFunc) Lookup(ctx context.Context, key string) (auth.Principal, bool, error) {
	return f(ctx, key)
}

// DefaultScheme is the auth-scheme this authenticator expects in an
// Authorization header.
const DefaultScheme = "ApiKey"

type authenticator struct {
	store  Store
	scheme string // "" accepts any
}

// An Option configures [New].
type Option func(*authenticator)

// Scheme replaces the auth-scheme the credential must carry.
func Scheme(name string) Option {
	return func(a *authenticator) { a.scheme = name }
}

// AnyScheme accepts the credential whatever scheme it arrived under, for a
// deployment whose clients send the key as a bearer token.
//
// It is opt-in rather than the default because an endpoint that also accepts
// JWTs would otherwise hand every expired JWT to the key store as a candidate
// key, and a store that logs misses would then log tokens.
func AnyScheme() Option {
	return func(a *authenticator) { a.scheme = "" }
}

// New builds the authenticator. It panics on a nil store: a key authenticator
// with nothing to look keys up in refuses every request, and that is a
// misconfiguration a process should not start with ([[D-021]]).
func New(s Store, opts ...Option) auth.Authenticator {
	if s == nil {
		panic("apikey: New needs a Store; without one every request is refused")
	}
	a := &authenticator{store: s, scheme: DefaultScheme}
	for _, o := range opts {
		if o != nil {
			o(a)
		}
	}
	return a
}

// Authenticate implements [auth.Authenticator].
func (a *authenticator) Authenticate(ctx context.Context, c auth.Credential) (auth.Principal, error) {
	if a.scheme != "" && !c.Is(a.scheme) {
		return nil, auth.Unauthenticatedf("credential is not %s", a.scheme)
	}
	if c.Token == "" {
		return nil, auth.Unauthenticated("no key presented")
	}
	p, ok, err := a.store.Lookup(ctx, c.Token)
	if err != nil {
		// Not a 401. The key may well be valid and nothing here can tell.
		return nil, err
	}
	if !ok || p == nil {
		return nil, auth.Unauthenticated("no such key")
	}
	return p, nil
}

// Static is the in-memory [Store]: a fixed set of keys, fixed at start-up.
//
// It is a slice rather than a map, and it compares every entry rather than
// stopping at the first match. A map lookup branches on the key's bytes and
// returns as soon as it knows, which times differently for a key that shares a
// prefix with a real one than for one that does not — enough, over enough
// requests, to recover a key a character at a time. Comparing all of them with
// crypto/subtle costs a few hundred nanoseconds for the handful of keys this is
// meant to hold.
//
// It copies the map, so a caller that keeps mutating theirs does not change who
// may call.
func Static(keys map[string]auth.Principal) Store {
	type entry struct {
		key []byte
		p   auth.Principal
	}
	entries := make([]entry, 0, len(keys))
	for k, p := range keys {
		entries = append(entries, entry{key: []byte(k), p: p})
	}
	return StoreFunc(func(_ context.Context, key string) (auth.Principal, bool, error) {
		candidate := []byte(key)
		var found auth.Principal
		match := 0
		for _, e := range entries {
			if subtle.ConstantTimeCompare(e.key, candidate) == 1 {
				found = e.p
				match = 1
			}
		}
		if match == 0 {
			return nil, false, nil
		}
		return found, true, nil
	})
}
