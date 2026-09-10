package i18n

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const SourceVersion = "frostgrove.i18n.source/v1"

var (
	ErrInvalidSource = errors.New("i18n: invalid source")
	ErrSourceIO      = errors.New("i18n: source I/O")
)

type SourceCodec struct {
	Limits        ArtifactLimits
	CatalogLimits Limits
}

func DecodeSource(ctx context.Context, source io.Reader) (CatalogSpec, error) {
	return (SourceCodec{}).Decode(ctx, source)
}

func EncodeSource(spec CatalogSpec) ([]byte, error) {
	return (SourceCodec{}).Encode(spec)
}

func EncodeSourceContext(ctx context.Context, spec CatalogSpec) ([]byte, error) {
	return (SourceCodec{}).EncodeContext(ctx, spec)
}

func (c SourceCodec) Decode(ctx context.Context, source io.Reader) (CatalogSpec, error) {
	artifactLimits, err := checkedArtifactLimits(c.Limits)
	if err != nil {
		return CatalogSpec{}, err
	}
	ceiling, err := checkedLimits(c.CatalogLimits)
	if err != nil {
		return CatalogSpec{}, err
	}
	if ctx == nil {
		return CatalogSpec{}, fmt.Errorf("%w: context is nil", ErrInvalidSource)
	}
	if nilInterface(source) {
		return CatalogSpec{}, fmt.Errorf("%w: reader is nil", ErrInvalidSource)
	}
	raw, err := readAndScanSourceJSON(ctx, source, artifactLimits, ceiling)
	if err != nil {
		return CatalogSpec{}, err
	}
	if err := ctx.Err(); err != nil {
		return CatalogSpec{}, err
	}
	var document sourceDocument
	decoder := json.NewDecoder(&sourceContextReader{ctx: ctx, source: bytes.NewReader(raw)})
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return CatalogSpec{}, fmt.Errorf("%w: decoding document: %v", ErrInvalidSource, err)
	}
	if err := ctx.Err(); err != nil {
		return CatalogSpec{}, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		return CatalogSpec{}, fmt.Errorf("%w: trailing data: %v", ErrInvalidSource, err)
	}
	if document.Source != SourceVersion {
		return CatalogSpec{}, fmt.Errorf("%w: unsupported source version %q", ErrInvalidSource, boundedProblemText(document.Source))
	}
	spec, err := document.catalogSpec(ctx)
	if err != nil {
		return CatalogSpec{}, err
	}
	return canonicalSourceSpec(ctx, spec, ceiling)
}

func (c SourceCodec) Encode(spec CatalogSpec) ([]byte, error) {
	return c.EncodeContext(context.Background(), spec)
}

func (c SourceCodec) EncodeContext(ctx context.Context, spec CatalogSpec) ([]byte, error) {
	expectedSize, artifactLimits, ceiling, err := c.encodedSizeContext(ctx, spec)
	if err != nil {
		return nil, err
	}
	canonical, err := canonicalSourceSpec(ctx, spec, ceiling)
	if err != nil {
		return nil, err
	}
	document, err := sourceDocumentFromSpecContext(ctx, canonical)
	if err != nil {
		return nil, err
	}
	encoder := boundedArtifactJSON{
		bytes:  make([]byte, 0, min(artifactLimits.MaxBytes, 32<<10)),
		limits: artifactLimits,
		ctx:    ctx,
	}
	if err := encoder.value(reflect.ValueOf(document), 1); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if errors.Is(err, ErrLimitExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: encoding document: %v", ErrInvalidSource, err)
	}
	if len(encoder.bytes) != expectedSize {
		return nil, fmt.Errorf("%w: canonical source size changed during materialization", ErrInvalidSource)
	}
	return encoder.bytes, nil
}

func (c SourceCodec) EncodedSize(spec CatalogSpec) (int, error) {
	return c.EncodedSizeContext(context.Background(), spec)
}

func (c SourceCodec) EncodedSizeContext(ctx context.Context, spec CatalogSpec) (int, error) {
	size, _, _, err := c.encodedSizeContext(ctx, spec)
	return size, err
}

func (c SourceCodec) encodedSizeContext(ctx context.Context, spec CatalogSpec) (int, ArtifactLimits, Limits, error) {
	if ctx == nil {
		return 0, ArtifactLimits{}, Limits{}, fmt.Errorf("%w: encoding context is nil", ErrInvalidSource)
	}
	if err := ctx.Err(); err != nil {
		return 0, ArtifactLimits{}, Limits{}, err
	}
	artifactLimits, err := checkedArtifactLimits(c.Limits)
	if err != nil {
		return 0, ArtifactLimits{}, Limits{}, err
	}
	ceiling, err := checkedLimits(c.CatalogLimits)
	if err != nil {
		return 0, ArtifactLimits{}, Limits{}, err
	}
	declared, err := checkedLimits(spec.Limits)
	if err != nil {
		return 0, ArtifactLimits{}, Limits{}, err
	}
	if err := requireCatalogLimitCeiling(declared, ceiling); err != nil {
		return 0, ArtifactLimits{}, Limits{}, err
	}
	var counter artifactFloorCounter
	if err := checkSourceArtifactFloorIntoContext(ctx, spec, declared, artifactLimits, &counter); err != nil {
		return 0, ArtifactLimits{}, Limits{}, err
	}
	if err := preflightCatalogCardinalityContext(ctx, spec, declared); err != nil {
		return 0, ArtifactLimits{}, Limits{}, sourceSemanticError(err)
	}
	return counter.written, artifactLimits, ceiling, nil
}

type sourceDocument struct {
	Source              string           `json:"source"`
	Revision            string           `json:"revision"`
	Profile             string           `json:"profile"`
	SourceLocale        string           `json:"source_locale"`
	DefaultLocale       string           `json:"default_locale"`
	Supported           []string         `json:"supported"`
	Required            []string         `json:"required"`
	Parents             []sourceParent   `json:"parents"`
	MatchMode           string           `json:"match_mode"`
	DefaultOnMiss       bool             `json:"default_on_miss"`
	DefaultTimeZone     string           `json:"default_time_zone"`
	TimeZoneDataVersion string           `json:"time_zone_data_version"`
	Capabilities        []string         `json:"capabilities"`
	Limits              artifactLimits   `json:"limits"`
	Modules             []sourceModule   `json:"modules"`
	Overrides           []sourceOverride `json:"overrides"`
}

type sourceParent struct {
	Locale string `json:"locale"`
	Parent string `json:"parent"`
}

type sourceModule struct {
	Name     string          `json:"name"`
	Messages []sourceMessage `json:"messages"`
}

type sourceMessage struct {
	ID           string              `json:"id"`
	Revision     string              `json:"revision"`
	Source       string              `json:"source"`
	Description  string              `json:"description"`
	Arguments    []sourceArgument    `json:"arguments"`
	Output       string              `json:"output"`
	Markup       []string            `json:"markup"`
	Override     string              `json:"override"`
	Translations []sourceTranslation `json:"translations"`
	AllowEmpty   bool                `json:"allow_empty"`
	Public       bool                `json:"public"`
}

type sourceArgument struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Nullable bool     `json:"nullable"`
	Values   []string `json:"values"`
}

type sourceTranslation struct {
	Locale           string `json:"locale"`
	Text             string `json:"text"`
	Review           string `json:"review"`
	ContractRevision string `json:"contract_revision"`
	SourceDigest     string `json:"source_digest"`
	ReviewDigest     string `json:"review_digest"`
}

