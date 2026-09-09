package audit

import "bytes"

type storePosition struct {
	wire []byte
}

type StorePosition struct {
	value storePosition
}

func NewStorePosition(wire []byte) (StorePosition, error) {
	if len(wire) == 0 {
		return StorePosition{}, auditErrorAt(ErrBadPosition, "position")
	}
	if len(wire) > MaxStorePosition {
		return StorePosition{}, auditTooLarge("position", MaxStorePosition)
	}
	return StorePosition{value: storePosition{wire: bytes.Clone(wire)}}, nil
}

func (p StorePosition) Bytes() []byte {
	return bytes.Clone(p.value.wire)
}

func (StorePosition) String() string {
	return "[audit store position]"
}
