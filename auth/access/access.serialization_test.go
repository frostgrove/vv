package access

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/errs"
	"github.com/google/uuid"
)

func TestLoginUsesTheCurrentLockingReadNotTheCandidateSnapshot(t *testing.T) {
	ref := SubjectRef{Type: testSubject, ID: uuid.New()}
	id := uuid.New()

	for _, tc := range []struct {
		name        string
		password    string
		wantIssued  bool
		wantBadCode bool
	}{
		{name: "the old password is refused", password: "before-reset", wantBadCode: true},
		{name: "the new password is accepted", password: "after-reset", wantIssued: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := crudtest.Postgres().Push(
				crudtest.Rows(serialCredentialRow(id, ref, "person@example.test", "hashed:before-reset")),
				crudtest.Rows([]any{id.String()}),
				crudtest.Rows(serialCredentialRow(id, ref, "person@example.test", "hashed:after-reset")),
			)
			issuer := &serialIssuer{response: AuthResponse{Token: "only-after-commit"}}
			deps := serialDeps(t, recorder, issuer)

			response, err := NewLogin(deps).Issuing(issuer).Execute(t.Context(), LoginCommand{
				Subject: testSubject, Identifier: "person@example.test", Password: tc.password,
			})
			if tc.wantBadCode {
				fault, ok := errs.AsFault(err)
				if !ok || fault.Code != CodeBadCredentials {
					t.Fatalf("old password error = %v, want typed %q fault", err, CodeBadCredentials)
				}
				if response.Token != "" || issuer.calls != 0 {
					t.Fatalf("stale candidate issued response=%+v calls=%d", response, issuer.calls)
				}
			} else {
				if err != nil || response.Token != "only-after-commit" || issuer.calls != 1 {
					t.Fatalf("new password response=%+v calls=%d err=%v", response, issuer.calls, err)
				}
				if !issuer.inTransaction {
					t.Fatal("SessionIssuer was called after the credential transaction/lock ended")
				}
			}

			statements := recorder.Statements()
			wantStatements := 3
			wantSuffix := ""
			if tc.wantIssued {
				wantStatements = 4
				wantSuffix = ", then issue fence"
			}
			if len(statements) != wantStatements {
				t.Fatalf("login ran %d statements, want candidate, locking read%s: %v",
					len(statements), wantSuffix, recorder.SQL())
			}
			first := strings.ToUpper(crudtest.Normalize(statements[0].SQL))
			discovery := strings.ToUpper(crudtest.Normalize(statements[1].SQL))
			locking := strings.ToUpper(crudtest.Normalize(statements[2].SQL))
			if strings.Contains(first, "FOR UPDATE") {
				t.Fatalf("candidate lookup locked the unique secondary index: %s", statements[0].SQL)
			}
			if strings.Contains(discovery, "FOR UPDATE") || !strings.HasPrefix(discovery, `SELECT "ID"`) {
				t.Fatalf("credential ID discovery took locks or selected full secrets: %s", statements[1].SQL)
			}
			if !strings.Contains(locking, `WHERE "ID" =`) || !strings.Contains(locking, "FOR UPDATE") ||
				strings.Contains(locking, `"SUBJECT_ID" =`) {
				t.Fatalf("canonical current read is not an exact primary-key lock: %s", statements[2].SQL)
			}
			if tc.wantIssued {
				fence := strings.ToUpper(crudtest.Normalize(statements[3].SQL))
				if !strings.HasPrefix(fence, "UPDATE") || !strings.Contains(fence, `"SECRET_HASH" =`) ||
					!strings.Contains(fence, `"ID" =`) {
					t.Fatalf("successful login has no credential tuple fence: %s", statements[3].SQL)
				}
			}
		})
	}
}