type sourceOverride struct {
	Layer            string `json:"layer"`
	Key              string `json:"key"`
	Locale           string `json:"locale"`
	Text             string `json:"text"`
	Review           string `json:"review"`
	ContractRevision string `json:"contract_revision"`
	SourceDigest     string `json:"source_digest"`
	ReviewDigest     string `json:"review_digest"`
}

func (d sourceDocument) catalogSpec(ctx context.Context) (CatalogSpec, error) {
	if err := ctx.Err(); err != nil {
		return CatalogSpec{}, err
	}
	profile := d.Profile
	if profile == "" {
		profile = GrammarProfile
	}
	matchMode := MatchLookup
	if d.MatchMode != "" {
		parsed, ok := parseMatchMode(d.MatchMode)
		if !ok {
			return CatalogSpec{}, fmt.Errorf("%w: unknown match mode %q", ErrInvalidSource, boundedProblemText(d.MatchMode))
		}
		matchMode = parsed
	}
	spec := CatalogSpec{
		Revision:            d.Revision,
		Profile:             profile,
		SourceLocale:        d.SourceLocale,
		DefaultLocale:       d.DefaultLocale,
		Supported:           slices.Clone(d.Supported),
		Required:            slices.Clone(d.Required),
		MatchMode:           matchMode,
		DefaultOnMiss:       d.DefaultOnMiss,
		DefaultTimeZone:     d.DefaultTimeZone,
		TimeZoneDataVersion: d.TimeZoneDataVersion,
		Limits:              decodeArtifactLimits(d.Limits),
		Parents:             make([]LocaleEdge, 0, len(d.Parents)),
		Capabilities:        make([]Capability, 0, len(d.Capabilities)),
		Modules:             make([]Module, 0, len(d.Modules)),
		Overrides:           make([]Override, 0, len(d.Overrides)),
	}
	for _, parent := range d.Parents {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		spec.Parents = append(spec.Parents, LocaleEdge{Locale: parent.Locale, Parent: parent.Parent})
	}
	for index, value := range d.Capabilities {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		capability, ok := parseCapability(value)
		if !ok {
			return CatalogSpec{}, fmt.Errorf("%w: capabilities[%d] has unknown value %q", ErrInvalidSource, index, boundedProblemText(value))
		}
		spec.Capabilities = append(spec.Capabilities, capability)
	}
	for moduleIndex, encodedModule := range d.Modules {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		module := Module{Name: encodedModule.Name, Messages: make([]MessageSpec, 0, len(encodedModule.Messages))}
		for messageIndex, encodedMessage := range encodedModule.Messages {
			if err := ctx.Err(); err != nil {
				return CatalogSpec{}, err
			}
			output, ok := parseOutputKind(encodedMessage.Output)
			if !ok {
				return CatalogSpec{}, fmt.Errorf("%w: modules[%d].messages[%d].output has unknown value %q", ErrInvalidSource, moduleIndex, messageIndex, boundedProblemText(encodedMessage.Output))
			}
			override, ok := parseOverridePolicy(encodedMessage.Override)
			if !ok {
				return CatalogSpec{}, fmt.Errorf("%w: modules[%d].messages[%d].override has unknown value %q", ErrInvalidSource, moduleIndex, messageIndex, boundedProblemText(encodedMessage.Override))
			}
			message := MessageSpec{
				ID: encodedMessage.ID, Key: Qualify(encodedModule.Name, encodedMessage.ID),
				Revision: encodedMessage.Revision, Source: encodedMessage.Source,
				Description: encodedMessage.Description, Output: output,
				Markup: slices.Clone(encodedMessage.Markup), Override: override,
				AllowEmpty: encodedMessage.AllowEmpty, Public: encodedMessage.Public,
				Arguments:    make([]ArgumentSpec, 0, len(encodedMessage.Arguments)),
				Translations: make([]Translation, 0, len(encodedMessage.Translations)),
			}
			for argumentIndex, encodedArgument := range encodedMessage.Arguments {
				if err := ctx.Err(); err != nil {
					return CatalogSpec{}, err
				}
				argumentType, ok := parseArgumentType(encodedArgument.Type)
				if !ok {
					return CatalogSpec{}, fmt.Errorf("%w: modules[%d].messages[%d].arguments[%d].type has unknown value %q", ErrInvalidSource, moduleIndex, messageIndex, argumentIndex, boundedProblemText(encodedArgument.Type))
				}
				message.Arguments = append(message.Arguments, ArgumentSpec{
					Name: encodedArgument.Name, Type: argumentType, Required: encodedArgument.Required,
					Nullable: encodedArgument.Nullable, Values: slices.Clone(encodedArgument.Values),
				})
			}
			for translationIndex, encodedTranslation := range encodedMessage.Translations {
				if err := ctx.Err(); err != nil {
					return CatalogSpec{}, err
				}
				review, ok := parseSourceReview(encodedTranslation.Review)
				if !ok {
					return CatalogSpec{}, fmt.Errorf("%w: modules[%d].messages[%d].translations[%d].review has unknown value %q", ErrInvalidSource, moduleIndex, messageIndex, translationIndex, boundedProblemText(encodedTranslation.Review))
				}
				message.Translations = append(message.Translations, Translation{
					Locale: encodedTranslation.Locale, Text: encodedTranslation.Text, Review: review,
					ContractRevision: encodedTranslation.ContractRevision, SourceDigest: encodedTranslation.SourceDigest,
					ReviewDigest: encodedTranslation.ReviewDigest,
				})
			}
			module.Messages = append(module.Messages, message)
		}
		spec.Modules = append(spec.Modules, module)
	}
	for index, encodedOverride := range d.Overrides {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		layer := encodedOverride.Layer
		if layer == "" {
			layer = LayerApplication.String()
		}
		parsedLayer, ok := parseLayer(layer)
		if !ok || parsedLayer != LayerApplication {
			return CatalogSpec{}, fmt.Errorf("%w: overrides[%d].layer must be %q", ErrInvalidSource, index, LayerApplication.String())
		}
		review, ok := parseSourceReview(encodedOverride.Review)
		if !ok {
			return CatalogSpec{}, fmt.Errorf("%w: overrides[%d].review has unknown value %q", ErrInvalidSource, index, boundedProblemText(encodedOverride.Review))
		}
		spec.Overrides = append(spec.Overrides, Override{
			Key: Key(encodedOverride.Key), Locale: encodedOverride.Locale, Text: encodedOverride.Text,
			Review: review, ContractRevision: encodedOverride.ContractRevision, SourceDigest: encodedOverride.SourceDigest,
			ReviewDigest: encodedOverride.ReviewDigest,
		})
	}
	return spec, nil
}

func sourceDocumentFromSpec(spec CatalogSpec) sourceDocument {
	document, _ := sourceDocumentFromSpecContext(context.Background(), spec)
	return document
}

