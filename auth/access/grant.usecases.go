package access

import (
	"context"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/errs"
)

// GrantService is what an administrator screen calls: give a subject a role,
// take it away, grant one permission directly, attach a permission to a role.
//
// Every method here takes a [SubjectRef] and none of them knows what is behind
// it. That is the whole point of this context, and it is why granting a role to
// a service account later needs no code in this file.
type GrantService struct {
	store *Store
}

func NewGrantService(store *Store) *GrantService {
	return &GrantService{store: store}
}

// CodeUnknownRole and CodeUnknownPermission are refusals a client can branch
// on. A role nobody declared is a 422 rather than a 404: the request is
// well-formed and names something that does not exist, which is a problem with
// the payload.
const (
	CodeUnknownRole       errs.Code = "unknown_role"
	CodeUnknownPermission errs.Code = "unknown_permission"
)

// GrantRole gives a subject a role. Granting one it already holds succeeds and
// changes nothing: an administrator clicking twice is not an error, and a
// unique-index violation surfaced here would read as one.
func (this *GrantService) GrantRole(ctx context.Context, cmd GrantRoleCommand) error {
	role, err := this.store.RoleBySlug(ctx, cmd.Role)
	if notFound(err) {
		return unknown("Role", CodeUnknownRole, string(cmd.Role))
	}
	if err != nil {
		return err
	}

	held, err := this.store.SubjectRoles.Exists(ctx,
		ofSubject(cmd.Subject),
		specs.As(SubjectRole_.RoleID.Eq(role.ID)),
	)
	if err != nil || held {
		return err
	}
	return this.store.SubjectRoles.SaveOnly(ctx, &SubjectRole{
		SubjectType: string(cmd.Subject.Type),
		SubjectID:   cmd.Subject.ID,
		RoleID:      role.ID,
	})
}

// RevokeRole takes it back, and reports nothing about whether it was held. The
// caller asked for an end state and got it.
func (this *GrantService) RevokeRole(ctx context.Context, cmd GrantRoleCommand) error {
	role, err := this.store.RoleBySlug(ctx, cmd.Role)
	if notFound(err) {
		return unknown("Role", CodeUnknownRole, string(cmd.Role))
	}
	if err != nil {
		return err
	}
	_, err = this.store.SubjectRoles.DeleteAll(ctx,
		ofSubject(cmd.Subject),
		specs.As(SubjectRole_.RoleID.Eq(role.ID)),
	)
	return err
}

// GrantPermission attaches one permission to a subject without a role. It is
// the exception that would otherwise become a role with a single member.
func (this *GrantService) GrantPermission(ctx context.Context, cmd GrantPermissionCommand) error {
	permission, err := this.store.PermissionByCode(ctx, cmd.Permission)
	if notFound(err) {
		return unknown("Permission", CodeUnknownPermission, string(cmd.Permission))
	}
	if err != nil {
		return err
	}

	held, err := this.store.SubjectPermissions.Exists(ctx,
		ofSubject(cmd.Subject),
		specs.As(SubjectPermission_.PermissionID.Eq(permission.ID)),
	)
	if err != nil || held {
		return err
	}
	return this.store.SubjectPermissions.SaveOnly(ctx, &SubjectPermission{
		SubjectType:  string(cmd.Subject.Type),
		SubjectID:    cmd.Subject.ID,
		PermissionID: permission.ID,
	})
}

// RevokePermission removes a direct grant. It does not touch what a role
// grants: taking "role.write" away from somebody who holds it through admin
// would have to either fail or silently do nothing, and doing nothing while
// answering 204 is the worse of the two.
func (this *GrantService) RevokePermission(ctx context.Context, cmd GrantPermissionCommand) error {
	permission, err := this.store.PermissionByCode(ctx, cmd.Permission)
	if notFound(err) {
		return unknown("Permission", CodeUnknownPermission, string(cmd.Permission))
	}
	if err != nil {
		return err
	}
	_, err = this.store.SubjectPermissions.DeleteAll(ctx,
		ofSubject(cmd.Subject),
		specs.As(SubjectPermission_.PermissionID.Eq(permission.ID)),
	)
	return err
}

// AttachToRole adds a permission to a role.
func (this *GrantService) AttachToRole(ctx context.Context, cmd AttachPermissionCommand) error {
	permission, err := this.store.PermissionByCode(ctx, cmd.Permission)
	if notFound(err) {
		return unknown("Permission", CodeUnknownPermission, string(cmd.Permission))
	}
	if err != nil {
		return err
	}
	held, err := this.store.RolePermissions.Exists(ctx, crud.Where(crud.And(
		crud.Eq("RoleID", cmd.Role),
		crud.Eq("PermissionID", permission.ID),
	)))
	if err != nil || held {
		return err
	}
	return this.store.RolePermissions.SaveOnly(ctx, &RolePermission{RoleID: cmd.Role, PermissionID: permission.ID})
}

// DetachFromRole removes it.
//
// A system role is left alone: the start-up sync would put the permission
// straight back, so allowing the call would answer 204 to a change that does
// not survive a restart.
func (this *GrantService) DetachFromRole(ctx context.Context, cmd AttachPermissionCommand) error {
	role, err := this.store.Roles.GetByID(ctx, cmd.Role)
	if err != nil {
		return err
	}
	if role.IsSystem {
		return frozenSystemRole("DetachPermission", role.Slug)
	}
	permission, err := this.store.PermissionByCode(ctx, cmd.Permission)
	if notFound(err) {
		return unknown("Permission", CodeUnknownPermission, string(cmd.Permission))
	}
	if err != nil {
		return err
	}
	_, err = this.store.RolePermissions.DeleteAll(ctx, crud.Where(crud.And(
		crud.Eq("RoleID", role.ID),
		crud.Eq("PermissionID", permission.ID),
	)))
	return err
}

// Describe answers what a subject holds, split into what it was given and what
// the gate will actually enforce. The two differ whenever a role carries more
// than somebody remembers, which is the question this endpoint exists for.
func (this *GrantService) Describe(ctx context.Context, grants *GrantsService, ref SubjectRef) (GrantsDto, error) {
	principal, err := grants.For(ctx, ref)
	if err != nil {
		return GrantsDto{}, err
	}

	direct, err := this.store.SubjectPermissions.GetAll(ctx,
		ofSubject(ref),
		crud.Preload(SubjectPermission_.Permission.Path()),
	)
	if err != nil {
		return GrantsDto{}, err
	}
	codes := make([]string, 0, len(direct))
	for _, directPermission := range direct {
		if directPermission.Permission != nil {
			codes = append(codes, directPermission.Permission.Code)
		}
	}

	return GrantsDto{
		Subject:           ref,
		Roles:             texts(principal.Roles),
		DirectPermissions: codes,
		Effective:         texts(principal.Permissions),
	}, nil
}

func unknown(field string, code errs.Code, value string) error {
	return errs.Validation().
		Field(field).Code(code).
		Message(value + " is not something this application declares").
		Entity("Grant").Fault()
}
