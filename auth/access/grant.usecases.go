package access

import (
	"context"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/frostgrove/vv/errs"
)

type GrantService struct {
	store *Store
}

func NewGrantService(store *Store) *GrantService {
	return &GrantService{store: store}
}

const (
	CodeUnknownRole       errs.Code = "unknown_role"
	CodeUnknownPermission errs.Code = "unknown_permission"
)

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
