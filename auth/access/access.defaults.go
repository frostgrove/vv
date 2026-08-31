package access

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/google/uuid"
)

func (this *Deps) DefaultRole(ctx context.Context, subjectType SubjectType) (*Role, error) {
	row, err := this.Store.DefaultRoleRow(ctx, subjectType)
	switch {
	case notFound(err):
		return nil, nil
	case err != nil:
		return nil, err
	case row.Role == nil:
		return nil, fmt.Errorf("access: the default role of %q resolved to no role", subjectType)
	}
	return row.Role, nil
}

type Seeder struct {
	store  *Store
	logger *slog.Logger
}

func NewSeeder(store *Store, logger *slog.Logger) *Seeder {
	return &Seeder{store: store, logger: logger}
}

type RoleSpec struct {
	Slug auth.Role

	Name string

	System bool

	Permissions []auth.Permission
}

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

func (this *Seeder) ClearDefaultRole(ctx context.Context, subjectType SubjectType) error {
	_, err := this.store.DefaultRoles.DeleteAll(ctx, whereSubjectType(subjectType))
	return err
}

func whereSubjectType(subjectType SubjectType) crud.Option {
	return crud.Where(crud.Eq("SubjectType", string(subjectType)))
}
