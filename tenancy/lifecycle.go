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

// Admission stores one bit per lifecycle state in a uint8, so a seventh state
// added after Deleted would shift past the word and set no bit at all — it would
// read as "not admitted" everywhere and no test would name it. This underflows
// and fails the build instead.
const _ = uint(7 - Deleted)

// A deployment states its policy and the policy is checked at construction, so
// the two meanings the zero value used to carry are now separate: `asked` records
// that somebody wrote an Admit, and `refused` records that what they wrote named
// a class or a state this package does not have. Without the first, a mistyped
// Admit collapsed to the zero value and New read it as "not configured" and
// substituted the permissive default — a deployment asking for less got more,
// silently.
type Admission struct {
	states  [3]uint8
	asked   bool
	refused bool
}

func Admit(class Class, states ...Lifecycle) Admission {
	var admission Admission
	admission.asked = true
	if !class.Valid() || len(states) == 0 {
		admission.refused = true
		return admission
	}
	for _, state := range states {
		if !state.Valid() {
			admission.refused = true
			continue
		}
		admission.states[class] |= 1 << state
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
	merged.asked = this.asked || other.asked
	merged.refused = this.refused || other.refused
	return merged
}

func (this Admission) Admits(class Class, state Lifecycle) bool {
	return class.Valid() && state.Valid() && this.states[class]&(1<<state) != 0
}

func (this Admission) IsZero() bool { return !this.asked }

func (this Admission) valid() bool { return !this.refused }

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
