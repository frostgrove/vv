package i18n

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	messageformat "github.com/kaptinlin/messageformat-go"
	"github.com/kaptinlin/messageformat-go/pkg/datamodel"
	"github.com/kaptinlin/messageformat-go/pkg/functions"
	"golang.org/x/text/unicode/norm"
)

const (
	GrammarProfile         = "frostgrove-mf2/v1"
	MessageFormatSpec      = "unicode-messageformat/48.2"
	EngineVersion          = "messageformat-go/v0.8.6"
	LocaleDataVersion      = "go-intl/v0.4.1:cldr/48.1.0+icu/78;x-text/v0.41.0"
	TimeZoneDataModel      = "go/time.LoadLocation:runtime-selected"
	maximumIdentifierBytes = 1024
)

type Key string

func Qualify(module, id string) Key {
	return Key(module + "." + id)
}

type OutputKind uint8

const (
	OutputPlain OutputKind = iota
	OutputRich
)

func (o OutputKind) String() string {
	switch o {
	case OutputPlain:
		return "plain"
	case OutputRich:
		return "rich"
	default:
		return "unknown"
	}
}

func (o OutputKind) Valid() bool { return o == OutputPlain || o == OutputRich }

type ReviewState uint8

const (
	ReviewUnset ReviewState = iota
	ReviewApproved
	ReviewRequired
	ReviewRejected
)

