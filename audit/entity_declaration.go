package audit

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/utils"
)

type Policy[M any, ID comparable] struct {
	Model      *crud.Meta
	Semantics  PolicySemantics
	Descriptor Descriptor
	Subject    SubjectPolicy[ID]
	Actions    []EntityAction
	Fields     []EntityField[M]
}

type subjectPolicy[ID comparable] struct {
	mapReference   func(ID) string
	classification Classification
	mode           StorageMode
}

type SubjectPolicy[ID comparable] struct {
	value subjectPolicy[ID]
}

type modelMember[M, F any] struct {
	selectField func(*M) *F
}

type ModelMember[M, F any] struct {
	value modelMember[M, F]
}

type entityFieldValue[M, V any] struct {
	source         string
	member         func(*M) *V
	name           FieldName
	codec          Codec[V]
	classification Classification
	mode           StorageMode
	optional       bool
	reconstruct    bool
	historicalOnly bool
	queryIndex     bool
}

type ReconstructField[M, V any] struct {
	field entityFieldValue[M, V]
}

type EntityField[M any] interface {
	auditEntityField(*M)
}

type EntityIndex[M, V any] struct {
	value entityFieldValue[M, V]
}

type boundEntityField[M any] interface {
	description() FieldDescription
	extract(*M) (extractedEntityValue, error)
}

type entityFieldDeclaration[M any] interface {
	EntityField[M]
	bindEntityField(*crud.Schema) (boundEntityField[M], error)
}

type boundEntityFieldValue[M, V any] struct {
	field            *crud.Field
	schema           *crud.Schema
	descriptionValue FieldDescription
	codec            Codec[V]
	optional         bool
}

type extractedEntityValue struct {
	value      draftValue
	comparison []byte
}

type resourcePolicy[M any, ID comparable] struct {
	seal       declarationSeal
	schema     *crud.Schema
	table      string
	descriptor Descriptor
	subject    subjectPolicy[ID]
	actions    []EntityAction
	fields     []boundEntityField[M]
}

type ResourcePolicy[M any, ID comparable] struct {
	value *resourcePolicy[M, ID]
}

func PlaintextSubject[ID comparable](mapper func(ID) string, classification Classification) SubjectPolicy[ID] {
	return subject[ID](mapper, classification, AsPlaintext)
}

func TokenizedSubject[ID comparable](mapper func(ID) string, classification Classification) SubjectPolicy[ID] {
	return subject[ID](mapper, classification, AsToken)
}

func IndexedProtectedSubject[ID comparable](mapper func(ID) string, classification Classification) SubjectPolicy[ID] {
	return subject[ID](mapper, classification, AsIndexedProtected)
}

func subject[ID comparable](mapper func(ID) string, classification Classification, mode StorageMode) SubjectPolicy[ID] {
	if mapper == nil {
		panic(auditErrorAt(ErrDeclaration, "subject.mapper"))
	}
	if err := validateSubjectPrivacy(classification, mode, false); err != nil {
		panic(err)
	}
	return SubjectPolicy[ID]{value: subjectPolicy[ID]{mapReference: mapper, classification: classification, mode: mode}}
}

func Actions(actions ...EntityAction) []EntityAction {
	return slices.Clone(actions)
}

func Fields[M any](fields ...EntityField[M]) []EntityField[M] {
	return slices.Clone(fields)
}

func Member[M, F any](selector func(*M) *F) ModelMember[M, F] {
	member, err := TryMember(selector)
	if err != nil {
		panic(err)
	}
	return member
}

func TryMember[M, F any](selector func(*M) *F) (ModelMember[M, F], error) {
	if selector == nil {
		return ModelMember[M, F]{}, auditErrorAt(ErrDeclaration, "member.selector")
	}
	return ModelMember[M, F]{value: modelMember[M, F]{selectField: selector}}, nil
}

func Reconstruct[M, V any](source string, name FieldName, codec Codec[V], classification Classification) ReconstructField[M, V] {
	return ReconstructField[M, V]{field: newEntityField[M](source, nil, name, codec, classification, AsProtected, false, true, false, false)}
}

