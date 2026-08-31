package access

import (
	"context"
	"strings"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
	"github.com/google/uuid"
)

func RolePolicy() security.Policy[Role, uuid.UUID] {
	return security.PerAction[Role, uuid.UUID](map[security.Action]auth.Permission{
		security.Read:   PermRoleRead,
		security.Create: PermRoleWrite,
		security.Update: PermRoleWrite,
		security.Delete: PermRoleDelete,
	})
}

func PermissionPolicy() security.Policy[Permission, uuid.UUID] {
	return security.Combine(
		security.ReadOnly[Permission, uuid.UUID](),
		security.RequirePermission[Permission, uuid.UUID](PermRoleRead),
	)
}

func RoleQuery() *query.Config {
	return &query.Config{
		Filterable:          []string{"Slug", "Name", "IsSystem"},
		Searchable:          []string{"Slug", "Name"},
		DefaultSearchFields: []string{"Slug", "Name"},
		Sortable:            []string{"Slug", "Name", "CreatedAt"},
		Selectable:          []string{"ID", "Slug", "Name", "IsSystem", "CreatedAt"},
		Preloadable:         []string{"Permissions"},
		MaxLimit:            100,
	}
}

func PermissionQuery() *query.Config {
	return &query.Config{
		Filterable:          []string{"Code", "Name", "Module"},
		Searchable:          []string{"Code", "Name", "Module"},
		DefaultSearchFields: []string{"Code", "Name"},
		Sortable:            []string{"Code", "Module", "CreatedAt"},
		MaxLimit:            200,
	}
}

type RoleService struct {
	*port.DefaultService[Role, uuid.UUID, RoleUpdate]

	roles *crud.Repo[Role, uuid.UUID, RoleUpdate]
}

func NewRoleService(store *Store) *RoleService {
	gated := crud.Decorate(store.Roles, security.Gate(RolePolicy()))
	return &RoleService{
		DefaultService: port.NewService[Role, uuid.UUID, RoleUpdate](gated,
			port.WithQuery(RoleQuery()), port.WithPaths(RolePaths)),
		roles: gated,
	}
}

var _ port.Service[Role, uuid.UUID, RoleUpdate] = (*RoleService)(nil)

func (this *RoleService) Create(ctx context.Context, cmd port.CreateCommand[Role]) (Role, error) {
	slug := Slugify(cmpOr(cmd.Model.Slug, cmd.Model.Name))
	if slug == "" {
		return Role{}, errs.Validation().
			Field("Slug").Code(errs.CodeRequired).
			Message("a role needs a slug or a name to derive one from").
			Entity("Role").Op("Create").Fault()
	}
	cmd.Model.Slug = slug
	cmd.Model.Name = cmpOr(cmd.Model.Name, slug)

	cmd.Model.IsSystem = false
	return this.DefaultService.Create(ctx, cmd)
}

func (this *RoleService) Update(ctx context.Context, cmd port.UpdateCommand[uuid.UUID, RoleUpdate]) (Role, error) {
	if cmd.Patch.Slug != nil {
		role, err := this.roles.GetByID(ctx, cmd.ID)
		if err != nil {
			return Role{}, err
		}
		if role.IsSystem {
			return Role{}, frozenSystemRole("Update", role.Slug)
		}
		normalised := Slugify(*cmd.Patch.Slug)
		if normalised == "" {
			return Role{}, errs.Validation().
				Field("Slug").Code(errs.CodeRequired).
				Entity("Role").Op("Update").Fault()
		}
		cmd.Patch.Slug = &normalised
	}
	return this.DefaultService.Update(ctx, cmd)
}

func (this *RoleService) Delete(ctx context.Context, cmd port.DeleteCommand[uuid.UUID]) (int64, error) {
	role, err := this.roles.GetByID(ctx, cmd.ID)
	if err != nil {
		return 0, err
	}
	if role.IsSystem {
		return 0, frozenSystemRole("Delete", role.Slug)
	}
	return this.DefaultService.Delete(ctx, cmd)
}

func (this *RoleService) DeleteMany(ctx context.Context, cmd port.BulkDeleteCommand[uuid.UUID]) (int64, error) {
	for _, id := range cmd.IDs {
		role, err := this.roles.GetByID(ctx, id)
		if err != nil {
			return 0, err
		}
		if role.IsSystem {
			return 0, frozenSystemRole("DeleteMany", role.Slug)
		}
	}
	return this.DefaultService.DeleteMany(ctx, cmd)
}

func frozenSystemRole(operation, slug string) error {
	return errs.Forbidden().
		Field("IsSystem").Code(CodeSystemRole).
		Message("the " + slug + " role is part of the application and cannot be renamed or removed").
		Entity("Role").Op(operation).Fault()
}

const CodeSystemRole errs.Code = "system_role"

func NewPermissionService(store *Store) *port.DefaultService[Permission, uuid.UUID, PermissionUpdate] {
	gated := crud.Decorate(store.Permissions, security.Gate(PermissionPolicy()))
	return port.NewService[Permission, uuid.UUID, PermissionUpdate](gated,
		port.WithQuery(PermissionQuery()), port.WithPaths(PermissionPaths))
}

func Slugify(input string) string {
	var builder strings.Builder
	builder.Grow(len(input))
	dash := false
	for _, character := range strings.ToLower(strings.TrimSpace(input)) {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			if dash && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			dash = false
			builder.WriteRune(character)
		default:
			dash = builder.Len() > 0
		}
	}
	return builder.String()
}
