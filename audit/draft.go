package audit

import "time"

type EntityStateKind uint8

const (
	EntityDeltaState EntityStateKind = iota + 1
	EntityFullState
)

type ValueState uint8

const (
	ValueAbsent ValueState = iota + 1
	ValuePresent
	ValueRedacted
)

type draftValue struct {
	field          FieldName
	codec          CodecDescription
	classification Classification
	mode           StorageMode
	state          ValueState
	canonical      []byte
}

type draftChange struct {
	field  FieldName
	before draftValue
	after  draftValue
}

type draft struct {
	declaration   Declaration
	descriptor    Descriptor
	target        Reference
	targetPolicy  SubjectDescription
	targetPresent bool
	occurredAt    time.Time
	outcome       Outcome
	reason        Reason
	values        []draftValue
}

type Draft struct {
	value draft
}

type entityDraft struct {
	declaration Declaration
	descriptor  Descriptor
	action      EntityAction
	state       EntityStateKind
	subject     Reference
	subjectInfo SubjectDescription
	values      []draftValue
	changes     []draftChange
}

type EntityDraft struct {
	value entityDraft
}
