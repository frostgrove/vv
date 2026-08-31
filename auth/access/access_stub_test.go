package access

import (
	"context"

	"github.com/frostgrove/vv/errs"
	"github.com/google/uuid"
)

type stubDirectory struct {
	t        SubjectType
	active   bool
	profile  Profile
	provided uuid.UUID

	activeErr    error
	describeErr  error
	provisionErr error
}

const testSubject SubjectType = "user"

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
