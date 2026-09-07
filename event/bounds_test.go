package event

import "testing"

func TestTheResidentPageIsTheCeilingCountedInEnvelopes(t *testing.T) {
	for _, one := range []struct {
		what       string
		maxPayload int
		want       int
	}{
		{"a payload bound of zero", 0, 0},
		{"a payload bound below zero", -1, 0},
		{"the kernel's own payload ceiling", MaxPayloadBytes, 64},
		{"a payload bound the ceiling divides", 4 << 20, 16},
		{"a payload bound the ceiling does not divide", 3 << 20, 21},
		{"a payload bound of one byte", 1, 64 << 20},
		{"a payload bound above the ceiling", (64 << 20) + 1, 0},
	} {
		if got := ResidentPage(one.maxPayload); got != one.want {
			t.Fatalf("%s admits a page of %d envelopes where %d of them is what 64 MiB holds, so a store deriving its page from this and the kernel checking it at the door round one quantity differently and the bound on the product is enforced nowhere",
				one.what, got, one.want)
		}
	}
	if MaxResidentBytes != 64<<20 {
		t.Fatalf("the ceiling on what one read may hold is %d bytes where the counts above were derived by hand from 64 MiB", MaxResidentBytes)
	}
}
