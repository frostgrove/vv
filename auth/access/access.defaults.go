package access

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/google/uuid"
)

// The default role a kind of caller is given, and the idempotent writes an
// application's seed command performs to arrange it.
//
// Both halves are here rather than split between a use case and a helper,
// because they are two ends of one fact: what [Seeder.SetDefaultRole] writes is
// exactly what [Deps.DefaultRole] reads on the next sign-up, and a change to
// either that forgets the other is a default that silently stops applying.

// DefaultRole answers the role a freshly registered caller of this kind gets.
//
// nil means the deployment has no default for this subject type, and that is a
// state rather than a fault: an invitation-only product grants nothing on
// sign-up and an administrator does the granting afterwards. What it must never
// be is a *guess* — see [[D-070]] for why this is a row and not a setting.
//
// It answers the whole role and not its slug, because the caller is about to
// grant it: handing back a name that the enrolment then looks up again is a
// second statement for a row this one already read.
func (this *Deps) DefaultRole(ctx context.Context, subjectType SubjectType) (*Role, error) {
	row, err := this.Store.DefaultRoleRow(ctx, subjectType)
	switch {
	case notFound(err):
		return nil, nil
	case err != nil:
		return nil, err
	case row.Role == nil:
		// The foreign key is RESTRICT, so the role cannot have been deleted out
		// from under this row. Reaching here means the preload did not run, and
		// granting nothing while the table says otherwise is the one outcome
		// worth refusing over.
		return nil, fmt.Errorf("access: the default role of %q resolved to no role", subjectType)
	}
	return row.Role, nil
}

// A Seeder is the idempotent write half of this context: what an application's
// seed command calls instead of writing SQL.
//
// It is separate from [Sync] and the split is the point. Sync folds in what the
// *code* declares — which permissions exist follows from which modules are
// compiled in, so it runs at every start and needs no operator. A Seeder writes
// what the *product* decided: that this deployment has a "lawyer" role, and that
// somebody who signs up is a "client". Neither is derivable from the other, and
// running the second at every start would make an administrator's change to a
// role revert on the next deploy.
//
// Every method here can be run twice. That is not politeness — a seed command is
// re-run after every migration by whoever is not sure whether it was run, and
// one that inserts a second row the second time is a command nobody dares use.
type Seeder struct {
	store  *Store
	logger *slog.Logger
}

// NewSeeder builds it over the store an application already has. [Runtime.Seeder]
// is the ordinary way to reach one.
func NewSeeder(store *Store, logger *slog.Logger) *Seeder {
	return &Seeder{store: store, logger: logger}
}

// A RoleSpec is one role a product wants to exist, and what it may do.
//
// Permissions are attached and never detached, the same rule [Sync] follows: a
// permission an administrator granted by hand survives the next seed, and taking
// access away stays a decision with an audit story rather than a side effect of
// running a command.
type RoleSpec struct {
	// Slug is the identifier everything else names the role by. Required.
	Slug auth.Role
	// Name is what a person reads. Empty falls back to the slug.
	Name string
	// System marks the role as one the application depends on: the CRUD service
	// then refuses to rename or delete it. A role a deployment cannot function
	// without — the one a sign-up grants, the one an administrator holds — wants
	// this; a role somebody invented for one team does not.
	System bool
	// Permissions this role grants. Every code has to be one some module
	// declared, because a code nothing enforces is a row that grants nothing and
	// looks like it grants something.
	Permissions []auth.Permission
}

// EnsureRole makes the role exist and hold at least what the spec names.
//
// The whole of it is one transaction: a role created and then refused its
// permissions is worse than no role at all, because it exists, it can be
// granted, and it does nothing.
func (this *Seeder) EnsureRole(ctx context.Context, spec RoleSpec) (Role, error) {
	if spec.Slug == "" {
		return Role{}, fmt.Errorf("access: seeding a role with no slug")
	}

	var seeded Role
	err := this.store.Tx(ctx, func(txCtx context.Context) error {
		role, err := this.upsertRole(txCtx, spec)
		if err != nil {
			return err
		}
		seeded = role

		if len(spec.Permissions) == 0 {
			return nil
		}
		byCode, err := this.permissionIDs(txCtx, spec.Permissions)
		if err != nil {
			return err
		}
		return attachAll(txCtx, this.store, role, spec.Permissions, byCode)
	})
	if err != nil {
		return Role{}, err
	}
	return seeded, nil
}

