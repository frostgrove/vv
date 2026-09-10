package audit

import (
	"bytes"
	"slices"
	"time"
)

type ScopeSelectorKind uint8

const (
	ScopeCurrent ScopeSelectorKind = iota + 1
	ScopeExact
)

type ScopeSelectorView struct {
	Kind      ScopeSelectorKind
	Reference ScopedReference
}

type ScopeSelector struct {
	value ScopeSelectorView
}

func CurrentScope() ScopeSelector {
	return ScopeSelector{value: ScopeSelectorView{Kind: ScopeCurrent}}
}

func ExactScope(reference ScopedReference) (ScopeSelector, error) {
	if !validReferenceText(string(reference.Scope)) || !validReferenceText(string(reference.Reference)) {
		return ScopeSelector{}, auditErrorAt(ErrInvalid, "scope")
	}
	return ScopeSelector{value: ScopeSelectorView{Kind: ScopeExact, Reference: reference}}, nil
}

func (s ScopeSelector) View() ScopeSelectorView { return s.value }

type ProjectionSelectionKind uint8

const (
	ProjectionNone ProjectionSelectionKind = iota + 1
	ProjectionAll
	ProjectionOnly
)

type FieldProjectionView struct {
	Kind   ProjectionSelectionKind
	Fields []FieldName
}

type FieldProjection struct {
	value FieldProjectionView
}

func NoFields() FieldProjection {
	return FieldProjection{value: FieldProjectionView{Kind: ProjectionNone}}
}

func AllFields() FieldProjection {
	return FieldProjection{value: FieldProjectionView{Kind: ProjectionAll}}
}

func OnlyFields(fields ...FieldName) FieldProjection {
	projection, err := TryOnlyFields(fields...)
	if err != nil {
		panic(err)
	}
	return projection
}

func TryOnlyFields(fields ...FieldName) (FieldProjection, error) {
	if len(fields) == 0 || len(fields) > MaxFieldsPerItem {
		return FieldProjection{}, auditErrorAt(ErrInvalid, "query.fields")
	}
	values := slices.Clone(fields)
	for _, field := range values {
		if !validSemanticName(string(field)) {
			return FieldProjection{}, auditErrorAt(ErrInvalid, "query.fields")
		}
	}
	slices.Sort(values)
	if len(slices.Compact(values)) != len(values) {
		return FieldProjection{}, auditErrorAt(ErrInvalid, "query.fields")
	}
	return FieldProjection{value: FieldProjectionView{Kind: ProjectionOnly, Fields: values}}, nil
}

func (p FieldProjection) View() FieldProjectionView {
	view := p.value
	view.Fields = slices.Clone(view.Fields)
	return view
}

type ContextProjectionView struct {
	Kind  ProjectionSelectionKind
	Facts []ContextFactKind
}

type ContextProjection struct {
	value ContextProjectionView
}

func NoContext() ContextProjection {
	return ContextProjection{value: ContextProjectionView{Kind: ProjectionNone}}
}

func AllContext() ContextProjection {
	return ContextProjection{value: ContextProjectionView{Kind: ProjectionAll}}
}

func OnlyContext(facts ...ContextFactKind) ContextProjection {
	projection, err := TryOnlyContext(facts...)
	if err != nil {
		panic(err)
	}
	return projection
}

func TryOnlyContext(facts ...ContextFactKind) (ContextProjection, error) {
	if len(facts) == 0 || len(facts) > int(SourceContext) {
		return ContextProjection{}, auditErrorAt(ErrInvalid, "query.context")
	}
	values := slices.Clone(facts)
	for _, fact := range values {
		if !fact.Valid() {
			return ContextProjection{}, auditErrorAt(ErrInvalid, "query.context")
		}
	}
	slices.Sort(values)
	if len(slices.Compact(values)) != len(values) {
		return ContextProjection{}, auditErrorAt(ErrInvalid, "query.context")
	}
	return ContextProjection{value: ContextProjectionView{Kind: ProjectionOnly, Facts: values}}, nil
}

func (p ContextProjection) View() ContextProjectionView {
	view := p.value
	view.Facts = slices.Clone(view.Facts)
	return view
}

type TimeAxis uint8

const (
	ObservedTimeAxis TimeAxis = iota + 1
	OccurredTimeAxis
	RecordedTimeAxis
)

