package access

import (
	"context"
	"fmt"
	"slices"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/specs"
	"github.com/google/uuid"
)

type GrantsService struct {
	store       *Store
	directories Directories

	// The identifier-folding rule each registered subject declared. It is here
	// because the password paths reach a subject through this service and
	// nowhere else, and an identifier written without folding is one that no
	// sign-in can ever look up.
	normalizers map[SubjectType]func(string) string
}

func (this *GrantsService) registerNormalizer(subject Subject) {
	if this == nil || subject.Normalize == nil {
		return
	}
	if this.normalizers == nil {
		this.normalizers = make(map[SubjectType]func(string) string, 1)
	}
	this.normalizers[subject.Type] = subject.Normalize
}

// The identifier as it must be stored and looked up. A subject that declared no
// rule folds nothing, which is the same answer Subject.Identifier gives.
func (this *GrantsService) normalize(subjectType SubjectType, raw string) string {
	if this == nil {
		return raw
	}
	if fold, ok := this.normalizers[subjectType]; ok && fold != nil {
		return fold(raw)
	}
	return raw
}

func NewGrants(store *Store, directories Directories) *GrantsService {
	return &GrantsService{store: store, directories: directories}
}

var _ Grants = (*GrantsService)(nil)

func (this *GrantsService) For(ctx context.Context, ref SubjectRef) (*Principal, error) {
	if ref.Zero() {
		return nil, fmt.Errorf("access: resolving grants for an empty subject")
	}

	assigned, err := this.store.SubjectRoles.GetAll(ctx, ofSubject(ref))
	if err != nil {
		return nil, err
	}

	roles := make([]auth.Role, 0, len(assigned))
	permissions := make([]auth.Permission, 0, 16)

	if ids := roleIDs(assigned); len(ids) > 0 {
		held, err := this.store.Roles.GetAll(ctx,
			crud.Where(crud.InAny("ID", ids)),
			crud.Preload(Role_.Permissions.Path()),
		)
		if err != nil {
			return nil, err
		}
		for _, role := range held {
			roles = append(roles, auth.Role(role.Slug))
			for _, permission := range role.Permissions {
				permissions = append(permissions, auth.Permission(permission.Code))
			}
		}
	}

	direct, err := this.store.SubjectPermissions.GetAll(ctx,
		ofSubject(ref),
		crud.Preload(SubjectPermission_.Permission.Path()),
	)
	if err != nil {
		return nil, err
	}
	for _, directPermission := range direct {
		if directPermission.Permission != nil {
			permissions = append(permissions, auth.Permission(directPermission.Permission.Code))
		}
	}

	principal := &Principal{
		Ref:         ref,
		Roles:       dedupe(roles),
		Permissions: dedupe(permissions),
	}

	if directory, ok := this.directories[ref.Type]; ok {
		profile, err := directory.Describe(ctx, ref.ID)
		switch {
		case err == nil:
			principal.Profile = profile
		default:
			principal.ProfileUnresolved = true
		}
	}
	return principal, nil
}

func (this *GrantsService) Directory(subjectType SubjectType) (Directory, bool) {
	directory, ok := this.directories[subjectType]
	return directory, ok
}

func roleIDs(rows []SubjectRole) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rows))
	for _, role := range rows {
		out = append(out, role.RoleID)
	}
	return out
}

func dedupe[T ~string](values []T) []T {
	slices.Sort(values)
	return slices.Compact(values)
}

func permissionCodes(permissions []auth.Permission) []string {
	out := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		out = append(out, string(permission))
	}
	return out
}

func permissionsIn(codes []auth.Permission) crud.Option {
	return specs.As(Permission_.Code.In(permissionCodes(codes)...))
}