// upsertRole creates the row or leaves it alone.
//
// An existing role is not overwritten with the spec's name and flag. Renaming a
// role somebody edited in the admin screen back to what a Go literal says, on
// every run of the seed, is the failure mode that makes people stop running it.
// What the spec decides is what the role *starts* as.
//
// The one case worth saying out loud is a spec that wants a system role and
// finds an ordinary one — somebody created the slug through the API before the
// seed ever ran. The application then believes the role cannot be renamed or
// deleted and it can, which is a difference nothing else would report. It is a
// warning and not a refusal: the deployment works, and promoting the row is a
// decision with an audit story rather than something a seed does on its way
// past. IsSystem is `immutable` besides, so no update DTO can reach it.
func (this *Seeder) upsertRole(ctx context.Context, spec RoleSpec) (Role, error) {
	role, err := this.store.RoleBySlug(ctx, spec.Slug)
	switch {
	case err == nil:
		if spec.System && !role.IsSystem {
			this.logger.WarnContext(ctx, "seeded role exists but is not a system role",
				slog.String("slug", string(spec.Slug)),
				slog.String("consequence", "it can be renamed or deleted through the API"))
		}
		return role, nil
	case !notFound(err):
		return Role{}, err
	}
	saved, err := this.store.Roles.Save(ctx, &Role{
		Slug:     string(spec.Slug),
		Name:     cmpOr(spec.Name, string(spec.Slug)),
		IsSystem: spec.System,
	})
	if err != nil {
		return Role{}, fmt.Errorf("access: seeding role %q: %w", spec.Slug, err)
	}
	this.logger.InfoContext(ctx, "access role seeded", slog.String("slug", string(spec.Slug)))
	return saved, nil
}

// permissionIDs resolves the codes a role wants into ids, and refuses a code
// nothing declared.
//
// Refusing rather than skipping: a permission that is not in the table is one no
// module enforces, and attaching it would produce a row that reads like a grant
// and decides nothing. The usual cause is a typo, and the usual symptom without
// this is a role that quietly cannot do one of the things its spec lists.
func (this *Seeder) permissionIDs(ctx context.Context, codes []auth.Permission) (map[auth.Permission]uuid.UUID, error) {
	rows, err := this.store.Permissions.GetAll(ctx, permissionsIn(codes))
	if err != nil {
		return nil, err
	}
	byCode := make(map[auth.Permission]uuid.UUID, len(rows))
	for _, permission := range rows {
		byCode[auth.Permission(permission.Code)] = permission.ID
	}
	for _, code := range codes {
		if _, declared := byCode[code]; !declared {
			return nil, fmt.Errorf(
				"access: seeding wants the permission %q, which no module declared; run the start-up sync first", code)
		}
	}
	return byCode, nil
}

// SetDefaultRole points a subject type at the role its sign-ups grant.
//
// The role is named by slug and resolved here, so the row can only ever hold an
// id that exists. That is the whole difference from the configuration key this
// replaced: a slug nobody created is refused now, by the command an operator is
// watching, instead of at the first registration weeks later.
func (this *Seeder) SetDefaultRole(ctx context.Context, subjectType SubjectType, slug auth.Role) (Role, error) {
	if subjectType == "" {
		return Role{}, fmt.Errorf("access: setting a default role for no subject type")
	}
	if slug == "" {
		return Role{}, fmt.Errorf("access: setting the default role of %q to nothing; use ClearDefaultRole", subjectType)
	}

	var bound Role
	err := this.store.Tx(ctx, func(txCtx context.Context) error {
		role, err := this.store.RoleBySlug(txCtx, slug)
		if err != nil {
			return fmt.Errorf("access: the default role %q for %q does not exist: %w", slug, subjectType, err)
		}
		bound = role

		// FOR UPDATE serialises two operators running the seed at once. It locks
		// nothing when the row is absent — the unique index on subject_type is
		// what covers that case, by refusing the second insert rather than
		// letting the type end up with two answers.
		existing, err := this.store.DefaultRoles.First(txCtx,
			whereSubjectType(subjectType), crud.ForUpdate())
		switch {
		case notFound(err):
			return this.store.DefaultRoles.SaveOnly(txCtx, &SubjectDefaultRole{
				SubjectType: string(subjectType),
				RoleID:      role.ID,
			})
		case err != nil:
			return err
		case existing.RoleID == role.ID:
			// Already what it should be. Writing anyway would move updated_at,
			// and "when did the default last change" is a question somebody asks
			// of that column.
			return nil
		}
		_, err = this.store.DefaultRoles.Update(txCtx, existing.ID, SubjectDefaultRoleUpdate{RoleID: &role.ID})
		return err
	})
	if err != nil {
		return Role{}, err
	}

	this.logger.InfoContext(ctx, "access default role bound",
		slog.String("subject_type", string(subjectType)), slog.String("role", string(slug)))
	return bound, nil
}

// DefaultRole answers what a subject type's sign-ups currently grant, and
// whether anything does.
func (this *Seeder) DefaultRole(ctx context.Context, subjectType SubjectType) (Role, bool, error) {
	row, err := this.store.DefaultRoleRow(ctx, subjectType)
	switch {
	case notFound(err):
		return Role{}, false, nil
	case err != nil:
		return Role{}, false, err
	case row.Role == nil:
		return Role{}, false, fmt.Errorf("access: the default role of %q resolved to no role", subjectType)
	}
	return *row.Role, true, nil
}

// ClearDefaultRole makes this subject type's sign-ups grant nothing.
//
// A method of its own rather than SetDefaultRole("") because turning the default
// off is a decision, and one spelled as an empty string is one a bad
// configuration value can make by accident.
func (this *Seeder) ClearDefaultRole(ctx context.Context, subjectType SubjectType) error {
	_, err := this.store.DefaultRoles.DeleteAll(ctx, whereSubjectType(subjectType))
	return err
}

func whereSubjectType(subjectType SubjectType) crud.Option {
	return crud.Where(crud.Eq("SubjectType", string(subjectType)))
}