func (r ReviewState) String() string {
	switch r {
	case ReviewUnset:
		return "unset"
	case ReviewApproved:
		return "approved"
	case ReviewRequired:
		return "required"
	case ReviewRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

func (r ReviewState) Valid() bool { return r >= ReviewUnset && r <= ReviewRejected }

type OverridePolicy uint8

const (
	OverrideDenied      OverridePolicy = 0
	OverrideApplication OverridePolicy = 1
	OverrideTenant      OverridePolicy = 2
	OverrideAny                        = OverrideApplication | OverrideTenant
)

func (o OverridePolicy) String() string {
	switch o {
	case OverrideDenied:
		return "denied"
	case OverrideApplication:
		return "application"
	case OverrideTenant:
		return "tenant"
	case OverrideAny:
		return "any"
	default:
		return "unknown"
	}
}

func (o OverridePolicy) Valid() bool { return o&^OverrideAny == 0 }

type Layer uint8

const (
	LayerModule Layer = iota + 1
	LayerApplication
	LayerTenant
)

func (l Layer) String() string {
	switch l {
	case LayerModule:
		return "module"
	case LayerApplication:
		return "application"
	case LayerTenant:
		return "tenant"
	default:
		return "unknown"
	}
}

func (l Layer) Valid() bool { return l >= LayerModule && l <= LayerTenant }

type Capability uint8

const (
	CapabilityDateTime Capability = iota + 1
	CapabilityUnit
)

func (c Capability) String() string {
	switch c {
	case CapabilityDateTime:
		return "date_time"
	case CapabilityUnit:
		return "unit"
	default:
		return "unknown"
	}
}

func (c Capability) Valid() bool { return c == CapabilityDateTime || c == CapabilityUnit }

type Limits struct {
	Locale              LocaleLimits
	MaxModules          int
	MaxMessages         int
	MaxTranslations     int
	MaxLocales          int
	MaxArguments        int
	MaxEnumValues       int
	MaxMarkupNames      int
	MaxCatalogItems     int
	MaxIdentifierBytes  int
	MaxRevisionBytes    int
	MaxDescriptionBytes int
	MaxEnumValueBytes   int
	MaxArgumentBytes    int
	MaxBigIntegerBits   int
	MaxTemplateBytes    int
	MaxCatalogBytes     int
	MaxDeclarations     int
	MaxSelectors        int
	MaxVariants         int
	MaxLocalDepth       int
	MaxTemplateParts    int
	MaxMarkupDepth      int
	MaxOutputBytes      int
	MaxOutputParts      int
	MaxExplainSteps     int
}

func DefaultLimits() Limits {
	limits, err := checkedLimits(Limits{})
	if err != nil {
		panic(err)
	}
	return limits
}

type Translation struct {
	Locale           string
	Text             string
	Review           ReviewState
	ContractRevision string
	SourceDigest     string
	ReviewDigest     string
}

type MessageSpec struct {
	ID           string
	Key          Key
	Revision     string
	Source       string
	Description  string
	Arguments    []ArgumentSpec
	Output       OutputKind
	Markup       []string
	Override     OverridePolicy
	Translations []Translation
	AllowEmpty   bool
	Public       bool
}

type Module struct {
	Name     string
	Messages []MessageSpec
}

type CatalogSpec struct {
	Revision            string
	Profile             string
	SourceLocale        string
	DefaultLocale       string
	Supported           []string
	Required            []string
	Parents             []LocaleEdge
	Modules             []Module
	Overrides           []Override
	Capabilities        []Capability
	MatchMode           MatchMode
	DefaultOnMiss       bool
	DefaultTimeZone     string
	TimeZoneDataVersion string
	Limits              Limits
	Observer            Observer
}

type Descriptor struct {
	Key         Key
	Revision    string
	Description string
	Arguments   []ArgumentSpec
	Output      OutputKind
	Markup      []string
	Override    OverridePolicy
	AllowEmpty  bool
	Public      bool
}

type compiledTranslation struct {
	locale    string
	layer     Layer
	text      string
	formatter *messageformat.MessageFormat
	work      templateWork
}

type messageRecord struct {
	descriptor   Descriptor
	contractHash string
	sourceDigest string
	allowEmpty   bool
	templates    map[string]compiledTranslation
}

type Snapshot struct {
	revision            string
	digest              string
	profile             string
	sourceLocale        string
	defaultLocale       string
	defaultTimeZone     string
	timeZoneDataVersion string
	supported           []string
	required            []string
	parents             map[string]string
	records             map[Key]*messageRecord
	resolver            *Resolver
	capabilities        map[Capability]bool
	matchMode           MatchMode
	defaultOnMiss       bool
	limits              Limits
	observer            Observer
	highestLayer        Layer
	formatRequirements  map[string]formatRequirements
}

func New(spec CatalogSpec) (*Snapshot, error) {
	return NewContext(context.Background(), spec)
}

func NewContext(ctx context.Context, spec CatalogSpec) (*Snapshot, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: catalog context is nil", ErrInvalidCatalog)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	problems := &problemSet{}
	limits, err := checkedLimits(spec.Limits)
	if err != nil {
		return nil, err
	}
	if err := preflightCatalogContext(ctx, spec, limits); err != nil {
		return nil, err
	}
	profile := spec.Profile
	if profile == "" {
		profile = GrammarProfile
	}
	if profile != GrammarProfile {
		problems.add(ProblemUnsupported, "profile", profile)
	}
	revision := spec.Revision
	if revision == "" {
		problems.add(ProblemMissing, "revision", "a valid schema revision is required")
	} else if len(revision) > limits.MaxRevisionBytes {
		problems.add(ProblemLimit, "revision", strconv.Itoa(len(revision)))
	} else if !utf8.ValidString(revision) {
		problems.add(ProblemInvalid, "revision", "schema revision is not valid UTF-8")
	}
	defaultTimeZone := spec.DefaultTimeZone
	if defaultTimeZone == "" {
		defaultTimeZone = "UTC"
	}
	canonicalTimeZone, timeZoneErr := canonicalTimeZone(defaultTimeZone)
	if timeZoneErr != nil {
		problems.addError(ProblemInvalid, "default_time_zone", timeZoneErr)
	}
	if !utf8.ValidString(spec.TimeZoneDataVersion) || len(spec.TimeZoneDataVersion) > limits.MaxRevisionBytes {
		problems.add(ProblemInvalid, "time_zone_data_version", "time-zone data version is invalid or too large")
	} else if canonicalTimeZone != "" && canonicalTimeZone != "UTC" && spec.TimeZoneDataVersion == "" {
		problems.add(ProblemMissing, "time_zone_data_version", "non-UTC formatting requires a time-zone data version")
	}

	sourceLocale := spec.SourceLocale
	if sourceLocale == "" {
		problems.add(ProblemMissing, "source_locale", "source locale is required")
	}
	_, canonicalSource, sourceErr := canonicalLocale(sourceLocale, limits.Locale.MaxTagBytes)
	if sourceErr != nil {
		problems.addError(ProblemInvalid, "source_locale", sourceErr)
	}
	defaultLocale := spec.DefaultLocale
	if defaultLocale == "" {
		defaultLocale = sourceLocale
	}
	_, canonicalDefault, defaultErr := canonicalLocale(defaultLocale, limits.Locale.MaxTagBytes)
	if defaultErr != nil {
		problems.addError(ProblemInvalid, "default_locale", defaultErr)
	}

	resolver, resolverErr := NewResolver(LocalePolicy{
		Supported:     spec.Supported,
		Default:       defaultLocale,
		Parents:       spec.Parents,
		Mode:          spec.MatchMode,
		DefaultOnMiss: spec.DefaultOnMiss,
		Limits:        limits.Locale,
		Observer:      spec.Observer,
	})
	if resolverErr != nil {
		problems.addError(ProblemInvalid, "locale_policy", resolverErr)
	}

	supported := canonicalLocaleList(spec.Supported, limits.Locale.MaxTagBytes, "supported", problems)
	if len(supported) > limits.MaxLocales {
		problems.add(ProblemLimit, "supported", strconv.Itoa(len(supported)))
	}
	required := canonicalLocaleList(spec.Required, limits.Locale.MaxTagBytes, "required", problems)
	supportedSet := make(map[string]bool, len(supported))
	for _, locale := range supported {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		supportedSet[locale] = true
	}
	for i, locale := range required {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !supportedSet[locale] {
			problems.add(ProblemInvalid, problemPath("required[%d]", i), "locale is not supported")
		}
	}

	parents := make(map[string]string, len(spec.Parents))
	for _, edge := range spec.Parents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, child, childErr := canonicalLocale(edge.Locale, limits.Locale.MaxTagBytes)
		_, parent, parentErr := canonicalLocale(edge.Parent, limits.Locale.MaxTagBytes)
		if childErr == nil && parentErr == nil {
			parents[child] = parent
		}
	}
	if resolverErr == nil {
		fallbackSeeds := slices.Clone(supported)
		fallbackSeeds = append(fallbackSeeds, canonicalSource, canonicalDefault)
		for child, parent := range parents {
			fallbackSeeds = append(fallbackSeeds, child, parent)
		}
		cycle, tooDeep := effectiveLocaleGraphProblem(fallbackSeeds, parents, limits.Locale.MaxFallbackDepth)
		if cycle != "" {
			problems.add(ProblemInvalid, "parents", "template fallback cycle at "+cycle)
		}
		if tooDeep != "" {
			problems.add(ProblemLimit, "parents", "template fallback chain exceeds "+strconv.Itoa(limits.Locale.MaxFallbackDepth))
		}
	}

	capabilities := make(map[Capability]bool, len(spec.Capabilities))
	for i, capability := range spec.Capabilities {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if capability != CapabilityDateTime && capability != CapabilityUnit {
			problems.add(ProblemUnsupported, problemPath("capabilities[%d]", i), "unknown capability")
			continue
		}
		if capabilities[capability] {
			problems.add(ProblemDuplicate, problemPath("capabilities[%d]", i), "capability is repeated")
		}
		capabilities[capability] = true
	}

	allowedLocales := maps.Clone(supportedSet)
	if canonicalSource != "" {
		allowedLocales[canonicalSource] = true
	}
	if canonicalDefault != "" {
		allowedLocales[canonicalDefault] = true
	}
	for child, parent := range parents {
		allowedLocales[child] = true
		allowedLocales[parent] = true
	}

	records := make(map[Key]*messageRecord)
	totalTranslations := 0
	if len(spec.Modules) > limits.MaxModules {
		problems.add(ProblemLimit, "modules", strconv.Itoa(len(spec.Modules)))
	}
	for moduleIndex, module := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		modulePath := problemPath("modules[%d]", moduleIndex)
		if !validModuleName(module.Name, limits.MaxIdentifierBytes) {
			problems.add(ProblemInvalid, modulePath+".name", module.Name)
		}
		for messageIndex, message := range module.Messages {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			messagePath := problemPath("%s.messages[%d]", modulePath, messageIndex)
			key := message.Key
			if key == "" {
				key = Qualify(module.Name, message.ID)
			}
			if message.ID == "" || !validMessageID(message.ID, limits.MaxIdentifierBytes) {
				problems.add(ProblemInvalid, messagePath+".id", message.ID)
			}
			if key != Qualify(module.Name, message.ID) || !validKey(key, limits.MaxIdentifierBytes) {
				problems.add(ProblemInvalid, messagePath+".key", string(key))
			}
			if _, exists := records[key]; exists {
				problems.add(ProblemDuplicate, messagePath+".key", string(key))
				continue
			}
			record, buildErr := buildMessageRecord(ctx, key, message, canonicalSource, allowedLocales, capabilities, limits, messagePath, problems)
			if buildErr != nil {
				return nil, buildErr
			}
			records[key] = record
			totalTranslations += len(record.templates)
		}
	}
	if len(records) > limits.MaxMessages {
		problems.add(ProblemLimit, "messages", strconv.Itoa(len(records)))
	}
	if totalTranslations > limits.MaxTranslations {
		problems.add(ProblemLimit, "translations", strconv.Itoa(totalTranslations))
	}
	if len(records) == 0 {
		problems.add(ProblemMissing, "modules", "catalog has no messages")
	}

	if err := problems.err(); err != nil {
		return nil, err
	}
	snapshot := &Snapshot{
		revision:            revision,
		profile:             profile,
		sourceLocale:        canonicalSource,
		defaultLocale:       canonicalDefault,
		defaultTimeZone:     canonicalTimeZone,
		timeZoneDataVersion: spec.TimeZoneDataVersion,
		supported:           slices.Clone(supported),
		required:            slices.Clone(required),
		parents:             maps.Clone(parents),
		records:             records,
		resolver:            resolver,
		capabilities:        maps.Clone(capabilities),
		matchMode:           spec.MatchMode,
		defaultOnMiss:       spec.DefaultOnMiss,
		limits:              limits,
		observer:            spec.Observer,
		highestLayer:        LayerModule,
	}
	snapshot.formatRequirements, err = snapshotFormattingRequirementsContext(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	bytes, items, err := snapshotCatalogMaterialContext(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	if bytes > limits.MaxCatalogBytes {
		problems.add(ProblemLimit, "catalog_bytes", strconv.Itoa(bytes))
		return nil, problems.err()
	}
	if items > limits.MaxCatalogItems {
		problems.add(ProblemLimit, "catalog_items", strconv.Itoa(items))
		return nil, problems.err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot.digest, err = snapshotDigestContext(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	if len(spec.Overrides) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		snapshot, err = snapshot.overlayContext(ctx, OverlaySpec{Layer: LayerApplication, Revision: revision, Overrides: spec.Overrides}, false)
		if err != nil {
			return nil, err
		}
	}
	requiredProblems := &problemSet{}
	if err := validateRequiredLocalesContext(ctx, snapshot, requiredProblems); err != nil {
		return nil, err
	}
	if err := requiredProblems.err(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *Snapshot) Revision() string {
	if s == nil {
		return ""
	}
	return s.revision
}

func (s *Snapshot) Digest() string {
	if s == nil {
		return ""
	}
	return s.digest
}

func (s *Snapshot) Profile() string {
	if s == nil {
		return ""
	}
	return s.profile
}

func (s *Snapshot) TimeZoneDataVersion() string {
	if s == nil {
		return ""
	}
	return s.timeZoneDataVersion
}

func (s *Snapshot) Supported() []string {
	if s == nil {
		return nil
	}
	return slices.Clone(s.supported)
}

func (s *Snapshot) Keys() []Key {
	keys, _ := snapshotKeysContext(context.Background(), s)
	return keys
}

func snapshotKeysContext(ctx context.Context, snapshot *Snapshot) ([]Key, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: snapshot keys context is nil", ErrInvalidCatalog)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, nil
	}
	keys := make([]Key, 0, len(snapshot.records))
	for key := range snapshot.records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys, ctx.Err()
}

func (s *Snapshot) Descriptor(key Key) (Descriptor, bool) {
	if s == nil || validateLookupKey(key, s.limits.MaxIdentifierBytes) != nil {
		return Descriptor{}, false
	}
	record, ok := s.records[key]
	if !ok {
		return Descriptor{}, false
	}
	return cloneDescriptor(record.descriptor), true
}

func (d Descriptor) ContractRef() ContractRef {
	if d.Revision == "" || len(d.Key) > maximumIdentifierBytes || !validKey(d.Key, maximumIdentifierBytes) {
		return ContractRef{}
	}
	return ContractRef{Key: d.Key, Revision: d.Revision, Digest: descriptorDigest(d)}
}

func (s *Snapshot) ContractRef(key Key) (ContractRef, bool) {
	if s == nil || validateLookupKey(key, s.limits.MaxIdentifierBytes) != nil {
		return ContractRef{}, false
	}
	record, ok := s.records[key]
	if !ok {
		return ContractRef{}, false
	}
	return ContractRef{Key: key, Revision: record.descriptor.Revision, Digest: record.contractHash}, true
}

func (s *Snapshot) SourceDigest(key Key) (string, bool) {
	if s == nil || validateLookupKey(key, s.limits.MaxIdentifierBytes) != nil {
		return "", false
	}
	record, ok := s.records[key]
	if !ok {
		return "", false
	}
	return record.sourceDigest, true
}

func ExpectedSourceDigestForLocale(profile, sourceLocale, module string, message MessageSpec) (string, error) {
	if profile == "" {
		profile = GrammarProfile
	}
	if profile != GrammarProfile {
		return "", fmt.Errorf("%w: unsupported grammar profile %q", ErrInvalidCatalog, profile)
	}
	limits, err := checkedLimits(Limits{
		Locale:              LocaleLimits{MaxChoices: 64, MaxHeaderBytes: 65536, MaxRanges: 256, MaxSupported: 1024, MaxTagBytes: 512, MaxFallbackDepth: 64},
		MaxArguments:        256,
		MaxEnumValues:       4096,
		MaxMarkupNames:      4096,
		MaxCatalogItems:     1 << 24,
		MaxIdentifierBytes:  maximumIdentifierBytes,
		MaxRevisionBytes:    1 << 20,
		MaxDescriptionBytes: 4 << 20,
		MaxEnumValueBytes:   1 << 20,
		MaxTemplateBytes:    4 << 20,
		MaxCatalogBytes:     1 << 30,
	})
	if err != nil {
		return "", err
	}
	if len(message.Arguments) > limits.MaxArguments || len(message.Markup) > limits.MaxMarkupNames {
		return "", fmt.Errorf("%w: message schema exceeds supported bounds", ErrLimitExceeded)
	}
	counter := artifactCounter{maxBytes: limits.MaxCatalogBytes, maxItems: limits.MaxCatalogItems}
	_, canonicalSourceLocale, err := canonicalLocale(sourceLocale, limits.Locale.MaxTagBytes)
	if err != nil {
		return "", fmt.Errorf("%w: source locale: %v", ErrInvalidLocale, err)
	}
	if !counter.add(profile, canonicalSourceLocale, module, message.ID, string(message.Key), message.Revision, message.Source, message.Description) {
		return "", fmt.Errorf("%w: message material exceeds supported bounds", ErrLimitExceeded)
	}
	for _, argument := range message.Arguments {
		if len(argument.Values) > limits.MaxEnumValues || len(argument.Name) > limits.MaxIdentifierBytes || !counter.add(argument.Name) {
			return "", fmt.Errorf("%w: argument schema exceeds supported bounds", ErrLimitExceeded)
		}
		for _, value := range argument.Values {
			if len(value) > limits.MaxEnumValueBytes || !counter.add(value) {
				return "", fmt.Errorf("%w: enum schema exceeds supported bounds", ErrLimitExceeded)
			}
		}
	}
	for _, markup := range message.Markup {
		if len(markup) > limits.MaxIdentifierBytes || !counter.add(markup) {
			return "", fmt.Errorf("%w: markup schema exceeds supported bounds", ErrLimitExceeded)
		}
	}
	if len(module) > limits.MaxIdentifierBytes {
		return "", fmt.Errorf("%w: module exceeds supported bounds", ErrLimitExceeded)
	}
	if !validModuleName(module, limits.MaxIdentifierBytes) {
		return "", fmt.Errorf("%w: invalid module %q", ErrInvalidCatalog, module)
	}
	if len(message.ID) > limits.MaxIdentifierBytes || len(message.Key) > limits.MaxIdentifierBytes {
		return "", fmt.Errorf("%w: message key exceeds supported bounds", ErrLimitExceeded)
	}
	key := message.Key
	if key == "" {
		key = Qualify(module, message.ID)
	}
	if len(key) > limits.MaxIdentifierBytes {
		return "", fmt.Errorf("%w: message key exceeds supported bounds", ErrLimitExceeded)
	}
	if message.ID == "" || !validMessageID(message.ID, limits.MaxIdentifierBytes) || key != Qualify(module, message.ID) || !validKey(key, limits.MaxIdentifierBytes) {
		return "", fmt.Errorf("%w: invalid message key", ErrInvalidCatalog)
	}
	if len(message.Revision) > limits.MaxRevisionBytes {
		return "", fmt.Errorf("%w: message revision exceeds supported bounds", ErrLimitExceeded)
	}
	if message.Revision == "" || !utf8.ValidString(message.Revision) {
		return "", fmt.Errorf("%w: invalid message revision", ErrInvalidCatalog)
	}
	if !message.Output.Valid() {
		return "", fmt.Errorf("%w: invalid output kind", ErrInvalidCatalog)
	}
	if len(message.Source) > limits.MaxTemplateBytes {
		return "", fmt.Errorf("%w: message source exceeds supported bounds", ErrLimitExceeded)
	}
	if !utf8.ValidString(message.Source) || hasUnsafeAuthoredBidiControls(message.Source) {
		return "", fmt.Errorf("%w: invalid message source", ErrInvalidCatalog)
	}
	if message.Source == "" && !message.AllowEmpty {
		return "", fmt.Errorf("%w: empty message source", ErrInvalidCatalog)
	}
	if len(message.Description) > limits.MaxDescriptionBytes {
		return "", fmt.Errorf("%w: message description exceeds supported bounds", ErrLimitExceeded)
	}
	if !utf8.ValidString(message.Description) || strings.TrimSpace(message.Description) == "" {
		return "", fmt.Errorf("%w: invalid message description", ErrInvalidCatalog)
	}
	problems := &problemSet{}
	capabilities := map[Capability]bool{CapabilityDateTime: true, CapabilityUnit: true}
	arguments := validateArgumentSpecs(message.Arguments, capabilities, limits, "arguments", problems)
	markup := validateMarkupAllowlist(message.Markup, message.Output, limits.MaxIdentifierBytes, limits.MaxMarkupNames, "markup", problems)
	if err := problems.err(); err != nil {
		return "", err
	}
	descriptor := Descriptor{
		Key:         key,
		Revision:    message.Revision,
		Description: message.Description,
		Arguments:   arguments,
		Output:      message.Output,
		Markup:      markup,
		Override:    message.Override,
		AllowEmpty:  message.AllowEmpty,
		Public:      message.Public,
	}
	return sourceReviewDigest(profile, canonicalSourceLocale, key, descriptorDigest(descriptor), message.Source, message.Description), nil
}

func ExpectedReviewDigest(sourceDigest, locale, text string) (string, error) {
	decoded, err := hex.DecodeString(sourceDigest)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != sourceDigest {
		return "", fmt.Errorf("%w: invalid source digest", ErrInvalidCatalog)
	}
	_, canonical, err := canonicalLocale(locale, 512)
	if err != nil {
		return "", fmt.Errorf("%w: review locale: %v", ErrInvalidLocale, err)
	}
	if !utf8.ValidString(text) || hasUnsafeAuthoredBidiControls(text) {
		return "", fmt.Errorf("%w: invalid reviewed text", ErrInvalidCatalog)
	}
	hash := sha256.New()
	writeDigestField(hash.Write, "domain", "frostgrove.i18n.translation-review-digest/v1")
	writeDigestField(hash.Write, "source_digest", sourceDigest)
	writeDigestField(hash.Write, "locale", canonical)
	writeDigestField(hash.Write, "text", text)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *Snapshot) Resolve(choices ...Choice) Resolution {
	if s == nil || s.resolver == nil {
		return Resolution{Outcome: OutcomeInvalid, Reason: ReasonMalformed}
	}
	return s.resolver.Resolve(choices...)
}

func (s *Snapshot) ResolveContext(ctx context.Context, choices ...Choice) Resolution {
	if s == nil || s.resolver == nil {
		return Resolution{Outcome: OutcomeInvalid, Reason: ReasonMalformed}
	}
	return s.resolver.ResolveContext(ctx, choices...)
}

func (s *Snapshot) Bind(key Key, arguments ...Argument) (Message, error) {
	if s == nil {
		return Message{}, fmt.Errorf("%w: snapshot is nil", ErrInvalidMessage)
	}
	if err := validateLookupKey(key, s.limits.MaxIdentifierBytes); err != nil {
		return Message{}, err
	}
	record, ok := s.records[key]
	if !ok {
		return Message{}, fmt.Errorf("%w: %q", ErrNotFound, key)
	}
	checked, err := checkArguments(record.descriptor.Arguments, arguments, s.limits)
	if err != nil {
		return Message{}, err
	}
	return Message{key: key, revision: record.descriptor.Revision, digest: record.contractHash, arguments: checked}, nil
}

func validateLookupKey(key Key, maximum int) error {
	if len(key) > maximum {
		return fmt.Errorf("%w: key exceeds %d bytes", ErrLimitExceeded, maximum)
	}
	if !validKey(key, maximum) {
		return fmt.Errorf("%w: key is invalid", ErrInvalidMessage)
	}
	return nil
}

func cloneDescriptor(descriptor Descriptor) Descriptor {
	clone, _ := cloneDescriptorContext(context.Background(), descriptor)
	return clone
}

func cloneDescriptorContext(ctx context.Context, descriptor Descriptor) (Descriptor, error) {
	clone := descriptor
	clone.Arguments = make([]ArgumentSpec, len(descriptor.Arguments))
	for i, argument := range descriptor.Arguments {
		if err := ctx.Err(); err != nil {
			return Descriptor{}, err
		}
		clone.Arguments[i] = argument
		values, err := cloneSliceContext(ctx, argument.Values)
		if err != nil {
			return Descriptor{}, err
		}
		clone.Arguments[i].Values = values
	}
	markup, err := cloneSliceContext(ctx, descriptor.Markup)
	if err != nil {
		return Descriptor{}, err
	}
	clone.Markup = markup
	return clone, ctx.Err()
}

func buildMessageRecord(
	ctx context.Context,
	key Key,
	spec MessageSpec,
	sourceLocale string,
	allowedLocales map[string]bool,
	capabilities map[Capability]bool,
	limits Limits,
	path string,
	problems *problemSet,
) (*messageRecord, error) {
	revision := spec.Revision
	if revision == "" {
		problems.add(ProblemMissing, path+".revision", "message contract revision is required")
	} else if len(revision) > limits.MaxRevisionBytes {
		problems.add(ProblemLimit, path+".revision", strconv.Itoa(len(revision)))
	} else if !utf8.ValidString(revision) {
		problems.add(ProblemInvalid, path+".revision", "message contract revision is not valid UTF-8")
	}
	if len(spec.Description) > limits.MaxDescriptionBytes {
		problems.add(ProblemLimit, path+".description", strconv.Itoa(len(spec.Description)))
	} else if !utf8.ValidString(spec.Description) {
		problems.add(ProblemInvalid, path+".description", "description is not valid UTF-8")
	} else if strings.TrimSpace(spec.Description) == "" {
		problems.add(ProblemMissing, path+".description", "translator description is required")
	}
	if spec.Output != OutputPlain && spec.Output != OutputRich {
		problems.add(ProblemInvalid, path+".output", "unknown output kind")
	}
	if spec.Override&^OverrideAny != 0 {
		problems.add(ProblemInvalid, path+".override", "unknown override policy")
	}
	arguments := validateArgumentSpecs(spec.Arguments, capabilities, limits, path+".arguments", problems)
	markup := validateMarkupAllowlist(spec.Markup, spec.Output, limits.MaxIdentifierBytes, limits.MaxMarkupNames, path+".markup", problems)
	descriptor := Descriptor{
		Key:         key,
		Revision:    revision,
		Description: spec.Description,
		Arguments:   arguments,
		Output:      spec.Output,
		Markup:      markup,
		Override:    spec.Override,
		AllowEmpty:  spec.AllowEmpty,
		Public:      spec.Public,
	}
	record := &messageRecord{descriptor: descriptor, allowEmpty: spec.AllowEmpty, templates: make(map[string]compiledTranslation)}
	record.contractHash = descriptorDigest(descriptor)
	record.sourceDigest = sourceReviewDigest(GrammarProfile, sourceLocale, key, record.contractHash, spec.Source, spec.Description)

	if len(spec.Source) > limits.MaxTemplateBytes {
		problems.add(ProblemLimit, path+".source", strconv.Itoa(len(spec.Source)))
	} else if !utf8.ValidString(spec.Source) {
		problems.add(ProblemInvalid, path+".source", "source is not valid UTF-8")
	} else if hasUnsafeAuthoredBidiControls(spec.Source) {
		problems.add(ProblemSecurity, path+".source", "template contains directional controls")
	} else if spec.Source == "" && !spec.AllowEmpty {
		problems.add(ProblemMissing, path+".source", "empty source is not allowed")
	} else if sourceLocale != "" {
		compiled, ok := compileTranslation(sourceLocale, LayerModule, spec.Source, descriptor, capabilities, limits, path+".source", problems)
		if ok {
			record.templates[sourceLocale] = compiled
		}
	}

	seenLocales := make(map[string]string, len(spec.Translations))
	for i, translation := range spec.Translations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		translationPath := problemPath("%s.translations[%d]", path, i)
		_, locale, err := canonicalLocale(translation.Locale, limits.Locale.MaxTagBytes)
		if err != nil {
			problems.addError(ProblemInvalid, translationPath+".locale", err)
			continue
		}
		if previous, exists := seenLocales[locale]; exists {
			code := ProblemDuplicate
			if previous != translation.Locale {
				code = ProblemCollision
			}
			problems.add(code, translationPath+".locale", locale)
			continue
		}
		seenLocales[locale] = translation.Locale
		if locale == sourceLocale {
			problems.add(ProblemDuplicate, translationPath+".locale", "source locale is supplied by source")
			continue
		}
		if !allowedLocales[locale] {
			problems.add(ProblemInvalid, translationPath+".locale", "locale is outside the declared graph")
			continue
		}
		if translation.Review == ReviewUnset {
			problems.add(ProblemMissing, translationPath+".review", "translation review is required")
			continue
		}
		if translation.Review != ReviewApproved && translation.Review != ReviewRequired && translation.Review != ReviewRejected {
			problems.add(ProblemInvalid, translationPath+".review", "unknown review state")
			continue
		}
		if translation.Review != ReviewApproved {
			problems.add(ProblemStale, translationPath+".review", "translation is not approved")
			continue
		}
		translationRevision := translation.ContractRevision
		if translationRevision == "" {
			problems.add(ProblemMissing, translationPath+".contract_revision", "translation contract revision is required")
			continue
		}
		if len(translationRevision) > limits.MaxRevisionBytes {
			problems.add(ProblemLimit, translationPath+".contract_revision", strconv.Itoa(len(translationRevision)))
			continue
		}
		if translationRevision != "" && translationRevision != revision {
			problems.add(ProblemStale, translationPath+".contract_revision", translationRevision)
			continue
		}
		if translation.SourceDigest == "" {
			problems.add(ProblemMissing, translationPath+".source_digest", "translation source digest is required")
			continue
		}
		if translation.SourceDigest != record.sourceDigest {
			problems.add(ProblemStale, translationPath+".source_digest", translation.SourceDigest)
			continue
		}
		text := translation.Text
		if text == "" && !spec.AllowEmpty {
			problems.add(ProblemMissing, translationPath+".text", "empty translation is not allowed")
			continue
		}
		if len(text) > limits.MaxTemplateBytes {
			problems.add(ProblemLimit, translationPath+".text", strconv.Itoa(len(text)))
			continue
		}
		if !utf8.ValidString(text) {
			problems.add(ProblemInvalid, translationPath+".text", "translation is not valid UTF-8")
			continue
		}
		if hasUnsafeAuthoredBidiControls(text) {
			problems.add(ProblemSecurity, translationPath+".text", "template contains directional controls")
			continue
		}
		expectedReviewDigest, digestErr := ExpectedReviewDigest(record.sourceDigest, locale, text)
		if digestErr != nil {
			problems.addError(ProblemInvalid, translationPath+".review_digest", digestErr)
			continue
		}
		if translation.ReviewDigest == "" {
			problems.add(ProblemMissing, translationPath+".review_digest", "translation content review digest is required")
			continue
		}
		if translation.ReviewDigest != expectedReviewDigest {
			problems.add(ProblemStale, translationPath+".review_digest", translation.ReviewDigest)
			continue
		}
		compiled, ok := compileTranslation(locale, LayerModule, text, descriptor, capabilities, limits, translationPath+".text", problems)
		if ok {
			record.templates[locale] = compiled
		}
	}
	return record, nil
}

func validateArgumentSpecs(specs []ArgumentSpec, capabilities map[Capability]bool, limits Limits, path string, problems *problemSet) []ArgumentSpec {
	if len(specs) > limits.MaxArguments {
		problems.add(ProblemLimit, path, strconv.Itoa(len(specs)))
	}
	seen := make(map[string]bool, len(specs))
	out := make([]ArgumentSpec, len(specs))
	for i, spec := range specs {
		argumentPath := problemPath("%s[%d]", path, i)
		if !validIdentifier(spec.Name, limits.MaxIdentifierBytes) {
			problems.add(ProblemInvalid, argumentPath+".name", spec.Name)
		}
		if seen[spec.Name] {
			problems.add(ProblemDuplicate, argumentPath+".name", spec.Name)
		}
		seen[spec.Name] = true
		if spec.Type < TypeText || spec.Type > TypeEnum {
			problems.add(ProblemInvalid, argumentPath+".type", spec.Type.String())
		}
		if (spec.Type == TypeDate || spec.Type == TypeInstant) && !capabilities[CapabilityDateTime] {
			problems.add(ProblemUnsupported, argumentPath+".type", "date/time capability is disabled")
		}
		if spec.Type != TypeEnum && len(spec.Values) > 0 {
			problems.add(ProblemSchema, argumentPath+".values", "only enum arguments declare values")
		}
		if spec.Type == TypeEnum {
			if len(spec.Values) == 0 {
				problems.add(ProblemMissing, argumentPath+".values", "enum values are empty")
			}
			if len(spec.Values) > limits.MaxEnumValues {
				problems.add(ProblemLimit, argumentPath+".values", strconv.Itoa(len(spec.Values)))
			}
			values := make(map[string]bool, len(spec.Values))
			for valueIndex, value := range spec.Values {
				if len(value) > limits.MaxEnumValueBytes {
					problems.add(ProblemLimit, problemPath("%s.values[%d]", argumentPath, valueIndex), strconv.Itoa(len(value)))
				} else if value == "" || !utf8.ValidString(value) {
					problems.add(ProblemInvalid, problemPath("%s.values[%d]", argumentPath, valueIndex), value)
				} else if hasUnsafeBidiControls(value) {
					problems.add(ProblemSecurity, problemPath("%s.values[%d]", argumentPath, valueIndex), "enum value contains directional controls")
				}
				if values[value] {
					problems.add(ProblemDuplicate, problemPath("%s.values[%d]", argumentPath, valueIndex), value)
				}
				values[value] = true
			}
		}
		out[i] = spec
		out[i].Values = slices.Clone(spec.Values)
	}
	return out
}

func validateMarkupAllowlist(markup []string, output OutputKind, maxIdentifierBytes, maxNames int, path string, problems *problemSet) []string {
	if len(markup) > maxNames {
		problems.add(ProblemLimit, path, strconv.Itoa(len(markup)))
	}
	seen := make(map[string]bool, len(markup))
	out := slices.Clone(markup)
	for i, name := range markup {
		if !validIdentifier(name, maxIdentifierBytes) {
			problems.add(ProblemInvalid, problemPath("%s[%d]", path, i), name)
		}
		if seen[name] {
			problems.add(ProblemDuplicate, problemPath("%s[%d]", path, i), name)
		}
		seen[name] = true
	}
	if output == OutputPlain && len(markup) > 0 {
		problems.add(ProblemSecurity, path, "plain messages cannot allow markup")
	}
	slices.Sort(out)
	return out
}

func compileTranslation(
	locale string,
	layer Layer,
	text string,
	descriptor Descriptor,
	capabilities map[Capability]bool,
	limits Limits,
	path string,
	problems *problemSet,
) (compiled compiledTranslation, ok bool) {
	defer func() {
		if recover() != nil {
			compiled = compiledTranslation{}
			ok = false
			problems.add(ProblemInvalidSyntax, path, "message engine rejected the template")
		}
	}()
	model, err := datamodel.ParseMessage(text)
	if err != nil {
		problems.addError(ProblemInvalidSyntax, path, err)
		return compiledTranslation{}, false
	}
	if !validateTemplateModel(model, locale, descriptor, capabilities, limits, path, problems) {
		return compiledTranslation{}, false
	}
	work := compileTemplateWork(model, descriptor.Arguments)
	if err := validateFormatRequirements(locale, work.requirements); err != nil {
		problems.addError(ProblemUnsupported, path, err)
		return compiledTranslation{}, false
	}
	formatter, err := messageformat.Compile([]string{locale}, model, messageformat.WithFunctions(runtimeFunctions(capabilities)))
	if err != nil {
		problems.addError(ProblemInvalidSyntax, path, err)
		return compiledTranslation{}, false
	}
	return compiledTranslation{locale: locale, layer: layer, text: text, formatter: formatter, work: work}, true
}

func validateTemplateModel(model datamodel.Message, locale string, descriptor Descriptor, capabilities map[Capability]bool, limits Limits, path string, problems *problemSet) bool {
	before := len(problems.items)
	if !validateTemplateStructure(model, limits, path, problems) {
		return false
	}
	arguments := make(map[string]ArgumentSpec, len(descriptor.Arguments))
	for _, argument := range descriptor.Arguments {
		arguments[argument.Name] = argument
	}
	locals := make(map[string]bool)
	for _, declaration := range model.Declarations() {
		if declaration.Type() == "local" {
			locals[declaration.Name()] = true
		}
	}
	variables := inferTemplateVariables(model, arguments)
	profiles := declarationProfiles(model.Declarations(), arguments)
	partCount := 0
	markupDepth := 0
	datamodel.Visit(model, &datamodel.Visitor{
		Expression: func(expression *datamodel.Expression, _ datamodel.VisitContext) func() {
			if len(expression.Attributes()) != 0 {
				problems.add(ProblemUnsupported, path, "expression attributes are outside the profile")
			}
			return nil
		},
		Pattern: func(pattern datamodel.Pattern) func() {
			partCount += pattern.Len()
			depth := validatePatternMarkup(pattern, descriptor, limits, path, problems)
			if depth > markupDepth {
				markupDepth = depth
			}
			return nil
		},
		Value: func(value datamodel.ExpressionArg, _ datamodel.VisitContext, _ datamodel.ValuePosition) {
			variable, ok := value.(*datamodel.VariableRef)
			if !ok || locals[variable.Name()] {
				return
			}
			if _, ok := arguments[variable.Name()]; !ok {
				problems.add(ProblemSchema, path, "unknown external argument "+variable.Name())
			}
		},
		FunctionRef: func(function *datamodel.FunctionRef, _ datamodel.VisitContext, operand datamodel.ExpressionArg) func() {
			validateFunction(locale, function, operand, variables, profiles, capabilities, limits, path, problems)
			return nil
		},
	})
	if partCount > limits.MaxTemplateParts {
		problems.add(ProblemLimit, path, "template parts exceed "+strconv.Itoa(limits.MaxTemplateParts))
	}
	if markupDepth > limits.MaxMarkupDepth {
		problems.add(ProblemLimit, path, "markup depth exceeds "+strconv.Itoa(limits.MaxMarkupDepth))
	}
	if selected, ok := model.(*datamodel.SelectMessage); ok {
		selectors := selected.Selectors()
		if len(selectors) > limits.MaxSelectors {
			problems.add(ProblemLimit, path, "selectors exceed "+strconv.Itoa(limits.MaxSelectors))
		}
		catchAll := false
		for _, variant := range selected.Variants() {
			keys := variant.Keys()
			if len(keys) != len(selectors) {
				problems.add(ProblemInvalidSyntax, path, "variant key count does not match selectors")
				continue
			}
			all := true
			for _, key := range keys {
				if _, ok := key.(*datamodel.CatchallKey); !ok {
					all = false
				}
			}
			catchAll = catchAll || all
		}
		if !catchAll {
			problems.add(ProblemMissing, path, "select message has no all-other branch")
		}
		validateSelectorKeys(selected, locale, arguments, path, problems)
	}
	return len(problems.items) == before
}

func validateTemplateStructure(model datamodel.Message, limits Limits, path string, problems *problemSet) bool {
	declarations := model.Declarations()
	valid := true
	if len(declarations) > limits.MaxDeclarations {
		problems.add(ProblemLimit, path, "declarations exceed "+strconv.Itoa(limits.MaxDeclarations))
		valid = false
	}
	if selected, ok := model.(*datamodel.SelectMessage); ok && len(selected.Variants()) > limits.MaxVariants {
		problems.add(ProblemLimit, path, "variants exceed "+strconv.Itoa(limits.MaxVariants))
		valid = false
	}
	if !valid {
		return false
	}

	locals := make(map[string]bool, len(declarations))
	for _, declaration := range declarations {
		if _, ok := declaration.(*datamodel.LocalDeclaration); ok {
			locals[declaration.Name()] = true
		}
	}
	dependencies := make(map[string]string, len(locals))
	for _, declaration := range declarations {
		local, ok := declaration.(*datamodel.LocalDeclaration)
		if !ok {
			continue
		}
		variable, ok := local.Value().Arg().(*datamodel.VariableRef)
		if ok && locals[variable.Name()] {
			dependencies[local.Name()] = variable.Name()
		}
	}

	depths := make(map[string]int, len(locals))
	names := sortedMapKeys(locals)
	for _, start := range names {
		if depths[start] != 0 {
			continue
		}
		pathNames := make([]string, 0, limits.MaxLocalDepth+1)
		positions := make(map[string]int)
		current := start
		base := 0
		for {
			if depth := depths[current]; depth != 0 {
				base = depth
				break
			}
			if _, exists := positions[current]; exists {
				problems.add(ProblemCycle, path, "local reference cycle at "+current)
				return false
			}
			positions[current] = len(pathNames)
			pathNames = append(pathNames, current)
			if len(pathNames) > limits.MaxLocalDepth {
				problems.add(ProblemLimit, path, "local reference depth exceeds "+strconv.Itoa(limits.MaxLocalDepth))
				return false
			}
			next, exists := dependencies[current]
			if !exists {
				break
			}
			current = next
		}
		for index := len(pathNames) - 1; index >= 0; index-- {
			base++
			if base > limits.MaxLocalDepth {
				problems.add(ProblemLimit, path, "local reference depth exceeds "+strconv.Itoa(limits.MaxLocalDepth))
				return false
			}
			depths[pathNames[index]] = base
		}
	}
	return true
}

func validatePatternMarkup(pattern datamodel.Pattern, descriptor Descriptor, limits Limits, path string, problems *problemSet) int {
	stack := make([]string, 0)
	maximum := 0
	for _, element := range pattern.Elements() {
		markup, ok := element.(*datamodel.Markup)
		if !ok {
			continue
		}
		if descriptor.Output == OutputPlain {
			problems.add(ProblemSecurity, path, "plain template contains markup")
		}
		if !slices.Contains(descriptor.Markup, markup.Name()) {
			problems.add(ProblemSecurity, path, "markup is not allowlisted: "+markup.Name())
		}
		validateUniversalOptions(markup.Options(), false, limits, path, problems)
		for name := range markup.Options() {
			if !strings.HasPrefix(name, "u:") {
				problems.add(ProblemUnsupported, path, "markup option "+name+" is outside the profile")
			}
		}
		if len(markup.Attributes()) != 0 {
			problems.add(ProblemUnsupported, path, "markup attributes are outside the profile")
		}
		switch markup.Kind() {
		case datamodel.MarkupOpen:
			stack = append(stack, markup.Name())
			if len(stack) > maximum {
				maximum = len(stack)
			}
		case datamodel.MarkupClose:
			if len(stack) == 0 || stack[len(stack)-1] != markup.Name() {
				problems.add(ProblemInvalidSyntax, path, "markup is not balanced")
				continue
			}
			stack = stack[:len(stack)-1]
		case datamodel.MarkupStandalone:
		default:
			problems.add(ProblemInvalidSyntax, path, "unknown markup kind")
		}
	}
	if len(stack) > 0 {
		problems.add(ProblemInvalidSyntax, path, "markup is not closed")
	}
	return maximum
}

type selectorProfile struct {
	argument        ArgumentSpec
	function        string
	numericFunction string
	options         map[string]any
}

func validateSelectorKeys(selected *datamodel.SelectMessage, localeName string, arguments map[string]ArgumentSpec, path string, problems *problemSet) {
	profiles := selectorProfiles(selected, arguments)
	selectors := selected.Selectors()
	for selectorIndex, selector := range selectors {
		profile, ok := profiles[selector.Name()]
		if !ok || profile.function == "" {
			problems.add(ProblemSchema, path, "selector "+selector.Name()+" requires an annotated typed declaration")
			continue
		}
		categories := map[string]bool{}
		if profile.function == "number" || profile.function == "integer" || profile.function == "percent" || profile.function == "offset" {
			selectMode, _ := optionString(profile.options, "select")
			if selectMode != "exact" {
				function := profile.function
				if function == "offset" {
					function = profile.numericFunction
				}
				for _, category := range numberPluralCategories(localeName, function, profile.options) {
					categories[category] = true
				}
			}
		}
		for variantIndex, variant := range selected.Variants() {
			keys := variant.Keys()
			if selectorIndex >= len(keys) {
				continue
			}
			literal, ok := keys[selectorIndex].(*datamodel.Literal)
			if !ok {
				continue
			}
			if !validSelectorLiteral(literal.Value(), profile, categories) {
				problems.add(ProblemSchema, problemPath("%s.variants[%d].keys[%d]", path, variantIndex, selectorIndex), "key is not valid for selector "+selector.Name())
			}
		}
	}
}

func selectorProfiles(selected *datamodel.SelectMessage, arguments map[string]ArgumentSpec) map[string]selectorProfile {
	return declarationProfiles(selected.Declarations(), arguments)
}

func declarationProfiles(declarations []datamodel.Declaration, arguments map[string]ArgumentSpec) map[string]selectorProfile {
	profiles := make(map[string]selectorProfile)
	variables := maps.Clone(arguments)
	for _, declaration := range declarations {
		var expression *datamodel.Expression
		switch declaration := declaration.(type) {
		case *datamodel.InputDeclaration:
			expression = declaration.Value()
		case *datamodel.LocalDeclaration:
			expression = declaration.Value()
		}
		if expression == nil {
			continue
		}
		operand, variableOperand := expression.Arg().(*datamodel.VariableRef)
		argument, known := variables[declaration.Name()]
		if variableOperand {
			if source, exists := variables[operand.Name()]; exists {
				argument, known = source, true
			}
		}
		if !known {
			continue
		}
		profile := selectorProfile{argument: argument}
		if variableOperand {
			if inherited, exists := profiles[operand.Name()]; exists {
				profile = inherited
				profile.argument = argument
			}
		}
		if function := expression.FunctionRef(); function != nil {
			name := function.Name()
			profile.function = name
			if name == "offset" {
				if profile.numericFunction == "" {
					profile.numericFunction = "number"
				}
			} else if style, numeric := functionNumericStyle(name); numeric {
				profile.options = resolvedNumberOptions(style, profile.options, literalOptions(function.Options()))
				profile.numericFunction = name
			} else {
				profile.options = literalOptions(function.Options())
				profile.numericFunction = ""
			}
		} else if variableOperand {
			profile = profiles[operand.Name()]
		}
		profiles[declaration.Name()] = profile
		argument.Name = declaration.Name()
		variables[declaration.Name()] = argument
	}
	return profiles
}

func literalOptions(options datamodel.Options) map[string]any {
	out := make(map[string]any, len(options))
	for name, value := range options {
		if strings.HasPrefix(name, "u:") {
			continue
		}
		if literal, ok := value.(*datamodel.Literal); ok {
			out[name] = literal.Value()
		}
	}
	return out
}

func validSelectorLiteral(key string, profile selectorProfile, categories map[string]bool) bool {
	if key == "null" && (!profile.argument.Required || profile.argument.Nullable) {
		return true
	}
	switch profile.function {
	case "number", "integer", "percent", "offset":
		if categories[key] {
			return true
		}
		return validateDecimal(strings.TrimPrefix(key, "=")) == nil
	case "currency", "unit":
		return validateDecimal(strings.TrimPrefix(key, "=")) == nil
	case "string":
		switch profile.argument.Type {
		case TypeEnum:
			if !norm.NFC.IsNormalString(key) {
				return false
			}
			for _, value := range profile.argument.Values {
				if norm.NFC.String(value) == key {
					return true
				}
			}
			return false
		case TypeBool:
			return key == "true" || key == "false"
		case TypeText:
			return utf8.ValidString(key) && norm.NFC.IsNormalString(key) && !hasUnsafeBidiControls(key)
		}
	}
	return false
}

func inferTemplateVariables(model datamodel.Message, arguments map[string]ArgumentSpec) map[string]ArgumentSpec {
	variables := maps.Clone(arguments)
	for _, declaration := range model.Declarations() {
		var expression *datamodel.Expression
		switch declaration := declaration.(type) {
		case *datamodel.InputDeclaration:
			expression = declaration.Value()
		case *datamodel.LocalDeclaration:
			expression = declaration.Value()
		}
		if expression == nil {
			continue
		}
		var inferred ArgumentSpec
		known := false
		switch operand := expression.Arg().(type) {
		case *datamodel.VariableRef:
			inferred, known = variables[operand.Name()]
		case *datamodel.Literal:
			inferred, known = ArgumentSpec{Type: TypeText}, true
		}
		if !known {
			continue
		}
		if function := expression.FunctionRef(); function != nil && function.Name() == "string" {
			inferred.Type = TypeText
			inferred.Values = nil
		}
		inferred.Name = declaration.Name()
		variables[declaration.Name()] = inferred
	}
	return variables
}

func validateFunction(locale string, function *datamodel.FunctionRef, operand datamodel.ExpressionArg, variables map[string]ArgumentSpec, profiles map[string]selectorProfile, capabilities map[Capability]bool, limits Limits, path string, problems *problemSet) {
	name := function.Name()
	if !functionAllowed(name, capabilities) {
		problems.add(ProblemUnsupported, path, "function "+name)
		return
	}
	options := make(map[string]any, len(function.Options()))
	validOptions := validateUniversalOptions(function.Options(), true, limits, path, problems)
	for option, value := range function.Options() {
		if strings.HasPrefix(option, "u:") {
			continue
		}
		if !functionOptionAllowed(name, option) {
			problems.add(ProblemUnsupported, path, "option "+name+"."+option)
			validOptions = false
		}
		literal, ok := value.(*datamodel.Literal)
		if !ok {
			problems.add(ProblemUnsupported, path, "function options must be literals")
			validOptions = false
			continue
		}
		options[option] = literal.Value()
	}
	var argument ArgumentSpec
	var inherited selectorProfile
	switch operand := operand.(type) {
	case *datamodel.VariableRef:
		var known bool
		argument, known = variables[operand.Name()]
		if !known {
			problems.add(ProblemSchema, path, "function "+name+" has an unknown operand")
			return
		}
		inherited = profiles[operand.Name()]
	case *datamodel.Literal:
		problems.add(ProblemSchema, path, "function "+name+" requires a typed argument")
		return
	default:
		problems.add(ProblemSchema, path, "function "+name+" requires an operand")
		return
	}
	if !functionAccepts(name, argument.Type) {
		problems.add(ProblemSchema, path, "function "+name+" does not accept "+argument.Type.String())
		return
	}
	configuration := options
	if style, numeric := functionNumericStyle(name); numeric && inherited.numericFunction != "" {
		configuration = resolvedNumberOptions(style, inherited.options, options)
	}
	if name == "currency" && argument.Type != TypeMoney {
		if _, exists := configuration["currency"]; !exists {
			problems.add(ProblemSchema, path, "currency function requires Money or a currency option")
			validOptions = false
		}
	}
	if name == "unit" {
		if _, exists := configuration["unit"]; !exists {
			problems.add(ProblemSchema, path, "unit function requires a unit option")
			validOptions = false
		}
	}
	if validOptions {
		if err := validateFunctionConfiguration(locale, name, argument.Type, configuration); err != nil {
			problems.addError(ProblemInvalid, path, err)
		}
	}
}

func validateUniversalOptions(options datamodel.Options, allowDirection bool, limits Limits, path string, problems *problemSet) bool {
	valid := true
	for name, value := range options {
		if !strings.HasPrefix(name, "u:") {
			continue
		}
		if name != "u:id" && name != "u:dir" {
			problems.add(ProblemUnsupported, path, "unrecognized universal option "+name)
			valid = false
			continue
		}
		if name == "u:dir" && !allowDirection {
			problems.add(ProblemUnsupported, path, "u:dir is not valid for markup")
			valid = false
			continue
		}
		literal, ok := value.(*datamodel.Literal)
		if !ok {
			problems.add(ProblemUnsupported, path, "universal option "+name+" must be a literal")
			valid = false
			continue
		}
		text := literal.Value()
		switch name {
		case "u:id":
			if len(text) > limits.MaxIdentifierBytes {
				problems.add(ProblemLimit, path, "u:id exceeds "+strconv.Itoa(limits.MaxIdentifierBytes)+" bytes")
				valid = false
			} else if !validUniversalID(text, limits.MaxIdentifierBytes) {
				problems.add(ProblemInvalid, path, "u:id is empty, unsafe, invalid UTF-8, or not NFC")
				valid = false
			}
		case "u:dir":
			if !oneOf(text, "ltr", "rtl", "auto", "inherit") {
				problems.add(ProblemInvalid, path, "u:dir value "+strconv.Quote(text)+" is invalid")
				valid = false
			}
		}
	}
	return valid
}

func validUniversalID(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value) && norm.NFC.IsNormalString(value) && !hasUnsafeAuthoredBidiControls(value)
}

func withoutUniversalOptions(options map[string]any) map[string]any {
	if options == nil {
		return nil
	}
	out := make(map[string]any, len(options))
	for name, value := range options {
		if !strings.HasPrefix(name, "u:") {
			out[name] = value
		}
	}
	return out
}

func functionNumericStyle(name string) (numericStyle, bool) {
	switch name {
	case "number":
		return styleNumber, true
	case "integer":
		return styleInteger, true
	case "currency":
		return styleCurrency, true
	case "percent":
		return stylePercent, true
	case "unit":
		return styleUnit, true
	default:
		return 0, false
	}
}

func functionAllowed(name string, capabilities map[Capability]bool) bool {
	switch name {
	case "number", "integer", "currency", "percent", "offset", "string":
		return true
	case "date", "time", "datetime":
		return capabilities[CapabilityDateTime]
	case "unit":
		return capabilities[CapabilityUnit]
	default:
		return false
	}
}

func functionAccepts(name string, argument ArgumentType) bool {
	switch name {
	case "number":
		return numericArgument(argument)
	case "integer":
		return integerArgument(argument)
	case "currency":
		return numericArgument(argument) || argument == TypeMoney
	case "percent", "unit", "offset":
		return numericArgument(argument)
	case "string":
		return argument == TypeText || argument == TypeEnum || argument == TypeBool
	case "date":
		return argument == TypeDate || argument == TypeInstant
	case "time":
		return argument == TypeInstant
	case "datetime":
		return argument == TypeInstant
	default:
		return false
	}
}

func validateFunctionConfiguration(locale, name string, argumentType ArgumentType, options map[string]any) error {
	switch name {
	case "number", "integer", "currency", "percent", "unit":
		return validateNumberConfiguration(locale, name, argumentType, options)
	case "offset":
		return validateOffsetConfiguration(options)
	case "string":
		if selectMode, ok := optionString(options, "select"); ok && selectMode != "exact" {
			return fmt.Errorf("string select option %q is invalid", selectMode)
		}
		return nil
	case "date", "time", "datetime":
		return validateDateConfiguration(locale, name, argumentType, options)
	default:
		return fmt.Errorf("function %s is not in the profile", name)
	}
}

func numericArgument(argument ArgumentType) bool {
	return argument == TypeInteger || argument == TypeUnsignedInteger || argument == TypeBigInteger || argument == TypeDecimal
}

func integerArgument(argument ArgumentType) bool {
	return argument == TypeInteger || argument == TypeUnsignedInteger || argument == TypeBigInteger
}

func functionOptionAllowed(function, option string) bool {
	switch function {
	case "number":
		return oneOf(option,
			"select", "minimumIntegerDigits", "minimumFractionDigits", "maximumFractionDigits",
			"minimumSignificantDigits", "maximumSignificantDigits", "roundingIncrement", "roundingMode",
			"roundingPriority", "trailingZeroDisplay", "notation", "compactDisplay", "useGrouping",
			"signDisplay", "numberingSystem", "localeMatcher")
	case "integer":
		return oneOf(option, "select", "minimumIntegerDigits", "maximumSignificantDigits", "useGrouping", "signDisplay", "numberingSystem", "localeMatcher")
	case "currency":
		return oneOf(option,
			"currency", "currencyDisplay", "currencySign", "minimumIntegerDigits", "fractionDigits", "minimumFractionDigits",
			"maximumFractionDigits", "minimumSignificantDigits", "maximumSignificantDigits", "roundingIncrement",
			"roundingMode", "roundingPriority", "trailingZeroDisplay", "notation", "compactDisplay",
			"useGrouping", "signDisplay", "numberingSystem", "localeMatcher")
	case "percent":
		return oneOf(option,
			"select", "minimumFractionDigits", "maximumFractionDigits", "minimumSignificantDigits",
			"maximumSignificantDigits", "roundingMode", "roundingPriority", "trailingZeroDisplay",
			"useGrouping", "signDisplay", "numberingSystem", "localeMatcher")
	case "offset":
		return oneOf(option, "add", "subtract")
	case "unit":
		return oneOf(option,
			"unit", "unitDisplay", "minimumIntegerDigits", "minimumFractionDigits", "maximumFractionDigits",
			"minimumSignificantDigits", "maximumSignificantDigits", "roundingMode", "roundingPriority",
			"trailingZeroDisplay", "useGrouping", "signDisplay", "numberingSystem", "localeMatcher")
	case "string":
		return oneOf(option, "select")
	case "date":
		return oneOf(option,
			"calendar", "numberingSystem", "localeMatcher", "formatMatcher", "weekday", "era",
			"year", "month", "day", "dateStyle")
	case "time":
		return oneOf(option,
			"calendar", "numberingSystem", "localeMatcher", "formatMatcher", "timeZoneName", "dayPeriod",
			"hour", "minute", "second", "hourCycle", "timeStyle")
	case "datetime":
		return oneOf(option,
			"calendar", "numberingSystem", "localeMatcher", "formatMatcher", "timeZoneName", "weekday", "era",
			"year", "month", "day", "dayPeriod", "hour", "minute", "second", "hourCycle", "dateStyle", "timeStyle")
	default:
		return false
	}
}

func oneOf(value string, allowed ...string) bool {
	return slices.Contains(allowed, value)
}

func runtimeFunctions(capabilities map[Capability]bool) map[string]functions.MessageFunction {
	available := map[string]functions.MessageFunction{
		"number":   exactNumberFunction(styleNumber),
		"integer":  exactNumberFunction(styleInteger),
		"currency": exactNumberFunction(styleCurrency),
		"percent":  exactNumberFunction(stylePercent),
		"offset":   exactOffsetFunction,
		"string":   exactStringFunction,
	}
	if capabilities[CapabilityUnit] {
		available["unit"] = exactNumberFunction(styleUnit)
	}
	if capabilities[CapabilityDateTime] {
		available["date"] = exactDateFunction(styleDate)
		available["time"] = exactDateFunction(styleTime)
		available["datetime"] = exactDateFunction(styleDateTime)
	}
	return available
}

func canonicalLocaleList(raw []string, maxBytes int, path string, problems *problemSet) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]string, len(raw))
	for i, value := range raw {
		_, canonical, err := canonicalLocale(value, maxBytes)
		if err != nil {
			problems.addError(ProblemInvalid, problemPath("%s[%d]", path, i), err)
			continue
		}
		if previous, exists := seen[canonical]; exists {
			code := ProblemDuplicate
			if previous != value {
				code = ProblemCollision
			}
			problems.add(code, problemPath("%s[%d]", path, i), canonical)
			continue
		}
		seen[canonical] = value
		out = append(out, canonical)
	}
	return out
}

