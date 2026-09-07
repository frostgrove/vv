package event

// The kernel's own ceilings. A store publishes its numbers in Limits and the
// kernel refuses any of them above the ceiling for it, so a deployment reads
// these to choose them. Five bound the factors of a product; MaxResidentBytes
// bounds the product itself, as a worst case at the two read doors — where the
// store fills a page before the kernel sees a byte of it — and as the measured
// sum of the payload bytes at an append, where the kernel is holding them.
const (
	MaxPayloadBytes  = 1 << 20  // the ceiling on Limits().MaxPayload
	MaxNameBytes     = 128      // a family and a wire type name
	MaxKeyBytes      = 2 << 10  // the ceiling on Limits().MaxKey
	MaxBatchCount    = 1024     // the ceiling on Limits().MaxBatch
	MaxPageCount     = 4096     // the ceiling on Limits().StreamPage and Limits().MaxRead
	MaxResidentBytes = 64 << 20 // what one read page or one append may hold
)

// The most envelopes one read may keep resident at a payload bound, and the one
// place MaxResidentBytes becomes a count: a store derives its page from this
// where its numbers are chosen and the kernel applies it again at the door, so
// the two cannot round one quantity differently. A division rather than a
// product, so nothing overflows an int, and a store that publishes a payload
// bound of zero or less admits no page at all.
func ResidentPage(maxPayload int) int {
	if maxPayload <= 0 {
		return 0
	}
	return MaxResidentBytes / maxPayload
}