func sourceDocumentFromSpecContext(ctx context.Context, spec CatalogSpec) (sourceDocument, error) {
	if err := ctx.Err(); err != nil {
		return sourceDocument{}, err
	}
	document := sourceDocument{
		Source: SourceVersion, Revision: spec.Revision, Profile: spec.Profile,
		SourceLocale: spec.SourceLocale, DefaultLocale: spec.DefaultLocale,
		Supported: nonNilSourceStrings(spec.Supported), Required: nonNilSourceStrings(spec.Required),
		MatchMode: spec.MatchMode.String(), DefaultOnMiss: spec.DefaultOnMiss,
		DefaultTimeZone: spec.DefaultTimeZone, TimeZoneDataVersion: spec.TimeZoneDataVersion,
		Limits: encodeArtifactLimits(spec.Limits), Parents: make([]sourceParent, 0, len(spec.Parents)),
		Capabilities: make([]string, 0, len(spec.Capabilities)), Modules: make([]sourceModule, 0, len(spec.Modules)),
		Overrides: make([]sourceOverride, 0, len(spec.Overrides)),
	}
	for _, parent := range spec.Parents {
		if err := ctx.Err(); err != nil {
			return sourceDocument{}, err
		}
		document.Parents = append(document.Parents, sourceParent{Locale: parent.Locale, Parent: parent.Parent})
	}
	for _, capability := range spec.Capabilities {
		if err := ctx.Err(); err != nil {
			return sourceDocument{}, err
		}
		document.Capabilities = append(document.Capabilities, capability.String())
	}
	for _, module := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return sourceDocument{}, err
		}
		encodedModule := sourceModule{Name: module.Name, Messages: make([]sourceMessage, 0, len(module.Messages))}
		for _, message := range module.Messages {
			if err := ctx.Err(); err != nil {
				return sourceDocument{}, err
			}
			encodedMessage := sourceMessage{
				ID: message.ID, Revision: message.Revision, Source: message.Source,
				Description: message.Description, Output: message.Output.String(),
				Markup: nonNilSourceStrings(message.Markup), Override: message.Override.String(),
				AllowEmpty: message.AllowEmpty, Public: message.Public,
				Arguments:    make([]sourceArgument, 0, len(message.Arguments)),
				Translations: make([]sourceTranslation, 0, len(message.Translations)),
			}
			for _, argument := range message.Arguments {
				if err := ctx.Err(); err != nil {
					return sourceDocument{}, err
				}
				encodedMessage.Arguments = append(encodedMessage.Arguments, sourceArgument{
					Name: argument.Name, Type: argument.Type.String(), Required: argument.Required,
					Nullable: argument.Nullable, Values: nonNilSourceStrings(argument.Values),
				})
			}
			for _, translation := range message.Translations {
				if err := ctx.Err(); err != nil {
					return sourceDocument{}, err
				}
				encodedMessage.Translations = append(encodedMessage.Translations, sourceTranslation{
					Locale: translation.Locale, Text: translation.Text, Review: translation.Review.String(),
					ContractRevision: translation.ContractRevision, SourceDigest: translation.SourceDigest,
					ReviewDigest: translation.ReviewDigest,
				})
			}
			encodedModule.Messages = append(encodedModule.Messages, encodedMessage)
		}
		document.Modules = append(document.Modules, encodedModule)
	}
	for _, override := range spec.Overrides {
		if err := ctx.Err(); err != nil {
			return sourceDocument{}, err
		}
		document.Overrides = append(document.Overrides, sourceOverride{
			Layer: LayerApplication.String(), Key: string(override.Key), Locale: override.Locale,
			Text: override.Text, Review: override.Review.String(), ContractRevision: override.ContractRevision,
			SourceDigest: override.SourceDigest, ReviewDigest: override.ReviewDigest,
		})
	}
	return document, ctx.Err()
}

func checkSourceArtifactFloor(spec CatalogSpec, limits Limits, artifactLimits ArtifactLimits) error {
	return checkSourceArtifactFloorContext(context.Background(), spec, limits, artifactLimits)
}

func checkSourceArtifactFloorContext(ctx context.Context, spec CatalogSpec, limits Limits, artifactLimits ArtifactLimits) error {
	return checkSourceArtifactFloorIntoContext(ctx, spec, limits, artifactLimits, nil)
}

func checkSourceArtifactFloorIntoContext(ctx context.Context, spec CatalogSpec, limits Limits, artifactLimits ArtifactLimits, result *artifactFloorCounter) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	profile := spec.Profile
	if profile == "" {
		profile = GrammarProfile
	}
	sourceLocale := canonicalArtifactLocale(spec.SourceLocale, limits.Locale.MaxTagBytes)
	defaultLocale := spec.DefaultLocale
	if defaultLocale == "" {
		defaultLocale = spec.SourceLocale
	}
	defaultLocale = canonicalArtifactLocale(defaultLocale, limits.Locale.MaxTagBytes)
	defaultTimeZone := spec.DefaultTimeZone
	if defaultTimeZone == "" {
		defaultTimeZone = "UTC"
	} else if canonical, err := canonicalTimeZone(defaultTimeZone); err == nil {
		defaultTimeZone = canonical
	} else {
		defaultTimeZone = ""
	}
	matchMode := ""
	if spec.MatchMode.Valid() {
		matchMode = spec.MatchMode.String()
	}
	document := sourceDocument{
		Source: SourceVersion, Revision: spec.Revision, Profile: profile,
		SourceLocale: sourceLocale, DefaultLocale: defaultLocale,
		Supported: []string{}, Required: []string{}, Parents: []sourceParent{},
		MatchMode: matchMode, DefaultOnMiss: spec.DefaultOnMiss,
		DefaultTimeZone: defaultTimeZone, TimeZoneDataVersion: spec.TimeZoneDataVersion,
		Capabilities: []string{}, Limits: encodeArtifactLimits(limits),
		Modules: []sourceModule{}, Overrides: []sourceOverride{},
	}
	counter := artifactFloorCounter{limits: artifactLimits, ctx: ctx}
	if !counter.addValue(document, 1) {
		if counter.err != nil {
			return counter.err
		}
		return fmt.Errorf("%w: declared catalog cannot fit within source artifact bounds", ErrLimitExceeded)
	}
	failed := func() error {
		if counter.err != nil {
			return counter.err
		}
		return fmt.Errorf("%w: declared catalog cannot fit within source artifact bounds", ErrLimitExceeded)
	}
	for index, localeName := range spec.Supported {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !counter.addArrayValue(canonicalArtifactLocale(localeName, limits.Locale.MaxTagBytes), index, 3) {
			return failed()
		}
	}
	for index, localeName := range spec.Required {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !counter.addArrayValue(canonicalArtifactLocale(localeName, limits.Locale.MaxTagBytes), index, 3) {
			return failed()
		}
	}
	for index, edge := range spec.Parents {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent := sourceParent{
			Locale: canonicalArtifactLocale(edge.Locale, limits.Locale.MaxTagBytes),
			Parent: canonicalArtifactLocale(edge.Parent, limits.Locale.MaxTagBytes),
		}
		if !counter.addArrayValue(parent, index, 3) {
			return failed()
		}
	}
	for index, capability := range spec.Capabilities {
		if err := ctx.Err(); err != nil {
			return err
		}
		value := ""
		if capability.Valid() {
			value = capability.String()
		}
		if !counter.addArrayValue(value, index, 3) {
			return failed()
		}
	}
	for moduleIndex, module := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return err
		}
		encodedModule := sourceModule{Name: module.Name, Messages: []sourceMessage{}}
		if !counter.addArrayValue(encodedModule, moduleIndex, 3) {
			return failed()
		}
		for messageIndex, message := range module.Messages {
			if err := ctx.Err(); err != nil {
				return err
			}
			output := ""
			if message.Output.Valid() {
				output = message.Output.String()
			}
			override := ""
			if message.Override.Valid() {
				override = message.Override.String()
			}
			encodedMessage := sourceMessage{
				ID: message.ID, Revision: message.Revision, Source: message.Source,
				Description: message.Description, Arguments: []sourceArgument{}, Output: output,
				Markup: []string{}, Override: override, Translations: []sourceTranslation{},
				AllowEmpty: message.AllowEmpty, Public: message.Public,
			}
			if !counter.addArrayValue(encodedMessage, messageIndex, 5) {
				return failed()
			}
			for argumentIndex, argument := range message.Arguments {
				argumentType := ""
				if argument.Type.Valid() {
					argumentType = argument.Type.String()
				}
				encodedArgument := sourceArgument{
					Name: argument.Name, Type: argumentType, Required: argument.Required,
					Nullable: argument.Nullable, Values: []string{},
				}
				if !counter.addArrayValue(encodedArgument, argumentIndex, 7) {
					return failed()
				}
				for valueIndex, value := range argument.Values {
					if !counter.addArrayValue(value, valueIndex, 9) {
						return failed()
					}
				}
			}
			for markupIndex, markup := range message.Markup {
				if !counter.addArrayValue(markup, markupIndex, 7) {
					return failed()
				}
			}
			for translationIndex, translation := range message.Translations {
				review := ""
				if translation.Review.Valid() {
					review = translation.Review.String()
				}
				encodedTranslation := sourceTranslation{
					Locale: canonicalArtifactLocale(translation.Locale, limits.Locale.MaxTagBytes),
					Text:   translation.Text, Review: review, ContractRevision: translation.ContractRevision,
					SourceDigest: translation.SourceDigest, ReviewDigest: translation.ReviewDigest,
				}
				if !counter.addArrayValue(encodedTranslation, translationIndex, 7) {
					return failed()
				}
			}
		}
	}
	for index, override := range spec.Overrides {
		if err := ctx.Err(); err != nil {
			return err
		}
		review := ""
		if override.Review.Valid() {
			review = override.Review.String()
		}
		encodedOverride := sourceOverride{
			Layer: LayerApplication.String(), Key: string(override.Key),
			Locale: canonicalArtifactLocale(override.Locale, limits.Locale.MaxTagBytes),
			Text:   override.Text, Review: review, ContractRevision: override.ContractRevision,
			SourceDigest: override.SourceDigest, ReviewDigest: override.ReviewDigest,
		}
		if !counter.addArrayValue(encodedOverride, index, 3) {
			return failed()
		}
	}
	if result != nil {
		*result = counter
	}
	return nil
}