func validModuleName(value string, maxIdentifierBytes int) bool {
	if value == "" || len(value) > maxIdentifierBytes || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.Contains(value, "..") {
		return false
	}
	for _, segment := range strings.Split(value, ".") {
		if !validIdentifier(segment, maxIdentifierBytes) {
			return false
		}
	}
	return true
}

func validMessageID(value string, maxIdentifierBytes int) bool {
	return validModuleName(value, maxIdentifierBytes)
}

func validKey(key Key, maxIdentifierBytes int) bool {
	value := string(key)
	return strings.Contains(value, ".") && validModuleName(value, maxIdentifierBytes)
}

func validIdentifier(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') || (i > 0 && r == '-') {
			continue
		}
		return false
	}
	return true
}

type artifactCounter struct {
	bytes    int
	items    int
	maxBytes int
	maxItems int
}

func (c *artifactCounter) add(values ...string) bool {
	if c.items > c.maxItems-len(values) {
		return false
	}
	c.items += len(values)
	for _, value := range values {
		if len(value) > c.maxBytes-c.bytes {
			return false
		}
		c.bytes += len(value)
	}
	return true
}

func preflightCatalogCardinality(spec CatalogSpec, limits Limits) error {
	return preflightCatalogCardinalityContext(context.Background(), spec, limits)
}