func ReconstructBy[M, F, V any](member ModelMember[M, F], name FieldName, codec Codec[V], classification Classification) ReconstructField[M, V] {
	return ReconstructField[M, V]{field: newEntityField[M]("", adaptMember[M, F, V](member), name, codec, classification, AsProtected, false, true, false, false)}
}

func HistoricalReconstruct[M, V any](name FieldName, codec Codec[V], classification Classification) ReconstructField[M, V] {
	return ReconstructField[M, V]{field: newEntityField[M]("", nil, name, codec, classification, AsProtected, false, true, true, false)}
}

func Value[M, V any](source string, name FieldName, codec Codec[V], classification Classification) EntityField[M] {
	return newEntityField[M](source, nil, name, codec, classification, AsPlaintext, false, false, false, false)
}

func ValueBy[M, F, V any](member ModelMember[M, F], name FieldName, codec Codec[V], classification Classification) EntityField[M] {
	return newEntityField[M]("", adaptMember[M, F, V](member), name, codec, classification, AsPlaintext, false, false, false, false)
}

func Optional[M, V any](source string, name FieldName, codec Codec[V], classification Classification) EntityField[M] {
	return newEntityField[M](source, nil, name, codec, classification, AsPlaintext, true, false, false, false)
}

func OptionalBy[M, F, V any](member ModelMember[M, F], name FieldName, codec Codec[V], classification Classification) EntityField[M] {
	return newEntityField[M]("", adaptMember[M, F, V](member), name, codec, classification, AsPlaintext, true, false, false, false)
}

func Redacted[M, V any](source string, name FieldName, codec Codec[V], classification Classification) EntityField[M] {
	return newEntityField[M](source, nil, name, codec, classification, AsRedacted, false, false, false, false)
}

func RedactedBy[M, F, V any](member ModelMember[M, F], name FieldName, codec Codec[V], classification Classification) EntityField[M] {
	return newEntityField[M]("", adaptMember[M, F, V](member), name, codec, classification, AsRedacted, false, false, false, false)
}

func Tokenized[M, V any](source string, name FieldName, codec Codec[V], classification Classification) EntityField[M] {
	return newEntityField[M](source, nil, name, codec, classification, AsToken, false, false, false, false)
}

func TokenizedBy[M, F, V any](member ModelMember[M, F], name FieldName, codec Codec[V], classification Classification) EntityField[M] {
	return newEntityField[M]("", adaptMember[M, F, V](member), name, codec, classification, AsToken, false, false, false, false)
}

func Protected[M, V any](source string, name FieldName, codec Codec[V], classification Classification) EntityField[M] {
	return newEntityField[M](source, nil, name, codec, classification, AsProtected, false, false, false, false)
}

func ProtectedBy[M, F, V any](member ModelMember[M, F], name FieldName, codec Codec[V], classification Classification) EntityField[M] {
	return newEntityField[M]("", adaptMember[M, F, V](member), name, codec, classification, AsProtected, false, false, false, false)
}

func EntityPlaintextIndex[M, V any](source string, name FieldName, codec Codec[V], classification Classification) EntityIndex[M, V] {
	return EntityIndex[M, V]{value: newEntityField[M](source, nil, name, codec, classification, AsPlaintext, false, false, false, true)}
}

func EntityTokenIndex[M, V any](source string, name FieldName, codec Codec[V], classification Classification) EntityIndex[M, V] {
	return EntityIndex[M, V]{value: newEntityField[M](source, nil, name, codec, classification, AsToken, false, false, false, true)}
}

func EntityIndexedProtectedIndex[M, V any](source string, name FieldName, codec Codec[V], classification Classification) EntityIndex[M, V] {
	return EntityIndex[M, V]{value: newEntityField[M](source, nil, name, codec, classification, AsIndexedProtected, false, false, false, true)}
}

func EntityPlaintextIndexBy[M, F, V any](member ModelMember[M, F], name FieldName, codec Codec[V], classification Classification) EntityIndex[M, V] {
	return EntityIndex[M, V]{value: newEntityField[M]("", adaptMember[M, F, V](member), name, codec, classification, AsPlaintext, false, false, false, true)}
}