func canonicalSourceSpec(ctx context.Context, spec CatalogSpec, ceiling Limits) (CatalogSpec, error) {
	if err := ctx.Err(); err != nil {
		return CatalogSpec{}, err
	}
	limits, err := checkedLimits(spec.Limits)
	if err != nil {
		return CatalogSpec{}, err
	}
	if err := requireCatalogLimitCeiling(limits, ceiling); err != nil {
		return CatalogSpec{}, err
	}
	if err := preflightCatalogContext(ctx, spec, limits); err != nil {
		return CatalogSpec{}, sourceSemanticError(err)
	}
	if err := ctx.Err(); err != nil {
		return CatalogSpec{}, err
	}
	canonical, err := cloneCatalogSpecContext(ctx, spec)
	if err != nil {
		return CatalogSpec{}, err
	}
	canonical.Observer = nil
	canonical.Limits = limits
	if canonical.Profile == "" {
		canonical.Profile = GrammarProfile
	}
	if canonical.Profile != GrammarProfile {
		return CatalogSpec{}, fmt.Errorf("%w: unsupported grammar profile %q", ErrInvalidSource, boundedProblemText(canonical.Profile))
	}
	if canonical.DefaultLocale == "" {
		canonical.DefaultLocale = canonical.SourceLocale
	}
	if canonical.DefaultTimeZone == "" {
		canonical.DefaultTimeZone = "UTC"
	}
	zone, err := canonicalTimeZone(canonical.DefaultTimeZone)
	if err != nil {
		return CatalogSpec{}, fmt.Errorf("%w: default_time_zone: %v", ErrInvalidSource, err)
	}
	canonical.DefaultTimeZone = zone
	problems := &problemSet{}
	if canonical.Revision == "" || !utf8.ValidString(canonical.Revision) {
		problems.add(ProblemInvalid, "revision", "revision is empty or not valid UTF-8")
	}
	if !utf8.ValidString(canonical.TimeZoneDataVersion) {
		problems.add(ProblemInvalid, "time_zone_data_version", "value is not valid UTF-8")
	}
	canonical.SourceLocale = canonicalSourceLocale(canonical.SourceLocale, limits.Locale.MaxTagBytes, "source_locale", problems)
	canonical.DefaultLocale = canonicalSourceLocale(canonical.DefaultLocale, limits.Locale.MaxTagBytes, "default_locale", problems)
	canonical.Supported = canonicalSourceLocaleList(canonical.Supported, limits.Locale.MaxTagBytes, "supported", problems, true)
	canonical.Required = canonicalSourceLocaleList(canonical.Required, limits.Locale.MaxTagBytes, "required", problems, false)
	supported := make(map[string]bool, len(canonical.Supported))
	for _, locale := range canonical.Supported {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		supported[locale] = true
	}
	for index, locale := range canonical.Required {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		if !supported[locale] {
			problems.add(ProblemInvalid, fmt.Sprintf("required[%d]", index), "locale is not supported")
		}
	}
	parentChildren := make(map[string]string, len(canonical.Parents))
	for index := range canonical.Parents {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		path := fmt.Sprintf("parents[%d]", index)
		canonical.Parents[index].Locale = canonicalSourceLocale(canonical.Parents[index].Locale, limits.Locale.MaxTagBytes, path+".locale", problems)
		canonical.Parents[index].Parent = canonicalSourceLocale(canonical.Parents[index].Parent, limits.Locale.MaxTagBytes, path+".parent", problems)
		child := canonical.Parents[index].Locale
		if previous, exists := parentChildren[child]; exists {
			code := ProblemDuplicate
			if previous != canonical.Parents[index].Parent {
				code = ProblemCollision
			}
			problems.add(code, path+".locale", child)
		}
		parentChildren[child] = canonical.Parents[index].Parent
	}
	capabilities := make(map[Capability]bool, len(canonical.Capabilities))
	for index, capability := range canonical.Capabilities {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		if !capability.Valid() {
			problems.add(ProblemInvalid, fmt.Sprintf("capabilities[%d]", index), capability.String())
		} else if capabilities[capability] {
			problems.add(ProblemDuplicate, fmt.Sprintf("capabilities[%d]", index), capability.String())
		}
		capabilities[capability] = true
	}
	if !canonical.MatchMode.Valid() {
		problems.add(ProblemInvalid, "match_mode", canonical.MatchMode.String())
	}
	if _, err := NewResolver(LocalePolicy{
		Supported: canonical.Supported, Default: canonical.DefaultLocale, Parents: canonical.Parents,
		Mode: canonical.MatchMode, DefaultOnMiss: canonical.DefaultOnMiss, Limits: limits.Locale,
	}); err != nil {
		problems.addError(ProblemInvalid, "locale_policy", err)
	}
	allowedLocales := make(map[string]bool, len(canonical.Supported)+len(canonical.Parents)*2+2)
	for _, locale := range canonical.Supported {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		allowedLocales[locale] = true
	}
	allowedLocales[canonical.SourceLocale] = true
	allowedLocales[canonical.DefaultLocale] = true
	for _, edge := range canonical.Parents {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		allowedLocales[edge.Locale] = true
		allowedLocales[edge.Parent] = true
	}
	moduleNames := make(map[string]bool, len(canonical.Modules))
	messageKeys := make(map[Key]bool)
	for moduleIndex := range canonical.Modules {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		module := &canonical.Modules[moduleIndex]
		modulePath := fmt.Sprintf("modules[%d]", moduleIndex)
		if !validModuleName(module.Name, limits.MaxIdentifierBytes) {
			problems.add(ProblemInvalid, modulePath+".name", module.Name)
		} else if moduleNames[module.Name] {
			problems.add(ProblemDuplicate, modulePath+".name", module.Name)
		}
		moduleNames[module.Name] = true
		for messageIndex := range module.Messages {
			if err := ctx.Err(); err != nil {
				return CatalogSpec{}, err
			}
			message := &module.Messages[messageIndex]
			messagePath := fmt.Sprintf("%s.messages[%d]", modulePath, messageIndex)
			derived := Qualify(module.Name, message.ID)
			if !validMessageID(message.ID, limits.MaxIdentifierBytes) || !validKey(derived, limits.MaxIdentifierBytes) {
				problems.add(ProblemInvalid, messagePath+".id", message.ID)
			}
			if message.Key != "" && message.Key != derived {
				problems.add(ProblemInvalid, messagePath+".key", string(message.Key))
			}
			message.Key = derived
			if messageKeys[derived] {
				problems.add(ProblemDuplicate, messagePath+".key", string(derived))
			}
			messageKeys[derived] = true
			if !message.Output.Valid() {
				problems.add(ProblemInvalid, messagePath+".output", message.Output.String())
			}
			if !message.Override.Valid() {
				problems.add(ProblemInvalid, messagePath+".override", message.Override.String())
			}
			if !utf8.ValidString(message.Revision) || !utf8.ValidString(message.Source) || !utf8.ValidString(message.Description) {
				problems.add(ProblemInvalid, messagePath, "message strings are not valid UTF-8")
			}
			validatedArguments := validateArgumentSpecs(message.Arguments, capabilities, limits, messagePath+".arguments", problems)
			message.Arguments = validatedArguments
			message.Markup = validateMarkupAllowlist(message.Markup, message.Output, limits.MaxIdentifierBytes, limits.MaxMarkupNames, messagePath+".markup", problems)
			translationLocales := make(map[string]string, len(message.Translations))
			for translationIndex := range message.Translations {
				if err := ctx.Err(); err != nil {
					return CatalogSpec{}, err
				}
				translation := &message.Translations[translationIndex]
				translationPath := fmt.Sprintf("%s.translations[%d]", messagePath, translationIndex)
				rawLocale := translation.Locale
				translation.Locale = canonicalSourceLocale(rawLocale, limits.Locale.MaxTagBytes, translationPath+".locale", problems)
				if previous, exists := translationLocales[translation.Locale]; exists {
					code := ProblemDuplicate
					if previous != rawLocale {
						code = ProblemCollision
					}
					problems.add(code, translationPath+".locale", translation.Locale)
				}
				translationLocales[translation.Locale] = rawLocale
				if translation.Locale == canonical.SourceLocale {
					problems.add(ProblemDuplicate, translationPath+".locale", "source locale is supplied by source")
				}
				if !allowedLocales[translation.Locale] {
					problems.add(ProblemInvalid, translationPath+".locale", "locale is outside the declared graph")
				}
				if !translation.Review.Valid() {
					problems.add(ProblemInvalid, translationPath+".review", translation.Review.String())
				}
				if !utf8.ValidString(translation.Text) || !utf8.ValidString(translation.ContractRevision) || !utf8.ValidString(translation.SourceDigest) || !utf8.ValidString(translation.ReviewDigest) {
					problems.add(ProblemInvalid, translationPath, "translation strings are not valid UTF-8")
				}
			}
		}
	}
	overrideIdentities := make(map[string]bool, len(canonical.Overrides))
	for index := range canonical.Overrides {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		override := &canonical.Overrides[index]
		path := fmt.Sprintf("overrides[%d]", index)
		if !validKey(override.Key, limits.MaxIdentifierBytes) {
			problems.add(ProblemInvalid, path+".key", string(override.Key))
		} else if !messageKeys[override.Key] {
			problems.add(ProblemMissing, path+".key", string(override.Key))
		}
		override.Locale = canonicalSourceLocale(override.Locale, limits.Locale.MaxTagBytes, path+".locale", problems)
		identity := string(override.Key) + "\x00" + override.Locale
		if overrideIdentities[identity] {
			problems.add(ProblemDuplicate, path, identity)
		}
		overrideIdentities[identity] = true
		if !allowedLocales[override.Locale] {
			problems.add(ProblemInvalid, path+".locale", "locale is outside the declared graph")
		}
		if !override.Review.Valid() {
			problems.add(ProblemInvalid, path+".review", override.Review.String())
		}
		if !utf8.ValidString(override.Text) || !utf8.ValidString(override.ContractRevision) || !utf8.ValidString(override.SourceDigest) || !utf8.ValidString(override.ReviewDigest) {
			problems.add(ProblemInvalid, path, "override strings are not valid UTF-8")
		}
	}
	if err := problems.err(); err != nil {
		return CatalogSpec{}, sourceSemanticError(err)
	}
	if err := ctx.Err(); err != nil {
		return CatalogSpec{}, err
	}
	if err := preflightCatalogContext(ctx, canonical, limits); err != nil {
		return CatalogSpec{}, sourceSemanticError(err)
	}
	slices.Sort(canonical.Required)
	slices.SortFunc(canonical.Parents, func(a, b LocaleEdge) int {
		if value := strings.Compare(a.Locale, b.Locale); value != 0 {
			return value
		}
		return strings.Compare(a.Parent, b.Parent)
	})
	slices.Sort(canonical.Capabilities)
	slices.SortFunc(canonical.Modules, func(a, b Module) int { return strings.Compare(a.Name, b.Name) })
	for moduleIndex := range canonical.Modules {
		slices.SortFunc(canonical.Modules[moduleIndex].Messages, func(a, b MessageSpec) int {
			return strings.Compare(a.ID, b.ID)
		})
		for messageIndex := range canonical.Modules[moduleIndex].Messages {
			message := &canonical.Modules[moduleIndex].Messages[messageIndex]
			slices.SortFunc(message.Translations, func(a, b Translation) int {
				return strings.Compare(a.Locale, b.Locale)
			})
		}
	}
	slices.SortFunc(canonical.Overrides, func(a, b Override) int {
		if value := strings.Compare(string(a.Key), string(b.Key)); value != 0 {
			return value
		}
		return strings.Compare(a.Locale, b.Locale)
	})
	if err := ctx.Err(); err != nil {
		return CatalogSpec{}, err
	}
	return canonical, nil
}