func preflightCatalogCardinalityContext(ctx context.Context, spec CatalogSpec, limits Limits) error {
	if ctx == nil {
		return fmt.Errorf("%w: catalog context is nil", ErrInvalidCatalog)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	problems := &problemSet{}
	checks := []struct {
		path    string
		actual  int
		maximum int
	}{
		{path: "modules", actual: len(spec.Modules), maximum: limits.MaxModules},
		{path: "supported", actual: len(spec.Supported), maximum: limits.MaxLocales},
		{path: "required", actual: len(spec.Required), maximum: limits.MaxLocales},
		{path: "parents", actual: len(spec.Parents), maximum: limits.MaxLocales},
		{path: "capabilities", actual: len(spec.Capabilities), maximum: 2},
		{path: "overrides", actual: len(spec.Overrides), maximum: limits.MaxTranslations},
	}
	for _, check := range checks {
		if check.actual > check.maximum {
			problems.add(ProblemLimit, check.path, strconv.Itoa(check.actual))
		}
	}
	if err := problems.err(); err != nil {
		return err
	}
	totalMessages := 0
	totalTranslations := len(spec.Overrides)
	for moduleIndex, module := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(module.Messages) > limits.MaxMessages-totalMessages {
			problems.add(ProblemLimit, "messages", strconv.Itoa(len(module.Messages)))
			return problems.err()
		}
		totalMessages += len(module.Messages)
		for messageIndex, message := range module.Messages {
			if err := ctx.Err(); err != nil {
				return err
			}
			path := problemPath("modules[%d].messages[%d]", moduleIndex, messageIndex)
			if len(message.Arguments) > limits.MaxArguments {
				problems.add(ProblemLimit, path+".arguments", strconv.Itoa(len(message.Arguments)))
				return problems.err()
			}
			if len(message.Markup) > limits.MaxMarkupNames {
				problems.add(ProblemLimit, path+".markup", strconv.Itoa(len(message.Markup)))
				return problems.err()
			}
			if totalTranslations >= limits.MaxTranslations || len(message.Translations) > limits.MaxTranslations-totalTranslations-1 {
				problems.add(ProblemLimit, "translations", strconv.Itoa(len(message.Translations)))
				return problems.err()
			}
			totalTranslations += 1 + len(message.Translations)
			for argumentIndex, argument := range message.Arguments {
				if len(argument.Values) > limits.MaxEnumValues {
					problems.add(ProblemLimit, problemPath("%s.arguments[%d].values", path, argumentIndex), strconv.Itoa(len(argument.Values)))
					return problems.err()
				}
			}
		}
	}
	return nil
}

