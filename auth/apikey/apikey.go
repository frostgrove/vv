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

type Store interface {
	Lookup(ctx context.Context, key string) (auth.Principal, bool, error)
}

type StoreFunc func(ctx context.Context, key string) (auth.Principal, bool, error)

func (this StoreFunc) Lookup(ctx context.Context, key string) (auth.Principal, bool, error) {
	return this(ctx, key)
}

const DefaultScheme = "ApiKey"

var ErrUnsupportedStaticAttribute = errors.New("apikey: Static cannot safely snapshot a Claims attribute")

var ErrUnsupportedStaticPrincipal = errors.New("apikey: Static can safely snapshot only auth.Claims principals")

type authenticator struct {
	store  Store
	scheme string
}

type Option func(*authenticator)

func Scheme(name string) Option {
	return func(a *authenticator) {
		if strings.TrimSpace(name) == "" {
			panic("apikey: Scheme needs a non-empty auth-scheme; use AnyScheme to waive the check")
		}
		a.scheme = name
	}
}

func AnyScheme() Option {
	return func(a *authenticator) { a.scheme = "" }
}

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

func (this *authenticator) Authenticate(ctx context.Context, c auth.Credential) (auth.Principal, error) {
	if this.scheme != "" && !c.Is(this.scheme) {
		return nil, auth.Unauthenticatedf("credential is not %s", this.scheme)
	}
	if c.Token == "" {
		return nil, auth.Unauthenticated("no key presented")
	}
	p, ok, err := this.store.Lookup(ctx, c.Token)
	if err != nil {
		return nil, err
	}
	if !ok || nilvalue.Is(p) {
		return nil, auth.Unauthenticated("no such key")
	}
	return p, nil
}

func Static(keys map[string]auth.Principal) Store {
	store, err := TryStatic(keys)
	if err != nil {
		panic(err)
	}
	return store
}

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
