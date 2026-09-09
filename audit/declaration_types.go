package audit

type Descriptor struct {
	Resource    Resource
	Action      Action
	Owner       Owner
	Purpose     Purpose
	Retention   RetentionClass
	Consequence Consequence
	Context     ContextPolicy
}

type Declaration interface {
	auditDeclaration()
}

type SubjectDescription struct {
	Classification Classification
	Mode           StorageMode
}

type CatalogRef struct {
	ID         CatalogID
	Generation CatalogGeneration
	Digest     CatalogDigest
}

func (r CatalogRef) valid() bool {
	return validSemanticName(string(r.ID)) && r.Generation > 0 && r.Digest != (CatalogDigest{})
}

type RevisionRef struct {
	Catalog  CatalogRef
	Revision RevisionID
}

func (r RevisionRef) valid() bool {
	return r.Catalog.valid() && r.Revision != (RevisionID{})
}

type ItemRef struct {
	Revision RevisionRef
	Ordinal  uint16
}

func (r ItemRef) valid() bool {
	return r.Revision.valid() && r.Ordinal < MaxItems
}

type EntityAction Action

const (
	EntityCreated     EntityAction = "entity.created"
	EntityChanged     EntityAction = "entity.changed"
	EntitySoftDeleted EntityAction = "entity.soft_deleted"
	EntityHardDeleted EntityAction = "entity.hard_deleted"
	EntityRestored    EntityAction = "entity.restored"
)
