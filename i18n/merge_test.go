package i18n

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMergeSourceKeepsTheNewSourceAndCarriesMissingWorkVerbatim(t *testing.T) {
	newTranslation := Translation{
		Locale: "fr", Text: "Nouveau", Review: ReviewRequired,
		ContractRevision: "new-contract", SourceDigest: strings.Repeat("1", 64), ReviewDigest: strings.Repeat("2", 64),
	}
	oldTranslation := Translation{
		Locale: "RU", Text: "Старый перевод", Review: ReviewRejected,
		ContractRevision: "old-contract", SourceDigest: strings.Repeat("3", 64), ReviewDigest: strings.Repeat("4", 64),
	}
	newOverride := Override{
		Key: "app.notice", Locale: "fr", Text: "Nouvelle application", Review: ReviewRequired,
		ContractRevision: "new-override", SourceDigest: strings.Repeat("5", 64), ReviewDigest: strings.Repeat("6", 64),
	}
	oldOverride := Override{
		Key: "app.notice", Locale: "RU", Text: "Старое приложение", Review: ReviewApproved,
		ContractRevision: "old-override", SourceDigest: strings.Repeat("7", 64), ReviewDigest: strings.Repeat("8", 64),
	}
	newSource := mergeSourceFixture("new", "EN", []string{"EN", "fr", "ru"}, []MessageSpec{{
		ID: "notice", Revision: "new-message", Source: "New source", Description: "New description",
		Output: OutputPlain, Override: OverrideApplication, Translations: []Translation{newTranslation},
	}}, []Override{newOverride})
	previous := mergeSourceFixture("previous", "en", []string{"ru", "en", "fr"}, []MessageSpec{{
		ID: "notice", Revision: "old-message", Source: "Old source", Description: "Old description",
		Output: OutputPlain, Override: OverrideApplication,
		Translations: []Translation{
			{Locale: "fr", Text: "Ancien", Review: ReviewApproved, ContractRevision: "superseded"},
			oldTranslation,
		},
	}}, []Override{
		{Key: "app.notice", Locale: "fr", Text: "Ancienne application", Review: ReviewApproved},
		oldOverride,
	})
	newBefore := cloneCatalogSpec(newSource)
	previousBefore := cloneCatalogSpec(previous)

	merged, err := MergeSource(newSource, previous, SourceMergePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if merged.Revision != "new" || merged.Modules[0].Messages[0].Revision != "new-message" || merged.Modules[0].Messages[0].Source != "New source" {
		t.Fatalf("new source was not authoritative: %+v", merged)
	}
	translations := merged.Modules[0].Messages[0].Translations
	if len(translations) != 2 || translations[0] != newTranslation {
		t.Fatalf("authoritative translation changed: %+v", translations)
	}
	wantOldTranslation := oldTranslation
	wantOldTranslation.Locale = "ru"
	if translations[1] != wantOldTranslation {
		t.Fatalf("carried translation = %+v", translations[1])
	}
	if len(merged.Overrides) != 2 || merged.Overrides[0] != newOverride {
		t.Fatalf("authoritative overrides changed: %+v", merged.Overrides)
	}
	wantOldOverride := oldOverride
	wantOldOverride.Locale = "ru"
	if merged.Overrides[1] != wantOldOverride {
		t.Fatalf("carried override = %+v", merged.Overrides[1])
	}
	if !reflect.DeepEqual(cloneCatalogSpec(newSource), newBefore) {
		t.Fatalf("merge mutated new input:\n%+v\n%+v", newSource, newBefore)
	}
	if !reflect.DeepEqual(cloneCatalogSpec(previous), previousBefore) {
		t.Fatalf("merge mutated previous input:\n%+v\n%+v", previous, previousBefore)
	}

	again, err := MergeSource(merged, previous, SourceMergePolicy{})
	if err != nil || !reflect.DeepEqual(again, merged) {
		t.Fatalf("idempotent merge = %+v, %v", again, err)
	}
	again.Modules[0].Messages[0].Translations[1].Text = "changed"
	if previous.Modules[0].Messages[0].Translations[1].Text != oldTranslation.Text {
		t.Fatal("result aliases the previous source")
	}
}

func TestMergeSourceRefusesObsoleteWorkUnlessPruned(t *testing.T) {
	newSource := mergeSourceFixture("new", "en", []string{"en"}, []MessageSpec{
		{ID: "locked", Revision: "r2", Source: "Locked", Description: "Locked", Output: OutputPlain, Override: OverrideDenied},
	}, nil)
	previous := mergeSourceFixture("previous", "en", []string{"en", "fr"}, []MessageSpec{
		{ID: "z", Revision: "r1", Source: "Z", Description: "Z", Output: OutputPlain, Override: OverrideApplication, Translations: []Translation{{Locale: "fr", Text: "Zed"}}},
		{ID: "locked", Revision: "r1", Source: "Locked", Description: "Locked", Output: OutputPlain, Override: OverrideApplication, Translations: []Translation{{Locale: "fr", Text: "Fermé"}}},
		{ID: "a", Revision: "r1", Source: "A", Description: "A", Output: OutputPlain, Override: OverrideApplication, Translations: []Translation{{Locale: "fr", Text: "A"}}},
	}, []Override{{Key: "app.locked", Locale: "en", Text: "Override"}})

	_, firstErr := MergeSource(newSource, previous, SourceMergePolicy{})
	if !errors.Is(firstErr, ErrMergeWouldDiscard) {
		t.Fatalf("obsolete merge error = %v", firstErr)
	}
	previous.Modules[0].Messages[0], previous.Modules[0].Messages[2] = previous.Modules[0].Messages[2], previous.Modules[0].Messages[0]
	_, secondErr := MergeSource(newSource, previous, SourceMergePolicy{})
	if firstErr == nil || secondErr == nil || firstErr.Error() != secondErr.Error() || !strings.Contains(firstErr.Error(), `application override "app.locked" at locale "en"`) {
		t.Fatalf("non-deterministic obsolete errors: %v / %v", firstErr, secondErr)
	}

	pruned, err := MergeSource(newSource, previous, SourceMergePolicy{PruneObsolete: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned.Modules) != 1 || len(pruned.Modules[0].Messages) != 1 || len(pruned.Modules[0].Messages[0].Translations) != 0 || len(pruned.Overrides) != 0 {
		t.Fatalf("obsolete work survived pruning: %+v", pruned)
	}
}

func TestMergeSourceRefusesSourceLocaleChangesEvenWhenPruning(t *testing.T) {
	newSource := mergeSourceFixture("new", "fr", []string{"fr"}, []MessageSpec{{
		ID: "notice", Revision: "r", Source: "Avis", Description: "Avis", Output: OutputPlain,
	}}, nil)
	previous := mergeSourceFixture("previous", "en", []string{"en"}, []MessageSpec{{
		ID: "notice", Revision: "r", Source: "Notice", Description: "Notice", Output: OutputPlain,
	}}, nil)
	_, err := MergeSource(newSource, previous, SourceMergePolicy{PruneObsolete: true})
	if !errors.Is(err, ErrMergeWouldDiscard) || !strings.Contains(err.Error(), `source locale changed from "en" to "fr"`) {
		t.Fatalf("source locale change = %v", err)
	}
}

func TestMergeSourceCanonicalizesCarriedLocalesAndHonorsNewBounds(t *testing.T) {
	newSource := mergeSourceFixture("new", "en", []string{"en", "pt-BR"}, []MessageSpec{{
		ID: "notice", Revision: "r2", Source: "Notice", Description: "Notice", Output: OutputPlain,
	}}, nil)
	previous := mergeSourceFixture("previous", "EN", []string{"PT-br", "en"}, []MessageSpec{{
		ID: "notice", Revision: "r1", Source: "Notice", Description: "Notice", Output: OutputPlain,
		Translations: []Translation{{Locale: "pt-br", Text: "Aviso", Review: ReviewRequired}},
	}}, nil)
	merged, err := MergeSource(newSource, previous, SourceMergePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if got := merged.Modules[0].Messages[0].Translations[0].Locale; got != "pt-BR" {
		t.Fatalf("canonical carried locale = %q", got)
	}

	newSource.Limits.MaxTranslations = 1
	_, err = MergeSource(newSource, previous, SourceMergePolicy{})
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("bounded merge = %v", err)
	}
}

func TestMergeSourcePreflightsMaterialAndSourceOutputBeforeAppending(t *testing.T) {
	newSource := mergeSourceFixture("new", "en", []string{"en", "fr"}, []MessageSpec{{
		ID: "notice", Revision: "r2", Source: "Notice", Description: "Notice", Output: OutputPlain,
	}}, nil)
	previous := mergeSourceFixture("previous", "en", []string{"en", "fr"}, []MessageSpec{{
		ID: "notice", Revision: "r1", Source: "Notice", Description: "Notice", Output: OutputPlain,
		Translations: []Translation{{Locale: "fr", Text: "Avis", Review: ReviewRequired}},
	}}, nil)

	newSource.Limits = DefaultLimits()
	newSource.Modules[0].Messages[0].Key = Qualify("app", "notice")
	counter, _, err := pseudoInputMaterial(newSource, newSource.Limits)
	if err != nil {
		t.Fatal(err)
	}
	newSource.Limits.MaxCatalogBytes = counter.bytes
	before, err := EncodeSource(newSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (SourceMerger{}).Merge(newSource, previous, SourceMergePolicy{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("material preflight error = %v", err)
	}
	after, err := EncodeSource(newSource)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("material refusal mutated the authoritative source")
	}

	newSource.Limits = Limits{}
	raw, err := EncodeSource(newSource)
	if err != nil {
		t.Fatal(err)
	}
	merger := SourceMerger{SourceLimits: ArtifactLimits{MaxBytes: len(raw)}}
	if _, err := merger.Merge(newSource, previous, SourceMergePolicy{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("source output preflight error = %v", err)
	}
}

func TestMergeSourceRetainsOnlyCountAndLexicalFirstObsoleteDiagnostic(t *testing.T) {
	newSource := mergeSourceFixture("new", "en", []string{"en"}, []MessageSpec{{
		ID: "current", Revision: "r2", Source: "Current", Description: "Current", Output: OutputPlain,
	}}, nil)
	messages := make([]MessageSpec, 512)
	for index := range messages {
		id := fmt.Sprintf("m%03d", 511-index)
		messages[index] = MessageSpec{
			ID: id, Revision: "r1", Source: "Old", Description: "Old", Output: OutputPlain,
			Translations: []Translation{{Locale: "fr", Text: "Ancien", Review: ReviewRequired}},
		}
	}
	previous := mergeSourceFixture("previous", "en", []string{"en", "fr"}, messages, nil)
	_, err := MergeSource(newSource, previous, SourceMergePolicy{})
	if !errors.Is(err, ErrMergeWouldDiscard) || !strings.Contains(err.Error(), `512 obsolete entries; first is translation "app.m000" at locale "fr"`) {
		t.Fatalf("bounded obsolete diagnostic = %v", err)
	}
	if len(err.Error()) > 256 {
		t.Fatalf("obsolete diagnostic retained proportional material: %d bytes", len(err.Error()))
	}
}

func TestMergeSourceRejectsInvalidAndCanceledInputs(t *testing.T) {
	valid := mergeSourceFixture("valid", "en", []string{"en"}, []MessageSpec{{
		ID: "notice", Revision: "r", Source: "Notice", Description: "Notice", Output: OutputPlain,
	}}, nil)
	if _, err := MergeSourceContext(nil, valid, valid, SourceMergePolicy{}); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("nil context = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := MergeSourceContext(ctx, valid, valid, SourceMergePolicy{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled merge = %v", err)
	}
	invalid := valid
	invalid.Revision = ""
	if _, err := MergeSource(valid, invalid, SourceMergePolicy{PruneObsolete: true}); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("invalid previous source = %v", err)
	}
}

func TestSourceMergerUsesAnExplicitLocalCatalogCeiling(t *testing.T) {
	const messageCount = 4097
	limits := DefaultLimits()
	limits.MaxMessages = messageCount
	newMessages := make([]MessageSpec, messageCount)
	previousMessages := make([]MessageSpec, messageCount)
	for index := range messageCount {
		id := fmt.Sprintf("message-%04d", index)
		newMessages[index] = MessageSpec{
			ID: id, Revision: "r2", Source: "New " + id, Description: id, Output: OutputPlain,
		}
		previousMessages[index] = MessageSpec{
			ID: id, Revision: "r1", Source: "Old " + id, Description: id, Output: OutputPlain,
		}
	}
	previousMessages[messageCount-1].Translations = []Translation{{Locale: "fr", Text: "Dernier"}}
	newSource := mergeSourceFixture("new", "en", []string{"en", "fr"}, newMessages, nil)
	previous := mergeSourceFixture("previous", "en", []string{"en", "fr"}, previousMessages, nil)
	newSource.Limits = limits
	previous.Limits = limits
	codec := SourceCodec{CatalogLimits: limits}
	roundTrip := func(spec CatalogSpec) CatalogSpec {
		t.Helper()
		raw, err := codec.Encode(spec)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := codec.Decode(context.Background(), bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	newSource = roundTrip(newSource)
	previous = roundTrip(previous)
	newBefore := cloneCatalogSpec(newSource)
	previousBefore := cloneCatalogSpec(previous)

	merged, err := (SourceMerger{CatalogLimits: limits}).Merge(newSource, previous, SourceMergePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Modules) != 1 || len(merged.Modules[0].Messages) != messageCount {
		t.Fatalf("merged message count = %d", len(merged.Modules[0].Messages))
	}
	last := merged.Modules[0].Messages[messageCount-1]
	if last.Source != "New message-4096" || !reflect.DeepEqual(last.Translations, []Translation{{Locale: "fr", Text: "Dernier"}}) {
		t.Fatalf("last merged message = %+v", last)
	}
	if !reflect.DeepEqual(cloneCatalogSpec(newSource), newBefore) {
		t.Fatal("merge mutated the new source")
	}
	if !reflect.DeepEqual(cloneCatalogSpec(previous), previousBefore) {
		t.Fatal("merge mutated the previous source")
	}

	lower := limits
	lower.MaxMessages--
	if _, err := (SourceMerger{CatalogLimits: lower}).Merge(newSource, previous, SourceMergePolicy{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("lower local ceiling = %v", err)
	}
	if _, err := MergeSource(newSource, previous, SourceMergePolicy{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("default local ceiling = %v", err)
	}
	if !reflect.DeepEqual(cloneCatalogSpec(newSource), newBefore) || !reflect.DeepEqual(cloneCatalogSpec(previous), previousBefore) {
		t.Fatal("rejected merge mutated an input")
	}
}

func mergeSourceFixture(revision, sourceLocale string, supported []string, messages []MessageSpec, overrides []Override) CatalogSpec {
	return CatalogSpec{
		Revision: revision, Profile: GrammarProfile, SourceLocale: sourceLocale, DefaultLocale: sourceLocale,
		Supported: supported, Required: []string{sourceLocale}, DefaultTimeZone: "UTC",
		Modules: []Module{{Name: "app", Messages: messages}}, Overrides: overrides,
	}
}
