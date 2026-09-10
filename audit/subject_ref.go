package audit

import "github.com/frostgrove/vv/crud"

type subjectRef struct {
	declaration Declaration
	view        SubjectRefView
}

type SubjectRef struct{ value subjectRef }

type SubjectRefView struct {
	Resource       Resource
	Subject        Reference
	Mode           StorageMode
	Classification Classification
}

func (s SubjectRef) View() SubjectRefView { return s.value.view }

func (p *ResourcePolicy[M, ID]) SubjectRef(identifier ID) (_ SubjectRef, err error) {
	if p == nil || p.value == nil {
		return SubjectRef{}, auditErrorAt(ErrInvalid, "policy")
	}
	defer func() {
		if recover() != nil {
			err = auditErrorAt(ErrInvalid, "subject.mapper")
		}
	}()
	reference := Reference(p.value.subject.mapReference(identifier))
	if !validOpaqueReference(string(reference), MaxReferenceBytes) {
		return SubjectRef{}, auditErrorAt(ErrInvalid, "subject")
	}
	return SubjectRef{value: subjectRef{declaration: p, view: SubjectRefView{
		Resource: p.value.seal.description.Resource, Subject: reference,
		Mode: p.value.subject.mode, Classification: p.value.subject.classification,
	}}}, nil
}

func (p *ResourcePolicy[M, ID]) CheckModel(meta *crud.Meta) error {
	if p == nil || p.value == nil {
		return auditErrorAt(ErrInvalid, "policy")
	}
	if err := validateResourceModel[M, ID](meta); err != nil {
		return err
	}
	description := p.value.seal.description
	if policyFingerprint(description, resourceMetadata(meta, description.Fields)) != description.Semantics.Fingerprint {
		return auditErrorAt(ErrWrongCatalog, "model")
	}
	return nil
}
