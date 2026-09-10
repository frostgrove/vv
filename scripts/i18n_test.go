package scripts

import "testing"

const i18nExtension = "github.com/frostgrove/vv/i18n"

func TestNoBaseSubsystemDependsOnTheI18nExtension(t *testing.T) {
	noBaseSubsystemDependsOn(t, i18nExtension)
}

func TestNoI18nPackageCostsMoreThanItsErrorSeam(t *testing.T) {
	reached := firstPartyDependenciesIn(t, "..", "i18n", "./...")
	allowed := firstPartyDependenciesIn(t, "..", "i18n",
		"github.com/frostgrove/vv/errs",
		"github.com/agentable/go-intl/displaynames",
		"github.com/agentable/go-intl/datetimeformat",
		"github.com/agentable/go-intl/durationformat",
		"github.com/agentable/go-intl/listformat",
		"github.com/agentable/go-intl/locale",
		"github.com/agentable/go-intl/numberformat",
		"github.com/agentable/go-intl/pluralrules",
		"github.com/agentable/go-intl/relativetimeformat",
		"github.com/kaptinlin/messageformat-go",
		"golang.org/x/mod/modfile",
		"golang.org/x/text/language",
	)
	for dependency := range reached {
		if under(i18nExtension, dependency) || allowed[dependency] {
			continue
		}
		t.Errorf("the i18n extension reaches %s outside its error seam and declared MessageFormat/CLDR ecosystem", dependency)
	}
}

func TestMerelyImportingTheI18nExtensionStartsNothing(t *testing.T) {
	startsNothing(t, i18nExtension, "i18n")
}
