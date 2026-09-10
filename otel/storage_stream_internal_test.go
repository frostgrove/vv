package vvotel

import (
	"math"
	"testing"
)

func TestAddStorageStreamBytes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		total     int64
		n         int
		capacity  int
		want      int64
		wantValid bool
	}{
		{name: "zero", total: 3, n: 0, capacity: 0, want: 3, wantValid: true},
		{name: "positive", total: 3, n: 4, capacity: 4, want: 7, wantValid: true},
		{name: "negative", total: 3, n: -1, capacity: 4, want: 3},
		{name: "larger_than_buffer", total: 3, n: 5, capacity: 4, want: 3},
		{name: "max", total: math.MaxInt64 - 4, n: 4, capacity: 4, want: math.MaxInt64, wantValid: true},
		{name: "overflow", total: math.MaxInt64 - 3, n: 4, capacity: 4, want: math.MaxInt64 - 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, valid := addStorageStreamBytes(tc.total, tc.n, tc.capacity)
			if got != tc.want || valid != tc.wantValid {
				t.Fatalf("addStorageStreamBytes(%d,%d,%d)=(%d,%t), want (%d,%t)", tc.total, tc.n, tc.capacity, got, valid, tc.want, tc.wantValid)
			}
		})
	}
}
