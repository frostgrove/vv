package main

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"flag"
	"fmt"
	"reflect"
	"slices"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/frostgrove/vv/i18n"
)

const (
	commandLimitsSchema       = "frostgrove.i18n.limits/v1"
	maximumCommandLimitsBytes = 64 << 10
	maximumCommandOutputBytes = 1 << 30
)

type commandLimits struct {
	Catalog  i18n.Limits
	Source   i18n.ArtifactLimits
	Compiled i18n.ArtifactLimits
	Output   commandOutputLimits
}

type commandLimitsDocument struct {
	Schema   string                       `json:"schema"`
	Catalog  *commandCatalogLimits        `json:"catalog,omitempty"`
	Source   *commandArtifactLimits       `json:"source,omitempty"`
	Compiled *commandArtifactLimits       `json:"compiled,omitempty"`
	Output   *commandOutputLimitsDocument `json:"output,omitempty"`
}

type commandOutputLimits struct {
	MaxBytes int `json:"max_bytes,omitempty"`
}

type commandOutputLimitsDocument struct {
	MaxBytes *int `json:"max_bytes,omitempty"`
}

type commandArtifactLimits struct {
	MaxBytes   *int `json:"max_bytes,omitempty"`
	MaxDepth   *int `json:"max_depth,omitempty"`
	MaxMembers *int `json:"max_members,omitempty"`
}

type commandLocaleLimits struct {
	MaxChoices       *int `json:"max_choices,omitempty"`
	MaxHeaderBytes   *int `json:"max_header_bytes,omitempty"`
	MaxRanges        *int `json:"max_ranges,omitempty"`
	MaxSupported     *int `json:"max_supported,omitempty"`
	MaxTagBytes      *int `json:"max_tag_bytes,omitempty"`
	MaxFallbackDepth *int `json:"max_fallback_depth,omitempty"`
}

type commandCatalogLimits struct {
	Locale              *commandLocaleLimits `json:"locale,omitempty"`
	MaxModules          *int                 `json:"max_modules,omitempty"`
	MaxMessages         *int                 `json:"max_messages,omitempty"`
	MaxTranslations     *int                 `json:"max_translations,omitempty"`
	MaxLocales          *int                 `json:"max_locales,omitempty"`
	MaxArguments        *int                 `json:"max_arguments,omitempty"`
	MaxEnumValues       *int                 `json:"max_enum_values,omitempty"`
	MaxMarkupNames      *int                 `json:"max_markup_names,omitempty"`
	MaxCatalogItems     *int                 `json:"max_catalog_items,omitempty"`
	MaxIdentifierBytes  *int                 `json:"max_identifier_bytes,omitempty"`
	MaxRevisionBytes    *int                 `json:"max_revision_bytes,omitempty"`
	MaxDescriptionBytes *int                 `json:"max_description_bytes,omitempty"`
	MaxEnumValueBytes   *int                 `json:"max_enum_value_bytes,omitempty"`
	MaxArgumentBytes    *int                 `json:"max_argument_bytes,omitempty"`
	MaxBigIntegerBits   *int                 `json:"max_big_integer_bits,omitempty"`
	MaxTemplateBytes    *int                 `json:"max_template_bytes,omitempty"`
	MaxCatalogBytes     *int                 `json:"max_catalog_bytes,omitempty"`
	MaxDeclarations     *int                 `json:"max_declarations,omitempty"`
	MaxSelectors        *int                 `json:"max_selectors,omitempty"`
	MaxVariants         *int                 `json:"max_variants,omitempty"`
	MaxLocalDepth       *int                 `json:"max_local_depth,omitempty"`
	MaxTemplateParts    *int                 `json:"max_template_parts,omitempty"`
	MaxMarkupDepth      *int                 `json:"max_markup_depth,omitempty"`
	MaxOutputBytes      *int                 `json:"max_output_bytes,omitempty"`
	MaxOutputParts      *int                 `json:"max_output_parts,omitempty"`
	MaxExplainSteps     *int                 `json:"max_explain_steps,omitempty"`
}

func bindCommandLimits(flags *flag.FlagSet) *string {
	return flags.String("limits", "", "optional frostgrove.i18n.limits/v1 operator policy")
}

