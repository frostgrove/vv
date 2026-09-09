package audit

type Support uint8

const (
	SupportUnstated Support = iota
	SupportUnsupported
	SupportSupported
)

func (value Support) Valid() bool { return value <= SupportSupported }

func (value Support) String() string {
	switch value {
	case SupportUnstated:
		return "unstated"
	case SupportUnsupported:
		return "unsupported"
	case SupportSupported:
		return "supported"
	default:
		return "unknown"
	}
}
