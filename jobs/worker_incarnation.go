package jobs

import (
	"fmt"
	"log/slog"
)

const WorkerIncarnationBytes = 16

type WorkerIncarnation struct{ value [WorkerIncarnationBytes]byte }

func WorkerIncarnationFromBytes(value [WorkerIncarnationBytes]byte) (WorkerIncarnation, error) {
	if value == [WorkerIncarnationBytes]byte{} {
		return WorkerIncarnation{}, invalid("worker incarnation")
	}
	return WorkerIncarnation{value: value}, nil
}

func (i WorkerIncarnation) Bytes() [WorkerIncarnationBytes]byte { return i.value }
func (i WorkerIncarnation) IsZero() bool {
	return i.value == [WorkerIncarnationBytes]byte{}
}
func (WorkerIncarnation) String() string { return "[job worker incarnation]" }
func (i WorkerIncarnation) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, i.String())
}
func (i WorkerIncarnation) LogValue() slog.Value { return slog.StringValue(i.String()) }
func (WorkerIncarnation) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: worker incarnation cannot be serialized", ErrUnsupported)
}
func (i WorkerIncarnation) valid() bool { return !i.IsZero() }

func newWorkerIncarnation(entropy *entropySource) (WorkerIncarnation, error) {
	var value [WorkerIncarnationBytes]byte
	if err := entropy.read(value[:]); err != nil {
		return WorkerIncarnation{}, err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return WorkerIncarnationFromBytes(value)
}