func readCommandLimits(ctx context.Context, path string) (commandLimits, error) {
	if path == "" {
		return commandLimits{}, nil
	}
	raw, err := readRegularFile(ctx, path, maximumCommandLimitsBytes)
	if err != nil {
		return commandLimits{}, err
	}
	return decodeCommandLimits(ctx, raw)
}

func decodeCommandLimits(ctx context.Context, raw []byte) (commandLimits, error) {
	if ctx == nil {
		return commandLimits{}, errors.New("limits context is nil")
	}
	if err := ctx.Err(); err != nil {
		return commandLimits{}, err
	}
	if len(raw) == 0 || len(raw) > maximumCommandLimitsBytes {
		return commandLimits{}, fmt.Errorf("limits document exceeds %d bytes", maximumCommandLimitsBytes)
	}
	if err := rejectNullCommandLimits(raw); err != nil {
		return commandLimits{}, err
	}
	var document commandLimitsDocument
	if err := jsonv2.Unmarshal(raw, &document, jsonv2.RejectUnknownMembers(true), jsontext.AllowDuplicateNames(false)); err != nil {
		return commandLimits{}, fmt.Errorf("decode limits document: %w", err)
	}
	if document.Schema != commandLimitsSchema {
		return commandLimits{}, fmt.Errorf("unsupported limits schema %q", document.Schema)
	}
	if err := requirePositiveLimitOverrides(reflect.ValueOf(document), "limits"); err != nil {
		return commandLimits{}, err
	}
	limits := commandLimitsFromDocument(document)
	if err := validateCommandLimits(limits); err != nil {
		return commandLimits{}, err
	}
	if err := ctx.Err(); err != nil {
		return commandLimits{}, err
	}
	return limits, nil
}

func rejectNullCommandLimits(raw []byte) error {
	decoder := stdjson.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	return rejectNullCommandLimitValue(value, "limits")
}

