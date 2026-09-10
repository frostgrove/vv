package audit

type storeCatalogState struct {
	active    CatalogRef
	setDigest CatalogSetDigest
	hasActive bool
	installed bool
}

type StoreCatalogState struct {
	value storeCatalogState
}

func NewEmptyStoreCatalogState() StoreCatalogState {
	return StoreCatalogState{}
}

func NewInactiveStoreCatalogState(digest CatalogSetDigest) (StoreCatalogState, error) {
	if digest == (CatalogSetDigest{}) {
		return StoreCatalogState{}, auditErrorAt(ErrInvalid, "catalog_set")
	}
	return StoreCatalogState{value: storeCatalogState{setDigest: digest, installed: true}}, nil
}

func NewStoreCatalogState(active CatalogRef, digest CatalogSetDigest) (StoreCatalogState, error) {
	if !active.valid() {
		return StoreCatalogState{}, auditErrorAt(ErrInvalid, "active_catalog")
	}
	if digest == (CatalogSetDigest{}) {
		return StoreCatalogState{}, auditErrorAt(ErrInvalid, "catalog_set")
	}
	return StoreCatalogState{value: storeCatalogState{active: active, setDigest: digest, hasActive: true, installed: true}}, nil
}

func (s StoreCatalogState) HasActive() bool {
	return s.value.hasActive
}

func (s StoreCatalogState) Active() CatalogRef {
	return s.value.active
}

func (s StoreCatalogState) SetDigest() CatalogSetDigest {
	return s.value.setDigest
}

func (s StoreCatalogState) hasInstalled() bool {
	return s.value.installed
}

type StoreInfo interface {
	Capabilities() Capabilities
	Limits() Limits
	Backing() Backing
	BackingID() BackingID
	LogID() LogID
	Catalogs() StoreCatalogState
}
