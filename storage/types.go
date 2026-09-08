package storage

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"time"
)

const (
	MaxKeyBytes           = 768
	MaxKeySegmentBytes    = 128
	MaxMetadataEntries    = 32
	MaxMetadataKeyBytes   = 64
	MaxMetadataValueBytes = 512

	MaxMetadataTotalBytes = 1536
	DefaultStageTTL       = 24 * time.Hour
	MaxStageTTL           = 7 * 24 * time.Hour

	// How long one Stage, Promote or Abort may hold a stage before another
	// process may take it over. This bounds an *interruption*, so it is measured
	// in minutes while a stage's own TTL is measured in days: a process killed
	// mid-promote must not strand the stage for the stage's lifetime.
	DefaultStageClaimTTL   = 5 * time.Minute
	MaxStageClaimTTL       = time.Hour
	DefaultTemporaryURLTTL = 15 * time.Minute
	MaxTemporaryURLTTL     = 7 * 24 * time.Hour
	DefaultCleanupLimit    = 100
	MaxCleanupLimit        = 1000
)

type Namespace struct{ value string }

func ParseNamespace(raw string) (Namespace, error) {
	if err := validateNamespace(raw); err != nil {
		return Namespace{}, NewError("parse namespace", KindInvalid, err)
	}
	return Namespace{value: raw}, nil
}

func (this Namespace) Value() string  { return this.value }
func (this Namespace) String() string { return "[storage namespace]" }
func (this Namespace) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}
func (this Namespace) valid() bool {
	return validateNamespace(this.value) == nil
}

type Key struct{ value string }

func ParseKey(raw string) (Key, error) {
	if err := validateKey(raw); err != nil {
		return Key{}, NewError("parse key", KindInvalid, err)
	}
	return Key{value: raw}, nil
}

func (this Key) Value() string  { return this.value }
func (this Key) String() string { return "[storage key]" }
func (this Key) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}
func (this Key) valid() bool { return validateKey(this.value) == nil }

type StageID struct{ value string }

func NewStageID() (StageID, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return StageID{}, NewError("create stage id", KindInternal, err)
	}
	return StageID{value: base64.RawURLEncoding.EncodeToString(raw[:])}, nil
}

func ParseStageID(raw string) (StageID, error) {
	if len(raw) != 32 {
		return StageID{}, NewError("parse stage id", KindInvalid, errInvalidStageID)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != 24 || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return StageID{}, NewError("parse stage id", KindInvalid, errInvalidStageID)
	}
	return StageID{value: raw}, nil
}

func (this StageID) Value() string  { return this.value }
func (this StageID) String() string { return "[storage stage]" }
func (this StageID) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}
func (this StageID) valid() bool {
	_, err := ParseStageID(this.value)
	return err == nil
}

type Metadata map[string]string

type WriteMode uint8

const (
	CreateOnly WriteMode = iota + 1
	Replace
)

// IfMatch makes the write conditional on the object's current ETag. Without it
// two callers who read, decided and wrote lose one of the two decisions with no
// error on either side: CreateOnly only guards the first write of a key, and
// Replace guards nothing. A backend that cannot honour a precondition refuses the
// option with ErrUnsupported rather than writing unconditionally, and says so
// through Capabilities.ConditionalWrite.
type PutOptions struct {
	Mode        WriteMode
	Size        *int64
	ContentType string
	Metadata    Metadata
	IfMatch     *string
}

type StageOptions struct {
	Size        *int64
	ContentType string
	Metadata    Metadata
	ExpiresIn   time.Duration
}

type PromoteOptions struct {
	Mode    WriteMode
	IfMatch *string
}

type DeleteOptions struct {
	IfMatch *string
}

type TemporaryURLOptions struct {
	ExpiresIn time.Duration
}

// A ranged read. Offset is from the start of the object; a nil Length means "to
// the end". Without it a caller who needs the last megabyte of a multi-gigabyte
// object has to transfer all of it, and a video or a resumable download has no
// expressible form at all. A backend that cannot honour a range refuses a
// non-zero one with ErrUnsupported and says so through Capabilities.RangeRead.
type ReadOptions struct {
	Offset int64
	Length *int64
}

func (this ReadOptions) ranged() bool { return this.Offset != 0 || this.Length != nil }

type CleanupOptions struct {
	Limit int
}

type CleanupResult struct {
	Removed int

	More bool
}

func ExactSize(n int64) *int64 { return &n }

type Info struct {
	Size        int64
	ContentType string
	Metadata    Metadata
	ModifiedAt  time.Time
	ETag        string
	Version     string

	// Set when the object carried metadata this library will not hand back — an
	// entry past the portable budget, an invalid key, a value with control bytes.
	// A bucket holds objects this library did not write, and refusing the whole
	// read over one such entry makes the bytes unreachable, so the entry is
	// dropped and the answer says it is partial.
	MetadataTruncated bool
}

type Staged struct {
	ID        StageID
	Info      Info
	ExpiresAt time.Time
}

type Link struct {
	rawURL    string
	expiresAt time.Time
}

func NewLink(rawURL string, expiresAt time.Time) (Link, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return Link{}, NewError("create temporary URL", KindInternal, errInvalidTemporaryURL)
	}
	if expiresAt.IsZero() {
		return Link{}, NewError("create temporary URL", KindInternal, errInvalidTemporaryURL)
	}
	return Link{rawURL: rawURL, expiresAt: expiresAt}, nil
}

func (this Link) URL() string          { return this.rawURL }
func (this Link) ExpiresAt() time.Time { return this.expiresAt }
func (this Link) String() string       { return "[temporary storage URL]" }
func (this Link) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

type Capabilities struct {
	CreateOnly       bool
	Replace          bool
	Staging          bool
	TemporaryURL     bool
	ConditionalWrite bool
	RangeRead        bool
}

// IfMatch names the ETag a write requires the object to carry. It is a pointer so
// that "no precondition" and "the object must not exist" stay distinguishable
// from each other and from the zero string.
func IfMatch(etag string) *string { return &etag }

var (
	errInvalidStageID      = fmt.Errorf("invalid stage id")
	errInvalidTemporaryURL = fmt.Errorf("invalid temporary URL")
)