func EntityTokenIndexBy[M, F, V any](member ModelMember[M, F], name FieldName, codec Codec[V], classification Classification) EntityIndex[M, V] {
	return EntityIndex[M, V]{value: newEntityField[M]("", adaptMember[M, F, V](member), name, codec, classification, AsToken, false, false, false, true)}
}

func EntityIndexedProtectedIndexBy[M, F, V any](member ModelMember[M, F], name FieldName, codec Codec[V], classification Classification) EntityIndex[M, V] {
	return EntityIndex[M, V]{value: newEntityField[M]("", adaptMember[M, F, V](member), name, codec, classification, AsIndexedProtected, false, false, false, true)}
}

func (field ReconstructField[M, V]) auditEntityField(*M) {}

func (field ReconstructField[M, V]) bindEntityField(schema *crud.Schema) (boundEntityField[M], error) {
	return field.field.bindEntityField(schema)
}

func (field entityFieldValue[M, V]) auditEntityField(*M) {}

func (field entityFieldValue[M, V]) bindEntityField(schema *crud.Schema) (boundEntityField[M], error) {
	if field.historicalOnly {
		description := field.description("")
		return boundEntityFieldValue[M, V]{descriptionValue: description, codec: field.codec}, nil
	}
	resolved, err := resolveEntityField(schema, field.source, field.member)
	if err != nil {
		return nil, err
	}
	if err := validateEntityFieldType(resolved.Type, reflect.TypeFor[V](), field.optional); err != nil {
		return nil, err
	}
	if err := validateFieldPrivacy(field.classification, field.mode, resolved.Secret); err != nil {
		return nil, err
	}
	if field.reconstruct && (resolved.Secret || field.mode != AsPlaintext && field.mode != AsProtected) {
		return nil, auditErrorAt(ErrDeclaration, "field.reconstruct")
	}
	frozen := *resolved
	return boundEntityFieldValue[M, V]{
		field:            &frozen,
		schema:           schema,
		descriptionValue: field.description(resolved.Name),
		codec:            field.codec,
		optional:         field.optional,
	}, nil
}

func (index EntityIndex[M, V]) Field() EntityField[M] {
	return index.value
}

func newEntityField[M, V any](source string, member func(*M) *V, name FieldName, codec Codec[V], classification Classification, mode StorageMode, optional, reconstruct, historicalOnly, queryIndex bool) entityFieldValue[M, V] {
	if !validSemanticName(string(name)) {
		panic(auditErrorAt(ErrDeclaration, "field.name"))
	}
	if codec.value == nil {
		panic(auditErrorAt(ErrDeclaration, "field.codec"))
	}
	if err := validateFieldPrivacy(classification, mode, false); err != nil {
		panic(err)
	}
	if historicalOnly != (source == "" && member == nil) || !historicalOnly && source == "" && member == nil {
		panic(auditErrorAt(ErrDeclaration, "field.source"))
	}
	return entityFieldValue[M, V]{
		source: source, member: member, name: name, codec: codec,
		classification: classification, mode: mode, optional: optional,
		reconstruct: reconstruct, historicalOnly: historicalOnly, queryIndex: queryIndex,
	}
}

func adaptMember[M, F, V any](member ModelMember[M, F]) func(*M) *V {
	if member.value.selectField == nil || reflect.TypeFor[F]() != reflect.TypeFor[V]() {
		panic(auditErrorAt(ErrDeclaration, "member.type"))
	}
	return func(model *M) *V {
		return any(member.value.selectField(model)).(*V)
	}
}

func (field entityFieldValue[M, V]) description(source string) FieldDescription {
	return FieldDescription{
		Source: source, Name: field.name, Codec: field.codec.Description(),
		Classification: field.classification, Mode: field.mode, Reconstruct: field.reconstruct,
		HistoricalOnly: field.historicalOnly, QueryIndex: field.queryIndex,
	}
}

