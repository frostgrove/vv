package audit

import (
	"bytes"
	"errors"
	"testing"
)

type attemptDeclarationExtraction struct {
	Target  Reference
	Payload []byte
}

func TestAttemptDeclarationExtractorsAreSingleUseOwnedAndPanicSafe(t *testing.T) {
	targetCalls := 0
	fieldCalls := 0
	target := AttemptTarget(func(value attemptDeclarationExtraction) Reference {
		targetCalls++
		return value.Target
	}, Internal, AsToken)
	field := AttemptValue("payload", func(value attemptDeclarationExtraction) []byte {
		fieldCalls++
		return value.Payload
	}, Bytes(), Internal)
	start := AttemptStart(target, AttemptFields(field))
	if targetCalls != 0 || fieldCalls != 0 {
		t.Fatalf("declaration invoked extractors: target=%d field=%d", targetCalls, fieldCalls)
	}
	input := attemptDeclarationExtraction{Target: "order-1", Payload: []byte("alpha")}
	reference, err := start.value.target.reference(input)
	if err != nil || reference != "order-1" || targetCalls != 1 {
		t.Fatalf("target extraction = %q calls=%d err=%v", reference, targetCalls, err)
	}
	draft, semantic, err := start.value.fields[0].extract(input)
	if err != nil || fieldCalls != 1 || !bytes.Equal(draft.canonical, []byte("alpha")) || !bytes.Equal(semantic, []byte("alpha")) {
		t.Fatalf("field extraction calls=%d draft=%q semantic=%q err=%v", fieldCalls, draft.canonical, semantic, err)
	}
	input.Payload[0] = 'z'
	semantic[1] = 'z'
	if !bytes.Equal(draft.canonical, []byte("alpha")) {
		t.Fatalf("extracted field aliases caller or returned semantic bytes: %q", draft.canonical)
	}
	panicTarget := AttemptTarget(func(attemptDeclarationExtraction) Reference { panic("secret target") }, Internal, AsToken)
	if _, err := panicTarget.value.reference(attemptDeclarationExtraction{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("target extractor panic error = %v, want ErrInvalid", err)
	}
	panicField := AttemptValue("payload", func(attemptDeclarationExtraction) []byte { panic("secret field") }, Bytes(), Internal)
	if _, _, err := panicField.value.extract(attemptDeclarationExtraction{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("field extractor panic error = %v, want ErrInvalid", err)
	}
}
