package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shardit-io/vv/auth"
	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/crud/crudtest"
	"github.com/shardit-io/vv/crud/decorators/security"
)

// The three constructors below are the ones a consumer reaches for, and the
// leak they close is [[D-007]]'s: Scope narrows the statement's own FROM, so a
// preload of a second table is a second statement over a table nobody narrowed.
// ScopeRelationAttr and ScopeRelationSubject are the one-line spellings of the
// declaration that closes it, and the claim they carry comes off the token
// rather than out of a context key the application invented.

// preloadOf drives one GetAll with a preload of Notes through the given policy
// and hands back the statement that read the far table.
func preloadOf(t *testing.T, ctx context.Context, p security.Policy[Folder, int64]) crudtest.Statement {
	t.Helper()
	rec := crudtest.Postgres().Push(
		crudtest.Rows(folderRow(1, 7, "mine", 1)), // the folders page
		crudtest.Rows(), // the notes preload
	)
	if _, err := Folders.Bind(rec, security.Gate(p)).GetAll(ctx, crud.Preload("Notes")); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.Statements()); n != 2 {
		t.Fatalf("%d statements ran, want the page and its preload", n)
	}
	return rec.Statements()[1]
}

// ScopeRelationAttr is ScopeRelationField with the extractor filled in, so the
// only honest way to say it works is to compile both and compare what reached
// the database.
func TestScopeRelationAttrNarrowsThePreloadTheWayTheHandWrittenFormDoes(t *testing.T) {
	ctx := auth.WithPrincipal(withTenant(context.Background(), 7), editor)

	fromTheToken := preloadOf(t, ctx, security.ScopeRelationAttr[Folder, int64]("Notes", "TenantID", "tenant"))
	handWritten := preloadOf(t, ctx, security.ScopeRelationField[Folder, int64]("Notes", "TenantID", tenantOf))

	if fromTheToken.SQL != handWritten.SQL {
		t.Fatalf("the constructor built a different statement from the declaration it stands for:\n"+
			"ScopeRelationAttr:  %s\nScopeRelationField: %s", fromTheToken.SQL, handWritten.SQL)
	}
	if len(fromTheToken.Args) != len(handWritten.Args) || !containsArg(fromTheToken.Args, int64(7)) {
		t.Fatalf("args = %v, want the same binds as the hand-written form %v",
			fromTheToken.Args, handWritten.Args)
	}

	// Two policies that both narrow nothing also produce identical SQL, so the
	// comparison above only means something once the narrowing is shown to be
	// there at all.
	if !strings.Contains(fromTheToken.SQL, `"tenant_id" = $`) {
		t.Fatalf("neither form narrowed the preload, so the comparison above proves nothing:\n%s", fromTheToken.SQL)
	}
}

// ScopeRelationSubject is the same wrapper over the subject rather than a
// claim, and the far side names its owner column its own way.
func TestScopeRelationSubjectNarrowsTheFarTableToTheCallersOwnRows(t *testing.T) {
	ctx := auth.WithPrincipal(context.Background(), editor)

	st := preloadOf(t, ctx, security.ScopeRelationSubject[Folder, int64]("Notes", "Author"))
	if !strings.Contains(st.SQL, `"author" = $`) {
		t.Fatalf("the preload read every author's notes:\n%s", st.SQL)
	}
	if !containsArg(st.Args, "u-1") {
		t.Fatalf("preload args = %v, want the principal's subject", st.Args)
	}
}

// ---------------------------------------------------------------------------
// InspectOwner

// owned is the rule InspectOwner exists for: owning a row is not the same as
// the row naming you, so this cannot be written as a scope.
func owned() security.Policy[Ticket, int64] {
	return security.InspectOwner[Ticket, int64](func(p auth.Principal, _ security.Action, m *Ticket) bool {
		return m.Owner == p.Subject()
	})
}

func ticketRow(id int64, owner, body string) []any { return []any{id, owner, body} }

func TestInspectOwnerRefusesARowTheCallerDoesNotOwn(t *testing.T) {
	t.Run("somebody else's row is refused", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows(ticketRow(1, "u-2", "theirs")))
		_, err := Tickets.Bind(rec, security.Gate(owned())).GetByID(as(editor), 1)
		if !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("a row owned by somebody else answered %v, want a denial", err)
		}
	})

	// The control. Without it the refusal above passes for a policy that
	// refuses every row, and nothing would ever be readable.
	t.Run("control: the caller's own row is handed back", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows(ticketRow(1, "u-1", "mine")))
		got, err := Tickets.Bind(rec, security.Gate(owned())).GetByID(as(editor), 1)
		if err != nil {
			t.Fatalf("the caller's own row was refused: %v", err)
		}
		if got.Body != "mine" {
			t.Fatalf("ticket = %+v, want the row that was read", got)
		}
	})
}

// The check sees the row *and* the verb. A rule that ignored the action would
// authorise a delete with whatever answer it gave for a read.
func TestInspectOwnerIsToldWhichVerbIsBeingAttempted(t *testing.T) {
	var seen []security.Action
	readOnlyOwner := security.InspectOwner[Ticket, int64](
		func(_ auth.Principal, a security.Action, _ *Ticket) bool {
			seen = append(seen, a)
			return a == security.Read
		})

	rec := crudtest.Postgres().Push(crudtest.Rows(ticketRow(1, "u-1", "mine")))
	repo := Tickets.Bind(rec, security.Gate(readOnlyOwner))

	if _, err := repo.Delete(as(editor), 1); !errors.Is(err, crud.ErrForbidden) {
		t.Fatalf("the delete answered %v, want the rule's refusal of that verb", err)
	}
	if len(seen) == 0 {
		t.Fatal("the rule was never consulted")
	}
	if last := seen[len(seen)-1]; last != security.Delete {
		t.Fatalf("the rule was told %s, want delete — a rule that cannot tell the verbs apart "+
			"authorises a delete with the answer it gave a read", last)
	}
	for _, s := range rec.SQL() {
		if strings.HasPrefix(s, "DELETE") {
			t.Fatalf("the refused delete still reached the database: %v", rec.SQL())
		}
	}
}

// Without a principal there is nobody to compare the row against, and the rule
// must not be asked — a callback that read a nil principal would decide on one.
func TestInspectOwnerRefusesBeforeConsultingTheRuleWhenNobodyIsAuthenticated(t *testing.T) {
	consulted := false
	policy := security.InspectOwner[Ticket, int64](func(auth.Principal, security.Action, *Ticket) bool {
		consulted = true
		return true
	})

	rec := crudtest.Postgres().Push(crudtest.Rows(ticketRow(1, "u-1", "mine")))
	_, err := Tickets.Bind(rec, security.Gate(policy)).GetByID(context.Background(), 1)
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("an unauthenticated read answered %v, want auth.ErrUnauthenticated", err)
	}
	if consulted {
		t.Fatal("the rule was asked to judge a row with no caller to judge it against")
	}
}
