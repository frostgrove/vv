package jobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

type panicEntropyReader struct{}

func (panicEntropyReader) Read([]byte) (int, error) { panic("private entropy panic") }

func TestWorkerIncarnationIsFixedOpaqueAndRedacted(t *testing.T) {
	var raw [WorkerIncarnationBytes]byte
	for index := range raw {
		raw[index] = byte(index + 1)
	}
	incarnation, err := WorkerIncarnationFromBytes(raw)
	if err != nil || incarnation.IsZero() || incarnation.Bytes() != raw {
		t.Fatalf("incarnation = (%v, %v)", incarnation, err)
	}
	copyOut := incarnation.Bytes()
	copyOut[0] = 0
	if incarnation.Bytes() != raw {
		t.Fatal("worker incarnation retained a returned copy")
	}
	for _, rendered := range []string{fmt.Sprint(incarnation), fmt.Sprintf("%+v", incarnation), fmt.Sprintf("%#v", incarnation), incarnation.LogValue().String(), slog.AnyValue(incarnation).Resolve().String()} {
		if strings.Contains(rendered, fmt.Sprint(raw[0])) || rendered != "[job worker incarnation]" {
			t.Fatalf("worker incarnation rendering = %q", rendered)
		}
	}
	if _, err := json.Marshal(incarnation); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("worker incarnation JSON = %v", err)
	}
	if _, err := WorkerIncarnationFromBytes([WorkerIncarnationBytes]byte{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero worker incarnation = %v", err)
	}
}

func TestNewWorkerIncarnationUsesInjectedEntropyOnce(t *testing.T) {
	raw := bytes.Repeat([]byte{0xff}, WorkerIncarnationBytes)
	source := &entropySource{reader: bytes.NewReader(raw)}
	incarnation, err := newWorkerIncarnation(source)
	if err != nil {
		t.Fatal(err)
	}
	value := incarnation.Bytes()
	if value[6]&0xf0 != 0x40 || value[8]&0xc0 != 0x80 {
		t.Fatalf("incarnation variant = %x", value)
	}
	if _, err := newWorkerIncarnation(source); !errors.Is(err, ErrEntropy) {
		t.Fatalf("exhausted entropy = %v", err)
	}
	var typedNil *errReader
	for _, invalid := range []*entropySource{
		nil,
		{},
		{reader: errReader{err: errors.New("private entropy failure")}},
		{reader: bytes.NewReader(make([]byte, WorkerIncarnationBytes-1))},
		{reader: panicEntropyReader{}},
		{reader: typedNil},
	} {
		if _, err := newWorkerIncarnation(invalid); err != ErrEntropy {
			t.Fatalf("invalid entropy = %v", err)
		}
	}
}
