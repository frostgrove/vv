package access

import (
	"context"

	"github.com/google/uuid"
)

// A Subject is everything one kind of caller has to supply to be signed in.
//
// It exists so the answer to "what do I have to write to make this work" is a
// struct the compiler checks, rather than a page of prose somebody has to find.
// A transport binding takes one of these and mounts a whole sign-in surface
// from it; nothing else in this module needs it.
type Subject struct {
	// Type is the morph key. Every row this caller owns carries it, and an
	// identifier is unique within it — so two kinds of caller may both know an
	// "ops@example.com" without either having to rename.
	Type SubjectType

	// Directory is the identity store behind Type.
	Directory Directory

	// Normalize folds an identifier into the one spelling that is stored and
	// looked up. nil stores and compares verbatim.
	//
	// One function and not two, applied by everything that touches the column,
	// because the failure from applying it on one side only is an account that
	// enrols under "Ann@Example.com" and cannot sign in as "ann@example.com" —
	// and nothing reports it.
	//
	// What belongs here is an equality rule and not validation: strings.ToLower
	// for an address, nil for a Google `sub`. Refusing a malformed one is the
	// registrar's, where it can answer with a field name.
	Normalize func(identifier string) string
}

// Identifier answers the stored spelling of what a caller typed.
func (this Subject) Identifier(raw string) string {
	if this.Normalize == nil {
		return raw
	}
	return this.Normalize(raw)
}

// Ref builds a reference to one caller of this kind.
func (this Subject) Ref(id uuid.UUID) SubjectRef {
	return SubjectRef{Type: this.Type, ID: id}
}

// A Registrar creates the account behind a self-service sign-up.
//
// P is the application's own sign-up payload — whatever its form asks for. That
// is the whole reason this is generic: a fixed command struct here would be a
// field list that fits nobody, and a map[string]any would type-check nothing.
// Adding a field to a sign-up form stays an edit to the application.
//
// A [Subject] with no registrar has no sign-up. That is not a degraded mode: an
// account provisioned by an administrator and given a password through
// credentials.SetPasswordUseCase is the other half of how this module expects
// to be used.
type Registrar[P any] interface {
	// Create writes the account row and answers its id and the identifier it
	// will sign in with.
	//
	// It runs inside the enrolment transaction, so returning an error rolls the
	// credential back with it — which is what stops a half-registered account
	// from existing at all. A duplicate refused by a unique index of the
	// application's own reaches the caller as that refusal.
	//
	// The identifier it returns is normalised by [Subject] afterwards, so an
	// implementation returns what it stored and does not fold it twice.
	Create(ctx context.Context, payload P) (uuid.UUID, string, error)

	// Password is the secret the payload carries. It is read through a method
	// rather than a field so P stays the application's own struct, with no
	// tag, embedding or field name this module has to agree with.
	Password(payload P) string
}

// There is no Role method here, and its absence is [[D-070]]. What a sign-up
// grants is read from subject_default_roles, keyed by this subject's type: a
// registrar that answered it would be the application spelling a role slug in
// Go, and the slug it spelled would be checked against the roles table for the
// first time at the first registration.
