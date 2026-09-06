package tenancy

type Lifecycle uint8

const (
	LifecycleUnknown Lifecycle = iota
	Provisioning
	Active
	Suspended
	Migrating
	Deleting
	Deleted
)

func (this Lifecycle) Valid() bool {
	return this >= Provisioning && this <= Deleted
}

func (this Lifecycle) String() string {
	switch this {
	case Provisioning:
		return "provisioning"
	case Active:
		return "active"
	case Suspended:
		return "suspended"
	case Migrating:
		return "migrating"
	case Deleting:
		return "deleting"
	case Deleted:
		return "deleted"
	default:
		return "unknown"
	}
}

type Class uint8

const (
	ClassRead Class = iota
	ClassWrite
	ClassDurable
)

func (this Class) Valid() bool { return this <= ClassDurable }

func (this Class) String() string {
	switch this {
	case ClassRead:
		return "read"
	case ClassWrite:
		return "write"
	case ClassDurable:
		return "durable"
	default:
		return "unknown"
	}
}

func Classes() []Class { return []Class{ClassRead, ClassWrite, ClassDurable} }

type Admission struct{ states [3]uint8 }

func Admit(class Class, states ...Lifecycle) Admission {
	var admission Admission
	if !class.Valid() {
		return admission
	}
	for _, state := range states {
		if state.Valid() {
			admission.states[class] |= 1 << state
		}
	}
	return admission
}

func AdmitAll(states ...Lifecycle) Admission {
	var admission Admission
	for _, class := range Classes() {
		admission = admission.Merge(Admit(class, states...))
	}
	return admission
}

func (this Admission) Merge(other Admission) Admission {
	var merged Admission
	for i := range this.states {
		merged.states[i] = this.states[i] | other.states[i]
	}
	return merged
}

func (this Admission) Admits(class Class, state Lifecycle) bool {
	return class.Valid() && state.Valid() && this.states[class]&(1<<state) != 0
}

func (this Admission) IsZero() bool { return this == Admission{} }

// A row write reads first — the narrowing predicate every mutating verb carries
// is resolved for the read class — so a state admitted for writing but not for
// reading is a policy that means something other than what it says. Refusing the
// configuration at construction is the alternative to discovering it as a refused
// provisioning write under deadline.
//
// Durable work is deliberately not held to that floor. Its producer asks for
// ClassDurable directly and reads nothing first, so "durable work during a
// migration, no reads and no writes" is a policy a deployment may genuinely mean.
func (this Admission) inconsistent() Lifecycle {
	for state := Provisioning; state <= Deleted; state++ {
		if !this.Admits(ClassRead, state) && this.Admits(ClassWrite, state) {
			return state
		}
	}
	return LifecycleUnknown
}