func rejectNullCommandLimitValue(value any, path string) error {
	if value == nil {
		return fmt.Errorf("%s must not be null", path)
	}
	switch value := value.(type) {
	case map[string]any:
		names := make([]string, 0, len(value))
		for name := range value {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if err := rejectNullCommandLimitValue(value[name], path+"."+name); err != nil {
				return err
			}
		}
	case []any:
		for index, item := range value {
			if err := rejectNullCommandLimitValue(item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func requirePositiveLimitOverrides(value reflect.Value, path string) error {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		if value.Elem().Kind() == reflect.Int {
			if value.Elem().Int() <= 0 {
				return fmt.Errorf("%s must be positive", path)
			}
			return nil
		}
		return requirePositiveLimitOverrides(value.Elem(), path)
	}
	if value.Kind() != reflect.Struct {
		return nil
	}
	typeOf := value.Type()
	for index := range value.NumField() {
		field := typeOf.Field(index)
		if field.Name == "Schema" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" {
			name = field.Name
		}
		if err := requirePositiveLimitOverrides(value.Field(index), path+"."+name); err != nil {
			return err
		}
	}
	return nil
}

func commandLimitsFromDocument(document commandLimitsDocument) commandLimits {
	limits := commandLimits{}
	if document.Source != nil {
		limits.Source = artifactLimitsFromDocument(*document.Source)
	}
	if document.Compiled != nil {
		limits.Compiled = artifactLimitsFromDocument(*document.Compiled)
	}
	if document.Output != nil {
		limits.Output.MaxBytes = limitOverride(document.Output.MaxBytes)
	}
	if document.Catalog == nil {
		return limits
	}
	catalog := document.Catalog
	limits.Catalog = i18n.Limits{
		MaxModules: limitOverride(catalog.MaxModules), MaxMessages: limitOverride(catalog.MaxMessages),
		MaxTranslations: limitOverride(catalog.MaxTranslations), MaxLocales: limitOverride(catalog.MaxLocales),
		MaxArguments: limitOverride(catalog.MaxArguments), MaxEnumValues: limitOverride(catalog.MaxEnumValues),
		MaxMarkupNames: limitOverride(catalog.MaxMarkupNames), MaxCatalogItems: limitOverride(catalog.MaxCatalogItems),
		MaxIdentifierBytes: limitOverride(catalog.MaxIdentifierBytes), MaxRevisionBytes: limitOverride(catalog.MaxRevisionBytes),
		MaxDescriptionBytes: limitOverride(catalog.MaxDescriptionBytes), MaxEnumValueBytes: limitOverride(catalog.MaxEnumValueBytes),
		MaxArgumentBytes: limitOverride(catalog.MaxArgumentBytes), MaxBigIntegerBits: limitOverride(catalog.MaxBigIntegerBits),
		MaxTemplateBytes: limitOverride(catalog.MaxTemplateBytes), MaxCatalogBytes: limitOverride(catalog.MaxCatalogBytes),
		MaxDeclarations: limitOverride(catalog.MaxDeclarations), MaxSelectors: limitOverride(catalog.MaxSelectors),
		MaxVariants: limitOverride(catalog.MaxVariants), MaxLocalDepth: limitOverride(catalog.MaxLocalDepth),
		MaxTemplateParts: limitOverride(catalog.MaxTemplateParts), MaxMarkupDepth: limitOverride(catalog.MaxMarkupDepth),
		MaxOutputBytes: limitOverride(catalog.MaxOutputBytes), MaxOutputParts: limitOverride(catalog.MaxOutputParts),
		MaxExplainSteps: limitOverride(catalog.MaxExplainSteps),
	}
	if catalog.Locale != nil {
		limits.Catalog.Locale = i18n.LocaleLimits{
			MaxChoices: limitOverride(catalog.Locale.MaxChoices), MaxHeaderBytes: limitOverride(catalog.Locale.MaxHeaderBytes),
			MaxRanges: limitOverride(catalog.Locale.MaxRanges), MaxSupported: limitOverride(catalog.Locale.MaxSupported),
			MaxTagBytes: limitOverride(catalog.Locale.MaxTagBytes), MaxFallbackDepth: limitOverride(catalog.Locale.MaxFallbackDepth),
		}
	}
	return limits
}

func artifactLimitsFromDocument(document commandArtifactLimits) i18n.ArtifactLimits {
	return i18n.ArtifactLimits{
		MaxBytes: limitOverride(document.MaxBytes), MaxDepth: limitOverride(document.MaxDepth), MaxMembers: limitOverride(document.MaxMembers),
	}
}

func limitOverride(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func validateCommandLimits(limits commandLimits) error {
	if limits.Output.MaxBytes > maximumCommandOutputBytes {
		return fmt.Errorf("limits.output.max_bytes exceeds %d", maximumCommandOutputBytes)
	}
	if _, err := limits.sourceCodec().Decode(context.Background(), nil); !errors.Is(err, i18n.ErrInvalidSource) {
		return fmt.Errorf("invalid source or catalog limits: %w", err)
	}
	if _, err := (i18n.Loader{Limits: limits.Compiled, CatalogLimits: limits.Catalog}).Load(context.Background(), nil); !errors.Is(err, i18n.ErrInvalidArtifact) {
		return fmt.Errorf("invalid compiled or catalog limits: %w", err)
	}
	return nil
}

func (limits commandLimits) sourceCodec() i18n.SourceCodec {
	return i18n.SourceCodec{Limits: limits.Source, CatalogLimits: limits.Catalog}
}

func (limits commandLimits) compiler() i18n.Compiler {
	return i18n.Compiler{Limits: limits.Compiled, CatalogLimits: limits.Catalog}
}

func (limits commandLimits) sourceBytes() int {
	return effectiveArtifactLimits(limits.Source).MaxBytes
}

func (limits commandLimits) compiledBytes() int {
	return effectiveArtifactLimits(limits.Compiled).MaxBytes
}

func (limits commandLimits) outputBytes() int {
	if limits.Output.MaxBytes != 0 {
		return limits.Output.MaxBytes
	}
	return maximumCommandInput
}

func effectiveArtifactLimits(limits i18n.ArtifactLimits) i18n.ArtifactLimits {
	defaults := i18n.DefaultArtifactLimits()
	if limits.MaxBytes == 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.MaxDepth == 0 {
		limits.MaxDepth = defaults.MaxDepth
	}
	if limits.MaxMembers == 0 {
		limits.MaxMembers = defaults.MaxMembers
	}
	return limits
}
