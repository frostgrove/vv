package wire

import (
	"strings"
	"testing"
)

type parcel struct {
	ID     int64  `db:"id,pk,auto"`
	Label  string `db:"label"`
	Weight int    `db:"weight"`
	Secret string `db:"secret"`
}

type parcelUpdate struct {
	Label  *string
	Weight *int
	Secret *string
}

type parcelPatch struct {
	Label *string
}

type parcelResponse struct {
	ID     int64
	Label  string
	Weight int
}

func TestAPatchBodyThatLeavesOutAColumnTheUpdateDTOWritesMustDeclareIt(t *testing.T) {
	err := CoversPatch[parcelUpdate, parcelPatch]()
	if err == nil {
		t.Fatal("a patch body that quietly drops two writable fields was accepted")
	}
	for _, name := range []string{"Secret", "Weight"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("the refusal does not name %s: %v", name, err)
		}
	}

	if err := CoversPatch[parcelUpdate, parcelPatch]("Secret", "Weight"); err != nil {
		t.Fatalf("the same pair, declared, was still refused: %v", err)
	}
}

func TestAPatchBodyNamingSomethingTheUpdateDTODoesNotCarryIsRefused(t *testing.T) {
	type invented struct {
		Label   *string
		Invoice *string
	}

	err := CoversPatch[parcelUpdate, invented]("Secret", "Weight")
	if err == nil {
		t.Fatal("a public field that reaches no column was accepted")
	}
	if !strings.Contains(err.Error(), "Invoice") {
		t.Fatalf("the refusal does not name the field nothing can write: %v", err)
	}
}

func TestAPublicFieldOfAnotherTypeThanTheColumnIsRefused(t *testing.T) {
	type mistyped struct {
		Label *int
	}

	err := CoversPatch[parcelUpdate, mistyped]("Secret", "Weight")
	if err == nil {
		t.Fatal("a patch field whose type cannot be assigned to the update DTO was accepted")
	}
	if !strings.Contains(err.Error(), "Label") {
		t.Fatalf("the refusal does not name the mistyped field: %v", err)
	}
}

func TestAnExclusionForSomethingTheSourceDoesNotCarryIsRefused(t *testing.T) {
	err := CoversPatch[parcelUpdate, parcelPatch]("Secret", "Weight", "Postmark")
	if err == nil {
		t.Fatal("an exclusion naming nothing was accepted, so a renamed column can hide behind it")
	}
	if !strings.Contains(err.Error(), "Postmark") {
		t.Fatalf("the refusal does not name the stale exclusion: %v", err)
	}
}

func TestAResponseBodyAccountsForEveryColumnOfTheModel(t *testing.T) {
	err := CoversResponse[parcel, parcelResponse]()
	if err == nil {
		t.Fatal("a response body that silently omits a column was accepted")
	}
	if !strings.Contains(err.Error(), "Secret") {
		t.Fatalf("the refusal does not name the omitted column: %v", err)
	}

	if err := CoversResponse[parcel, parcelResponse]("Secret"); err != nil {
		t.Fatalf("the same body, with the omission declared, was still refused: %v", err)
	}
}

func TestMustCoverPanicsWhereCoversWouldReturnTheSameRefusal(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("MustCoverPatch returned where CoversPatch refuses")
		}
		if failure, ok := recovered.(error); !ok || !strings.Contains(failure.Error(), "Weight") {
			t.Fatalf("the panic does not carry the refusal: %v", recovered)
		}
	}()
	MustCoverPatch[parcelUpdate, parcelPatch]("Secret")
}

func TestTheIdentityPatchAndPresenterHandBackWhatTheyWereGiven(t *testing.T) {
	label := "one"
	patched := IdentityPatch[parcelUpdate]().Update(parcelUpdate{Label: &label})
	if patched.Label != &label {
		t.Fatalf("the identity patch mapper rewrote its input: %+v", patched)
	}

	model := parcel{ID: 7, Label: "one"}
	if IdentityPresenter[parcel]().Response(model) != model {
		t.Fatal("the identity presenter rewrote the model")
	}
}
