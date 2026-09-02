package access

import (
	"log/slog"
	"time"

	"github.com/frostgrove/vv/errs"
)

type Deps struct {
	Store  *Store
	Grants *GrantsService
	Hasher Hasher
	Config Config
	Log    *slog.Logger

	revocations *revocationSinks
	protection  Protection
}

func newDeps(
	store *Store,
	grants *GrantsService,
	hasher Hasher,
	configuration Config,
	logger *slog.Logger,
	revocations *revocationSinks,
	protection Protection,
) *Deps {
	return &Deps{
		Store:       store,
		Grants:      grants,
		Hasher:      hasher,
		Config:      configuration,
		Log:         logger,
		revocations: revocations,
		protection:  protection,
	}
}

func (this *Deps) Now() time.Time {
	return this.Config.Now()
}

const (
	CodeBadCredentials errs.Code = "bad_credentials"

	CodeWeakPassword errs.Code = "weak_password"
)

func badCredentials(operation string) error {
	return errs.Unauthorized().
		Code(CodeBadCredentials).
		Message("the identifier or the password is wrong").
		Entity("Credential").Op(operation).Fault()
}
