package receipt

import (
	"encoding/hex"
	"fmt"
	"strings"
)

const fingerprintPrefix = "sha256:"

// SHA-256 over what Repo.Digest digested. It is safe to render — it names no
// data — and it is not a secret: over a small closed payload space a preimage is
// guessable, so it authenticates nothing and nothing here compares it in
// constant time.
//
// The zero value is the one a caller that never called Digest hands in, and
// every door refuses it.
type Fingerprint struct{ digest [32]byte }

func NewFingerprint(digest [32]byte) (Fingerprint, error) {
	if digest == ([32]byte{}) {
		return Fingerprint{}, fmt.Errorf("%w: the fingerprint is thirty-two zero bytes, which is what a caller that never digested an append hands in rather than a digest SHA-256 produced", ErrSpec)
	}
	return Fingerprint{digest: digest}, nil
}

// The other half of String, and the ledger's: a fingerprint is written to a
// column as the text String renders and read back through this, so the encoding
// stays in the one package that freezes it rather than being re-derived by every
// implementation.
func ParseFingerprint(raw string) (Fingerprint, error) {
	held, found := strings.CutPrefix(raw, fingerprintPrefix)
	if !found {
		return Fingerprint{}, fmt.Errorf("%w: this text names no digest algorithm this package writes", ErrSpec)
	}
	decoded, err := hex.DecodeString(held)
	if err != nil {
		return Fingerprint{}, fmt.Errorf("%w: this text is not the hexadecimal a fingerprint is rendered as", ErrSpec)
	}
	if len(decoded) != 32 {
		return Fingerprint{}, fmt.Errorf("%w: this text holds %d bytes where a SHA-256 digest holds thirty-two", ErrSpec, len(decoded))
	}
	return Fingerprint{digest: [32]byte(decoded)}, nil
}

func (this Fingerprint) Equal(other Fingerprint) bool { return this.digest == other.digest }

func (this Fingerprint) Zero() bool { return this.digest == [32]byte{} }

func (this Fingerprint) String() string {
	return fingerprintPrefix + hex.EncodeToString(this.digest[:])
}