func cloneCatalogSpec(spec CatalogSpec) CatalogSpec {
	clone, _ := cloneCatalogSpecContext(context.Background(), spec)
	return clone
}

func cloneCatalogSpecContext(ctx context.Context, spec CatalogSpec) (CatalogSpec, error) {
	if err := ctx.Err(); err != nil {
		return CatalogSpec{}, err
	}
	clone := spec
	var err error
	clone.Supported, err = cloneSliceContext(ctx, spec.Supported)
	if err != nil {
		return CatalogSpec{}, err
	}
	clone.Required, err = cloneSliceContext(ctx, spec.Required)
	if err != nil {
		return CatalogSpec{}, err
	}
	clone.Parents, err = cloneSliceContext(ctx, spec.Parents)
	if err != nil {
		return CatalogSpec{}, err
	}
	clone.Capabilities, err = cloneSliceContext(ctx, spec.Capabilities)
	if err != nil {
		return CatalogSpec{}, err
	}
	clone.Overrides, err = cloneSliceContext(ctx, spec.Overrides)
	if err != nil {
		return CatalogSpec{}, err
	}
	clone.Modules = make([]Module, len(spec.Modules))
	for moduleIndex, module := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return CatalogSpec{}, err
		}
		clone.Modules[moduleIndex] = Module{Name: module.Name, Messages: make([]MessageSpec, len(module.Messages))}
		for messageIndex, message := range module.Messages {
			if err := ctx.Err(); err != nil {
				return CatalogSpec{}, err
			}
			clonedMessage := message
			clonedMessage.Arguments = make([]ArgumentSpec, len(message.Arguments))
			for argumentIndex, argument := range message.Arguments {
				if err := ctx.Err(); err != nil {
					return CatalogSpec{}, err
				}
				clonedMessage.Arguments[argumentIndex] = argument
				clonedMessage.Arguments[argumentIndex].Values, err = cloneSliceContext(ctx, argument.Values)
				if err != nil {
					return CatalogSpec{}, err
				}
			}
			clonedMessage.Markup, err = cloneSliceContext(ctx, message.Markup)
			if err != nil {
				return CatalogSpec{}, err
			}
			clonedMessage.Translations, err = cloneSliceContext(ctx, message.Translations)
			if err != nil {
				return CatalogSpec{}, err
			}
			clone.Modules[moduleIndex].Messages[messageIndex] = clonedMessage
		}
	}
	return clone, ctx.Err()
}