func resolveEntityField[M, V any](schema *crud.Schema, source string, member func(*M) *V) (_ *crud.Field, err error) {
	if member != nil {
		defer func() {
			if recover() != nil {
				err = auditErrorAt(ErrDeclaration, "member.selector")
			}
		}()
		model := new(M)
		selected := member(model)
		if selected == nil {
			return nil, auditErrorAt(ErrDeclaration, "member.selector")
		}
		pointers, pointerErr := schema.Pointers(model, schema.Fields)
		if pointerErr != nil {
			return nil, auditError(ErrDeclaration, pointerErr)
		}
		selectedPointer := reflect.ValueOf(selected).Pointer()
		for index, pointer := range pointers {
			if reflect.ValueOf(pointer).Pointer() == selectedPointer {
				return schema.Fields[index], nil
			}
		}
		return nil, auditErrorAt(ErrDeclaration, "member.selector")
	}
	if source == "" || len(source) > MaxNameBytes {
		return nil, auditErrorAt(ErrDeclaration, "field.source")
	}
	var result *crud.Field
	for _, field := range schema.Fields {
		if field.Name != source && field.Column != source {
			continue
		}
		if result != nil && result != field {
			return nil, auditErrorAt(ErrDeclaration, "field.source")
		}
		result = field
	}
	if result == nil {
		return nil, auditErrorAt(ErrDeclaration, "field.source")
	}
	return result, nil
}

func validateEntityFieldType(field, value reflect.Type, optional bool) error {
	if field == nil || value == nil {
		return auditErrorAt(ErrDeclaration, "field.type")
	}
	if optional {
		if field.Kind() == reflect.Pointer && field.Elem() == value || crud.OptElem(field) == value {
			return nil
		}
		return auditErrorAt(ErrDeclaration, "field.type")
	}
	if field != value {
		return auditErrorAt(ErrDeclaration, "field.type")
	}
	return nil
}

func Define[M any, ID comparable](policy Policy[M, ID]) *ResourcePolicy[M, ID] {
	declared, err := TryDefine(policy)
	if err != nil {
		panic(err)
	}
	return declared
}

func TryDefine[M any, ID comparable](policy Policy[M, ID]) (*ResourcePolicy[M, ID], error) {
	if err := validateResourceModel[M, ID](policy.Model); err != nil {
		return nil, err
	}
	if err := validatePolicySemantics(policy.Semantics, true); err != nil {
		return nil, err
	}
	if err := validateDescriptor(policy.Descriptor, false); err != nil {
		return nil, err
	}
	if policy.Subject.value.mapReference == nil {
		return nil, auditErrorAt(ErrDeclaration, "subject")
	}
	if err := validateSubjectPrivacy(policy.Subject.value.classification, policy.Subject.value.mode, policy.Model.PK.Secret); err != nil {
		return nil, err
	}
	actions, err := canonicalEntityActions(policy.Actions)
	if err != nil {
		return nil, err
	}
	schema := freezeSchema(policy.Model.Schema)
	bound, descriptions, err := bindEntityFields(schema, policy.Fields)
	if err != nil {
		return nil, err
	}
	description := DeclarationDescription{
		Kind: ResourceDeclaration, Resource: policy.Descriptor.Resource,
		Owner: policy.Descriptor.Owner, Purpose: policy.Descriptor.Purpose,
		Retention: policy.Descriptor.Retention, Consequence: policy.Descriptor.Consequence,
		Subject: SubjectDescription{Classification: policy.Subject.value.classification, Mode: policy.Subject.value.mode},
		Actions: actions, Fields: descriptions, Context: policy.Descriptor.Context.description(),
	}
	description.Semantics = policy.Semantics.description(PolicyFingerprint{})
	description.Semantics.Fingerprint = policyFingerprint(description, resourceMetadata(policy.Model, descriptions))
	resource := &resourcePolicy[M, ID]{
		schema: schema, table: policy.Model.TableReference().String(), descriptor: policy.Descriptor,
		subject: policy.Subject.value, actions: actions, fields: bound,
	}
	resource.seal.description = description
	for _, action := range actions {
		resource.seal.members = append(resource.seal.members, declarationMember{
			resource: description.Resource, action: Action(action), retention: description.Retention,
			consequence: description.Consequence, key: declarationMemberKey(description.Resource, Action(action)),
		})
	}
	declared := &ResourcePolicy[M, ID]{value: resource}
	for index := range resource.seal.members {
		resource.seal.members[index].declaration = declared
	}
	return declared, nil
}

