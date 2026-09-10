package i18n

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/errs"
)

func FuzzCatalogAndMF2ValidationNeverPanic(f *testing.F) {
	f.Add("Hello {$name}", "name", "en")
	f.Add(".input {$n :number}\n.match $n\n1 {{one}}\n* {{other}}", "n", "ru")
	f.Add("{#x}broken", "value", "ast")
	f.Add("{@ ", "value", "en")
	f.Fuzz(func(t *testing.T, template, argument, localeName string) {
		limits := DefaultLimits()
		limits.MaxTemplateBytes = 4096
		limits.MaxIdentifierBytes = 128
		limits.Locale.MaxTagBytes = 128
		if len(template) > limits.MaxTemplateBytes || len(argument) > limits.MaxIdentifierBytes || len(localeName) > limits.Locale.MaxTagBytes {
			return
		}
		_, _ = New(CatalogSpec{
			Revision: "fuzz/v1", SourceLocale: localeName, DefaultLocale: localeName,
			Supported: []string{localeName}, Limits: limits,
			Modules: []Module{{Name: "fuzz", Messages: []MessageSpec{{
				ID: "message", Revision: "message/v1", Source: template,
				Arguments: []ArgumentSpec{{Name: argument, Type: TypeText}},
			}}}},
		})
	})
}

func FuzzApplicationOverlayNeverPanic(f *testing.F) {
	base := MessageSpec{ID: "message", Revision: "message/v1", Source: "base", Description: "Fuzz overlay target.", Override: OverrideApplication}
	digest, err := ExpectedSourceDigestForLocale(GrammarProfile, "en", "fuzz", base)
	if err != nil {
		f.Fatal(err)
	}
	snapshot, err := New(CatalogSpec{
		Revision: "base/v1", SourceLocale: "en", DefaultLocale: "en", Supported: []string{"en"},
		Modules: []Module{{Name: "fuzz", Messages: []MessageSpec{base}}},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add("replacement", "en", "message/v1", digest)
	f.Add("{$unknown}", "en-US", "wrong", "not-a-digest")
	f.Fuzz(func(t *testing.T, text, localeName, revision, sourceDigest string) {
		if len(text) > 4096 || len(localeName) > 128 || len(revision) > 128 || len(sourceDigest) > 256 {
			return
		}
		_, _ = snapshot.Overlay(OverlaySpec{
			Revision: "overlay/v1", Layer: LayerApplication,
			Overrides: []Override{{
				Key: Qualify("fuzz", "message"), Locale: localeName, Text: text,
				Review: ReviewApproved, ContractRevision: revision, SourceDigest: sourceDigest,
			}},
		})
	})
}

func FuzzErrorMessagesNeverPanicOrExposeUnmappedParams(f *testing.F) {
	message := MessageSpec{
		ID: "invalid", Revision: "invalid/v1", Source: "Value {$safe}", Description: "Fuzz public error.", Public: true,
		Arguments: []ArgumentSpec{{Name: "safe", Type: TypeText, Required: true}},
	}
	snapshot, err := New(CatalogSpec{
		Revision: "errors/v1", SourceLocale: "en", DefaultLocale: "en", Supported: []string{"en"},
		Modules: []Module{{Name: "errors", Messages: []MessageSpec{message}}},
	})
	if err != nil {
		f.Fatal(err)
	}
	source, err := snapshot.ErrorMessages(ErrorSpec{Mappings: []ErrorMapping{{
		Ladder: "invalid", Key: Qualify("errors", "invalid"),
		Params: []ErrorParam{{Param: "safe", Argument: "safe"}},
	}}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add("visible", "secret", "en")
	f.Add("\u202e", "never-leak", "not-a-locale")
	f.Fuzz(func(t *testing.T, safe, secret, localeName string) {
		if len(safe)+len(secret)+len(localeName) > 4096 {
			return
		}
		text, ok := source.Message(context.Background(), errs.Violation{
			Code: "invalid",
			Params: map[string]any{
				"safe":   safe,
				"secret": secret,
			},
		}, localeName)
		control, controlOK := source.Message(context.Background(), errs.Violation{
			Code:   "invalid",
			Params: map[string]any{"safe": safe},
		}, localeName)
		if text != control || ok != controlOK {
			t.Fatalf("unmapped parameter changed output: (%q, %v) != (%q, %v)", text, ok, control, controlOK)
		}
	})
}