func preflightCatalog(spec CatalogSpec, limits Limits) error {
	return preflightCatalogContext(context.Background(), spec, limits)
}

func preflightCatalogContext(ctx context.Context, spec CatalogSpec, limits Limits) error {
	if ctx == nil {
		return fmt.Errorf("%w: catalog context is nil", ErrInvalidCatalog)
	}
	if err := preflightCatalogCardinalityContext(ctx, spec, limits); err != nil {
		return err
	}
	problems := &problemSet{}
	tooLong := func(path string, actual, maximum int) bool {
		if actual <= maximum {
			return false
		}
		problems.add(ProblemLimit, path, strconv.Itoa(actual))
		return true
	}
	if tooLong("revision", len(spec.Revision), limits.MaxRevisionBytes) ||
		tooLong("profile", len(spec.Profile), limits.MaxIdentifierBytes) ||
		tooLong("source_locale", len(spec.SourceLocale), limits.Locale.MaxTagBytes) ||
		tooLong("default_locale", len(spec.DefaultLocale), limits.Locale.MaxTagBytes) ||
		tooLong("default_time_zone", len(spec.DefaultTimeZone), 255) ||
		tooLong("time_zone_data_version", len(spec.TimeZoneDataVersion), limits.MaxRevisionBytes) {
		return problems.err()
	}
	for i, value := range spec.Supported {
		if err := ctx.Err(); err != nil {
			return err
		}
		if tooLong(problemPath("supported[%d]", i), len(value), limits.Locale.MaxTagBytes) {
			return problems.err()
		}
	}
	for i, value := range spec.Required {
		if err := ctx.Err(); err != nil {
			return err
		}
		if tooLong(problemPath("required[%d]", i), len(value), limits.Locale.MaxTagBytes) {
			return problems.err()
		}
	}
	for i, edge := range spec.Parents {
		if err := ctx.Err(); err != nil {
			return err
		}
		if tooLong(problemPath("parents[%d].locale", i), len(edge.Locale), limits.Locale.MaxTagBytes) || tooLong(problemPath("parents[%d].parent", i), len(edge.Parent), limits.Locale.MaxTagBytes) {
			return problems.err()
		}
	}
	if err := problems.err(); err != nil {
		return err
	}

	counter := artifactCounter{maxBytes: limits.MaxCatalogBytes, maxItems: limits.MaxCatalogItems}
	add := func(path string, values ...string) bool {
		if counter.add(values...) {
			return true
		}
		code := ProblemLimit
		detail := "catalog material exceeds configured bounds"
		problems.add(code, path, detail)
		return false
	}
	if !add("catalog", spec.Revision, spec.Profile, spec.SourceLocale, spec.DefaultLocale, spec.DefaultTimeZone, spec.TimeZoneDataVersion) {
		return problems.err()
	}
	for i, value := range spec.Supported {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !add(problemPath("supported[%d]", i), value) {
			return problems.err()
		}
	}
	for i, value := range spec.Required {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !add(problemPath("required[%d]", i), value) {
			return problems.err()
		}
	}
	for i, edge := range spec.Parents {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !add(problemPath("parents[%d]", i), edge.Locale, edge.Parent) {
			return problems.err()
		}
	}
	totalMessages := 0
	totalTranslations := len(spec.Overrides)
	for moduleIndex, module := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return err
		}
		if tooLong(problemPath("modules[%d].name", moduleIndex), len(module.Name), limits.MaxIdentifierBytes) {
			return problems.err()
		}
		if totalMessages > limits.MaxMessages-len(module.Messages) {
			problems.add(ProblemLimit, "messages", strconv.Itoa(totalMessages+len(module.Messages)))
			return problems.err()
		}
		totalMessages += len(module.Messages)
		if !add(problemPath("modules[%d]", moduleIndex), module.Name) {
			return problems.err()
		}
		for messageIndex, message := range module.Messages {
			if err := ctx.Err(); err != nil {
				return err
			}
			path := problemPath("modules[%d].messages[%d]", moduleIndex, messageIndex)
			if tooLong(path+".id", len(message.ID), limits.MaxIdentifierBytes) ||
				tooLong(path+".key", len(message.Key), limits.MaxIdentifierBytes) ||
				tooLong(path+".revision", len(message.Revision), limits.MaxRevisionBytes) ||
				tooLong(path+".source", len(message.Source), limits.MaxTemplateBytes) ||
				tooLong(path+".description", len(message.Description), limits.MaxDescriptionBytes) {
				return problems.err()
			}
			if len(message.Arguments) > limits.MaxArguments {
				problems.add(ProblemLimit, path+".arguments", strconv.Itoa(len(message.Arguments)))
				return problems.err()
			}
			if len(message.Markup) > limits.MaxMarkupNames {
				problems.add(ProblemLimit, path+".markup", strconv.Itoa(len(message.Markup)))
				return problems.err()
			}
			if 1+len(message.Translations) > limits.MaxTranslations-totalTranslations {
				problems.add(ProblemLimit, "translations", strconv.Itoa(totalTranslations+1+len(message.Translations)))
				return problems.err()
			}
			totalTranslations += 1 + len(message.Translations)
			if !add(path, message.ID, string(message.Key), message.Revision, message.Source, message.Description) {
				return problems.err()
			}
			for argumentIndex, argument := range message.Arguments {
				if err := ctx.Err(); err != nil {
					return err
				}
				argumentPath := problemPath("%s.arguments[%d]", path, argumentIndex)
				if tooLong(argumentPath+".name", len(argument.Name), limits.MaxIdentifierBytes) {
					return problems.err()
				}
				if len(argument.Values) > limits.MaxEnumValues {
					problems.add(ProblemLimit, argumentPath+".values", strconv.Itoa(len(argument.Values)))
					return problems.err()
				}
				if !add(argumentPath, argument.Name) {
					return problems.err()
				}
				for valueIndex, value := range argument.Values {
					if tooLong(problemPath("%s.values[%d]", argumentPath, valueIndex), len(value), limits.MaxEnumValueBytes) {
						return problems.err()
					}
					if !add(problemPath("%s.values[%d]", argumentPath, valueIndex), value) {
						return problems.err()
					}
				}
			}
			for markupIndex, markup := range message.Markup {
				if err := ctx.Err(); err != nil {
					return err
				}
				if tooLong(problemPath("%s.markup[%d]", path, markupIndex), len(markup), limits.MaxIdentifierBytes) {
					return problems.err()
				}
				if !add(problemPath("%s.markup[%d]", path, markupIndex), markup) {
					return problems.err()
				}
			}
			for translationIndex, translation := range message.Translations {
				if err := ctx.Err(); err != nil {
					return err
				}
				translationPath := problemPath("%s.translations[%d]", path, translationIndex)
				if tooLong(translationPath+".locale", len(translation.Locale), limits.Locale.MaxTagBytes) ||
					tooLong(translationPath+".text", len(translation.Text), limits.MaxTemplateBytes) ||
					tooLong(translationPath+".contract_revision", len(translation.ContractRevision), limits.MaxRevisionBytes) ||
					tooLong(translationPath+".source_digest", len(translation.SourceDigest), sha256.Size*2) ||
					tooLong(translationPath+".review_digest", len(translation.ReviewDigest), sha256.Size*2) {
					return problems.err()
				}
				if !add(problemPath("%s.translations[%d]", path, translationIndex), translation.Locale, translation.Text, translation.ContractRevision, translation.SourceDigest, translation.ReviewDigest) {
					return problems.err()
				}
			}
		}
	}
	for i, override := range spec.Overrides {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := problemPath("overrides[%d]", i)
		if tooLong(path+".key", len(override.Key), limits.MaxIdentifierBytes) ||
			tooLong(path+".locale", len(override.Locale), limits.Locale.MaxTagBytes) ||
			tooLong(path+".text", len(override.Text), limits.MaxTemplateBytes) ||
			tooLong(path+".contract_revision", len(override.ContractRevision), limits.MaxRevisionBytes) ||
			tooLong(path+".source_digest", len(override.SourceDigest), sha256.Size*2) ||
			tooLong(path+".review_digest", len(override.ReviewDigest), sha256.Size*2) {
			return problems.err()
		}
		if !add(problemPath("overrides[%d]", i), string(override.Key), override.Locale, override.Text, override.ContractRevision, override.SourceDigest, override.ReviewDigest) {
			return problems.err()
		}
	}
	return nil
}

