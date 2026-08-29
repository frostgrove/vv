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

// GrantsService resolves what a subject may do, from the stored rows, every
// time it is asked.
//
// Not from a token claim, and not from a cache. A role taken away has to bite
// on the next request: an authorization that reads a claim minted an hour ago
// is an authorization that survives the revocation by an hour, and the incident
// where that matters is exactly the one where somebody is revoking access in a
// hurry.
type GrantsService struct {
	store       *Store
	directories Directories
}

// NewGrants builds the resolver. The directories are indexed by their own
// declared type rather than by a key somebody types twice — see
// [NewDirectories].
func NewGrants(store *Store, directories Directories) *GrantsService {
	return &GrantsService{store: store, directories: directories}
}

var _ Grants = (*GrantsService)(nil)

// For resolves one subject into a principal: its roles, the permissions those
// roles carry, the permissions granted to it directly, and whatever its
// directory says it looks like.
//
// Three statements plus their preloads, per request. That is the price of the
// paragraph above, and it is the right trade for an application whose whole
// job is deciding what a lawyer may see.
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

	// A directory that cannot describe the subject is not a reason to refuse
	// the request: the profile is decoration on a response, and the decision
	// this function exists for has already been made.
	if directory, ok := this.directories[ref.Type]; ok {
		if profile, err := directory.Describe(ctx, ref.ID); err == nil {
			principal.Profile = profile
		}
	}
	return principal, nil
}

// Directory answers the store behind a subject type, and whether there is one.
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

// permissionCodes is the metamodel-typed spelling of "these permissions", used
// where a set of codes has to be turned into rows.
func permissionCodes(permissions []auth.Permission) []string {
	out := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		out = append(out, string(permission))
	}
	return out
}

// permissionsIn is the predicate for a batch lookup by code.
func permissionsIn(codes []auth.Permission) crud.Option {
	return specs.As(Permission_.Code.In(permissionCodes(codes)...))
}
