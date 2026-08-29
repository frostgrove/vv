// Package credentials is the sign-in half of access: what turns an identifier
// and a password into a session, and what closes one again.
//
// It is a package of its own rather than four more files in access because the
// dependency runs one way and has to keep running one way. This package knows
// about access's models, its store and its Directory port; access knows nothing
// about this package, and the composition root is where both names appear. That
// is what will let a second sign-in method — the google/ folder next door —
// arrive as a third package rather than as branches in here.
package access

import (
	"log/slog"
	"time"

	"github.com/frostgrove/vv/errs"
)

// Deps is what every use case here needs. One struct, because the six of them
// need the same five things and a constructor per use case with five parameters
// each is five chances to wire one wrong.
type Deps struct {
	Store  *Store
	Grants *GrantsService
	Hasher Hasher
	Config Config
	Log    *slog.Logger

	// revocations is what a strategy registered to be told about a session it
	// can no longer see closing. nil for a deployment where every strategy
	// verifies by reading the row. See access.revocation.go.
	revocations *revocationSinks

	// now is a seam rather than a call to time.Now, because every expiry rule
	// in here is otherwise untestable without sleeping.
	now func() time.Time
}

// newDeps builds the dependency bundle. [Runtime] is what calls it: an
// application assembles a RuntimeSpec, never this.
func newDeps(
	store *Store,
	grants *GrantsService,
	hasher Hasher,
	configuration Config,
	logger *slog.Logger,
	revocations *revocationSinks,
) *Deps {
	return &Deps{
		Store:       store,
		Grants:      grants,
		Hasher:      hasher,
		Config:      configuration,
		Log:         logger,
		revocations: revocations,
		now:         time.Now,
	}
}

// WithClock replaces the clock these use cases read. It is what makes every
// expiry rule in this package testable without sleeping.
func (this *Deps) WithClock(clock func() time.Time) *Deps {
	this.now = clock
	return this
}

// Now is the clock these use cases run on.
func (this *Deps) Now() time.Time {
	if this.now == nil {
		return time.Now()
	}
	return this.now()
}

// The error codes this package produces. They are declared here rather than
// taken from the standard vocabulary because a client branches on them: an
// deployment that refuses a password for being too short wants a different
// screen from one answering "wrong password".
const (
	// CodeBadCredentials is the one answer to every way a sign-in can be wrong.
	CodeBadCredentials errs.Code = "bad_credentials"
	// CodeWeakPassword reports a password below the configured length.
	CodeWeakPassword errs.Code = "weak_password"
)

// badCredentials is the refusal every failed sign-in gets, whatever actually
// went wrong.
//
// One answer for an unknown identifier, a wrong password and a credential that
// belongs to a deactivated account. Telling them apart turns this endpoint into
// a way to ask "does this person have an account here", which is a question the
// product has no reason to answer to a stranger.
func badCredentials(operation string) error {
	return errs.Unauthorized().
		Code(CodeBadCredentials).
		Message("the identifier or the password is wrong").
		Entity("Credential").Op(operation).Fault()
}