func validateRequiredLocalesContext(ctx context.Context, snapshot *Snapshot, problems *problemSet) error {
	if ctx == nil {
		return fmt.Errorf("%w: required locale validation context is nil", ErrInvalidCatalog)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return nil
	}
	keys, err := snapshotKeysContext(ctx, snapshot)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		record := snapshot.records[key]
		for _, locale := range snapshot.required {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, ok := record.templates[locale]; !ok {
				problems.add(ProblemMissing, "messages."+string(key)+".translations."+locale, "required locale is missing")
			}
		}
	}
	return ctx.Err()
}

func checkedLimits(limits Limits) (Limits, error) {
	localeLimits, err := checkedLocaleLimits(limits.Locale)
	if err != nil {
		return Limits{}, err
	}
	limits.Locale = localeLimits
	defaultInt(&limits.MaxModules, 64)
	defaultInt(&limits.MaxMessages, 4096)
	defaultInt(&limits.MaxTranslations, 32768)
	defaultInt(&limits.MaxLocales, 128)
	defaultInt(&limits.MaxArguments, 64)
	defaultInt(&limits.MaxEnumValues, 256)
	defaultInt(&limits.MaxMarkupNames, 64)
	defaultInt(&limits.MaxCatalogItems, 1<<20)
	defaultInt(&limits.MaxIdentifierBytes, 128)
	defaultInt(&limits.MaxRevisionBytes, 512)
	defaultInt(&limits.MaxDescriptionBytes, 16384)
	defaultInt(&limits.MaxEnumValueBytes, 1024)
	defaultInt(&limits.MaxArgumentBytes, 65536)
	defaultInt(&limits.MaxBigIntegerBits, maxBigIntegerBits)
	defaultInt(&limits.MaxTemplateBytes, 65536)
	defaultInt(&limits.MaxCatalogBytes, 64<<20)
	defaultInt(&limits.MaxDeclarations, 256)
	defaultInt(&limits.MaxSelectors, 8)
	defaultInt(&limits.MaxVariants, 1024)
	defaultInt(&limits.MaxLocalDepth, 32)
	defaultInt(&limits.MaxTemplateParts, 2048)
	defaultInt(&limits.MaxMarkupDepth, 32)
	defaultInt(&limits.MaxOutputBytes, 1<<20)
	defaultInt(&limits.MaxOutputParts, 4096)
	defaultInt(&limits.MaxExplainSteps, 64)
	if limits.MaxModules < 1 || limits.MaxModules > 1024 || limits.MaxMessages < 1 || limits.MaxMessages > 100000 || limits.MaxTranslations < 1 || limits.MaxTranslations > 1000000 || limits.MaxLocales < 1 || limits.MaxLocales > 1024 || limits.MaxArguments < 1 || limits.MaxArguments > 256 || limits.MaxEnumValues < 1 || limits.MaxEnumValues > 4096 || limits.MaxMarkupNames < 1 || limits.MaxMarkupNames > 4096 || limits.MaxCatalogItems < 1 || limits.MaxCatalogItems > 1<<24 || limits.MaxIdentifierBytes < 1 || limits.MaxIdentifierBytes > maximumIdentifierBytes || limits.MaxRevisionBytes < 1 || limits.MaxRevisionBytes > 1<<20 || limits.MaxDescriptionBytes < 1 || limits.MaxDescriptionBytes > 4<<20 || limits.MaxEnumValueBytes < 1 || limits.MaxEnumValueBytes > 1<<20 || limits.MaxArgumentBytes < 1 || limits.MaxArgumentBytes > 16<<20 || limits.MaxBigIntegerBits < 1 || limits.MaxBigIntegerBits > maxBigIntegerBits || limits.MaxTemplateBytes < 1 || limits.MaxTemplateBytes > 4<<20 || limits.MaxCatalogBytes < 1 || limits.MaxCatalogBytes > 1<<30 || limits.MaxDeclarations < 1 || limits.MaxDeclarations > 4096 || limits.MaxSelectors < 1 || limits.MaxSelectors > 32 || limits.MaxVariants < 1 || limits.MaxVariants > 65536 || limits.MaxLocalDepth < 1 || limits.MaxLocalDepth > 256 || limits.MaxTemplateParts < 1 || limits.MaxTemplateParts > 65536 || limits.MaxMarkupDepth < 1 || limits.MaxMarkupDepth > 256 || limits.MaxOutputBytes < 1 || limits.MaxOutputBytes > 64<<20 || limits.MaxOutputParts < 1 || limits.MaxOutputParts > 65536 || limits.MaxExplainSteps < 1 || limits.MaxExplainSteps > 1024 {
		return Limits{}, fmt.Errorf("%w: catalog limits are outside their supported bounds", ErrLimitExceeded)
	}
	return limits, nil
}

