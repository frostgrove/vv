package access

import (
	"context"

	"github.com/frostgrove/vv/errs"
	"github.com/google/uuid"
)

// stubDirectory is a Directory that answers whatever a test needs it to and
// records nothing. It lives here rather than in each test file because three of
// them need one and three copies drift.
type stubDirectory struct {
	t        SubjectType
	active   bool
	profile  Profile
	provided uuid.UUID

	activeErr    error
	describeErr  error
	provisionErr error
}

// testSubject is the subject type these tests use.
//
// It is declared here rather than borrowed from a module because access ships
// no such constant: a subject type is whatever a registered directory claims,
// and a test reaching into the user module for the string would re-create the
// dependency the port exists to remove.
const testSubject SubjectType = "user"

// namelessDirectory is what a module that forgot to fill in its subject type
// looks like from here. It is a type of its own rather than a field on
// stubDirectory, so the stub's convenient default cannot hide the case.
type namelessDirectory struct{ stubDirectory }

func (namelessDirectory) SubjectType() SubjectType { return "" }

func (this stubDirectory) SubjectType() SubjectType {
	if this.t == "" {
		return testSubject
	}
	return this.t
}

func (this stubDirectory) Active(context.Context, uuid.UUID) (bool, error) {
	return this.active, this.activeErr
}

func (this stubDirectory) Describe(context.Context, uuid.UUID) (Profile, error) {
	return this.profile, this.describeErr
}

func (this stubDirectory) Touch(context.Context, uuid.UUID) error { return nil }

func errsPath(names ...string) errs.Path {
	out := make(errs.Path, 0, len(names))
	for _, n := range names {
		out = append(out, errs.Named(n))
	}
	return out
}