type RangeBounds uint8

const (
	ClosedOpenRange RangeBounds = iota + 1
	ClosedClosedRange
	OpenOpenRange
	OpenClosedRange
)

type TimeWindowView struct {
	Axis    TimeAxis
	Bounded bool
	From    time.Time
	Until   time.Time
	Bounds  RangeBounds
}

type TimeWindow struct {
	value TimeWindowView
}

func (w TimeWindow) View() TimeWindowView { return w.value }

type HistoryDirection uint8

const (
	NewestFirst HistoryDirection = iota + 1
	OldestFirst
)

type ChangedFieldMatch uint8

const (
	NoChangedFieldFilter ChangedFieldMatch = iota + 1
	AnyChangedField
	AllChangedFields
)

type ChangedFieldFilterView struct {
	Match    ChangedFieldMatch
	Fields   []FieldName
	Excluded []FieldName
}

type ChangedFieldFilter struct {
	value ChangedFieldFilterView
}

func (f ChangedFieldFilter) View() ChangedFieldFilterView {
	view := f.value
	view.Fields = slices.Clone(view.Fields)
	view.Excluded = slices.Clone(view.Excluded)
	return view
}

type SelectorSetView struct{}

type SelectorSet struct {
	configured bool
}

func (SelectorSet) View() SelectorSetView { return SelectorSetView{} }

type cursor struct {
	wire []byte
}

type Cursor struct {
	value cursor
}

func ParseCursor([]byte) (Cursor, error) {
	return Cursor{}, auditErrorAt(ErrUnsupported, "cursor")
}

func (c Cursor) Bytes() []byte { return bytes.Clone(c.value.wire) }

func (Cursor) String() string { return "[audit history cursor]" }

type Query struct {
	Purpose   Purpose
	Role      Reference
	Scope     ScopeSelector
	Actions   []Action
	Fields    FieldProjection
	Context   ContextProjection
	Time      TimeWindow
	Direction HistoryDirection
	Changed   ChangedFieldFilter
	Outcomes  []Outcome
	Reasons   []Reason
	Where     SelectorSet
	Limit     uint32
	Cursor    Cursor
}

type QueryClass uint8

const (
	SubjectHistoryQuery QueryClass = iota + 1
	ResourceHistoryQuery
	EventTargetHistoryQuery
	EventTypeHistoryQuery
	OperationTypeHistoryQuery
	OperationInstanceHistoryQuery
	AttemptHistoryQuery
	ActorHistoryQuery
	ContextHistoryQuery
	EventIndexHistoryQuery
	EntityIndexHistoryQuery
	AttemptTypeHistoryQuery
	AttemptTargetHistoryQuery
	ExactRevisionHistoryQuery
	ExactItemHistoryQuery
	RevisionNeighborsQuery
	ReconstructionQuery
	ComparisonQuery
)

type NormalizedQueryView struct {
	Purpose   Purpose
	Role      Reference
	Scope     ScopeSelectorView
	Class     QueryClass
	Actions   []Action
	Fields    FieldProjectionView
	Context   ContextProjectionView
	Time      TimeWindowView
	Direction HistoryDirection
	Changed   ChangedFieldFilterView
	Outcomes  []Outcome
	Reasons   []Reason
	Where     SelectorSetView
	Limit     uint32
}

func cloneNormalizedQuery(view NormalizedQueryView) NormalizedQueryView {
	view.Actions = slices.Clone(view.Actions)
	view.Fields.Fields = slices.Clone(view.Fields.Fields)
	view.Context.Facts = slices.Clone(view.Context.Facts)
	view.Changed.Fields = slices.Clone(view.Changed.Fields)
	view.Changed.Excluded = slices.Clone(view.Changed.Excluded)
	view.Outcomes = slices.Clone(view.Outcomes)
	view.Reasons = slices.Clone(view.Reasons)
	return view
}

