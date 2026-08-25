// Package auth is who the caller is, before anything has decided what they may
// do about it.
//
// A [Principal] is one authenticated caller. A transport puts one into the
// request context; a security policy reads it out. Between those two points
// this package is the only vocabulary either side has to agree on, and it is
// four methods long.
//
//	ctx = auth.WithPrincipal(ctx, p)   // the transport, once per request
//	p, err := auth.Require(ctx)        // the policy, once per operation
//
// # What this package does not do
//
// It does not put a principal into a context. Every path that could — a header,
// gRPC metadata, a session cookie — belongs to a transport, and a transport is
// a binding ([[D-045]]). What lives here is the part all four bindings would
// otherwise each write: splitting a credential out of a header, the context
// key, and the shape of a refusal.
//
// It does not issue anything. There is no login, no refresh, no key rotation
// and no user store. This package reads what was presented; minting it is an
// application.
//
// It does not decide. [Permission] and [Role] are strings with a type, not a
// rule engine. What a permission means is crud/decorators/security's, and the
// direction is one-way on purpose: security imports auth, auth imports nothing
// below errs. A middleware has no reason to compile a repository in.
//
// # Three refusals
//
// No init() registry and no package-level table. A [RoleMap] is a value the
// consumer wires at the call site. Two libraries declaring "admin" with
// different permissions must not settle it by link order, and a go.work joining
// seven modules makes an init() in the wrong one invisible. The rule is
// errs/doc.go's, applied to the one other place it fits.
//
// No principal type of ours forced on anybody. [Principal] is an interface and
// [Claims] is one implementation of it. A consumer whose identity already has a
// Go type implements the interface over that type, and nothing here ever sees
// it. That is the whole reason it is an interface rather than a struct.
//
// No transport type, for [[D-045]]'s reason. A [Credential] is a scheme and a
// token, both strings. An http.Header in this package would make the gRPC
// interceptor unable to use it.
//
// # A refusal is a fault that wraps a sentinel
//
// [Unauthenticated] builds an errs fault of [errs.KindUnauthorized], so every
// existing status table answers 401 with no new arm anywhere, and it wraps
// [ErrUnauthenticated], so errors.Is still branches ([[D-015]], [[D-038]]).
// Nothing in crud, port or errs changes to make that work.
//
// The reason is in the wrapped error and never in the fault's message.
// port.Violations copies Fault.Message into the one violation it synthesises
// for a fault that carries none, and that violation is rendered — so "signature
// does not verify" put there would reach the client and tell an attacker which
// half of the token to fix ([[D-044]]). See [[D-056]].
package auth