func cloneSliceContext[T any](ctx context.Context, values []T) ([]T, error) {
	if values == nil {
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	clone := make([]T, len(values))
	for index, value := range values {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		clone[index] = value
	}
	return clone, ctx.Err()
}

func canonicalSourceLocale(raw string, maximum int, path string, problems *problemSet) string {
	_, canonical, err := canonicalLocale(raw, maximum)
	if err != nil {
		problems.addError(ProblemInvalid, path, err)
		return ""
	}
	return canonical
}

func canonicalSourceLocaleList(raw []string, maximum int, path string, problems *problemSet, preserveOrder bool) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]string, len(raw))
	for index, value := range raw {
		canonical := canonicalSourceLocale(value, maximum, fmt.Sprintf("%s[%d]", path, index), problems)
		if previous, exists := seen[canonical]; exists {
			code := ProblemDuplicate
			if previous != value {
				code = ProblemCollision
			}
			problems.add(code, fmt.Sprintf("%s[%d]", path, index), canonical)
			continue
		}
		seen[canonical] = value
		out = append(out, canonical)
	}
	if !preserveOrder {
		slices.Sort(out)
	}
	return out
}

func parseSourceReview(value string) (ReviewState, bool) {
	switch value {
	case "", ReviewUnset.String():
		return ReviewUnset, true
	case ReviewApproved.String():
		return ReviewApproved, true
	case ReviewRequired.String():
		return ReviewRequired, true
	case ReviewRejected.String():
		return ReviewRejected, true
	default:
		return 0, false
	}
}

func nonNilSourceStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return slices.Clone(values)
}

func sourceSemanticError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, ErrLimitExceeded) {
		return err
	}
	var problems *Problems
	if errors.As(err, &problems) {
		for _, problem := range problems.Items() {
			if problem.Code == ProblemLimit {
				return fmt.Errorf("%w: %v", ErrLimitExceeded, err)
			}
		}
	}
	return fmt.Errorf("%w: %v", ErrInvalidSource, err)
}

type sourceContextReader struct {
	ctx    context.Context
	source *bytes.Reader
}

func (r *sourceContextReader) Read(destination []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(destination) > 32<<10 {
		destination = destination[:32<<10]
	}
	return r.source.Read(destination)
}

const sourceStreamChunkBytes = 4 << 10

type sourceStreamReader struct {
	ctx        context.Context
	source     io.Reader
	maxBytes   int
	raw        []byte
	pending    [sourceStreamChunkBytes]byte
	pendingAt  int
	pendingEnd int
	pendingErr error
	totalRead  int
	emptyReads int
	terminal   error
	guard      sourceLexicalGuard
}

func newSourceStreamReader(ctx context.Context, source io.Reader, artifactLimits ArtifactLimits, ceiling Limits) *sourceStreamReader {
	capacity := min(artifactLimits.MaxBytes, sourceStreamChunkBytes)
	return &sourceStreamReader{
		ctx: ctx, source: source, maxBytes: artifactLimits.MaxBytes,
		raw: make([]byte, 0, capacity),
		guard: sourceLexicalGuard{
			maxDepth:       artifactLimits.MaxDepth,
			maxStringBytes: max(ceiling.MaxCatalogBytes, len(SourceVersion), 64),
		},
	}
}

func (r *sourceStreamReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.terminal != nil {
		return 0, r.terminal
	}
	for r.pendingAt == r.pendingEnd {
		if r.pendingErr != nil {
			r.terminal = r.pendingErr
			r.pendingErr = nil
			return 0, r.terminal
		}
		if err := r.fill(); err != nil {
			return 0, err
		}
	}
	maximum := min(len(destination), r.pendingEnd-r.pendingAt)
	consumed := 0
	var terminal error
	for consumed < maximum {
		boundary, err := r.guard.consume(r.pending[r.pendingAt+consumed])
		consumed++
		if err != nil {
			terminal = err
			break
		}
		if boundary {
			break
		}
	}
	copy(destination[:consumed], r.pending[r.pendingAt:r.pendingAt+consumed])
	r.raw = append(r.raw, r.pending[r.pendingAt:r.pendingAt+consumed]...)
	r.pendingAt += consumed
	if terminal != nil {
		r.pendingAt = r.pendingEnd
		r.pendingErr = nil
		r.terminal = terminal
		return consumed, terminal
	}
	if r.pendingAt == r.pendingEnd && r.pendingErr != nil {
		err := r.pendingErr
		r.pendingErr = nil
		if !errors.Is(err, io.EOF) {
			r.terminal = err
		}
		return consumed, err
	}
	return consumed, nil
}

func (r *sourceStreamReader) fill() error {
	remaining := r.maxBytes + 1 - r.totalRead
	if remaining <= 0 {
		r.terminal = fmt.Errorf("%w: artifact bytes exceed %d", ErrLimitExceeded, r.maxBytes)
		return r.terminal
	}
	readSize := min(len(r.pending), remaining)
	n, err := r.source.Read(r.pending[:readSize])
	if n < 0 || n > readSize {
		r.terminal = fmt.Errorf("%w: reader returned an invalid byte count", ErrSourceIO)
		return r.terminal
	}
	if contextErr := r.ctx.Err(); contextErr != nil {
		r.terminal = contextErr
		return contextErr
	}
	if n == 0 {
		if err == nil {
			r.emptyReads++
			if r.emptyReads >= 100 {
				r.terminal = fmt.Errorf("%w: %w", ErrSourceIO, io.ErrNoProgress)
				return r.terminal
			}
			return nil
		}
		r.pendingErr = sourceReaderError(err)
		return nil
	}
	r.emptyReads = 0
	previous := r.totalRead
	r.totalRead += n
	accepted := n
	if r.totalRead > r.maxBytes {
		accepted = r.maxBytes - previous
		r.pendingErr = fmt.Errorf("%w: artifact bytes exceed %d", ErrLimitExceeded, r.maxBytes)
	} else if err != nil {
		r.pendingErr = sourceReaderError(err)
	}
	r.pendingAt = 0
	r.pendingEnd = accepted
	if accepted == 0 && r.pendingErr != nil {
		r.terminal = r.pendingErr
		r.pendingErr = nil
		return r.terminal
	}
	return nil
}

func sourceReaderError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrLimitExceeded) || errors.Is(err, ErrInvalidSource) || errors.Is(err, ErrSourceIO) {
		return err
	}
	return fmt.Errorf("%w: reading bytes: %w", ErrSourceIO, err)
}

type sourceLexicalFrame struct {
	kind      byte
	expectKey bool
}

type sourceLexicalGuard struct {
	maxDepth       int
	maxStringBytes int
	frames         []sourceLexicalFrame
	inString       bool
	stringIsKey    bool
	escaped        bool
	unicodeDigits  int
	stringBytes    int
	afterKey       bool
}