func normalizeBasicQuery(input Query, class QueryClass, actionCeiling []Action, fieldCeiling []FieldName, contextCeiling []ContextFactKind) (NormalizedQueryView, error) {
	if !validSemanticName(string(input.Purpose)) || !validOpaqueReference(string(input.Role), MaxReferenceBytes) {
		return NormalizedQueryView{}, auditErrorAt(ErrInvalid, "query")
	}
	if input.Limit == 0 {
		return NormalizedQueryView{}, auditErrorAt(ErrInvalid, "query.limit")
	}
	if input.Limit > MaxPageRevisions {
		return NormalizedQueryView{}, auditTooLarge("query.limit", MaxPageRevisions)
	}
	if input.Direction != NewestFirst && input.Direction != OldestFirst {
		return NormalizedQueryView{}, auditErrorAt(ErrInvalid, "query.direction")
	}
	if scope := input.Scope.View(); scope.Kind != 0 && scope.Kind != ScopeCurrent && scope.Kind != ScopeExact {
		return NormalizedQueryView{}, auditErrorAt(ErrInvalid, "query.scope")
	} else if scope.Kind == ScopeExact && (!validReferenceText(string(scope.Reference.Scope)) || !validReferenceText(string(scope.Reference.Reference))) {
		return NormalizedQueryView{}, auditErrorAt(ErrInvalid, "query.scope")
	}
	if input.Time.value != (TimeWindowView{}) || input.Changed.value.Match != 0 || len(input.Outcomes) != 0 || len(input.Reasons) != 0 || input.Where.configured || len(input.Cursor.value.wire) != 0 {
		return NormalizedQueryView{}, auditErrorAt(ErrUnsupported, "query")
	}
	actions, err := canonicalBasicActions(input.Actions, actionCeiling)
	if err != nil {
		return NormalizedQueryView{}, err
	}
	fields, err := normalizeFieldProjection(input.Fields, fieldCeiling)
	if err != nil {
		return NormalizedQueryView{}, err
	}
	facts, err := normalizeContextProjection(input.Context, contextCeiling)
	if err != nil {
		return NormalizedQueryView{}, err
	}
	return NormalizedQueryView{
		Purpose: input.Purpose, Role: input.Role, Scope: input.Scope.View(), Class: class,
		Actions: actions, Fields: fields, Context: facts, Direction: input.Direction, Limit: input.Limit,
	}, nil
}

func normalizeFieldProjection(input FieldProjection, ceiling []FieldName) (FieldProjectionView, error) {
	view := input.View()
	if view.Kind == 0 || view.Kind == ProjectionNone {
		return FieldProjectionView{Kind: ProjectionNone}, nil
	}
	if view.Kind == ProjectionAll {
		return FieldProjectionView{Kind: ProjectionAll, Fields: slices.Clone(ceiling)}, nil
	}
	if view.Kind != ProjectionOnly || len(view.Fields) == 0 || !subset(view.Fields, ceiling) {
		return FieldProjectionView{}, auditErrorAt(ErrRefused, "query.fields")
	}
	return view, nil
}

func normalizeContextProjection(input ContextProjection, ceiling []ContextFactKind) (ContextProjectionView, error) {
	view := input.View()
	if view.Kind == 0 || view.Kind == ProjectionNone {
		return ContextProjectionView{Kind: ProjectionNone}, nil
	}
	if view.Kind == ProjectionAll {
		return ContextProjectionView{Kind: ProjectionAll, Facts: slices.Clone(ceiling)}, nil
	}
	if view.Kind != ProjectionOnly || len(view.Facts) == 0 || !subset(view.Facts, ceiling) {
		return ContextProjectionView{}, auditErrorAt(ErrRefused, "query.context")
	}
	return view, nil
}

func canonicalBasicActions(input, ceiling []Action) ([]Action, error) {
	if len(input) > MaxQueryActions {
		return nil, auditTooLarge("query.actions", MaxQueryActions)
	}
	allowed := make(map[Action]struct{}, len(ceiling))
	for _, action := range ceiling {
		allowed[action] = struct{}{}
	}
	if len(input) == 0 {
		output := slices.Clone(ceiling)
		slices.Sort(output)
		return slices.Compact(output), nil
	}
	output := slices.Clone(input)
	for _, action := range output {
		if !validSemanticName(string(action)) {
			return nil, auditErrorAt(ErrInvalid, "query.actions")
		}
		if _, ok := allowed[action]; !ok {
			return nil, auditErrorAt(ErrRefused, "query.actions")
		}
	}
	slices.Sort(output)
	if len(slices.Compact(output)) != len(output) {
		return nil, auditErrorAt(ErrInvalid, "query.actions")
	}
	return output, nil
}