func validateResourceModel[M any, ID comparable](meta *crud.Meta) error {
	if meta == nil || meta.Schema == nil || meta.PK == nil || meta.Schema.Type != reflect.TypeFor[M]() {
		return auditErrorAt(ErrDeclaration, "model")
	}
	if err := meta.CheckID(reflect.TypeFor[ID]()); err != nil {
		return auditError(ErrDeclaration, err)
	}
	if err := meta.TableReference().Validate(); err != nil {
		return auditError(ErrDeclaration, err)
	}
	return nil
}

func freezeSchema(input *crud.Schema) *crud.Schema {
	output := *input
	output.Fields = make([]*crud.Field, len(input.Fields))
	for index, field := range input.Fields {
		copy := *field
		output.Fields[index] = &copy
		if field == input.PK {
			output.PK = &copy
		}
		if field == input.Tombstone {
			output.Tombstone = &copy
		}
	}
	return &output
}

func canonicalEntityActions(input []EntityAction) ([]EntityAction, error) {
	if len(input) == 0 || len(input) > MaxCodesPerDeclaration {
		return nil, auditErrorAt(ErrDeclaration, "actions")
	}
	actions := slices.Clone(input)
	slices.Sort(actions)
	for index, action := range actions {
		if !validEntityAction(action) || index > 0 && action == actions[index-1] {
			return nil, auditErrorAt(ErrDeclaration, "actions")
		}
	}
	return actions, nil
}

func validEntityAction(action EntityAction) bool {
	return action == EntityCreated || action == EntityChanged || action == EntitySoftDeleted || action == EntityHardDeleted || action == EntityRestored
}

func bindEntityFields[M any](schema *crud.Schema, fields []EntityField[M]) ([]boundEntityField[M], []FieldDescription, error) {
	if len(fields) > MaxFieldsPerItem {
		return nil, nil, auditTooLarge("fields", MaxFieldsPerItem)
	}
	bound := make([]boundEntityField[M], len(fields))
	descriptions := make([]FieldDescription, len(fields))
	seen := make(map[FieldName]struct{}, len(fields))
	for index, field := range fields {
		if nilByReflection(field) {
			return nil, nil, auditErrorAt(ErrDeclaration, "fields")
		}
		declaration, ok := field.(entityFieldDeclaration[M])
		if !ok {
			return nil, nil, auditErrorAt(ErrDeclaration, "fields")
		}
		value, err := declaration.bindEntityField(schema)
		if err != nil {
			return nil, nil, err
		}
		description := value.description()
		if _, duplicate := seen[description.Name]; duplicate {
			return nil, nil, auditErrorAt(ErrDeclaration, "fields")
		}
		seen[description.Name] = struct{}{}
		bound[index] = value
		descriptions[index] = description
	}
	order := make([]int, len(bound))
	for index := range order {
		order[index] = index
	}
	slices.SortFunc(order, func(left, right int) int { return compareFieldDescription(descriptions[left], descriptions[right]) })
	canonicalBound := make([]boundEntityField[M], len(bound))
	canonicalDescriptions := make([]FieldDescription, len(descriptions))
	for index, source := range order {
		canonicalBound[index] = bound[source]
		canonicalDescriptions[index] = descriptions[source]
	}
	return canonicalBound, canonicalDescriptions, nil
}

func (field boundEntityFieldValue[M, V]) description() FieldDescription {
	return field.descriptionValue
}

