package i18n

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExpectedReviewDigestPinsCanonicalLocaleAndExactText(t *testing.T) {
	message := simpleMessage("notice", "Notice")
	sourceDigest, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", "app", message)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ExpectedReviewDigest(sourceDigest, "pt-br", "Aviso")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := ExpectedReviewDigest(sourceDigest, "pt-BR", "Aviso")
	if err != nil || canonical != first {
		t.Fatalf("canonical digest = %q, %v; want %q", canonical, err, first)
	}
	changed, err := ExpectedReviewDigest(sourceDigest, "pt-BR", "Aviso!")
	if err != nil || changed == first {
		t.Fatalf("changed digest = %q, %v", changed, err)
	}
	if _, err := ExpectedReviewDigest(strings.ToUpper(sourceDigest), "pt-BR", "Aviso"); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("uppercase source digest error = %v", err)
	}
}

func TestSourceReviewDigestPinsCanonicalSourceLocale(t *testing.T) {
	message := simpleMessage("notice", "Notice")
	english, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", "app", message)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := ExpectedSourceDigestForLocale(GrammarProfile, "EN", "app", message)
	if err != nil || canonical != english {
		t.Fatalf("canonical source locale digest = %q, %v; want %q", canonical, err, english)
	}
	german, err := ExpectedSourceDigestForLocale(GrammarProfile, "de", "app", message)
	if err != nil || german == english {
		t.Fatalf("changed source locale digest = %q, %v", german, err)
	}
	if _, err := ExpectedSourceDigestForLocale(GrammarProfile, "", "app", message); !errors.Is(err, ErrInvalidLocale) {
		t.Fatalf("empty source locale error = %v", err)
	}
}

func TestSourceLocaleOnlyChangeMakesExistingApprovalStale(t *testing.T) {
	message := simpleMessage("notice", "Notice")
	message.Translations = []Translation{{
		Locale: "ru", Text: "Уведомление", Review: ReviewApproved, ContractRevision: message.Revision,
	}}
	spec := reviewedCatalog(t, testCatalog(message))
	spec.Supported = []string{"en", "de", "ru"}
	spec.SourceLocale = "de"
	spec.DefaultLocale = "de"
	if _, err := New(spec); err == nil || !strings.Contains(err.Error(), "source_digest") || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("source-locale-only approval error = %v", err)
	}
	report := Check(spec, CheckPolicy{})
	assertCheckFinding(t, report, CheckStale, SeverityWarning, "app.notice", "ru")
}

func TestCatalogAndOverlayRejectContentChangedAfterApproval(t *testing.T) {
	message := simpleMessage("notice", "Notice")
	message.Override = OverrideAny
	message.Translations = []Translation{{
		Locale: "ru", Text: "Уведомление", Review: ReviewApproved, ContractRevision: message.Revision,
	}}
	spec := reviewedCatalog(t, testCatalog(message))
	spec.Modules[0].Messages[0].Translations[0].Text = "Изменено"
	if _, err := New(spec); err == nil || !strings.Contains(err.Error(), "review_digest") || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("translation mutation error = %v", err)
	}

	base := mustSnapshot(t, testCatalog(message))
	override := reviewedOverride(t, base, Override{
		Key: "app.notice", Locale: "ru", Text: "Замена", Review: ReviewApproved, ContractRevision: message.Revision,
	})
	override.Text = "Изменённая замена"
	if _, err := base.Overlay(ApplicationOverlay("application/v1", override)); err == nil || !strings.Contains(err.Error(), "review_digest") || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("override mutation error = %v", err)
	}
}

func TestCheckClassifiesContentChangedAfterApprovalAsStale(t *testing.T) {
	translation := checkFixture(t)
	translation.Modules[0].Messages[0].Translations[0].Text = "Изменено {$name}"
	report := Check(translation, CheckPolicy{})
	assertCheckFinding(t, report, CheckStale, SeverityError, "app.notice", "ru")

	message := simpleMessage("override", "Base")
	message.Override = OverrideApplication
	base := mustSnapshot(t, testCatalog(message))
	override := reviewedOverride(t, base, Override{
		Key: "app.override", Locale: "ru", Text: "Замена", Review: ReviewApproved, ContractRevision: message.Revision,
	})
	override.Text = "Изменено"
	overrideSpec := testCatalog(message)
	overrideSpec.Required = []string{"ru"}
	overrideSpec.Overrides = []Override{override}
	report = Check(overrideSpec, CheckPolicy{})
	assertCheckFinding(t, report, CheckStale, SeverityError, "app.override", "ru")
}

func TestSourceRoundTripPreservesReviewDigest(t *testing.T) {
	message := simpleMessage("notice", "Notice")
	message.Override = OverrideApplication
	message.Translations = []Translation{{
		Locale: "ru", Text: "Уведомление", Review: ReviewApproved, ContractRevision: message.Revision,
	}}
	spec := reviewedCatalog(t, testCatalog(message))
	base := mustSnapshot(t, testCatalog(message))
	spec.Overrides = []Override{reviewedOverride(t, base, Override{
		Key: "app.notice", Locale: "ru", Text: "Замена", Review: ReviewApproved, ContractRevision: message.Revision,
	})}
	raw, err := EncodeSource(spec)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSource(context.Background(), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Modules[0].Messages[0].Translations[0].ReviewDigest != spec.Modules[0].Messages[0].Translations[0].ReviewDigest || decoded.Overrides[0].ReviewDigest != spec.Overrides[0].ReviewDigest {
		t.Fatal("source round trip changed review digests")
	}
}
