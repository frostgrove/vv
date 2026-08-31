package access

import (
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/auth"
	"github.com/google/uuid"
)

func TestSlugifyNormalisesWhatAnAdministratorTypes(t *testing.T) {
	cases := map[string]string{
		"Lease Reviewer":  "lease-reviewer",
		"  ADMIN  ":       "admin",
		"a//b":            "a-b",
		"Юрист":           "",
		"":                "",
		"-leading-":       "leading",
		"Level 2 Auditor": "level-2-auditor",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionIsClosedByRevocationExpiryAndIdleness(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	idle := time.Hour
	revoked := now.Add(-time.Minute)

	live := Session{LastUsedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
	if !live.Live(now, idle) {
		t.Fatal("a session used a minute ago and not yet expired was reported closed")
	}

	cases := map[string]Session{
		"revoked":                  {LastUsedAt: now, ExpiresAt: now.Add(time.Hour), RevokedAt: &revoked},
		"past its absolute expiry": {LastUsedAt: now, ExpiresAt: now.Add(-time.Second)},
		"idle too long":            {LastUsedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(time.Hour)},
	}
	for name, s := range cases {
		if s.Live(now, idle) {
			t.Errorf("a session %s still authenticates", name)
		}
	}

	atExpiry := Session{LastUsedAt: now, ExpiresAt: now}
	if atExpiry.Live(now, idle) {
		t.Error("a session exactly at its expiry still authenticates")
	}

	stale := Session{LastUsedAt: now.Add(-1000 * time.Hour), ExpiresAt: now.Add(time.Hour)}
	if !stale.Live(now, 0) {
		t.Error("an unset idle timeout closed a session instead of not applying")
	}
}

func TestPrincipalAnswersRolesPermissionsAndClaims(t *testing.T) {
	id := uuid.New()
	session := uuid.New()
	p := &Principal{
		Ref:         SubjectRef{Type: testSubject, ID: id},
		SessionID:   session,
		Roles:       []auth.Role{"admin"},
		Permissions: []auth.Permission{PermRoleRead},
	}

	if p.Subject() != id.String() {
		t.Fatalf("Subject() = %q; security.ScopeSubject compares this against an id column", p.Subject())
	}
	if !p.In("admin") || p.In("viewer") {
		t.Fatal("In answers the wrong roles")
	}
	if !p.Has(PermRoleRead) || p.Has(PermRoleDelete) {
		t.Fatal("Has answers the wrong permissions")
	}

	got, ok := p.Attr(AttrSubjectID)
	if !ok {
		t.Fatal("the subject id is not readable as a claim")
	}
	if _, isUUID := got.(uuid.UUID); !isUUID {
		t.Fatalf("Attr(%q) = %T, want uuid.UUID", AttrSubjectID, got)
	}
	if v, _ := p.Attr(AttrSubjectType); v != string(testSubject) {
		t.Fatalf("Attr(%q) = %v", AttrSubjectType, v)
	}
	if v, _ := p.Attr(AttrSessionID); v != session {
		t.Fatalf("Attr(%q) = %v", AttrSessionID, v)
	}
	if _, ok := p.Attr("tenant"); ok {
		t.Fatal("a claim nobody set was answered; security.ScopeAttr would compile it to a scope on a zero value")
	}
}

func TestAnEmptySubjectReferenceIsNeverACaller(t *testing.T) {
	if !(SubjectRef{}).Zero() {
		t.Fatal("the zero reference does not report itself as empty")
	}
	if !(SubjectRef{Type: testSubject}).Zero() {
		t.Fatal("a reference with no id is not empty")
	}
	if !(SubjectRef{ID: uuid.New()}).Zero() {
		t.Fatal("a reference with no type is not empty; it would grant across every subject type at once")
	}
	if (SubjectRef{Type: testSubject, ID: uuid.New()}).Zero() {
		t.Fatal("a complete reference reports itself empty")
	}
}

func TestTheUserAgentIsTruncatedBeforeItReachesAColumn(t *testing.T) {
	long := strings.Repeat("x", MaxUserAgent*3)
	got := Agent{UserAgent: long, IP: "127.0.0.1"}.Truncated()
	if len(got.UserAgent) != MaxUserAgent {
		t.Fatalf("stored user agent is %d bytes, want %d", len(got.UserAgent), MaxUserAgent)
	}
	if got.IP != "127.0.0.1" {
		t.Fatal("truncating the agent changed the address")
	}
	short := Agent{UserAgent: "curl/8"}.Truncated()
	if short.UserAgent != "curl/8" {
		t.Fatal("a short user agent was altered")
	}
}

func TestAdminIsPlannedToHoldEveryDeclaredPermission(t *testing.T) {
	plan := rolePlan([]ModuleGrants{
		{Module: "access", Permissions: []PermissionDef{{Code: PermRoleRead}, {Code: PermRoleWrite}}},
		{Module: "user", Permissions: []PermissionDef{{Code: "user.read"}},
			Roles: map[auth.Role][]auth.Permission{"viewer": {"user.read"}}},
	})

	admin := plan[RoleAdmin]
	for _, want := range []auth.Permission{PermRoleRead, PermRoleWrite, "user.read"} {
		if !contains(admin, want) {
			t.Errorf("admin was not planned to hold %q", want)
		}
	}
	if got := plan["viewer"]; len(got) != 1 || got[0] != "user.read" {
		t.Fatalf("viewer = %v, want exactly what the module granted it", got)
	}
	if _, invented := plan["editor"]; invented {
		t.Fatal("a role nobody named was planned")
	}
}

func TestAPermissionDeclaredTwiceIsPlannedOnce(t *testing.T) {
	plan := rolePlan([]ModuleGrants{
		{Module: "a", Permissions: []PermissionDef{{Code: PermRoleRead}}},
		{Module: "b", Permissions: []PermissionDef{{Code: PermRoleRead}}},
	})
	if n := len(plan[RoleAdmin]); n != 1 {
		t.Fatalf("admin holds %d copies of one permission; each one is a row the sync would try to insert", n)
	}
}

func TestTwoDirectoriesForOneSubjectTypeRefuseToWire(t *testing.T) {
	if _, err := NewDirectories(stubDirectory{}, stubDirectory{}); err == nil {
		t.Fatal("a duplicate subject type wired silently; whichever was passed first would then decide")
	}
}

func TestADirectoryWithNoSubjectTypeRefusesToWire(t *testing.T) {
	if _, err := NewDirectories(namelessDirectory{}); err == nil {
		t.Fatal("a directory claiming no subject type was accepted")
	}
}

func TestOneWellFormedDirectoryWires(t *testing.T) {
	indexed, err := NewDirectories(stubDirectory{})
	if err != nil {
		t.Fatalf("a single directory was refused: %v", err)
	}
	if _, served := indexed.Directory(testSubject); !served {
		t.Fatalf("the directory did not answer for the type it declared")
	}
}

func TestTheAuthBodyMapTranslatesToTheKeyTheClientSent(t *testing.T) {
	got, ok := AuthBodyPaths.Resolve(errsPath("SecretHash"))
	if !ok || len(got) != 1 || got[0].Name != "password" {
		t.Fatalf("SecretHash resolved to %v", got)
	}

	through, ok := AuthBodyPaths.Resolve(errsPath("Whatever"))
	if !ok || through[0].Name != "Whatever" {
		t.Fatalf("an unmapped field was rewritten to %v", through)
	}
}

func contains[T comparable](in []T, want T) bool {
	for _, v := range in {
		if v == want {
			return true
		}
	}
	return false
}