func TestCredentialLocksAreExactPrimaryKeyReadsInByteOrderAtHighCount(t *testing.T) {
	ref := SubjectRef{Type: testSubject, ID: uuid.New()}
	const total = 64
	ids := make([]uuid.UUID, total)
	for i := range ids {
		ids[i] = uuid.New()
	}
	discovery := append([]uuid.UUID(nil), ids...)

	for left, right := 0, len(discovery)-1; left < right; left, right = left+1, right-1 {
		discovery[left], discovery[right] = discovery[right], discovery[left]
	}
	sorted := append([]uuid.UUID(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i][:], sorted[j][:]) < 0 })

	idRows := make([][]any, 0, total)
	for _, id := range discovery {
		idRows = append(idRows, []any{id.String()})
	}
	recorder := crudtest.MySQL().Push(crudtest.Rows(idRows...))
	for _, id := range sorted {
		recorder.Push(crudtest.Rows(serialCredentialRow(
			id, ref, "person+"+id.String()+"@example.test", "hashed:password")))
	}

	locked, err := NewStore(recorder).LockPasswordCredentials(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked) != total {
		t.Fatalf("locked %d credentials, want %d", len(locked), total)
	}
	statements := recorder.Statements()
	if len(statements) != total+1 {
		t.Fatalf("statements=%d, want one discovery + %d PK locks", len(statements), total)
	}
	if strings.Contains(strings.ToUpper(statements[0].SQL), "FOR UPDATE") {
		t.Fatalf("discovery query locks the secondary index: %s", statements[0].SQL)
	}
	for i, id := range sorted {
		statement := statements[i+1]
		folded := strings.ToUpper(crudtest.Normalize(statement.SQL))
		if !strings.Contains(folded, "WHERE `ID` =") || !strings.Contains(folded, "FOR UPDATE") ||
			strings.Contains(folded, "`SUBJECT_ID` =") {
			t.Fatalf("lock %d is not exact-PK-only: %s", i, statement.SQL)
		}
		if len(statement.Args) == 0 || statement.Args[0] != id {
			t.Fatalf("lock %d id=%v, want byte-sorted %s", i, statement.Args, id)
		}
	}
}

func TestCredentialLockSetRevalidatesRowsChangedAfterDiscovery(t *testing.T) {
	ref := SubjectRef{Type: testSubject, ID: uuid.New()}
	movedID, currentID := uuid.New(), uuid.New()
	other := SubjectRef{Type: "other", ID: uuid.New()}
	ids := []uuid.UUID{movedID, currentID}
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })

	recorder := crudtest.Postgres().Push(crudtest.Rows(
		[]any{movedID.String()}, []any{currentID.String()},
	))
	for _, id := range ids {
		rowRef := ref
		if id == movedID {
			rowRef = other
		}
		recorder.Push(crudtest.Rows(serialCredentialRow(
			id, rowRef, "person+"+id.String()+"@example.test", "hashed:password")))
	}

	locked, err := NewStore(recorder).LockPasswordCredentials(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(locked) != 1 || locked[0].ID != currentID {
		t.Fatalf("revalidated lock set=%+v, want only current row %s", locked, currentID)
	}
}

func TestSessionIssueFenceDoesNotOptimiseAwayAnEqualValueOrRequireAffectedRows(t *testing.T) {
	ref := SubjectRef{Type: testSubject, ID: uuid.New()}
	credential := Credential{
		ID: uuid.New(), SubjectType: string(ref.Type), SubjectID: ref.ID,
		Provider: ProviderPassword, SecretHash: "hashed:password",
	}
	recorder := crudtest.MySQL().ExecResult(crud.Result{RowsAffected: 0})

	if err := NewStore(recorder).FenceSessionIssue(t.Context(), credential); err != nil {
		t.Fatalf("equal-value, zero-row fence: %v", err)
	}
	statements := recorder.Statements()
	if len(statements) != 1 {
		t.Fatalf("fence ran %d statements, want exactly one unconditional update: %v", len(statements), recorder.SQL())
	}
	statement := strings.ToUpper(crudtest.Normalize(statements[0].SQL))
	if !strings.HasPrefix(statement, "UPDATE") || !strings.Contains(statement, "`SECRET_HASH` =") ||
		!strings.Contains(statement, "`ID` =") || strings.Count(statement, "`SECRET_HASH` =") != 2 {
		t.Fatalf("fence was diffed away or not pinned to id/hash: %s", statements[0].SQL)
	}
}