func (g *sourceLexicalGuard) consume(character byte) (bool, error) {
	if g.inString {
		return false, g.consumeString(character)
	}
	switch character {
	case '{':
		if len(g.frames) >= g.maxDepth {
			return false, fmt.Errorf("%w: JSON depth exceeds %d", ErrLimitExceeded, g.maxDepth)
		}
		g.frames = append(g.frames, sourceLexicalFrame{kind: character, expectKey: true})
	case '[':
		if len(g.frames) >= g.maxDepth {
			return false, fmt.Errorf("%w: JSON depth exceeds %d", ErrLimitExceeded, g.maxDepth)
		}
		g.frames = append(g.frames, sourceLexicalFrame{kind: character})
	case '}', ']':
		if len(g.frames) > 0 {
			g.frames = g.frames[:len(g.frames)-1]
		}
	case ',':
		if len(g.frames) > 0 && g.frames[len(g.frames)-1].kind == '{' {
			g.frames[len(g.frames)-1].expectKey = true
		}
	case ':':
		if len(g.frames) > 0 && g.frames[len(g.frames)-1].kind == '{' && g.afterKey {
			g.frames[len(g.frames)-1].expectKey = false
			g.afterKey = false
			return true, nil
		}
	case '"':
		g.inString = true
		g.stringBytes = 0
		g.stringIsKey = len(g.frames) > 0 && g.frames[len(g.frames)-1].kind == '{' && g.frames[len(g.frames)-1].expectKey
	}
	return false, nil
}

func (g *sourceLexicalGuard) consumeString(character byte) error {
	if g.unicodeDigits > 0 {
		g.unicodeDigits--
		if g.unicodeDigits == 0 {
			return g.addStringBytes(1)
		}
		return nil
	}
	if g.escaped {
		g.escaped = false
		if character == 'u' {
			g.unicodeDigits = 4
			return nil
		}
		return g.addStringBytes(1)
	}
	switch character {
	case '\\':
		g.escaped = true
		return nil
	case '"':
		g.inString = false
		g.afterKey = g.stringIsKey
		return nil
	default:
		return g.addStringBytes(1)
	}
}

func (g *sourceLexicalGuard) addStringBytes(count int) error {
	g.stringBytes += count
	if g.stringIsKey {
		if g.stringBytes > 64 {
			return fmt.Errorf("%w: unknown object member exceeds 64 bytes", ErrInvalidSource)
		}
		return nil
	}
	if g.stringBytes > g.maxStringBytes {
		return fmt.Errorf("%w: source string exceeds %d bytes", ErrLimitExceeded, g.maxStringBytes)
	}
	return nil
}

type sourceScanState struct {
	members      int
	items        int
	bytes        int
	messages     int
	translations int
	module       *sourceModuleScan
}

type sourceModuleScan struct {
	name                  string
	nameKnown             bool
	pendingMessageIDBytes []int
}

func readAndScanSourceJSON(ctx context.Context, source io.Reader, limits ArtifactLimits, ceiling Limits) ([]byte, error) {
	stream := newSourceStreamReader(ctx, source, limits, ceiling)
	decoder := json.NewDecoder(stream)
	decoder.UseNumber()
	state := &sourceScanState{}
	if err := scanSourceValue(ctx, decoder, reflect.TypeFor[sourceDocument](), "", 1, limits, ceiling, state); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrLimitExceeded) || errors.Is(err, ErrInvalidSource) || errors.Is(err, ErrSourceIO) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrLimitExceeded) || errors.Is(err, ErrSourceIO) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: trailing data: %v", ErrInvalidSource, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !utf8.Valid(stream.raw) {
		return nil, fmt.Errorf("%w: input is not valid UTF-8", ErrInvalidSource)
	}
	if err := validateSourceJSONUnicode(stream.raw); err != nil {
		return nil, err
	}
	return stream.raw, nil
}

func scanSourceValue(ctx context.Context, decoder *json.Decoder, expected reflect.Type, path string, depth int, limits ArtifactLimits, ceiling Limits, state *sourceScanState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > limits.MaxDepth {
		return fmt.Errorf("%w: JSON depth exceeds %d", ErrLimitExceeded, limits.MaxDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		if expected.Kind() == reflect.Slice {
			return nil
		}
		return fmt.Errorf("expected %s, found null", expected.Kind())
	}
	switch expected.Kind() {
	case reflect.Struct:
		opening, ok := token.(json.Delim)
		if !ok || opening != '{' {
			return fmt.Errorf("expected object for %s", expected)
		}
		seen := make(map[string]bool, expected.NumField())
		for decoder.More() {
			if err := ctx.Err(); err != nil {
				return err
			}
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("object member name is not a string")
			}
			if seen[name] {
				return fmt.Errorf("duplicate object member %q", boundedProblemText(name))
			}
			seen[name] = true
			fieldType, ok := sourceJSONField(expected, name)
			if !ok {
				return fmt.Errorf("unknown object member %q", boundedProblemText(name))
			}
			if err := addSourceJSONMember(&state.members, limits); err != nil {
				return err
			}
			fieldPath := name
			if path != "" {
				fieldPath = path + "." + name
			}
			if err := scanSourceValue(ctx, decoder, fieldType, fieldPath, depth+1, limits, ceiling, state); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("object is not closed")
		}
		return nil
	case reflect.Slice:
		opening, ok := token.(json.Delim)
		if !ok || opening != '[' {
			return fmt.Errorf("expected array for %s", expected)
		}
		count := 0
		maximum := sourceSliceLimit(path, expected.Elem(), ceiling)
		for decoder.More() {
			count++
			if count > maximum {
				return fmt.Errorf("%w: %s exceeds %d entries", ErrLimitExceeded, boundedProblemText(path), maximum)
			}
			if err := addSourceJSONMember(&state.members, limits); err != nil {
				return err
			}
			state.items++
			if state.items > ceiling.MaxCatalogItems {
				return fmt.Errorf("%w: source items exceed %d", ErrLimitExceeded, ceiling.MaxCatalogItems)
			}
			if err := addSourceSemanticCount(expected.Elem(), ceiling, state); err != nil {
				return err
			}
			if expected.Elem() == reflect.TypeFor[sourceModule]() {
				previous := state.module
				state.module = &sourceModuleScan{}
				err := scanSourceValue(ctx, decoder, expected.Elem(), path+"[]", depth+1, limits, ceiling, state)
				module := state.module
				state.module = previous
				if err != nil {
					return err
				}
				for _, idBytes := range module.pendingMessageIDBytes {
					if err := addSourceSemanticBytes(state, ceiling, 1+idBytes); err != nil {
						return err
					}
				}
				continue
			}
			if err := scanSourceValue(ctx, decoder, expected.Elem(), path+"[]", depth+1, limits, ceiling, state); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("array is not closed")
		}
		return nil
	case reflect.String:
		value, ok := token.(string)
		if !ok {
			return errors.New("expected string")
		}
		if maximum := sourceStringLimit(path, ceiling); len(value) > maximum {
			return fmt.Errorf("%w: %s exceeds %d bytes", ErrLimitExceeded, boundedProblemText(path), maximum)
		}
		if sourceSemanticString(path) {
			if err := addSourceSemanticBytes(state, ceiling, len(value)); err != nil {
				return err
			}
		}
		if state.module != nil {
			switch path {
			case "modules[].name":
				state.module.name = value
				state.module.nameKnown = true
				for _, idBytes := range state.module.pendingMessageIDBytes {
					if err := addSourceSemanticBytes(state, ceiling, len(value)+1+idBytes); err != nil {
						return err
					}
				}
				state.module.pendingMessageIDBytes = nil
			case "modules[].messages[].id":
				if state.module.nameKnown {
					if err := addSourceSemanticBytes(state, ceiling, len(state.module.name)+1+len(value)); err != nil {
						return err
					}
				} else {
					state.module.pendingMessageIDBytes = append(state.module.pendingMessageIDBytes, len(value))
				}
			}
		}
		return nil
	case reflect.Bool:
		if _, ok := token.(bool); !ok {
			return errors.New("expected boolean")
		}
		return nil
	case reflect.Int:
		number, ok := token.(json.Number)
		if !ok {
			return errors.New("expected integer")
		}
		if _, err := strconv.ParseInt(number.String(), 10, 64); err != nil {
			return errors.New("expected integer")
		}
		return nil
	default:
		return fmt.Errorf("unsupported source JSON representation %s", expected.Kind())
	}
}