func defaultInt(target *int, value int) {
	if *target == 0 {
		*target = value
	}
}

func descriptorDigest(descriptor Descriptor) string {
	hash := sha256.New()
	writeDigestField(hash.Write, "domain", "frostgrove.i18n.contract-digest/v2")
	writeDigestField(hash.Write, "key", string(descriptor.Key))
	writeDigestField(hash.Write, "revision", descriptor.Revision)
	writeDigestField(hash.Write, "output", descriptor.Output.String())
	writeDigestField(hash.Write, "arguments.count", strconv.Itoa(len(descriptor.Arguments)))
	for index, argument := range descriptor.Arguments {
		prefix := "arguments." + strconv.Itoa(index) + "."
		writeDigestField(hash.Write, prefix+"name", argument.Name)
		writeDigestField(hash.Write, prefix+"type", argument.Type.String())
		writeDigestField(hash.Write, prefix+"required", strconv.FormatBool(argument.Required))
		writeDigestField(hash.Write, prefix+"nullable", strconv.FormatBool(argument.Nullable))
		writeDigestField(hash.Write, prefix+"values.count", strconv.Itoa(len(argument.Values)))
		for valueIndex, value := range argument.Values {
			writeDigestField(hash.Write, prefix+"values."+strconv.Itoa(valueIndex), value)
		}
	}
	writeDigestField(hash.Write, "markup.count", strconv.Itoa(len(descriptor.Markup)))
	for index, markup := range descriptor.Markup {
		writeDigestField(hash.Write, "markup."+strconv.Itoa(index), markup)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func sourceReviewDigest(profile, sourceLocale string, key Key, contractHash, source, description string) string {
	hash := sha256.New()
	writeDigestField(hash.Write, "domain", "frostgrove.i18n.source-review-digest/v3")
	writeDigestField(hash.Write, "profile", profile)
	writeDigestField(hash.Write, "source_locale", sourceLocale)
	writeDigestField(hash.Write, "key", string(key))
	writeDigestField(hash.Write, "contract", contractHash)
	writeDigestField(hash.Write, "source", source)
	writeDigestField(hash.Write, "description", description)
	return hex.EncodeToString(hash.Sum(nil))
}

func snapshotDigest(snapshot *Snapshot) string {
	digest, _ := snapshotDigestContext(context.Background(), snapshot)
	return digest
}

func snapshotDigestContext(ctx context.Context, snapshot *Snapshot) (string, error) {
	hash := sha256.New()
	writeDigestField(hash.Write, "domain", "frostgrove.i18n.snapshot-digest/v2")
	writeDigestField(hash.Write, "profile", snapshot.profile)
	writeDigestField(hash.Write, "engine", EngineVersion)
	writeDigestField(hash.Write, "locale_data", LocaleDataVersion)
	writeDigestField(hash.Write, "time_zone_data_model", TimeZoneDataModel)
	writeDigestField(hash.Write, "source_locale", snapshot.sourceLocale)
	writeDigestField(hash.Write, "default_locale", snapshot.defaultLocale)
	writeDigestField(hash.Write, "default_time_zone", snapshot.defaultTimeZone)
	writeDigestField(hash.Write, "time_zone_data_version", snapshot.timeZoneDataVersion)
	writeDigestField(hash.Write, "match_mode", strconv.Itoa(int(snapshot.matchMode)))
	writeDigestField(hash.Write, "default_on_miss", strconv.FormatBool(snapshot.defaultOnMiss))
	writeDigestField(hash.Write, "highest_layer", snapshot.highestLayer.String())
	writeLimitsDigest(hash.Write, snapshot.limits)
	writeDigestField(hash.Write, "capabilities.count", "2")
	for _, capability := range []Capability{CapabilityDateTime, CapabilityUnit} {
		prefix := "capabilities." + strconv.Itoa(int(capability)) + "."
		writeDigestField(hash.Write, prefix+"name", capability.String())
		writeDigestField(hash.Write, prefix+"enabled", strconv.FormatBool(snapshot.capabilities[capability]))
	}
	writeDigestField(hash.Write, "supported.count", strconv.Itoa(len(snapshot.supported)))
	for index, locale := range snapshot.supported {
		writeDigestField(hash.Write, "supported."+strconv.Itoa(index), locale)
	}
	writeDigestField(hash.Write, "required.count", strconv.Itoa(len(snapshot.required)))
	for index, locale := range snapshot.required {
		writeDigestField(hash.Write, "required."+strconv.Itoa(index), locale)
	}
	parentKeys := sortedMapKeys(snapshot.parents)
	writeDigestField(hash.Write, "parents.count", strconv.Itoa(len(parentKeys)))
	for index, locale := range parentKeys {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		prefix := "parents." + strconv.Itoa(index) + "."
		writeDigestField(hash.Write, prefix+"locale", locale)
		writeDigestField(hash.Write, prefix+"parent", snapshot.parents[locale])
	}
	keys, err := snapshotKeysContext(ctx, snapshot)
	if err != nil {
		return "", err
	}
	writeDigestField(hash.Write, "messages.count", strconv.Itoa(len(keys)))
	for index, key := range keys {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		record := snapshot.records[key]
		prefix := "messages." + strconv.Itoa(index) + "."
		writeDigestField(hash.Write, prefix+"key", string(key))
		writeDigestField(hash.Write, prefix+"contract", record.contractHash)
		writeDigestField(hash.Write, prefix+"source", record.sourceDigest)
		writeDigestField(hash.Write, prefix+"description", record.descriptor.Description)
		writeDigestField(hash.Write, prefix+"override", strconv.Itoa(int(record.descriptor.Override)))
		writeDigestField(hash.Write, prefix+"allow_empty", strconv.FormatBool(record.descriptor.AllowEmpty))
		writeDigestField(hash.Write, prefix+"public", strconv.FormatBool(record.descriptor.Public))
		locales := sortedMapKeys(record.templates)
		writeDigestField(hash.Write, prefix+"templates.count", strconv.Itoa(len(locales)))
		for localeIndex, locale := range locales {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			translation := record.templates[locale]
			templatePrefix := prefix + "templates." + strconv.Itoa(localeIndex) + "."
			writeDigestField(hash.Write, templatePrefix+"locale", locale)
			writeDigestField(hash.Write, templatePrefix+"layer", translation.layer.String())
			writeDigestField(hash.Write, templatePrefix+"text", translation.text)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), ctx.Err()
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func writeDigest(write func([]byte) (int, error), value string) {
	length := strconv.Itoa(len(value))
	_, _ = write([]byte(length))
	_, _ = write([]byte{':'})
	_, _ = write([]byte(value))
	_, _ = write([]byte{'\n'})
}

func writeDigestField(write func([]byte) (int, error), name, value string) {
	writeDigest(write, name)
	writeDigest(write, value)
}

func writeLimitsDigest(write func([]byte) (int, error), limits Limits) {
	values := []struct {
		name  string
		value int
	}{
		{"locale.max_choices", limits.Locale.MaxChoices},
		{"locale.max_header_bytes", limits.Locale.MaxHeaderBytes},
		{"locale.max_ranges", limits.Locale.MaxRanges},
		{"locale.max_supported", limits.Locale.MaxSupported},
		{"locale.max_tag_bytes", limits.Locale.MaxTagBytes},
		{"locale.max_fallback_depth", limits.Locale.MaxFallbackDepth},
		{"max_modules", limits.MaxModules},
		{"max_messages", limits.MaxMessages},
		{"max_translations", limits.MaxTranslations},
		{"max_locales", limits.MaxLocales},
		{"max_arguments", limits.MaxArguments},
		{"max_enum_values", limits.MaxEnumValues},
		{"max_markup_names", limits.MaxMarkupNames},
		{"max_catalog_items", limits.MaxCatalogItems},
		{"max_identifier_bytes", limits.MaxIdentifierBytes},
		{"max_revision_bytes", limits.MaxRevisionBytes},
		{"max_description_bytes", limits.MaxDescriptionBytes},
		{"max_enum_value_bytes", limits.MaxEnumValueBytes},
		{"max_argument_bytes", limits.MaxArgumentBytes},
		{"max_big_integer_bits", limits.MaxBigIntegerBits},
		{"max_template_bytes", limits.MaxTemplateBytes},
		{"max_catalog_bytes", limits.MaxCatalogBytes},
		{"max_declarations", limits.MaxDeclarations},
		{"max_selectors", limits.MaxSelectors},
		{"max_variants", limits.MaxVariants},
		{"max_local_depth", limits.MaxLocalDepth},
		{"max_template_parts", limits.MaxTemplateParts},
		{"max_markup_depth", limits.MaxMarkupDepth},
		{"max_output_bytes", limits.MaxOutputBytes},
		{"max_output_parts", limits.MaxOutputParts},
		{"max_explain_steps", limits.MaxExplainSteps},
	}
	writeDigestField(write, "limits.count", strconv.Itoa(len(values)))
	for _, value := range values {
		writeDigestField(write, "limits."+value.name, strconv.Itoa(value.value))
	}
}

func snapshotCatalogMaterial(snapshot *Snapshot) (int, int) {
	bytes, items, _ := snapshotCatalogMaterialContext(context.Background(), snapshot)
	return bytes, items
}

func snapshotCatalogMaterialContext(ctx context.Context, snapshot *Snapshot) (int, int, error) {
	if snapshot == nil {
		return 0, 0, nil
	}
	bytes := 0
	items := 0
	add := func(values ...string) {
		items += len(values)
		for _, value := range values {
			bytes += len(value)
		}
	}
	add(snapshot.revision, snapshot.sourceLocale, snapshot.defaultLocale, snapshot.defaultTimeZone, snapshot.timeZoneDataVersion, snapshot.matchMode.String(), snapshot.highestLayer.String())
	for _, locale := range snapshot.supported {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		add(locale)
	}
	for _, locale := range snapshot.required {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		add(locale)
	}
	for child, parent := range snapshot.parents {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		add(child, parent)
	}
	for capability := range snapshot.capabilities {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		add(capability.String())
	}
	for key, record := range snapshot.records {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		add(string(key), record.descriptor.Revision, record.descriptor.Description, record.descriptor.Output.String(), record.descriptor.Override.String(), record.contractHash, record.sourceDigest)
		for _, argument := range record.descriptor.Arguments {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
			add(argument.Name, argument.Type.String())
			for _, value := range argument.Values {
				if err := ctx.Err(); err != nil {
					return 0, 0, err
				}
				add(value)
			}
		}
		for _, markup := range record.descriptor.Markup {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
			add(markup)
		}
		for locale, translation := range record.templates {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
			add(locale, translation.layer.String(), translation.text)
		}
	}
	return bytes, items, ctx.Err()
}

func snapshotCatalogBytes(snapshot *Snapshot) int {
	bytes, _ := snapshotCatalogMaterial(snapshot)
	return bytes
}

func snapshotCatalogItems(snapshot *Snapshot) int {
	_, items := snapshotCatalogMaterial(snapshot)
	return items
}
