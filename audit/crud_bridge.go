package audit

import (
	"context"

	"github.com/frostgrove/vv/audit/internal/auditcrudbridge"
	"github.com/frostgrove/vv/crud"
)

type auditCRUDCarrier = auditcrudbridge.Carrier

func newAuditCRUDCarrier(recorder *Recorder) auditCRUDCarrier {
	return auditcrudbridge.NewCarrier(recorder.prepareAuditCRUD)
}

func (r *Recorder) prepareAuditCRUD(ctx context.Context, spec auditcrudbridge.Spec) (auditcrudbridge.Runner, error) {
	if r == nil || r.value == nil {
		return nil, auditErrorAt(ErrInvalid, "recorder")
	}
	policyValue, operationValue, subjectValues, generated, ok := auditcrudbridge.InspectSpec(spec)
	if !ok {
		return nil, auditErrorAt(ErrAdmission, "crud.spec")
	}
	declaration, ok := policyValue.(compiledDeclaration)
	if !ok || declaration.sealedDeclaration() == nil || declaration.sealedDeclaration().description.Kind != ResourceDeclaration || !r.value.catalogContains(declaration) {
		return nil, auditErrorAt(ErrWrongCatalog, "crud.policy")
	}
	operation, ok := operationValue.(compiledOperationMember)
	if !ok {
		return nil, auditErrorAt(ErrAdmission, "crud.operation")
	}
	member := operation.sealedOperationMember()
	if member.declaration != declaration || member.resource == "" || member.action == "" || member.key == "" {
		return nil, auditErrorAt(ErrAdmission, "crud.operation")
	}
	subjects, err := inspectCRUDSubjects(declaration, subjectValues, generated)
	if err != nil {
		return nil, err
	}
	executor, bound, err := crud.SourceBoundExecutorFor(ctx, r.value.writer.TransactionSource())
	if err != nil {
		return nil, auditError(ErrTransaction, err)
	}
	frame, _ := ctx.Value(groupContextKey{}).(*groupFrame)
	var grouped *OperationType
	var idempotency IdempotencyKey
	if frame != nil {
		if frame.recorder != r.value || frame.execution == nil || !bound || !operationAccepts(frame.operation, declaration, member.resource, member.action) {
			return nil, auditErrorAt(ErrTransaction, "crud.group")
		}
		current, bindErr := r.value.writer.BindTransaction(executor)
		if bindErr != nil || nilByReflection(current) || !SameAuthority(current.Authority(), frame.execution.Authority()) {
			return nil, auditError(ErrTransaction, bindErr)
		}
		grouped = frame.operation
		idempotency = frame.idempotency
	} else {
		if bound {
			return nil, auditErrorAt(ErrTransaction, "crud.group")
		}
		grouped = implicitCRUDOperation(member)
	}
	return func(runContext context.Context, mutation auditcrudbridge.Mutation) error {
		result, err := r.within(runContext, GroupSpec{Operation: grouped, IdempotencyKey: idempotency}, func(groupContext context.Context) error {
			batch, mutationErr := mutation(groupContext)
			if mutationErr != nil {
				return mutationErr
			}
			values, inspected := auditcrudbridge.InspectBatch(batch)
			if !inspected {
				return auditErrorAt(ErrAdmission, "crud.batch")
			}
			entities, conversionErr := inspectCRUDBatch(declaration, member, subjects, generated, values)
			if conversionErr != nil {
				return conversionErr
			}
			if len(entities) == 0 {
				return nil
			}
			return stageEntities(groupContext, r, entities...)
		}, true)
		if reconcile, ok := result.ReconcileKey(); ok {
			return auditcrudbridge.ReportRecovery(reconcile, err)
		}
		return err
	}, nil
}

func inspectCRUDSubjects(declaration compiledDeclaration, values []any, generated bool) (map[Reference]struct{}, error) {
	if generated {
		if len(values) != 0 {
			return nil, auditErrorAt(ErrAdmission, "crud.subjects")
		}
		return nil, nil
	}
	result := make(map[Reference]struct{}, len(values))
	for _, value := range values {
		subject, ok := value.(SubjectRef)
		if !ok || subject.value.declaration != declaration || !validOpaqueReference(string(subject.value.view.Subject), MaxReferenceBytes) {
			return nil, auditErrorAt(ErrAdmission, "crud.subjects")
		}
		result[subject.value.view.Subject] = struct{}{}
	}
	if len(result) == 0 {
		return nil, auditErrorAt(ErrAdmission, "crud.subjects")
	}
	return result, nil
}

func inspectCRUDBatch(declaration compiledDeclaration, member declarationMember, subjects map[Reference]struct{}, generated bool, values []any) ([]EntityDraft, error) {
	entities := make([]EntityDraft, len(values))
	seen := make(map[Reference]struct{}, len(values))
	for index, value := range values {
		entity, ok := value.(EntityDraft)
		if !ok || entity.value.declaration != declaration || entity.value.descriptor.Resource != member.resource || Action(entity.value.action) != member.action {
			return nil, auditErrorAt(ErrAdmission, "crud.batch")
		}
		if _, duplicate := seen[entity.value.subject]; duplicate {
			return nil, auditErrorAt(ErrAdmission, "crud.batch")
		}
		seen[entity.value.subject] = struct{}{}
		if !generated {
			if _, expected := subjects[entity.value.subject]; !expected {
				return nil, auditErrorAt(ErrAdmission, "crud.batch")
			}
		}
		entities[index] = entity
	}
	if generated && (len(entities) != 1 || entities[0].value.action != EntityCreated) {
		return nil, auditErrorAt(ErrAdmission, "crud.batch")
	}
	return entities, nil
}

func implicitCRUDOperation(member declarationMember) *OperationType {
	seal := member.declaration.(compiledDeclaration).sealedDeclaration()
	operation := &OperationType{value: &operationType{members: []declarationMember{member}}}
	operation.value.seal.description = DeclarationDescription{
		Kind: OperationDeclaration, Operation: OperationName(member.action), Retention: member.retention,
		Consequence: member.consequence, Context: cloneContextPolicyDescription(seal.description.Context),
		Members: []string{member.key},
	}
	operation.value.seal.operationMembers = []declarationMember{member}
	return operation
}