func (field boundEntityFieldValue[M, V]) extract(model *M) (_ extractedEntityValue, err error) {
	defer func() {
		if recover() != nil {
			err = auditErrorAt(ErrInvalid, "field.extractor")
		}
	}()
	if field.field == nil {
		return extractedEntityValue{}, auditErrorAt(ErrInvalid, "field.historical")
	}
	values, valueErr := field.schema.Values(model, []*crud.Field{field.field})
	if valueErr != nil {
		return extractedEntityValue{}, auditError(ErrInvalid, valueErr)
	}
	value, present, valueErr := entityValue[V](values[0], field.optional)
	if valueErr != nil {
		return extractedEntityValue{}, valueErr
	}
	draft := draftValue{
		field: field.descriptionValue.Name, codec: field.descriptionValue.Codec,
		classification: field.descriptionValue.Classification, mode: field.descriptionValue.Mode,
	}
	if !present {
		draft.state = ValueAbsent
		return extractedEntityValue{value: draft, comparison: []byte{0}}, nil
	}
	wire, encodeErr := field.codec.Encode(value)
	if encodeErr != nil {
		return extractedEntityValue{}, encodeErr
	}
	comparison := append([]byte{1}, wire...)
	if field.descriptionValue.Mode == AsRedacted {
		draft.state = ValueRedacted
		return extractedEntityValue{value: draft, comparison: comparison}, nil
	}
	draft.state = ValuePresent
	draft.canonical = bytes.Clone(wire)
	return extractedEntityValue{value: draft, comparison: comparison}, nil
}

func entityValue[V any](raw any, optional bool) (V, bool, error) {
	var zero V
	if optional {
		if value, defined, null, ok := utils.Inspect(raw); ok {
			if !defined || null {
				return zero, false, nil
			}
			typed, matches := value.(V)
			if !matches {
				return zero, false, auditErrorAt(ErrInvalid, "field.value")
			}
			return typed, true, nil
		}
		value := reflect.ValueOf(raw)
		if value.Kind() != reflect.Pointer || value.IsNil() {
			return zero, false, nil
		}
		raw = value.Elem().Interface()
	}
	value, ok := raw.(V)
	if !ok {
		return zero, false, auditErrorAt(ErrInvalid, "field.value")
	}
	return value, true, nil
}

func resourceMetadata(meta *crud.Meta, fields []FieldDescription) []byte {
	var output bytes.Buffer
	writeFrame(&output, []byte(meta.Schema.Type.PkgPath()))
	writeFrame(&output, []byte(meta.Schema.Type.String()))
	for _, component := range meta.TableReference().Components() {
		writeFrame(&output, []byte(component))
	}
	writeCrudField(&output, meta.PK)
	for _, description := range fields {
		if description.HistoricalOnly {
			continue
		}
		for _, field := range meta.Fields {
			if field.Name == description.Source {
				writeCrudField(&output, field)
				break
			}
		}
	}
	return output.Bytes()
}

func writeCrudField(output *bytes.Buffer, field *crud.Field) {
	writeFrame(output, []byte(field.Name))
	writeFrame(output, []byte(field.Column))
	writeFrame(output, []byte(field.Type.PkgPath()))
	writeFrame(output, []byte(field.Type.String()))
	flags := []bool{field.PK, field.Auto, field.Immutable, field.Generated, field.ServerOwned, field.Tombstone, field.Secret, field.Version, field.Optional}
	for _, flag := range flags {
		if flag {
			output.WriteByte(1)
		} else {
			output.WriteByte(0)
		}
	}
}

func (p *ResourcePolicy[M, ID]) auditDeclaration() {}

func (p *ResourcePolicy[M, ID]) sealedDeclaration() *declarationSeal {
	if p == nil || p.value == nil {
		return nil
	}
	return &p.value.seal
}

func (p *ResourcePolicy[M, ID]) Description() DeclarationDescription {
	if p == nil || p.value == nil {
		return DeclarationDescription{}
	}
	return cloneDeclarationDescription(p.value.seal.description)
}

