package audit

import (
	"bytes"
	"context"
	"slices"
)

type CatalogMutationKind uint8

const (
	CatalogInstallMutation CatalogMutationKind = iota + 1
	CatalogActivateMutation
)

type CatalogMutationView struct {
	Kind     CatalogMutationKind
	Catalog  CatalogRef
	Expected CatalogRef
	Active   CatalogRef
	Change   CatalogChangeRef
	proof    catalogActivationProof
}

type catalogMutationLog struct {
	backing   BackingID
	log       LogID
	mutations []CatalogMutationView
	canonical []byte
}

type CatalogMutationLog struct{ value *catalogMutationLog }

func CatalogInstalled(catalog CatalogRef, change CatalogChangeRef) CatalogMutationView {
	return CatalogMutationView{Kind: CatalogInstallMutation, Catalog: catalog, Change: change}
}

func CatalogActivated(expected, active CatalogRef, change CatalogChangeRef, proof CatalogActivationProof) CatalogMutationView {
	return CatalogMutationView{
		Kind: CatalogActivateMutation, Expected: expected, Active: active, Change: change,
		proof: proof.value,
	}
}

func NewCatalogMutationLog(backing BackingID, log LogID, mutations []CatalogMutationView) (CatalogMutationLog, error) {
	if backing == (BackingID{}) {
		return CatalogMutationLog{}, auditErrorAt(ErrInvalid, "catalog_mutations.backing")
	}
	if log == (LogID{}) {
		return CatalogMutationLog{}, auditErrorAt(ErrInvalid, "catalog_mutations.log")
	}
	if len(mutations) > MaxCatalogMutations {
		return CatalogMutationLog{}, auditTooLarge("catalog_mutations", MaxCatalogMutations)
	}
	copy := slices.Clone(mutations)
	if err := validateCatalogMutationSequence(copy); err != nil {
		return CatalogMutationLog{}, err
	}
	canonical := encodeCatalogMutationLog(backing, log, copy)
	if len(canonical) > MaxCatalogMutationBytes {
		return CatalogMutationLog{}, auditTooLarge("catalog_mutations", MaxCatalogMutationBytes)
	}
	return CatalogMutationLog{value: &catalogMutationLog{
		backing: backing, log: log, mutations: copy, canonical: canonical,
	}}, nil
}

func (l CatalogMutationLog) BackingID() BackingID {
	if l.value == nil {
		return BackingID{}
	}
	return l.value.backing
}

func (l CatalogMutationLog) LogID() LogID {
	if l.value == nil {
		return LogID{}
	}
	return l.value.log
}

func (l CatalogMutationLog) Mutations() []CatalogMutationView {
	if l.value == nil {
		return nil
	}
	return slices.Clone(l.value.mutations)
}

func VerifyCatalogMutations(origin StoreInfo, expected []CatalogMutationView, actual CatalogMutationLog) error {
	if nilByReflection(origin) || !origin.Backing().valueValid() || origin.BackingID() == (BackingID{}) || origin.LogID() == (LogID{}) {
		return auditErrorAt(ErrWrongStore, "catalog_mutations.origin")
	}
	want, err := NewCatalogMutationLog(origin.BackingID(), origin.LogID(), expected)
	if err != nil {
		return err
	}
	if actual.value == nil || actual.value.backing != origin.BackingID() || actual.value.log != origin.LogID() {
		return auditErrorAt(ErrWrongStore, "catalog_mutations.origin")
	}
	validated, err := NewCatalogMutationLog(actual.value.backing, actual.value.log, actual.value.mutations)
	if err != nil || !bytes.Equal(validated.value.canonical, actual.value.canonical) {
		return auditErrorAt(ErrMalformedEvidence, "catalog_mutations")
	}
	if !bytes.Equal(want.value.canonical, actual.value.canonical) {
		return auditErrorAt(ErrConflict, "catalog_mutations")
	}
	return nil
}

type CatalogMutationLogReader interface {
	StoreInfo
	CatalogMutations(context.Context) (CatalogMutationLog, error)
}

type CatalogAdmin interface {
	CatalogMutationLogReader
	InstallCatalog(context.Context, Manifest, CatalogChangeRef) error
	ActivateCatalog(context.Context, CatalogRef, CatalogRef, CatalogChangeRef, CatalogActivationProof) error
	VerifyCatalogs(context.Context, []Manifest) error
}

