package tenancy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxPurposeBytes = 64
	MaxCohortSize   = 10000
)

type Purpose struct{ value string }

func ParsePurpose(raw string) (Purpose, error) {
	purpose := Purpose{value: strings.Clone(raw)}
	if !purpose.valid() {
		return Purpose{}, ErrMalformed
	}
	return purpose, nil
}

func (this Purpose) Value() string  { return this.value }
func (this Purpose) String() string { return this.value }
func (this Purpose) IsZero() bool   { return this.value == "" }

func (this Purpose) valid() bool {
	if this.value == "" || len(this.value) > MaxPurposeBytes || !utf8.ValidString(this.value) {
		return false
	}
	for _, r := range this.value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

type Grant struct {
	purpose Purpose
	cohort  []Reference
	classes Admission
	until   time.Time
	binding [32]byte
}

func (this *Grant) Purpose() Purpose { return this.purpose }
func (this *Grant) Size() int        { return len(this.cohort) }
func (this *Grant) String() string   { return "[tenancy grant]" }

func (this *Grant) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, this.String())
}

func (this *Grant) LogValue() slog.Value { return slog.StringValue(this.String()) }

func (*Grant) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("tenancy: a grant cannot be serialized: %w", ErrUntrusted)
}

type Member struct {
	Reference Reference
	Outcome   Outcome
	Err       error
}

// Issuing a grant is deployment policy and lives in the control plane; what an
// authority does here is accept one, bound it and refuse to widen it. There is no
// wildcard cohort and no empty one: a grant that names nobody would be a second
// way to switch the narrowing off, and there is exactly one — this, naming every
// tenant it covers.
func (this *Authority) Accept(purpose Purpose, cohort []Reference, until time.Time, classes ...Class) (*Grant, error) {
	if !purpose.valid() {
		return nil, ErrMalformed
	}
	if len(classes) == 0 {
		return nil, fmt.Errorf("tenancy: a grant names the classes of work it permits: %w", ErrMalformed)
	}
	if len(cohort) == 0 || len(cohort) > MaxCohortSize {
		return nil, fmt.Errorf("tenancy: a grant names 1..%d tenants: %w", MaxCohortSize, ErrMalformed)
	}
	if !until.After(this.now()) {
		return nil, ErrStale
	}
	members := make([]Reference, 0, len(cohort))
	seen := make(map[Reference]struct{}, len(cohort))
	for _, reference := range cohort {
		if !reference.valid() {
			return nil, ErrMalformed
		}
		if _, duplicate := seen[reference]; duplicate {
			continue
		}
		seen[reference] = struct{}{}
		members = append(members, reference)
	}
	permitted, err := permits(classes)
	if err != nil {
		return nil, err
	}
	grant := &Grant{purpose: purpose, cohort: members, classes: permitted, until: until}
	grant.binding = grantBinding(this.salt, this.origin, purpose, members, permitted, until)
	return grant, nil
}

// Every member is entered on its own: the cohort is a list of units of work, not
// one. A member whose lifecycle moved since the grant was issued is recorded and
// skipped rather than failing the run, because a run that cannot be resumed is a
// run somebody will resume by hand across every tenant it already finished.
func (this *Authority) Each(ctx context.Context, grant *Grant, class Class, work func(context.Context) error) ([]Member, error) {
	if grant == nil {
		return nil, ErrGrantRequired
	}
	if !this.holds(grant) {
		return nil, ErrUntrusted
	}
	if !grant.until.After(this.now()) {
		return nil, ErrStale
	}
	if !grant.classes.Admits(class, Active) {
		return nil, ErrGrantRequired
	}
	if _, bound := From(ctx); bound {
		return nil, ErrPinned
	}
	ctx = withGrant(ctx, grant)

	outcomes := make([]Member, 0, len(grant.cohort))
	for _, reference := range grant.cohort {
		if !grant.until.After(this.now()) {
			return outcomes, ErrStale
		}
		if err := ctx.Err(); err != nil {
			return outcomes, err
		}
		outcomes = append(outcomes, this.member(ctx, reference, class, work))
	}
	return outcomes, nil
}

func (this *Authority) member(ctx context.Context, reference Reference, class Class, work func(context.Context) error) Member {
	scope, err := this.Lookup(ctx, reference, class)
	if err != nil {
		return Member{Reference: reference, Outcome: OutcomeFor(err), Err: err}
	}
	bound, err := this.With(ctx, scope)
	if err != nil {
		return Member{Reference: reference, Outcome: OutcomeFor(err), Err: err}
	}
	if err := work(bound); err != nil {
		return Member{Reference: reference, Outcome: OutcomeFor(err), Err: err}
	}
	return Member{Reference: reference, Outcome: OutcomeOk}
}

func (this *Authority) holds(grant *Grant) bool {
	if len(grant.cohort) == 0 {
		return false
	}
	want := grantBinding(this.salt, this.origin, grant.purpose, grant.cohort, grant.classes, grant.until)
	return hmac.Equal(grant.binding[:], want[:])
}

func permits(classes []Class) (Admission, error) {
	var permitted Admission
	for _, class := range classes {
		if !class.Valid() {
			return Admission{}, ErrMalformed
		}
		permitted = permitted.Merge(Admit(class, Active))
	}
	return permitted, nil
}

// Every member, every class and the deadline are inside the MAC. Authenticating
// the purpose and the first reference alone would let a grant for one tenant be
// edited into a grant for a thousand, or its expiry pushed out, without the
// binding noticing. Each run is counted as well as length-prefixed: a tenant may
// be named "write", so a read grant over two tenants and a read-and-write grant
// over one would otherwise write the same fields in the same order.
func grantBinding(salt []byte, origin string, purpose Purpose, cohort []Reference, classes Admission, until time.Time) [32]byte {
	mac := hmac.New(sha256.New, salt)
	writeField(mac, origin)
	writeField(mac, "grant")
	writeField(mac, purpose.Value())
	var deadline [8]byte
	binary.BigEndian.PutUint64(deadline[:], uint64(until.UnixNano()))
	_, _ = mac.Write(deadline[:])
	permitted := make([]Class, 0, len(Classes()))
	for _, class := range Classes() {
		if classes.Admits(class, Active) {
			permitted = append(permitted, class)
		}
	}
	writeCount(mac, len(permitted))
	for _, class := range permitted {
		writeField(mac, class.String())
	}
	writeCount(mac, len(cohort))
	for _, reference := range cohort {
		writeField(mac, reference.Value())
	}
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}

type grantKey struct{}

func withGrant(ctx context.Context, grant *Grant) context.Context {
	return context.WithValue(ctx, grantKey{}, grant)
}

// A grant for a monthly read must not be able to write, and a purpose is a label
// rather than a permission — so the classes the grant named travel with the work
// and every verb underneath is measured against them. Ordinary request work
// carries no grant and is unaffected.
func (this *Authority) permittedByGrant(ctx context.Context, class Class) error {
	grant, ok := ctx.Value(grantKey{}).(*Grant)
	if !ok {
		return nil
	}
	if !this.holds(grant) || !grant.classes.Admits(class, Active) {
		return ErrGrantRequired
	}
	if !grant.until.After(this.now()) {
		return ErrStale
	}
	return nil
}