func (p *ResourcePolicy[M, ID]) Action(action EntityAction) OperationMember {
	if p == nil || p.value == nil || !slices.Contains(p.value.actions, action) {
		panic(auditErrorAt(ErrDeclaration, "action"))
	}
	return resourceActionMember{value: declarationMember{
		declaration: p, resource: p.value.seal.description.Resource, action: Action(action),
		retention: p.value.seal.description.Retention, consequence: p.value.seal.description.Consequence,
		key: declarationMemberKey(p.value.seal.description.Resource, Action(action)),
	}}
}

func (p *ResourcePolicy[M, ID]) History(history *History) *ResourceHistory[M, ID] {
	if p == nil || p.value == nil || history == nil {
		return nil
	}
	return &ResourceHistory[M, ID]{history: history, policy: p}
}

type resourceActionMember struct {
	value declarationMember
}

func (resourceActionMember) auditOperationMember() {}

func (member resourceActionMember) sealedOperationMember() declarationMember {
	return member.value
}

func (p *ResourcePolicy[M, ID]) Created(model *M) (EntityDraft, error) {
	return p.entityDraft(EntityCreated, EntityFullState, nil, model)
}

func (p *ResourcePolicy[M, ID]) Changed(before, after *M) (EntityDraft, bool, error) {
	draft, changed, err := p.changedDraft(EntityDeltaState, before, after)
	return draft, changed, err
}

func (p *ResourcePolicy[M, ID]) BaselineChanged(before, after *M) (EntityDraft, bool, error) {
	draft, changed, err := p.changedDraft(EntityFullState, before, after)
	return draft, changed, err
}

func (p *ResourcePolicy[M, ID]) Deleted(model *M) (EntityDraft, error) {
	if p == nil || p.value == nil {
		return EntityDraft{}, auditErrorAt(ErrInvalid, "policy")
	}
	action := EntityHardDeleted
	if p.value.schema.Tombstone != nil {
		action = EntitySoftDeleted
	}
	return p.entityDraft(action, EntityFullState, model, nil)
}

func (p *ResourcePolicy[M, ID]) Restored(model *M) (EntityDraft, error) {
	return p.entityDraft(EntityRestored, EntityFullState, nil, model)
}

func (p *ResourcePolicy[M, ID]) changedDraft(state EntityStateKind, before, after *M) (EntityDraft, bool, error) {
	if p == nil || p.value == nil || before == nil || after == nil {
		return EntityDraft{}, false, auditErrorAt(ErrInvalid, "model")
	}
	subject, err := p.subjectOf(after)
	if err != nil {
		return EntityDraft{}, false, err
	}
	prior, err := p.subjectOf(before)
	if err != nil || prior != subject {
		return EntityDraft{}, false, auditErrorAt(ErrInvalid, "subject")
	}
	values, changes, err := p.extractChanges(before, after, state == EntityFullState)
	if err != nil {
		return EntityDraft{}, false, err
	}
	if len(changes) == 0 {
		return EntityDraft{}, false, nil
	}
	draft, err := p.buildEntityDraft(EntityChanged, state, subject, values, changes)
	return draft, true, err
}

func (p *ResourcePolicy[M, ID]) entityDraft(action EntityAction, state EntityStateKind, before, after *M) (EntityDraft, error) {
	if p == nil || p.value == nil || before == nil && after == nil {
		return EntityDraft{}, auditErrorAt(ErrInvalid, "model")
	}
	model := after
	if model == nil {
		model = before
	}
	subject, err := p.subjectOf(model)
	if err != nil {
		return EntityDraft{}, err
	}
	values, changes, err := p.extractChanges(before, after, true)
	if err != nil {
		return EntityDraft{}, err
	}
	return p.buildEntityDraft(action, state, subject, values, changes)
}

func (p *ResourcePolicy[M, ID]) buildEntityDraft(action EntityAction, state EntityStateKind, subject Reference, values []draftValue, changes []draftChange) (EntityDraft, error) {
	if !slices.Contains(p.value.actions, action) {
		return EntityDraft{}, auditErrorAt(ErrUnsupported, "action")
	}
	descriptor := p.value.descriptor
	descriptor.Action = Action(action)
	return EntityDraft{value: entityDraft{
		declaration: p, descriptor: descriptor, action: action, state: state,
		subject: subject, subjectInfo: p.value.seal.description.Subject,
		values: values, changes: changes,
	}}, nil
}

