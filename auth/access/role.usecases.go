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

// The CRUD half of this context: one use case per operation on a role, and the
// read-only view of the permissions table.
//
// Both are port.Service values, so the same value would mount on Gin, net/http
// or gRPC unchanged. The rules below sit between the handler and the
// repository, which is the only place a rule is safe from a second transport
// that forgot it.

// RolePolicy is the gate every role read and write passes.
//
// PerAction rather than a single permission: reading the role list is something
// an administrator screen does, creating one is a change to the security model,
// and deleting one can lock people out. A verb the map does not name is
// refused, so a verb added to the seam later is refused rather than inherited.
func RolePolicy() security.Policy[Role, uuid.UUID] {
	return security.PerAction[Role, uuid.UUID](map[security.Action]auth.Permission{
		security.Read:   PermRoleRead,
		security.Create: PermRoleWrite,
		security.Update: PermRoleWrite,
		security.Delete: PermRoleDelete,
	})
}

// PermissionPolicy gates the permissions table. It is read-only through the
// API on purpose: which permissions exist is decided by which modules are
// compiled in, and a row created by hand is a code nothing enforces.
func PermissionPolicy() security.Policy[Permission, uuid.UUID] {
	return security.Combine(
		security.ReadOnly[Permission, uuid.UUID](),
		security.RequirePermission[Permission, uuid.UUID](PermRoleRead),
	)
}

// RoleQuery bounds what a client may filter, sort and preload. An allow-list
// per endpoint, because the wire DSL is otherwise as wide as the model.
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

// PermissionQuery is the same for the read-only resource.
func PermissionQuery() *query.Config {
	return &query.Config{
		Filterable:          []string{"Code", "Name", "Module"},
		Searchable:          []string{"Code", "Name", "Module"},
		DefaultSearchFields: []string{"Code", "Name"},
		Sortable:            []string{"Code", "Module", "CreatedAt"},
		MaxLimit:            200,
	}
}

// RoleService is the default orchestration plus the rules a role has.
type RoleService struct {
	*port.DefaultService[Role, uuid.UUID, RoleUpdate]

	// roles is the *gated* repository, and the overrides below read through it
	// rather than through the store.
	//
	// That is not tidiness. Each override loads the row first to see whether it
	// is a system role, and an ungated load answers before anything has checked
	// the caller — so an anonymous DELETE /roles/{id} came back "the admin role
	// is part of the application and cannot be removed", which tells a stranger
	// that the id is real, that it is a system role, and what it is called. The
	// gate has to run first, and the way to make it run first is to ask it.
	roles *crud.Repo[Role, uuid.UUID, RoleUpdate]
}

// NewRoleService binds the gated repository and wraps it in the rules.
func NewRoleService(store *Store) *RoleService {
	gated := crud.Decorate(store.Roles, security.Gate(RolePolicy()))
	return &RoleService{
		DefaultService: port.NewService[Role, uuid.UUID, RoleUpdate](gated,
			port.WithQuery(RoleQuery()), port.WithPaths(RolePaths)),
		roles: gated,
	}
}

var _ port.Service[Role, uuid.UUID, RoleUpdate] = (*RoleService)(nil)

// Create normalises the slug before the unique index sees it.
//
// Normalising rather than rejecting: "Lease Reviewer" and "lease-reviewer" are
// the same intent, and an administrator typing the first should not have to
// learn the second. What is rejected is a slug that normalises to nothing.
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
	// A role created through the API is never a system role, whatever the body
	// said. IsSystem is what the two refusals below are keyed on, so letting a
	// request set it would let a request opt out of them.
	cmd.Model.IsSystem = false
	return this.DefaultService.Create(ctx, cmd)
}

// Update refuses to rename a system role.
//
// The seeding pass looks a system role up by slug; renaming one makes the next
// start create a second, empty role under the old name and quietly stop
// granting anything through the one people are actually in.
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

// Delete refuses to remove a system role, and takes its grants with it
// otherwise.
//
// The role_permissions and subject_roles rows go by ON DELETE CASCADE in the
// schema rather than here: a second statement in this method would be skipped
// by every other path that deletes a role, and the database is the only place
// that cannot be bypassed.
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

// DeleteMany applies the same rule to a bulk delete. Without this arm the
// endpoint that takes a list of ids is a way around the single-id one.
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

// CodeSystemRole is this context's own error code. A client branches on it
// rather than on the sentence, and the status comes from the kind — Forbidden —
// so no status table anywhere needed a new arm for it.
const CodeSystemRole errs.Code = "system_role"

// NewPermissionService is the read-only view of the declared permissions.
func NewPermissionService(store *Store) *port.DefaultService[Permission, uuid.UUID, PermissionUpdate] {
	gated := crud.Decorate(store.Permissions, security.Gate(PermissionPolicy()))
	return port.NewService[Permission, uuid.UUID, PermissionUpdate](gated,
		port.WithQuery(PermissionQuery()), port.WithPaths(PermissionPaths))
}

// Slugify is the one normalisation a role identifier gets: lowercase, and
// anything that is not a letter, a digit or a dash becomes a dash.
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