func addSourceSemanticBytes(state *sourceScanState, limits Limits, count int) error {
	if count > limits.MaxCatalogBytes-state.bytes {
		return fmt.Errorf("%w: source material exceeds %d bytes", ErrLimitExceeded, limits.MaxCatalogBytes)
	}
	state.bytes += count
	return nil
}

func sourceSliceLimit(path string, element reflect.Type, limits Limits) int {
	switch element {
	case reflect.TypeFor[sourceModule]():
		return limits.MaxModules
	case reflect.TypeFor[sourceMessage]():
		return limits.MaxMessages
	case reflect.TypeFor[sourceTranslation](), reflect.TypeFor[sourceOverride]():
		return limits.MaxTranslations
	case reflect.TypeFor[sourceArgument]():
		return limits.MaxArguments
	case reflect.TypeFor[sourceParent]():
		return limits.MaxLocales
	}
	switch {
	case path == "supported" || path == "required":
		return limits.MaxLocales
	case path == "capabilities":
		return 2
	case strings.HasSuffix(path, ".values"):
		return limits.MaxEnumValues
	case strings.HasSuffix(path, ".markup"):
		return limits.MaxMarkupNames
	default:
		return limits.MaxCatalogItems
	}
}

func addSourceSemanticCount(element reflect.Type, limits Limits, state *sourceScanState) error {
	switch element {
	case reflect.TypeFor[sourceMessage]():
		state.messages++
		state.translations++
	case reflect.TypeFor[sourceTranslation](), reflect.TypeFor[sourceOverride]():
		state.translations++
	}
	if state.messages > limits.MaxMessages {
		return fmt.Errorf("%w: messages exceed %d", ErrLimitExceeded, limits.MaxMessages)
	}
	if state.translations > limits.MaxTranslations {
		return fmt.Errorf("%w: translations exceed %d", ErrLimitExceeded, limits.MaxTranslations)
	}
	return nil
}

func sourceStringLimit(path string, limits Limits) int {
	switch {
	case path == "source":
		return len(SourceVersion)
	case path == "revision", strings.HasSuffix(path, ".revision"), strings.HasSuffix(path, ".contract_revision"):
		return limits.MaxRevisionBytes
	case path == "profile":
		return limits.MaxIdentifierBytes
	case path == "source_locale", path == "default_locale", path == "supported[]", path == "required[]", strings.HasSuffix(path, ".locale"), strings.HasSuffix(path, ".parent"):
		return limits.Locale.MaxTagBytes
	case path == "default_time_zone":
		return 255
	case path == "time_zone_data_version":
		return limits.MaxRevisionBytes
	case strings.HasSuffix(path, ".source"), strings.HasSuffix(path, ".text"):
		return limits.MaxTemplateBytes
	case strings.HasSuffix(path, ".description"):
		return limits.MaxDescriptionBytes
	case strings.HasSuffix(path, ".source_digest"), strings.HasSuffix(path, ".review_digest"):
		return sha256.Size * 2
	case strings.HasSuffix(path, ".id"), strings.HasSuffix(path, ".key"), strings.HasSuffix(path, ".name"):
		return limits.MaxIdentifierBytes
	case strings.Contains(path, ".values[]"):
		return limits.MaxEnumValueBytes
	case strings.Contains(path, ".markup[]"):
		return limits.MaxIdentifierBytes
	case path == "match_mode", path == "capabilities[]", strings.HasSuffix(path, ".output"), strings.HasSuffix(path, ".override"), strings.HasSuffix(path, ".review"), strings.HasSuffix(path, ".layer"), strings.HasSuffix(path, ".type"):
		return 32
	default:
		return limits.MaxCatalogBytes
	}
}

func sourceSemanticString(path string) bool {
	switch path {
	case "revision", "profile", "source_locale", "default_locale", "default_time_zone", "time_zone_data_version", "supported[]", "required[]":
		return true
	}
	return strings.HasSuffix(path, ".locale") ||
		strings.HasSuffix(path, ".parent") ||
		strings.HasSuffix(path, ".name") ||
		strings.HasSuffix(path, ".id") ||
		strings.HasSuffix(path, ".revision") ||
		strings.HasSuffix(path, ".source") ||
		strings.HasSuffix(path, ".description") ||
		strings.Contains(path, ".values[]") ||
		strings.Contains(path, ".markup[]") ||
		strings.HasSuffix(path, ".key") ||
		strings.HasSuffix(path, ".text") ||
		strings.HasSuffix(path, ".contract_revision") ||
		strings.HasSuffix(path, ".source_digest") ||
		strings.HasSuffix(path, ".review_digest")
}

func validateSourceJSONUnicode(raw []byte) error {
	inside := false
	for index := 0; index < len(raw); index++ {
		switch raw[index] {
		case '"':
			inside = !inside
		case '\\':
			if !inside || index+1 >= len(raw) {
				continue
			}
			index++
			if raw[index] != 'u' || index+4 >= len(raw) {
				continue
			}
			value, ok := sourceHex16(raw[index+1 : index+5])
			if !ok {
				continue
			}
			index += 4
			if value >= 0xdc00 && value <= 0xdfff {
				return fmt.Errorf("%w: JSON contains an unpaired UTF-16 surrogate", ErrInvalidSource)
			}
			if value < 0xd800 || value > 0xdbff {
				continue
			}
			if index+6 >= len(raw) || raw[index+1] != '\\' || raw[index+2] != 'u' {
				return fmt.Errorf("%w: JSON contains an unpaired UTF-16 surrogate", ErrInvalidSource)
			}
			low, lowOK := sourceHex16(raw[index+3 : index+7])
			if !lowOK || low < 0xdc00 || low > 0xdfff {
				return fmt.Errorf("%w: JSON contains an unpaired UTF-16 surrogate", ErrInvalidSource)
			}
			index += 6
		}
	}
	return nil
}

func sourceHex16(raw []byte) (uint16, bool) {
	if len(raw) != 4 {
		return 0, false
	}
	var value uint16
	for _, character := range raw {
		value <<= 4
		switch {
		case character >= '0' && character <= '9':
			value |= uint16(character - '0')
		case character >= 'a' && character <= 'f':
			value |= uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			value |= uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func sourceJSONField(structType reflect.Type, name string) (reflect.Type, bool) {
	for index := 0; index < structType.NumField(); index++ {
		field := structType.Field(index)
		encodedName := field.Tag.Get("json")
		if comma := strings.IndexByte(encodedName, ','); comma >= 0 {
			encodedName = encodedName[:comma]
		}
		if encodedName == "" {
			encodedName = field.Name
		}
		if encodedName == name {
			return field.Type, true
		}
	}
	return nil, false
}

func addSourceJSONMember(members *int, limits ArtifactLimits) error {
	if *members >= limits.MaxMembers {
		return fmt.Errorf("%w: JSON members exceed %d", ErrLimitExceeded, limits.MaxMembers)
	}
	*members++
	return nil
}