func (p *ResourcePolicy[M, ID]) subjectOf(model *M) (_ Reference, err error) {
	defer func() {
		if recover() != nil {
			err = auditErrorAt(ErrInvalid, "subject.mapper")
		}
	}()
	identifier, idErr := p.value.schema.ID(model)
	if idErr != nil {
		return "", auditError(ErrInvalid, idErr)
	}
	identifier = crud.ElemValue(identifier)
	typed, ok := identifier.(ID)
	if !ok {
		return "", auditErrorAt(ErrInvalid, "subject")
	}
	reference := Reference(p.value.subject.mapReference(typed))
	if !validOpaqueReference(string(reference), MaxReferenceBytes) {
		return "", auditErrorAt(ErrInvalid, "subject")
	}
	return reference, nil
}

func (p *ResourcePolicy[M, ID]) extractChanges(before, after *M, full bool) ([]draftValue, []draftChange, error) {
	values := make([]draftValue, 0, len(p.value.fields))
	changes := make([]draftChange, 0, len(p.value.fields))
	for _, field := range p.value.fields {
		description := field.description()
		if description.HistoricalOnly {
			continue
		}
		prior := extractedEntityValue{value: absentDraftValue(description), comparison: []byte{0}}
		next := prior
		var err error
		if before != nil {
			prior, err = field.extract(before)
			if err != nil {
				return nil, nil, err
			}
		}
		if after != nil {
			next, err = field.extract(after)
			if err != nil {
				return nil, nil, err
			}
		}
		if !bytes.Equal(prior.comparison, next.comparison) {
			changes = append(changes, draftChange{field: description.Name, before: cloneDraftValue(prior.value), after: cloneDraftValue(next.value)})
		}
		if full && description.Reconstruct {
			values = append(values, cloneDraftValue(next.value))
		}
	}
	return values, changes, nil
}

func absentDraftValue(description FieldDescription) draftValue {
	return draftValue{
		field: description.Name, codec: description.Codec,
		classification: description.Classification, mode: description.Mode, state: ValueAbsent,
	}
}

func cloneDraftValue(value draftValue) draftValue {
	value.codec.ReadVersions = slices.Clone(value.codec.ReadVersions)
	value.canonical = bytes.Clone(value.canonical)
	return value
}

func ComputeSubjectFixtureFingerprint[M any, ID comparable](policy *ResourcePolicy[M, ID], name FixtureName, identifier ID) (PolicyFixtureFingerprint, error) {
	if policy == nil || policy.value == nil || !policyHasFixture(policy.value.seal.description.Semantics, name) {
		return PolicyFixtureFingerprint{}, auditErrorAt(ErrInvalid, "fixture")
	}
	var reference Reference
	func() {
		defer func() { _ = recover() }()
		reference = Reference(policy.value.subject.mapReference(identifier))
	}()
	if !validOpaqueReference(string(reference), MaxReferenceBytes) {
		return PolicyFixtureFingerprint{}, auditErrorAt(ErrInvalid, "fixture.subject")
	}
	return fixtureFingerprint(DeclarationPolicyFixture, name, policy.value.seal.description.Semantics.Version,
		policy.value.seal.description.Resource, "", []byte(reference)), nil
}

func policyHasFixture(description PolicySemanticsDescription, name FixtureName) bool {
	for _, fixture := range description.Fixtures {
		if fixture.Name == name && fixture.Kind == DeclarationPolicyFixture {
			return true
		}
	}
	return false
}

func (p *ResourcePolicy[M, ID]) String() string {
	if p == nil || p.value == nil {
		return "[invalid audit resource policy]"
	}
	return fmt.Sprintf("[audit resource %s]", p.value.seal.description.Resource)
}

func entityFieldIdentity(description FieldDescription) string {
	return strings.Join([]string{description.Source, string(description.Name), description.Codec.Name}, "\x00")
}
