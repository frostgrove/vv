package access

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/google/uuid"
)

func Sync(ctx context.Context, store *Store, declared []ModuleGrants, logger *slog.Logger) error {
	byCode, err := syncPermissions(ctx, store, declared)
	if err != nil {
		return err
	}

	wanted := rolePlan(declared)
	for _, slug := range slices.Sorted(maps.Keys(wanted)) {
		role, err := ensureRole(ctx, store, slug)
		if err != nil {
			return err
		}
		if err := attachAll(ctx, store, role, wanted[slug], byCode); err != nil {
			return err
		}
	}

	logger.InfoContext(ctx, "access grants synchronised",
		slog.Int("permissions", len(byCode)), slog.Int("system_roles", len(wanted)))
	return nil
}

func syncPermissions(ctx context.Context, store *Store, declared []ModuleGrants) (map[auth.Permission]uuid.UUID, error) {
	existing, err := store.Permissions.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	byCode := make(map[auth.Permission]uuid.UUID, len(existing))
	for _, permission := range existing {
		byCode[auth.Permission(permission.Code)] = permission.ID
	}

	for _, module := range declared {
		for _, permissionDefinition := range module.Permissions {
			if permissionDefinition.Code == "" {
				return nil, fmt.Errorf("access: module %q declared a permission with no code", module.Module)
			}
			if _, have := byCode[permissionDefinition.Code]; have {
				continue
			}
			saved, err := store.Permissions.Save(ctx, &Permission{
				Code:   string(permissionDefinition.Code),
				Name:   cmpOr(permissionDefinition.Name, string(permissionDefinition.Code)),
				Module: module.Module,
			})
			if err != nil {
				return nil, fmt.Errorf("access: declaring permission %q: %w", permissionDefinition.Code, err)
			}
			byCode[permissionDefinition.Code] = saved.ID
		}
	}
	return byCode, nil
}

func rolePlan(declared []ModuleGrants) map[auth.Role][]auth.Permission {
	plan := map[auth.Role][]auth.Permission{RoleAdmin: nil}
	for _, module := range declared {
		for _, permissionDefinition := range module.Permissions {
			plan[RoleAdmin] = append(plan[RoleAdmin], permissionDefinition.Code)
		}
		for role, permissions := range module.Roles {
			plan[role] = append(plan[role], permissions...)
		}
	}
	for role, permissions := range plan {
		plan[role] = dedupe(permissions)
	}
	return plan
}

func ensureRole(ctx context.Context, store *Store, slug auth.Role) (Role, error) {
	role, err := store.RoleBySlug(ctx, slug)
	switch {
	case err == nil:
		return role, nil
	case !notFound(err):
		return Role{}, err
	}
	saved, err := store.Roles.Save(ctx, &Role{
		Slug:     string(slug),
		Name:     string(slug),
		IsSystem: true,
	})
	if err != nil {
		return Role{}, fmt.Errorf("access: seeding role %q: %w", slug, err)
	}
	return saved, nil
}

func attachAll(ctx context.Context, store *Store, role Role, want []auth.Permission, byCode map[auth.Permission]uuid.UUID) error {
	held, err := store.RolePermissions.GetAll(ctx, crud.Where(crud.Eq("RoleID", role.ID)))
	if err != nil {
		return err
	}
	have := make(map[uuid.UUID]struct{}, len(held))
	for _, rolePermission := range held {
		have[rolePermission.PermissionID] = struct{}{}
	}

	for _, code := range want {
		permissionID, declared := byCode[code]
		if !declared {
			return fmt.Errorf("access: role %q wants undeclared permission %q", role.Slug, code)
		}
		if _, duplicate := have[permissionID]; duplicate {
			continue
		}
		if err := store.RolePermissions.SaveOnly(ctx, &RolePermission{RoleID: role.ID, PermissionID: permissionID}); err != nil {
			return fmt.Errorf("access: granting %q to %q: %w", code, role.Slug, err)
		}
	}
	return nil
}

func OwnGrants() ModuleGrants {
	return ModuleGrants{
		Module: "access",
		Permissions: []PermissionDef{
			{Code: PermRoleRead, Name: "Read roles and permissions"},
			{Code: PermRoleWrite, Name: "Create and edit roles"},
			{Code: PermRoleDelete, Name: "Delete roles"},
			{Code: PermGrantRead, Name: "See what a subject was granted"},
			{Code: PermGrantWrite, Name: "Grant and revoke roles and permissions"},
			{Code: PermSessionRead, Name: "List anyone's sessions"},
			{Code: PermSessionKill, Name: "Close anyone's session"},
			{Code: PermCredentialWrite, Name: "Set another account's password"},
		},
	}
}

func cmpOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
