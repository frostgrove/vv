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
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/internal/nilvalue"
)

// A Store resolves a presented key to a caller.
//
// The three results separate the two failures that must not be confused. A key
// nobody issued is (nil, false, nil) and becomes a 401. A lookup that could not
// be performed — the database is down — is (nil, false, err), and that error
// travels on unchanged, so it renders as the 500 it is. Collapsing the second
// into the first would answer "your key is wrong" to every caller during an
// outage, and the callers would rotate their keys.
//
// Custom [auth.Principal] implementations belong behind Store (or [StoreFunc]),
// where the implementation explicitly owns their snapshot and concurrency
// semantics. [Static] deliberately accepts only the built-in [auth.Claims].
type Store interface {
	Lookup(ctx context.Context, key string) (auth.Principal, bool, error)
}

// StoreFunc adapts a function to [Store].
type StoreFunc func(ctx context.Context, key string) (auth.Principal, bool, error)

// Lookup implements [Store].
func (this StoreFunc) Lookup(ctx context.Context, key string) (auth.Principal, bool, error) {
	return this(ctx, key)
}

// DefaultScheme is the auth-scheme this authenticator expects in an
// Authorization header.
const DefaultScheme = "ApiKey"

// ErrUnsupportedStaticAttribute reports an auth.Claims attribute whose value
// cannot be copied without sharing mutable state. Use [TryStatic] when this is a
// configuration error the application wants to return; [Static] panics on it
// as a declarative start-up failure.
var ErrUnsupportedStaticAttribute = errors.New("apikey: Static cannot safely snapshot a Claims attribute")

// ErrUnsupportedStaticPrincipal reports a nil-like or custom [auth.Principal]
// passed to [Static] or [TryStatic]. Principal exposes queries rather than an
// enumerable value, so these constructors cannot make a fixed snapshot of an
// arbitrary implementation without retaining caller-owned state. Use [Store]
// or [StoreFunc] when principal construction is deliberately dynamic.
var ErrUnsupportedStaticPrincipal = errors.New("apikey: Static can safely snapshot only auth.Claims principals")

type authenticator struct {
	store  Store
	scheme string // "" accepts any
}

// An Option configures [New].
type Option func(*authenticator)