func validateCatalogMutationSequence(mutations []CatalogMutationView) error {
	installed := make(map[CatalogRef]struct{}, len(mutations))
	coordinates := make(map[catalogCoordinate]CatalogRef, len(mutations))
	var lineage CatalogID
	var latest CatalogGeneration
	var active CatalogRef
	for index, mutation := range mutations {
		if !mutation.Change.valid() {
			return auditErrorAt(ErrInvalid, "catalog_mutations.change")
		}
		switch mutation.Kind {
		case CatalogInstallMutation:
			if !mutation.Catalog.valid() || mutation.Expected != (CatalogRef{}) || mutation.Active != (CatalogRef{}) || mutation.proof.valid {
				return auditErrorAt(ErrInvalid, "catalog_mutations.install")
			}
			coordinate := catalogCoordinate{id: mutation.Catalog.ID, generation: mutation.Catalog.Generation}
			if _, duplicate := coordinates[coordinate]; duplicate {
				return auditErrorAt(ErrConflict, "catalog_mutations.install")
			}
			if latest == 0 {
				if mutation.Catalog.Generation != 1 {
					return auditErrorAt(ErrInvalid, "catalog_mutations.order")
				}
				lineage = mutation.Catalog.ID
			} else if mutation.Catalog.ID != lineage || mutation.Catalog.Generation != latest+1 {
				return auditErrorAt(ErrInvalid, "catalog_mutations.order")
			}
			latest = mutation.Catalog.Generation
			if latest > MaxCatalogs {
				return auditTooLarge("catalog_mutations.catalogs", MaxCatalogs)
			}
			installed[mutation.Catalog] = struct{}{}
			coordinates[coordinate] = mutation.Catalog
		case CatalogActivateMutation:
			if mutation.Catalog != (CatalogRef{}) || !mutation.Active.valid() || !mutation.proof.valid || mutation.proof.expected != mutation.Expected || mutation.proof.next != mutation.Active {
				return auditErrorAt(ErrInvalid, "catalog_mutations.activation")
			}
			if _, ok := installed[mutation.Active]; !ok || mutation.Expected != active || !directCatalogChild(mutation.Expected, mutation.Active) {
				return auditErrorAt(ErrInvalid, "catalog_mutations.order")
			}
			active = mutation.Active
		default:
			return auditErrorAt(ErrInvalid, "catalog_mutations.kind")
		}
		if index+1 > MaxCatalogMutations {
			return auditTooLarge("catalog_mutations", MaxCatalogMutations)
		}
	}
	return nil
}

type catalogCoordinate struct {
	id         CatalogID
	generation CatalogGeneration
}

func directCatalogChild(expected, active CatalogRef) bool {
	if expected == (CatalogRef{}) {
		return active.valid() && active.Generation == 1
	}
	return expected.valid() && active.valid() && expected.ID == active.ID && expected.Generation+1 == active.Generation
}

func encodeCatalogMutationLog(backing BackingID, log LogID, mutations []CatalogMutationView) []byte {
	var output bytes.Buffer
	writeFrame(&output, []byte("frostgrove.audit/catalog-mutation-log/v1"))
	writeFrame(&output, backing[:])
	writeFrame(&output, log[:])
	writeUint32(&output, uint32(len(mutations)))
	for _, mutation := range mutations {
		writeFrame(&output, encodeCatalogMutation(mutation))
	}
	return output.Bytes()
}

func encodeCatalogMutation(mutation CatalogMutationView) []byte {
	var output bytes.Buffer
	writeUint32(&output, uint32(mutation.Kind))
	writeCatalogRef(&output, mutation.Catalog)
	writeCatalogRef(&output, mutation.Expected)
	writeCatalogRef(&output, mutation.Active)
	change := mutation.Change.View()
	writeFrame(&output, []byte(change.Ledger))
	writeFrame(&output, []byte(change.Change))
	writeBool(&output, mutation.proof.valid)
	writeCatalogRef(&output, mutation.proof.expected)
	writeCatalogRef(&output, mutation.proof.next)
	return output.Bytes()
}
