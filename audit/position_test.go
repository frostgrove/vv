package audit_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/frostgrove/vv/audit"
)

func TestStorePositionIsBoundedOpaqueAndOwned(t *testing.T) {
	wire := []byte{1, 2, 3}
	position, err := audit.NewStorePosition(wire)
	if err != nil {
		t.Fatal(err)
	}
	wire[0] = 9
	first := position.Bytes()
	if !bytes.Equal(first, []byte{1, 2, 3}) || position.String() != "[audit store position]" {
		t.Fatalf("position = %v %s", first, position)
	}
	first[0] = 8
	if position.Bytes()[0] != 1 {
		t.Fatal("position accessor aliases internal storage")
	}
	if _, err := audit.NewStorePosition(nil); !errors.Is(err, audit.ErrBadPosition) {
		t.Fatalf("empty position = %v", err)
	}
	if _, err := audit.NewStorePosition(make([]byte, audit.MaxStorePosition+1)); !errors.Is(err, audit.ErrTooLarge) {
		t.Fatalf("oversized position = %v", err)
	}
}