func TestLoginRollsBackIssuerErrorsAndNeverReturnsAResponseAfterCommitFailure(t *testing.T) {
	ref := SubjectRef{Type: testSubject, ID: uuid.New()}
	id := uuid.New()
	issuerFailure := errors.New("issuer could not persist the session")
	fenceFailure := errors.New("credential issue fence failed")
	commitFailure := errors.New("database commit outcome was not successful")

	for _, tc := range []struct {
		name       string
		fenceErr   error
		issueErr   error
		commitErr  error
		want       error
		wantIssues int
		rollback   bool
	}{
		{name: "fence error", fenceErr: fenceFailure, want: fenceFailure, rollback: true},
		{name: "issuer error", issueErr: issuerFailure, want: issuerFailure, wantIssues: 1, rollback: true},
		{name: "commit error", commitErr: commitFailure, want: commitFailure, wantIssues: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := crudtest.Postgres().Push(
				crudtest.Rows(serialCredentialRow(id, ref, "person@example.test", "hashed:password")),
				crudtest.Rows([]any{id.String()}),
				crudtest.Rows(serialCredentialRow(id, ref, "person@example.test", "hashed:password")),
			)
			if tc.fenceErr != nil {
				recorder.Fail(tc.fenceErr)
			}
			source := &serialSource{Recorder: recorder, commitErr: tc.commitErr}
			issuer := &serialIssuer{
				response: AuthResponse{Token: "must-not-escape"},
				err:      tc.issueErr,
			}
			deps := serialDeps(t, source, issuer)

			response, err := NewLogin(deps).Issuing(issuer).Execute(t.Context(), LoginCommand{
				Subject: testSubject, Identifier: "person@example.test", Password: "password",
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want original typed error %v", err, tc.want)
			}
			if !reflect.DeepEqual(response, AuthResponse{}) {
				t.Fatalf("failed transaction leaked issuer response %+v", response)
			}
			if issuer.calls != tc.wantIssues {
				t.Fatalf("SessionIssuer calls=%d, want %d", issuer.calls, tc.wantIssues)
			}
			if source.last == nil || source.last.rolledBack != tc.rollback {
				t.Fatalf("rolledBack = %v, want %v", source.last != nil && source.last.rolledBack, tc.rollback)
			}
		})
	}
}

func TestAnUnknownLoginKeepsDatabaseRoundTripParityWithoutTakingAGapLock(t *testing.T) {
	recorder := crudtest.MySQL().Push(crudtest.Rows(), crudtest.Rows(), crudtest.Rows())
	issuer := &serialIssuer{response: AuthResponse{Token: "must-not-issue"}}
	deps := serialDeps(t, recorder, issuer)

	response, err := NewLogin(deps).Issuing(issuer).Execute(t.Context(), LoginCommand{
		Subject: testSubject, Identifier: "unknown@example.test", Password: "a guess",
	})
	fault, ok := errs.AsFault(err)
	if !ok || fault.Code != CodeBadCredentials {
		t.Fatalf("unknown login error = %v, want typed %q fault", err, CodeBadCredentials)
	}
	if response.Token != "" || issuer.calls != 0 {
		t.Fatalf("unknown login issued response=%+v calls=%d", response, issuer.calls)
	}

	statements := recorder.Statements()
	if len(statements) != 3 {
		t.Fatalf("unknown login ran %d DB statements, a refused one-credential login runs three: %v", len(statements), recorder.SQL())
	}
	for i, statement := range statements {
		if strings.Contains(strings.ToUpper(statement.SQL), "FOR UPDATE") {
			t.Fatalf("miss padding statement %d took a MySQL gap lock: %s", i+1, statement.SQL)
		}
	}
}

func TestCredentialDeletedBetweenCandidateAndDiscoveryKeepsRefusalParity(t *testing.T) {
	ref := SubjectRef{Type: testSubject, ID: uuid.New()}
	id := uuid.New()
	recorder := crudtest.MySQL().Push(
		crudtest.Rows(serialCredentialRow(id, ref, "person@example.test", "hashed:password")),
		crudtest.Rows(),
		crudtest.Rows(),
	)
	issuer := &serialIssuer{response: AuthResponse{Token: "must-not-issue"}}
	deps := serialDeps(t, recorder, issuer)

	response, err := NewLogin(deps).Issuing(issuer).Execute(t.Context(), LoginCommand{
		Subject: testSubject, Identifier: "person@example.test", Password: "password",
	})
	fault, ok := errs.AsFault(err)
	if !ok || fault.Code != CodeBadCredentials || response.Token != "" || issuer.calls != 0 {
		t.Fatalf("deleted-candidate response=%+v calls=%d err=%v", response, issuer.calls, err)
	}
	if statements := recorder.Statements(); len(statements) != 3 {
		t.Fatalf("deleted-candidate refusal ran %d statements, want parity three: %v", len(statements), recorder.SQL())
	}
}

