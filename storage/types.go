package storage

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"time"
)

const (
	// MaxKeyBytes leaves room for a bounded backend prefix, namespace and the
	// private object marker inside S3's 1024-byte object-name limit.
	MaxKeyBytes           = 768
	MaxKeySegmentBytes    = 128
	MaxMetadataEntries    = 32
	MaxMetadataKeyBytes   = 64
	MaxMetadataValueBytes = 512
	// MaxMetadataTotalBytes reserves enough of the S3-compatible 2 KiB user
	// metadata budget for up to MaxMetadataEntries x-amz-meta- prefixes and the
	// two private headers used by staged uploads.
	MaxMetadataTotalBytes  = 1536
	DefaultStageTTL        = 24 * time.Hour
	MaxStageTTL            = 7 * 24 * time.Hour
	DefaultTemporaryURLTTL = 15 * time.Minute
	MaxTemporaryURLTTL     = 7 * 24 * time.Hour
	DefaultCleanupLimit    = 100
	MaxCleanupLimit        = 1000
)

// Namespace is one validated logical collection. It is not a bucket or path.
type Namespace struct{ value string }

func ParseNamespace(raw string) (Namespace, error) {
	if err := validateNamespace(raw); err != nil {
		return Namespace{}, NewError("parse namespace", KindInvalid, err)
	}
	return Namespace{value: raw}, nil
}

func (n Namespace) Value() string  { return n.value }
func (n Namespace) String() string { return "[storage namespace]" }
func (n Namespace) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, n.String())
}
func (n Namespace) valid() bool {
	return validateNamespace(n.value) == nil
}

// Key is one canonical logical object identifier. Parsing is intentionally
// stricter than either POSIX paths or S3 object names so every backend sees the
// same identity.
type Key struct{ value string }

func ParseKey(raw string) (Key, error) {
	if err := validateKey(raw); err != nil {
		return Key{}, NewError("parse key", KindInvalid, err)
	}
	return Key{value: raw}, nil
}

// Value deliberately exposes the already validated representation for domain
// persistence and adapter implementations. String stays redacted so accidental
// formatting does not disclose the key.
func (k Key) Value() string  { return k.value }
func (k Key) String() string { return "[storage key]" }
func (k Key) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, k.String())
}
func (k Key) valid() bool { return validateKey(k.value) == nil }

// StageID identifies an unconfirmed upload. It is safe to round-trip through a
// form, but is still an authorization-sensitive opaque value.
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

func (id StageID) Value() string  { return id.value }
func (id StageID) String() string { return "[storage stage]" }
func (id StageID) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, id.String())
}
func (id StageID) valid() bool {
	_, err := ParseStageID(id.value)
	return err == nil
}

// Metadata is a small portable set of application values. It is copied at
// every public boundary.
type Metadata map[string]string

// WriteMode makes collision behaviour explicit. The zero value is normalized
// to CreateOnly; replacement never happens accidentally.
type WriteMode uint8

const (
	CreateOnly WriteMode = iota + 1
	Replace
)

type PutOptions struct {
	Mode        WriteMode
	Size        *int64
	ContentType string
	Metadata    Metadata
}

type StageOptions struct {
	Size        *int64
	ContentType string
	Metadata    Metadata
	ExpiresIn   time.Duration
}

type PromoteOptions struct {
	Mode WriteMode
}

type TemporaryURLOptions struct {
	ExpiresIn time.Duration
}

type CleanupOptions struct {
	// Limit bounds successful removals of owned temporary resources, not the
	// number of entries a backend may inspect while finding expired work.
	// Callers that also need a wall-time or remote-request bound must supply a
	// context deadline.
	Limit int
}

type CleanupResult struct {
	Removed int
	// More is conservative: the removal limit was reached, so another pass may
	// be necessary. It does not prove that another removable temporary resource
	// exists.
	More bool
}

// ExactSize returns a fresh size pointer suitable for PutOptions/StageOptions.
func ExactSize(n int64) *int64 { return &n }

type Info struct {
	Size        int64
	ContentType string
	Metadata    Metadata
	ModifiedAt  time.Time
	ETag        string // opaque validator; it is not a portable content hash
	Version     string // opaque backend version when one was returned
}

type Staged struct {
	ID        StageID
	Info      Info
	ExpiresAt time.Time
}

// Link is a temporary bearer capability. Call URL only at the response
// boundary; ordinary formatting is intentionally redacted.
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

func (l Link) URL() string          { return l.rawURL }
func (l Link) ExpiresAt() time.Time { return l.expiresAt }
func (l Link) String() string       { return "[temporary storage URL]" }
func (l Link) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, l.String())
}

type Capabilities struct {
	CreateOnly   bool
	Replace      bool
	Staging      bool
	TemporaryURL bool
}

var (
	errInvalidStageID      = fmt.Errorf("invalid stage id")
	errInvalidTemporaryURL = fmt.Errorf("invalid temporary URL")
)
