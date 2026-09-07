package eventmemory

import (
	"crypto/rand"
	"fmt"
	"sync"

	"github.com/frostgrove/vv/event"
)

// A sixteenth of the kernel's payload ceiling and a quarter of its key ceiling:
// enough for a fact carrying an embedded document, and for a family beside a
// composite identity, with the room left over there so a deployment that needs
// it raises one number rather than every number.
const (
	defaultMaxPayload = 64 << 10
	defaultMaxKey     = 512
)

type LogSpec struct {
	MaxPayload int
	MaxKey     int
}

// The two numbers the data depends on live here rather than on a store, so two
// store values over one log cannot disagree about which keys and which payloads
// are writable; the operational numbers are the store's and may differ.
type Log struct {
	maxPayload  int
	maxKey      int
	fingerprint string
	backing     event.Backing

	mutex    sync.Mutex
	streams  map[event.Stream][]event.Envelope
	global   []event.Envelope
	position event.Position
	claims   map[event.Stream]*Tx
}

func NewLog(spec LogSpec) (*Log, error) {
	maxPayload, err := bound("MaxPayload", spec.MaxPayload, defaultMaxPayload, event.MaxPayloadBytes)
	if err != nil {
		return nil, err
	}
	maxKey, err := bound("MaxKey", spec.MaxKey, defaultMaxKey, event.MaxKeyBytes)
	if err != nil {
		return nil, err
	}
	log := &Log{
		maxPayload:  maxPayload,
		maxKey:      maxKey,
		fingerprint: rand.Text(),
		streams:     map[event.Stream][]event.Envelope{},
		claims:      map[event.Stream]*Tx{},
	}
	log.backing, err = event.NewBacking(log)
	if err != nil {
		return nil, err
	}
	return log, nil
}

func bound(name string, chosen, byDefault, ceiling int) (int, error) {
	switch {
	case chosen == 0:
		return byDefault, nil
	case chosen < 0:
		return 0, fmt.Errorf("%w: %s is %d", event.ErrWrongStore, name, chosen)
	case chosen > ceiling:
		return 0, fmt.Errorf("%w: %s is %d, above the kernel ceiling of %d", event.ErrWrongStore, name, chosen, ceiling)
	}
	return chosen, nil
}

func (this *Log) version(stream event.Stream) event.Version {
	return event.Version(len(this.streams[stream]))
}

func (this *Log) claimedByAnother(stream event.Stream, tx *Tx) bool {
	holder, held := this.claims[stream]
	return held && holder != tx
}

// One critical section assigns the positions and publishes to both indexes, so
// commit order is position order and no lower position can still arrive.
func (this *Log) publish(envelopes []event.Envelope) {
	for _, envelope := range envelopes {
		this.position++
		envelope.Position = this.position
		this.streams[envelope.Stream] = append(this.streams[envelope.Stream], envelope)
		this.global = append(this.global, envelope)
	}
}
