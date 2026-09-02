package crud

type Action uint8

const (
	ActionRead Action = iota
	ActionCreate
	ActionUpdate
	ActionDelete
	ActionRestore
)

func Actions() []Action {
	return []Action{ActionRead, ActionCreate, ActionUpdate, ActionDelete, ActionRestore}
}

func (this Action) String() string {
	switch this {
	case ActionRead:
		return "read"
	case ActionCreate:
		return "create"
	case ActionUpdate:
		return "update"
	case ActionDelete:
		return "delete"
	case ActionRestore:
		return "restore"
	default:
		return "unknown"
	}
}
