package audit

type Classification uint8

const (
	Public Classification = iota + 1
	Internal
	Personal
	Secret
)

func (value Classification) Valid() bool { return value >= Public && value <= Secret }

func (value Classification) String() string {
	switch value {
	case Public:
		return "public"
	case Internal:
		return "internal"
	case Personal:
		return "personal"
	case Secret:
		return "secret"
	default:
		return "unknown"
	}
}

type Consequence uint8

const (
	Required Consequence = iota + 1
	BestEffort
)

func (value Consequence) Valid() bool { return value == Required || value == BestEffort }

func (value Consequence) String() string {
	switch value {
	case Required:
		return "required"
	case BestEffort:
		return "best_effort"
	default:
		return "unknown"
	}
}

type ContextPresence uint8

const (
	ContextRequired ContextPresence = iota + 1
	ContextOptional
)

func (value ContextPresence) Valid() bool {
	return value == ContextRequired || value == ContextOptional
}

func (value ContextPresence) String() string {
	switch value {
	case ContextRequired:
		return "required"
	case ContextOptional:
		return "optional"
	default:
		return "unknown"
	}
}

type ActorKind uint8

const (
	HumanActor ActorKind = iota + 1
	WorkloadActor
	ServiceActor
	SystemActor
	ExternalActor
)

func (value ActorKind) Valid() bool { return value >= HumanActor && value <= ExternalActor }

func (value ActorKind) String() string {
	switch value {
	case HumanActor:
		return "human"
	case WorkloadActor:
		return "workload"
	case ServiceActor:
		return "service"
	case SystemActor:
		return "system"
	case ExternalActor:
		return "external"
	default:
		return "unknown"
	}
}

type ItemKind uint8

const (
	EventItem ItemKind = iota + 1
	EntityItem
	AttemptItem
	AccessItem
	LifecycleItem
)

func (value ItemKind) Valid() bool { return value >= EventItem && value <= LifecycleItem }

func (value ItemKind) String() string {
	switch value {
	case EventItem:
		return "event"
	case EntityItem:
		return "entity"
	case AttemptItem:
		return "attempt"
	case AccessItem:
		return "access"
	case LifecycleItem:
		return "lifecycle"
	default:
		return "unknown"
	}
}

type ContextFactKind uint8

const (
	ActorChainContext ContextFactKind = iota + 1
	ScopeContext
	ServiceContext
	DeploymentContext
	ClientContext
	OperationContext
	CorrelationContext
	CausationContext
	TraceContext
	SourceContext
)

func (value ContextFactKind) Valid() bool {
	return value >= ActorChainContext && value <= SourceContext
}

func (value ContextFactKind) String() string {
	switch value {
	case ActorChainContext:
		return "actor_chain"
	case ScopeContext:
		return "scope"
	case ServiceContext:
		return "service"
	case DeploymentContext:
		return "deployment"
	case ClientContext:
		return "client"
	case OperationContext:
		return "operation"
	case CorrelationContext:
		return "correlation"
	case CausationContext:
		return "causation"
	case TraceContext:
		return "trace"
	case SourceContext:
		return "source"
	default:
		return "unknown"
	}
}

type StorageMode uint8

const (
	AsPlaintext StorageMode = iota + 1
	AsRedacted
	AsToken
	AsProtected
	AsIndexedProtected
)

func (value StorageMode) Valid() bool { return value >= AsPlaintext && value <= AsIndexedProtected }

func (value StorageMode) String() string {
	switch value {
	case AsPlaintext:
		return "plaintext"
	case AsRedacted:
		return "redacted"
	case AsToken:
		return "token"
	case AsProtected:
		return "protected"
	case AsIndexedProtected:
		return "indexed_protected"
	default:
		return "unknown"
	}
}

type Provenance uint8

const (
	UnstatedProvenance Provenance = iota
	ServerDerived
	Verified
	Forwarded
	ClientSupplied
)

func (value Provenance) Valid() bool { return value <= ClientSupplied }

func (value Provenance) String() string {
	switch value {
	case UnstatedProvenance:
		return "unstated"
	case ServerDerived:
		return "server_derived"
	case Verified:
		return "verified"
	case Forwarded:
		return "forwarded"
	case ClientSupplied:
		return "client_supplied"
	default:
		return "unknown"
	}
}