func TestLoginOwnsItsCommitBoundaryDespiteAmbientExecutors(t *testing.T) {
	ref := SubjectRef{Type: testSubject, ID: uuid.New()}
	credentialID := uuid.New()

	for _, ambient := range []string{"pool", "transaction"} {
		t.Run(ambient, func(t *testing.T) {
			recorder := crudtest.Postgres().Push(
				crudtest.Rows(serialCredentialRow(credentialID, ref, "person@example.test", "hashed:password")),
				crudtest.Rows([]any{credentialID.String()}),
				crudtest.Rows(serialCredentialRow(credentialID, ref, "person@example.test", "hashed:password")),
			)
			source := &serialSource{Recorder: recorder}
			issuer := &serialIssuer{response: AuthResponse{Token: "committed-token"}}
			deps := serialDeps(t, source, issuer)
			ctx := t.Context()
			var outer *serialTx
			switch ambient {
			case "pool":
				ctx = crud.BindExecutor(ctx, source, source)
			case "transaction":
				tx, err := source.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				outer = tx.(*serialTx)
				ctx = crud.BindExecutor(ctx, source, outer)
			}

			response, err := NewLogin(deps).Issuing(issuer).Execute(ctx, LoginCommand{
				Subject: testSubject, Identifier: "person@example.test", Password: "password",
			})
			if err != nil || response.Token != "committed-token" {
				t.Fatalf("response=%+v err=%v", response, err)
			}
			inner := source.last
			if inner == nil || inner == outer || !inner.committed {
				t.Fatalf("owned transaction=%p committed=%v outer=%p", inner, inner != nil && inner.committed, outer)
			}
			if outer != nil {
				if err := outer.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				if !inner.committed {
					t.Fatal("rolling back the ambient owner undid login's already-returned commit boundary")
				}
			}
		})
	}
}

func serialCredentialRow(id uuid.UUID, ref SubjectRef, identifier, secret string) []any {
	now := time.Now()
	return []any{
		id.String(), string(ref.Type), ref.ID.String(), ProviderPassword,
		identifier, secret, now, now,
	}
}

func serialDeps(t *testing.T, source crud.Source, issuer *serialIssuer) *Deps {
	t.Helper()
	store := NewStore(source)
	directories := MustDirectories(stubDirectory{active: true})
	grants := NewGrants(store, directories)
	issuer.store = store
	return newDeps(store, grants, cheapHasher{}, Config{}, slog.New(slog.DiscardHandler), nil)
}

type serialIssuer struct {
	store         *Store
	response      AuthResponse
	err           error
	calls         int
	inTransaction bool
}

func (this *serialIssuer) Issue(ctx context.Context, _ SubjectRef, _ Agent) (AuthResponse, error) {
	this.calls++
	if source, ok := crud.SourceOf(this.store.Sessions.Unwrap()); ok {
		executor, found := crud.ExecutorFor(ctx, source)
		this.inTransaction = found && crud.IsTransaction(executor)
	}
	return this.response, this.err
}

type serialSource struct {
	*crudtest.Recorder
	commitErr error
	last      *serialTx
}

func (this *serialSource) DataSource() any { return this }

func (this *serialSource) Begin(ctx context.Context) (crud.Tx, error) {
	inner, err := this.Recorder.Begin(ctx)
	if err != nil {
		return nil, err
	}
	tx := &serialTx{Tx: inner, source: this, commitErr: this.commitErr}
	this.last = tx
	return tx, nil
}

type serialTx struct {
	crud.Tx
	source     *serialSource
	commitErr  error
	committed  bool
	rolledBack bool
}

func (this *serialTx) DataSource() any { return this.source }

func (this *serialTx) Commit(ctx context.Context) error {
	if this.commitErr != nil {
		return this.commitErr
	}
	if err := this.Tx.Commit(ctx); err != nil {
		return err
	}
	this.committed = true
	return nil
}

func (this *serialTx) Rollback(ctx context.Context) error {
	this.rolledBack = true
	return this.Tx.Rollback(ctx)
}

var _ SessionIssuer = (*serialIssuer)(nil)
var _ crud.Source = (*serialSource)(nil)
var _ crud.Beginner = (*serialSource)(nil)
var _ crud.Tx = (*serialTx)(nil)
