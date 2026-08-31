package access

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/google/uuid"
)

type serialSignUpForm struct {
	Identifier string
	Password   string
}

type serialRegistrar struct {
	source        crud.Source
	id            uuid.UUID
	err           error
	calls         int
	inTransaction bool
}

func (this *serialRegistrar) Create(ctx context.Context, _ serialSignUpForm) (uuid.UUID, string, error) {
	this.calls++
	if executor, found := crud.ExecutorFor(ctx, this.source); found {
		this.inTransaction = crud.IsTransaction(executor)
	}
	return this.id, "person@example.test", this.err
}

func (*serialRegistrar) Password(form serialSignUpForm) string { return form.Password }

func TestSignUpDiscardsResponseAndRollsBackOnIssuerOrCommitFailure(t *testing.T) {
	issuerFailure := errors.New("signup issuer failed after session persistence")
	commitFailure := errors.New("signup commit failed")

	for _, tc := range []struct {
		name       string
		issuerErr  error
		commitErr  error
		want       error
		rolledBack bool
	}{
		{name: "issuer failure", issuerErr: issuerFailure, want: issuerFailure, rolledBack: true},
		{name: "commit failure", commitErr: commitFailure, want: commitFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := crudtest.Postgres().Push(crudtest.Rows())
			source := &serialSource{Recorder: recorder, commitErr: tc.commitErr}
			issuer := &serialIssuer{
				response: AuthResponse{Token: "must-not-escape"},
				err:      tc.issuerErr,
			}
			deps := serialDeps(t, source, issuer)
			registrar := &serialRegistrar{source: source, id: uuid.New()}
			signUp := NewSignUp(deps, Subject{Type: testSubject}, issuer, registrar)

			response, err := signUp.Execute(t.Context(), serialSignUpForm{
				Identifier: "person@example.test", Password: "a-long-enough-password",
			}, Agent{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			if !reflect.DeepEqual(response, AuthResponse{}) {
				t.Fatalf("failed signup leaked response %+v", response)
			}
			if registrar.calls != 1 || !registrar.inTransaction || issuer.calls != 1 || !issuer.inTransaction {
				t.Fatalf("registrar calls/tx=%d/%v issuer calls/tx=%d/%v",
					registrar.calls, registrar.inTransaction, issuer.calls, issuer.inTransaction)
			}
			if source.last == nil || source.last.rolledBack != tc.rolledBack || source.last.committed {
				t.Fatalf("transaction committed=%v rolledBack=%v, want false/%v",
					source.last != nil && source.last.committed,
					source.last != nil && source.last.rolledBack,
					tc.rolledBack)
			}
		})
	}
}

func TestSignUpOwnsCommitBoundaryDespiteAmbientTransactionRollback(t *testing.T) {
	recorder := crudtest.Postgres().Push(crudtest.Rows())
	source := &serialSource{Recorder: recorder}
	issuer := &serialIssuer{response: AuthResponse{Token: "committed-signup-token"}}
	deps := serialDeps(t, source, issuer)
	registrar := &serialRegistrar{source: source, id: uuid.New()}
	signUp := NewSignUp(deps, Subject{Type: testSubject}, issuer, registrar)

	outerExec, err := source.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	outer := outerExec.(*serialTx)
	ctx := crud.BindExecutor(t.Context(), source, outer)
	response, err := signUp.Execute(ctx, serialSignUpForm{
		Identifier: "person@example.test", Password: "a-long-enough-password",
	}, Agent{})
	if err != nil || response.Token != "committed-signup-token" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	inner := source.last
	if inner == nil || inner == outer || !inner.committed || !registrar.inTransaction || !issuer.inTransaction {
		t.Fatalf("inner=%p committed=%v outer=%p registrarTx=%v issuerTx=%v",
			inner, inner != nil && inner.committed, outer, registrar.inTransaction, issuer.inTransaction)
	}
	if err := outer.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if !inner.committed {
		t.Fatal("ambient rollback undid signup's returned owned commit")
	}
}

var _ Registrar[serialSignUpForm] = (*serialRegistrar)(nil)