// Scheme replaces the auth-scheme the credential must carry.
func Scheme(name string) Option {
	return func(a *authenticator) {
		if strings.TrimSpace(name) == "" {
			panic("apikey: Scheme needs a non-empty auth-scheme; use AnyScheme to waive the check")
		}
		a.scheme = name
	}
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

// Header reads a bare key from the named header. It deliberately uses
// [auth.Lookup] rather than [auth.Header]: auth.Header moves the Authorization
// parser and therefore still expects "ApiKey <key>", while this helper makes
// "X-Api-Key: <key>" the complete credential.
//
// It synthesises [DefaultScheme], so it pairs with the default authenticator.
// [Scheme] remains available for clients that send a scheme-shaped credential.
func Header(name string) auth.Option {
	if strings.TrimSpace(name) == "" {
		panic("apikey: Header needs a non-empty header name")
	}
	lookup := auth.Lookup(func(get func(string) string) (auth.Credential, bool) {
		key := get(name)
		if key == "" {
			return auth.Credential{}, false
		}
		return auth.Credential{Scheme: DefaultScheme, Token: key}, true
	})
	return lookup
}

// New builds the authenticator. It panics on a nil store: a key authenticator
// with nothing to look keys up in refuses every request, and that is a
// misconfiguration a process should not start with ([[D-021]]).
func New(s Store, options ...Option) auth.Authenticator {
	if nilvalue.Is(s) {
		panic("apikey: New needs a Store; without one every request is refused")
	}
	a := &authenticator{store: s, scheme: DefaultScheme}
	for _, o := range options {
		if o != nil {
			o(a)
		}
	}
	return a
}

// Authenticate implements [auth.Authenticator].
func (this *authenticator) Authenticate(ctx context.Context, c auth.Credential) (auth.Principal, error) {
	if this.scheme != "" && !c.Is(this.scheme) {
		return nil, auth.Unauthenticatedf("credential is not %s", this.scheme)
	}
	if c.Token == "" {
		return nil, auth.Unauthenticated("no key presented")
	}
	p, ok, err := this.store.Lookup(ctx, c.Token)
	if err != nil {
		// Not a 401. The key may well be valid and nothing here can tell.
		return nil, err
	}
	if !ok || nilvalue.Is(p) {
		return nil, auth.Unauthenticated("no such key")
	}
	return p, nil
}

// Static is the declarative in-memory [Store]: a fixed set of keys, fixed at
// start-up. It panics if [TryStatic] cannot make a sound snapshot; use TryStatic
// when configuration assembly needs an error instead.
//
// It is a slice rather than a map, and it compares every entry rather than
// stopping at the first match. A map lookup branches on the key's bytes and
// returns as soon as it knows, which times differently for a key that shares a
// prefix with a real one than for one that does not — enough, over enough
// requests, to recover a key a character at a time. Comparing all of them with
// crypto/subtle costs a few hundred nanoseconds for the handful of keys this is
// meant to hold.
//
// It snapshots the map. Built-in [auth.Claims] values (including pointers) are
// deep-copied at construction and copied again for each lookup, so mutating the
// declaration or one request's returned slices/maps cannot change another
// request's identity. Maps, slices, pointers, arrays and structs whose state is
// exported are copied recursively, including cycles. A struct with unexported
// state (bytes.Buffer and big.Int are examples), a function, channel or unsafe
// pointer is refused: shallow-copying one would make "fixed" a lie.
//
// A custom Principal has no enumeration API from which a copy could be made,
// so the safe declarative constructor refuses it. A deployment that owns the
// custom principal's lifetime uses the explicit lower-level [Store] or
// [StoreFunc] seam instead. A nil-like entry is also a declaration error, not a
// fixed key whose only possible result is unknown.
func Static(keys map[string]auth.Principal) Store {
	store, err := TryStatic(keys)
	if err != nil {
		panic(err)
	}
	return store
}

// TryStatic is [Static] with a configuration error result. It never includes a
// presented key in that error: keys are credentials, including at start-up.
func TryStatic(keys map[string]auth.Principal) (Store, error) {
	type entry struct {
		key   []byte
		fresh func() (auth.Principal, error)
	}
	entries := make([]entry, 0, len(keys))
	for k, p := range keys {
		fresh, err := freezePrincipal(p)
		if err != nil {
			return nil, fmt.Errorf("apikey: TryStatic cannot snapshot %T: %w", p, err)
		}
		entries = append(entries, entry{key: []byte(k), fresh: fresh})
	}
	return StoreFunc(func(_ context.Context, key string) (auth.Principal, bool, error) {
		candidate := []byte(key)
		var found func() (auth.Principal, error)
		match := 0
		for _, e := range entries {
			if subtle.ConstantTimeCompare(e.key, candidate) == 1 {
				found = e.fresh
				match = 1
			}
		}
		if match == 0 || found == nil {
			return nil, false, nil
		}
		principal, err := found()
		if err != nil {
			return nil, false, err
		}
		return principal, true, nil
	}), nil
}

// freezePrincipal returns a per-lookup materialiser. Principal deliberately has
// query methods rather than enumeration methods, so Claims is the only general
// purpose implementation Static can soundly snapshot.
func freezePrincipal(p auth.Principal) (func() (auth.Principal, error), error) {
	if nilvalue.Is(p) {
		return nil, ErrUnsupportedStaticPrincipal
	}
	switch claims := p.(type) {
	case auth.Claims:
		frozen, err := cloneClaims(claims)
		if err != nil {
			return nil, err
		}
		return func() (auth.Principal, error) { return cloneClaims(frozen) }, nil
	case *auth.Claims:
		frozen, err := cloneClaims(*claims)
		if err != nil {
			return nil, err
		}
		return func() (auth.Principal, error) {
			fresh, err := cloneClaims(frozen)
			if err != nil {
				return nil, err
			}
			return &fresh, nil
		}, nil
	default:
		return nil, ErrUnsupportedStaticPrincipal
	}
}

func cloneClaims(in auth.Claims) (auth.Claims, error) {
	out := in
	if in.Roles != nil {
		out.Roles = append(make([]auth.Role, 0, len(in.Roles)), in.Roles...)
	}
	if in.Permissions != nil {
		out.Permissions = append(make([]auth.Permission, 0, len(in.Permissions)), in.Permissions...)
	}
	if in.Attrs == nil {
		return out, nil
	}
	out.Attrs = make(map[string]any, len(in.Attrs))
	seen := make(map[attributeVisit]reflect.Value)
	for name, value := range in.Attrs {
		cloned, err := cloneAttribute(reflect.ValueOf(value), seen)
		if err != nil {
			return auth.Claims{}, fmt.Errorf("%w %q: %v", ErrUnsupportedStaticAttribute, name, err)
		}
		if cloned.IsValid() {
			out.Attrs[name] = cloned.Interface()
		} else {
			out.Attrs[name] = nil
		}
	}
	return out, nil
}

type attributeVisit struct {
	typ  reflect.Type
	kind reflect.Kind
	ptr  uintptr
	len  int
	cap  int
}

// cloneAttribute copies the mutable container shapes JSON claims use while
// preserving their concrete Go types. The seen table also makes hand-built,
// cyclic maps and slices safe even though decoded token attributes are acyclic.
func cloneAttribute(in reflect.Value, seen map[attributeVisit]reflect.Value) (reflect.Value, error) {
	if !in.IsValid() {
		return in, nil
	}
	switch in.Kind() {
	case reflect.Interface:
		if in.IsNil() {
			return reflect.Zero(in.Type()), nil
		}
		cloned, err := cloneAttribute(in.Elem(), seen)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(in.Type()).Elem()
		out.Set(cloned)
		return out, nil
	case reflect.Pointer:
		if in.IsNil() {
			return reflect.Zero(in.Type()), nil
		}
		visit := attributeVisit{typ: in.Type(), kind: in.Kind(), ptr: in.Pointer()}
		if prior, ok := seen[visit]; ok {
			return prior, nil
		}
		out := reflect.New(in.Type().Elem())
		seen[visit] = out
		cloned, err := cloneAttribute(in.Elem(), seen)
		if err != nil {
			return reflect.Value{}, err
		}
		out.Elem().Set(cloned)
		return out, nil
	case reflect.Map:
		if in.IsNil() {
			return reflect.Zero(in.Type()), nil
		}
		if err := validateMapKey(in.Type().Key()); err != nil {
			return reflect.Value{}, err
		}
		visit := attributeVisit{typ: in.Type(), kind: in.Kind(), ptr: in.Pointer()}
		if prior, ok := seen[visit]; ok {
			return prior, nil
		}
		out := reflect.MakeMapWithSize(in.Type(), in.Len())
		seen[visit] = out
		iter := in.MapRange()
		for iter.Next() {
			cloned, err := cloneAttribute(iter.Value(), seen)
			if err != nil {
				return reflect.Value{}, err
			}
			out.SetMapIndex(iter.Key(), cloned)
		}
		return out, nil
	case reflect.Slice:
		if in.IsNil() {
			return reflect.Zero(in.Type()), nil
		}
		visit := attributeVisit{
			typ: in.Type(), kind: in.Kind(), ptr: in.Pointer(), len: in.Len(), cap: in.Cap(),
		}
		if prior, ok := seen[visit]; ok {
			return prior, nil
		}
		out := reflect.MakeSlice(in.Type(), in.Len(), in.Len())
		seen[visit] = out
		for i := 0; i < in.Len(); i++ {
			cloned, err := cloneAttribute(in.Index(i), seen)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(cloned)
		}
		return out, nil
	case reflect.Array:
		out := reflect.New(in.Type()).Elem()
		for i := 0; i < in.Len(); i++ {
			cloned, err := cloneAttribute(in.Index(i), seen)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(cloned)
		}
		return out, nil
	case reflect.Struct:
		for i := 0; i < in.NumField(); i++ {
			field := in.Type().Field(i)
			if field.PkgPath != "" {
				return reflect.Value{}, fmt.Errorf("%s has unexported field %s", in.Type(), field.Name)
			}
		}
		out := reflect.New(in.Type()).Elem()
		for i := 0; i < in.NumField(); i++ {
			cloned, err := cloneAttribute(in.Field(i), seen)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Field(i).Set(cloned)
		}
		return out, nil
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return reflect.Value{}, fmt.Errorf("%s values may retain mutable state", in.Type())
	default:
		return in, nil
	}
}

func validateMapKey(key reflect.Type) error {
	switch key.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
		reflect.String:
		return nil
	case reflect.Array:
		return validateMapKey(key.Elem())
	case reflect.Struct:
		for i := 0; i < key.NumField(); i++ {
			field := key.Field(i)
			if field.PkgPath != "" {
				return fmt.Errorf("map key %s has unexported field %s", key, field.Name)
			}
			if err := validateMapKey(field.Type); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("map key %s may retain mutable identity", key)
	}
}
