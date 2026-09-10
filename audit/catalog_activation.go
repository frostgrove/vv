package audit

type catalogActivationProof struct {
	expected CatalogRef
	next     CatalogRef
	valid    bool
}

type CatalogActivationProof struct{ value catalogActivationProof }

func NoAttemptCatalogActivation(expected, next CatalogRef) (CatalogActivationProof, error) {
	if next.valid() && (expected == (CatalogRef{}) && next.Generation == 1 || expected.valid() && expected.ID == next.ID && expected.Generation+1 == next.Generation && next.Generation > 1) {
		return CatalogActivationProof{value: catalogActivationProof{expected: expected, next: next, valid: true}}, nil
	}
	return CatalogActivationProof{}, auditErrorAt(ErrInvalid, "catalog_activation")
}

func (p CatalogActivationProof) ValidFor(expected, next CatalogRef) bool {
	return p.value.valid && p.value.expected == expected && p.value.next == next
}
