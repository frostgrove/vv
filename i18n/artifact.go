package i18n

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const ArtifactVersion = "frostgrove.i18n.catalog/v1"

var (
	ErrInvalidArtifact      = errors.New("i18n: invalid compiled artifact")
	ErrIncompatibleArtifact = errors.New("i18n: incompatible compiled artifact")
	ErrArtifactIO           = errors.New("i18n: artifact I/O")
)

const (
	defaultArtifactBytes   = 128 << 20
	defaultArtifactDepth   = 64
	defaultArtifactMembers = 1 << 22
	maximumArtifactBytes   = 1 << 30
	maximumArtifactDepth   = 256
	maximumArtifactMembers = 1 << 24
)

type ArtifactLimits struct {
	MaxBytes   int
	MaxDepth   int
	MaxMembers int
}

func DefaultArtifactLimits() ArtifactLimits {
	return ArtifactLimits{
		MaxBytes:   defaultArtifactBytes,
		MaxDepth:   defaultArtifactDepth,
		MaxMembers: defaultArtifactMembers,
	}
}

type Compiler struct {
	Limits        ArtifactLimits
	CatalogLimits Limits
	Observer      Observer
}

type Loader struct {
	Limits        ArtifactLimits
	CatalogLimits Limits
	Observer      Observer
}

func Compile(spec CatalogSpec) ([]byte, error) {
	return (Compiler{}).Compile(spec)
}

func CompileContext(ctx context.Context, spec CatalogSpec) ([]byte, error) {
	return (Compiler{}).CompileContext(ctx, spec)
}

func (c Compiler) Compile(spec CatalogSpec) (raw []byte, err error) {
	return c.CompileContext(context.Background(), spec)
}

func (c Compiler) CompileContext(ctx context.Context, spec CatalogSpec) (raw []byte, err error) {
	observer := c.Observer
	if observer == nil {
		observer = spec.Observer
	}
	started := time.Now()
	defer func() {
		notifyTerminalOperation(ctx, observer, OperationCompile, started, err)
	}()
	if ctx == nil {
		return nil, fmt.Errorf("%w: compile context is nil", ErrInvalidArtifact)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	artifactLimits, err := checkedArtifactLimits(c.Limits)
	if err != nil {
		return nil, err
	}
	declaredLimits, err := checkedLimits(spec.Limits)
	if err != nil {
		return nil, err
	}
	localCeiling, err := checkedLimits(c.CatalogLimits)
	if err != nil {
		return nil, err
	}
	if err := requireCatalogLimitCeiling(declaredLimits, localCeiling); err != nil {
		return nil, err
	}
	if err := preflightCatalogCardinalityContext(ctx, spec, declaredLimits); err != nil {
		return nil, err
	}
	if err := checkCatalogArtifactFloorContext(ctx, spec, declaredLimits, artifactLimits); err != nil {
		return nil, err
	}
	if err := preflightCatalogContext(ctx, spec, declaredLimits); err != nil {
		return nil, err
	}
	spec.Limits = declaredLimits
	snapshot, err := NewContext(ctx, spec)
	if err != nil {
		return nil, err
	}
	return c.encode(ctx, snapshot)
}

func Encode(snapshot *Snapshot) ([]byte, error) {
	return (Compiler{}).Encode(snapshot)
}

func EncodeContext(ctx context.Context, snapshot *Snapshot) ([]byte, error) {
	return (Compiler{}).EncodeContext(ctx, snapshot)
}

func (c Compiler) Encode(snapshot *Snapshot) (raw []byte, err error) {
	return c.EncodeContext(context.Background(), snapshot)
}

func (c Compiler) EncodeContext(ctx context.Context, snapshot *Snapshot) (raw []byte, err error) {
	observer := c.Observer
	if observer == nil && snapshot != nil {
		observer = snapshot.observer
	}
	started := time.Now()
	defer func() {
		notifyTerminalOperation(ctx, observer, OperationCompile, started, err)
	}()
	return c.encode(ctx, snapshot)
}

func (c Compiler) encode(ctx context.Context, snapshot *Snapshot) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: encode context is nil", ErrInvalidArtifact)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	artifactLimits, err := checkedArtifactLimits(c.Limits)
	if err != nil {
		return nil, err
	}
	catalogLimits, err := checkedLimits(c.CatalogLimits)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, fmt.Errorf("%w: %w: controller snapshot is nil", ErrInvalidArtifact, ErrInvalidCatalog)
	}
	if err := requireCatalogLimitCeiling(snapshot.limits, catalogLimits); err != nil {
		return nil, err
	}
	if snapshotCatalogBytes(snapshot) > artifactLimits.MaxBytes {
		return nil, fmt.Errorf("%w: snapshot material cannot fit within artifact bytes", ErrLimitExceeded)
	}
	if err := validateControllerSnapshot(snapshot); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	document, err := artifactFromSnapshotContext(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	raw, err := marshalCanonicalArtifactContext(ctx, document, artifactLimits)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func Load(ctx context.Context, source io.Reader) (*Snapshot, error) {
	return (Loader{}).Load(ctx, source)
}

func LoadFS(ctx context.Context, filesystem fs.FS, name string) (*Snapshot, error) {
	return (Loader{}).LoadFS(ctx, filesystem, name)
}

func (l Loader) Load(ctx context.Context, source io.Reader) (snapshot *Snapshot, err error) {
	started := time.Now()
	defer func() {
		notifyTerminalOperation(ctx, l.Observer, OperationLoad, started, err)
	}()
	return l.load(ctx, source)
}

func (l Loader) load(ctx context.Context, source io.Reader) (*Snapshot, error) {
	limits, err := checkedArtifactLimits(l.Limits)
	if err != nil {
		return nil, err
	}
	catalogLimits, err := checkedLimits(l.CatalogLimits)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidArtifact)
	}
	if nilInterface(source) {
		return nil, fmt.Errorf("%w: source is nil", ErrInvalidArtifact)
	}
	raw, err := readBoundedInput(ctx, source, limits.MaxBytes, ErrArtifactIO)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("%w: input is not valid UTF-8", ErrInvalidArtifact)
	}
	if err := scanArtifactJSON(ctx, raw, limits); err != nil {
		return nil, err
	}
	var document artifactDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: decoding document: %w", ErrInvalidArtifact, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		return nil, fmt.Errorf("%w: trailing data: %w", ErrInvalidArtifact, err)
	}
	snapshot, err := snapshotFromArtifact(ctx, document, catalogLimits, l.Observer)
	if err != nil {
		return nil, err
	}
	canonical, err := (Compiler{Limits: limits, CatalogLimits: catalogLimits}).encode(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(raw, canonical) {
		return nil, fmt.Errorf("%w: input is not the canonical representation", ErrInvalidArtifact)
	}
	return snapshot, nil
}

func (l Loader) LoadFS(ctx context.Context, filesystem fs.FS, name string) (snapshot *Snapshot, err error) {
	started := time.Now()
	defer func() {
		notifyTerminalOperation(ctx, l.Observer, OperationLoad, started, err)
	}()
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidArtifact)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := checkedArtifactLimits(l.Limits); err != nil {
		return nil, err
	}
	if _, err := checkedLimits(l.CatalogLimits); err != nil {
		return nil, err
	}
	if nilInterface(filesystem) {
		return nil, fmt.Errorf("%w: filesystem is nil", ErrInvalidArtifact)
	}
	if !fs.ValidPath(name) {
		return nil, fmt.Errorf("%w: artifact path %q is invalid", ErrInvalidArtifact, name)
	}
	file, err := filesystem.Open(name)
	if err != nil {
		return nil, fmt.Errorf("%w: opening %q: %w", ErrArtifactIO, name, err)
	}
	snapshot, loadErr := l.load(ctx, file)
	closeErr := file.Close()
	if loadErr != nil && closeErr != nil {
		return nil, errors.Join(loadErr, fmt.Errorf("%w: closing %q: %w", ErrArtifactIO, name, closeErr))
	}
	if loadErr != nil {
		return nil, loadErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w: closing %q: %w", ErrArtifactIO, name, closeErr)
	}
	return snapshot, nil
}

func checkedArtifactLimits(limits ArtifactLimits) (ArtifactLimits, error) {
	defaults := DefaultArtifactLimits()
	defaultInt(&limits.MaxBytes, defaults.MaxBytes)
	defaultInt(&limits.MaxDepth, defaults.MaxDepth)
	defaultInt(&limits.MaxMembers, defaults.MaxMembers)
	if limits.MaxBytes < 1 || limits.MaxBytes > maximumArtifactBytes || limits.MaxDepth < 1 || limits.MaxDepth > maximumArtifactDepth || limits.MaxMembers < 1 || limits.MaxMembers > maximumArtifactMembers {
		return ArtifactLimits{}, fmt.Errorf("%w: artifact limits are outside their supported bounds", ErrLimitExceeded)
	}
	return limits, nil
}

func checkCatalogArtifactFloor(spec CatalogSpec, limits Limits, artifactLimits ArtifactLimits) error {
	return checkCatalogArtifactFloorContext(context.Background(), spec, limits, artifactLimits)
}

func checkCatalogArtifactFloorContext(ctx context.Context, spec CatalogSpec, limits Limits, artifactLimits ArtifactLimits) error {
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
	digest := strings.Repeat("0", sha256.Size*2)
	document := artifactDocument{
		Artifact: ArtifactVersion, Profile: profile, Engine: EngineVersion, LocaleData: LocaleDataVersion,
		TimeZoneDataModel: TimeZoneDataModel, SemanticDigest: digest,
		Snapshot: artifactSnapshot{
			Revision: spec.Revision, SourceLocale: sourceLocale, DefaultLocale: defaultLocale,
			DefaultTimeZone: defaultTimeZone, TimeZoneDataVersion: spec.TimeZoneDataVersion,
			MatchMode: spec.MatchMode.String(), HighestLayer: LayerModule.String(),
			DefaultOnMiss: spec.DefaultOnMiss, Supported: []string{}, Required: []string{},
			Parents: []artifactParent{}, Capabilities: []string{}, Limits: encodeArtifactLimits(limits),
			Messages: []artifactMessage{},
		},
	}
	counter := artifactFloorCounter{limits: artifactLimits, ctx: ctx}
	failed := func() error {
		if counter.err != nil {
			return counter.err
		}
		return fmt.Errorf("%w: declared catalog cannot fit within artifact bounds", ErrLimitExceeded)
	}
	if !counter.addValue(document, 1) {
		return failed()
	}
	overrides, err := minimumOverrideTemplatesContext(ctx, spec, limits)
	if err != nil {
		return err
	}
	for index, localeName := range spec.Supported {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !counter.addArrayValue(canonicalArtifactLocale(localeName, limits.Locale.MaxTagBytes), index, 4) {
			return failed()
		}
	}
	for index, localeName := range spec.Required {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !counter.addArrayValue(canonicalArtifactLocale(localeName, limits.Locale.MaxTagBytes), index, 4) {
			return failed()
		}
	}
	for index, edge := range spec.Parents {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent := artifactParent{
			Locale: canonicalArtifactLocale(edge.Locale, limits.Locale.MaxTagBytes),
			Parent: canonicalArtifactLocale(edge.Parent, limits.Locale.MaxTagBytes),
		}
		if !counter.addArrayValue(parent, index, 4) {
			return failed()
		}
	}
	for index, capability := range spec.Capabilities {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !counter.addArrayValue(capability.String(), index, 4) {
			return failed()
		}
	}
	messageIndex := 0
	for _, module := range spec.Modules {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, message := range module.Messages {
			if err := ctx.Err(); err != nil {
				return err
			}
			key := message.Key
			if key == "" {
				key = Qualify(module.Name, message.ID)
			}
			encoded := artifactMessage{
				Key: string(key), Revision: message.Revision, Description: message.Description,
				Output: message.Output.String(), Override: message.Override.String(), AllowEmpty: message.AllowEmpty,
				Public: message.Public, ContractDigest: digest, SourceDigest: digest,
				Arguments: []artifactArgument{}, Markup: []string{}, Templates: []artifactTemplate{},
			}
			if !counter.addArrayValue(encoded, messageIndex, 4) {
				return failed()
			}
			messageIndex++
			for argumentIndex, argument := range message.Arguments {
				values := argument.Values
				if values == nil {
					values = []string{}
				}
				encodedArgument := artifactArgument{
					Name: argument.Name, Type: argument.Type.String(), Required: argument.Required,
					Nullable: argument.Nullable, Values: values,
				}
				if !counter.addArrayValue(encodedArgument, argumentIndex, 6) {
					return failed()
				}
			}
			for markupIndex, markup := range message.Markup {
				if !counter.addArrayValue(markup, markupIndex, 6) {
					return failed()
				}
			}
			source := artifactTemplate{Locale: sourceLocale, Layer: LayerModule.String(), Text: message.Source}
			source = minimumEffectiveTemplate(key, source, overrides)
			if !counter.addArrayValue(source, 0, 6) {
				return failed()
			}
			for translationIndex, translation := range message.Translations {
				encodedTranslation := artifactTemplate{
					Locale: canonicalArtifactLocale(translation.Locale, limits.Locale.MaxTagBytes),
					Layer:  LayerModule.String(), Text: translation.Text,
				}
				encodedTranslation = minimumEffectiveTemplate(key, encodedTranslation, overrides)
				if !counter.addArrayValue(encodedTranslation, translationIndex+1, 6) {
					return failed()
				}
			}
		}
	}
	return nil
}

type artifactFloorCounter struct {
	limits  ArtifactLimits
	written int
	members int
	ctx     context.Context
	err     error
}

func (c *artifactFloorCounter) addArrayValue(value any, index, depth int) bool {
	if c.err != nil {
		return false
	}
	if c.ctx != nil {
		if err := c.ctx.Err(); err != nil {
			c.err = err
			return false
		}
	}
	if index > 0 && !c.addBytes(1) {
		return false
	}
	if c.members >= c.limits.MaxMembers {
		return false
	}
	c.members++
	return c.addValue(value, depth)
}

func (c *artifactFloorCounter) addValue(value any, depth int) bool {
	remainingBytes := c.limits.MaxBytes - c.written
	remainingMembers := c.limits.MaxMembers - c.members
	encoder := boundedArtifactJSON{
		limits: ArtifactLimits{
			MaxBytes: remainingBytes, MaxDepth: c.limits.MaxDepth, MaxMembers: remainingMembers,
		},
		countOnly: true,
		ctx:       c.ctx,
	}
	if err := encoder.value(reflect.ValueOf(value), depth); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			c.err = err
			return false
		}
		return !errors.Is(err, ErrLimitExceeded)
	}
	if !c.addBytes(encoder.written) {
		return false
	}
	c.members += encoder.members
	return true
}

func (c *artifactFloorCounter) addBytes(count int) bool {
	if count > c.limits.MaxBytes-c.written {
		return false
	}
	c.written += count
	return true
}

func minimumOverrideTemplates(spec CatalogSpec, limits Limits) map[Key]map[string]artifactTemplate {
	out, _ := minimumOverrideTemplatesContext(context.Background(), spec, limits)
	return out
}

func minimumOverrideTemplatesContext(ctx context.Context, spec CatalogSpec, limits Limits) (map[Key]map[string]artifactTemplate, error) {
	out := make(map[Key]map[string]artifactTemplate)
	for _, override := range spec.Overrides {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		localeName := canonicalArtifactLocale(override.Locale, limits.Locale.MaxTagBytes)
		candidate := artifactTemplate{Locale: localeName, Layer: LayerApplication.String(), Text: override.Text}
		byLocale := out[override.Key]
		if byLocale == nil {
			byLocale = make(map[string]artifactTemplate)
			out[override.Key] = byLocale
		}
		current, exists := byLocale[localeName]
		if !exists || artifactTemplateBytes(candidate) < artifactTemplateBytes(current) {
			byLocale[localeName] = candidate
		}
	}
	return out, nil
}

func minimumEffectiveTemplate(key Key, base artifactTemplate, overrides map[Key]map[string]artifactTemplate) artifactTemplate {
	candidate, exists := overrides[key][base.Locale]
	if exists && artifactTemplateBytes(candidate) < artifactTemplateBytes(base) {
		return candidate
	}
	return base
}

func artifactTemplateBytes(value artifactTemplate) int {
	encoder := boundedArtifactJSON{
		limits:    ArtifactLimits{MaxBytes: maximumArtifactBytes, MaxDepth: maximumArtifactDepth, MaxMembers: maximumArtifactMembers},
		countOnly: true,
	}
	if err := encoder.value(reflect.ValueOf(value), 1); err != nil {
		return 0
	}
	return encoder.written
}

func canonicalArtifactLocale(value string, maximum int) string {
	_, canonical, err := canonicalLocale(value, maximum)
	if err != nil {
		return ""
	}
	return canonical
}

func requireCatalogLimitCeiling(actual, ceiling Limits) error {
	return requireLimitStruct(reflect.ValueOf(actual), reflect.ValueOf(ceiling), "")
}

func requireLimitStruct(actual, ceiling reflect.Value, prefix string) error {
	typeOf := actual.Type()
	for index := 0; index < actual.NumField(); index++ {
		name := typeOf.Field(index).Name
		if prefix != "" {
			name = prefix + "." + name
		}
		left := actual.Field(index)
		right := ceiling.Field(index)
		switch left.Kind() {
		case reflect.Struct:
			if err := requireLimitStruct(left, right, name); err != nil {
				return err
			}
		case reflect.Int:
			if left.Int() > right.Int() {
				return fmt.Errorf("%w: catalog limit %s exceeds the local ceiling", ErrLimitExceeded, name)
			}
		default:
			return fmt.Errorf("%w: catalog limit %s has an unsupported representation", ErrInvalidArtifact, name)
		}
	}
	return nil
}

type boundedArtifactJSON struct {
	bytes     []byte
	limits    ArtifactLimits
	members   int
	written   int
	countOnly bool
	ctx       context.Context
}

func marshalCanonicalArtifact(document artifactDocument, limits ArtifactLimits) ([]byte, error) {
	return marshalCanonicalArtifactContext(context.Background(), document, limits)
}

func marshalCanonicalArtifactContext(ctx context.Context, document artifactDocument, limits ArtifactLimits) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: encoding context is nil", ErrInvalidArtifact)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	encoder := boundedArtifactJSON{bytes: make([]byte, 0, min(limits.MaxBytes, 32<<10)), limits: limits, ctx: ctx}
	if err := encoder.value(reflect.ValueOf(document), 1); err != nil {
		return nil, err
	}
	return encoder.bytes, nil
}

func (e *boundedArtifactJSON) value(value reflect.Value, depth int) error {
	if err := e.contextError(); err != nil {
		return err
	}
	if depth > e.limits.MaxDepth {
		return fmt.Errorf("%w: encoded JSON depth exceeds %d", ErrLimitExceeded, e.limits.MaxDepth)
	}
	switch value.Kind() {
	case reflect.Struct:
		if err := e.writeByte('{'); err != nil {
			return err
		}
		written := 0
		typeOf := value.Type()
		for index := 0; index < value.NumField(); index++ {
			field := typeOf.Field(index)
			if field.PkgPath != "" || field.Tag.Get("json") == "-" {
				continue
			}
			fieldValue := value.Field(index)
			name := field.Tag.Get("json")
			if comma := bytes.IndexByte([]byte(name), ','); comma >= 0 {
				if strings.Contains(name[comma+1:], "omitempty") && emptyJSONValue(fieldValue) {
					continue
				}
				name = name[:comma]
			}
			if name == "" {
				name = field.Name
			}
			if err := e.member(); err != nil {
				return err
			}
			if written > 0 {
				if err := e.writeByte(','); err != nil {
					return err
				}
			}
			written++
			if err := e.writeString(name); err != nil {
				return err
			}
			if err := e.writeByte(':'); err != nil {
				return err
			}
			if err := e.value(fieldValue, depth+1); err != nil {
				return err
			}
		}
		return e.writeByte('}')
	case reflect.Slice:
		if value.IsNil() {
			return e.writeBytes([]byte("null"))
		}
		if err := e.writeByte('['); err != nil {
			return err
		}
		for index := 0; index < value.Len(); index++ {
			if err := e.member(); err != nil {
				return err
			}
			if index > 0 {
				if err := e.writeByte(','); err != nil {
					return err
				}
			}
			if err := e.value(value.Index(index), depth+1); err != nil {
				return err
			}
		}
		return e.writeByte(']')
	case reflect.String:
		return e.writeString(value.String())
	case reflect.Bool:
		if value.Bool() {
			return e.writeBytes([]byte("true"))
		}
		return e.writeBytes([]byte("false"))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return e.writeBytes(strconv.AppendInt(nil, value.Int(), 10))
	default:
		return fmt.Errorf("%w: canonical JSON cannot encode %s", ErrInvalidArtifact, value.Kind())
	}
}

func emptyJSONValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Interface, reflect.Pointer:
		return value.IsZero()
	default:
		return false
	}
}

func (e *boundedArtifactJSON) member() error {
	if err := e.contextError(); err != nil {
		return err
	}
	if e.members >= e.limits.MaxMembers {
		return fmt.Errorf("%w: encoded JSON members exceed %d", ErrLimitExceeded, e.limits.MaxMembers)
	}
	e.members++
	return nil
}

func (e *boundedArtifactJSON) writeString(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: canonical JSON string is not valid UTF-8", ErrInvalidArtifact)
	}
	if err := e.writeByte('"'); err != nil {
		return err
	}
	start := 0
	for index := 0; index < len(value); {
		if index&4095 == 0 {
			if err := e.contextError(); err != nil {
				return err
			}
		}
		current := value[index]
		if current >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(value[index:])
			if r == '\u2028' || r == '\u2029' {
				if err := e.writeBytes([]byte(value[start:index])); err != nil {
					return err
				}
				if err := e.writeBytes([]byte{'\\', 'u', '2', '0', '2', "89"[r-'\u2028']}); err != nil {
					return err
				}
				index += size
				start = index
				continue
			}
			index += size
			continue
		}
		var escaped []byte
		switch current {
		case '\\', '"':
			escaped = []byte{'\\', current}
		case '\b':
			escaped = []byte{'\\', 'b'}
		case '\f':
			escaped = []byte{'\\', 'f'}
		case '\n':
			escaped = []byte{'\\', 'n'}
		case '\r':
			escaped = []byte{'\\', 'r'}
		case '\t':
			escaped = []byte{'\\', 't'}
		case '<', '>', '&':
			escaped = []byte{'\\', 'u', '0', '0', "0123456789abcdef"[(current>>4)&0xf], "0123456789abcdef"[current&0xf]}
		default:
			if current < 0x20 {
				escaped = []byte{'\\', 'u', '0', '0', "0123456789abcdef"[current>>4], "0123456789abcdef"[current&0xf]}
			}
		}
		if escaped == nil {
			index++
			continue
		}
		if err := e.writeBytes([]byte(value[start:index])); err != nil {
			return err
		}
		if err := e.writeBytes(escaped); err != nil {
			return err
		}
		index++
		start = index
	}
	if err := e.writeBytes([]byte(value[start:])); err != nil {
		return err
	}
	return e.writeByte('"')
}

func (e *boundedArtifactJSON) writeByte(value byte) error {
	if err := e.contextError(); err != nil {
		return err
	}
	if e.written >= e.limits.MaxBytes {
		return fmt.Errorf("%w: encoded artifact bytes exceed %d", ErrLimitExceeded, e.limits.MaxBytes)
	}
	e.written++
	if !e.countOnly {
		e.bytes = append(e.bytes, value)
	}
	return nil
}

func (e *boundedArtifactJSON) writeBytes(value []byte) error {
	if err := e.contextError(); err != nil {
		return err
	}
	if len(value) > e.limits.MaxBytes-e.written {
		return fmt.Errorf("%w: encoded artifact bytes exceed %d", ErrLimitExceeded, e.limits.MaxBytes)
	}
	e.written += len(value)
	if !e.countOnly {
		e.bytes = append(e.bytes, value...)
	}
	return nil
}

func (e *boundedArtifactJSON) contextError() error {
	if e.ctx == nil {
		return nil
	}
	return e.ctx.Err()
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func readBoundedInput(ctx context.Context, source io.Reader, limit int, ioError error) ([]byte, error) {
	const chunkSize = 32 << 10
	capacity := chunkSize
	if limit < capacity {
		capacity = limit
	}
	out := make([]byte, 0, capacity)
	chunk := make([]byte, chunkSize)
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := limit + 1 - len(out)
		if remaining <= 0 {
			return nil, fmt.Errorf("%w: artifact bytes exceed %d", ErrLimitExceeded, limit)
		}
		readSize := len(chunk)
		if remaining < readSize {
			readSize = remaining
		}
		n, err := source.Read(chunk[:readSize])
		if n < 0 || n > readSize {
			return nil, fmt.Errorf("%w: reader returned an invalid byte count", ioError)
		}
		if n > 0 {
			out = append(out, chunk[:n]...)
			emptyReads = 0
		} else if err == nil {
			emptyReads++
			if emptyReads >= 100 {
				return nil, fmt.Errorf("%w: %w", ioError, io.ErrNoProgress)
			}
		}
		if len(out) > limit {
			return nil, fmt.Errorf("%w: artifact bytes exceed %d", ErrLimitExceeded, limit)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, fmt.Errorf("%w: reading bytes: %w", ioError, err)
		}
	}
}

func scanArtifactJSON(ctx context.Context, raw []byte, limits ArtifactLimits) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	members := 0
	if err := scanArtifactValue(ctx, decoder, 1, limits, &members); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrLimitExceeded) {
			return err
		}
		return fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		return fmt.Errorf("%w: trailing data: %w", ErrInvalidArtifact, err)
	}
	return nil
}

func scanArtifactValue(ctx context.Context, decoder *json.Decoder, depth int, limits ArtifactLimits, members *int) error {
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
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			if err := ctx.Err(); err != nil {
				return err
			}
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object member name is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object member %q", boundedProblemText(key))
			}
			seen[key] = struct{}{}
			*members++
			if *members > limits.MaxMembers {
				return fmt.Errorf("%w: JSON members exceed %d", ErrLimitExceeded, limits.MaxMembers)
			}
			if err := scanArtifactValue(ctx, decoder, depth+1, limits, members); err != nil {
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
	case '[':
		for decoder.More() {
			if err := ctx.Err(); err != nil {
				return err
			}
			*members++
			if *members > limits.MaxMembers {
				return fmt.Errorf("%w: JSON members exceed %d", ErrLimitExceeded, limits.MaxMembers)
			}
			if err := scanArtifactValue(ctx, decoder, depth+1, limits, members); err != nil {
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
	default:
		return fmt.Errorf("unexpected delimiter %q", delimiter)
	}
	return nil
}

type artifactDocument struct {
	Artifact          string           `json:"artifact"`
	Profile           string           `json:"profile"`
	Engine            string           `json:"engine"`
	LocaleData        string           `json:"locale_data"`
	TimeZoneDataModel string           `json:"time_zone_data_model"`
	SemanticDigest    string           `json:"semantic_digest"`
	Snapshot          artifactSnapshot `json:"snapshot"`
}

type artifactSnapshot struct {
	Revision            string            `json:"revision"`
	SourceLocale        string            `json:"source_locale"`
	DefaultLocale       string            `json:"default_locale"`
	DefaultTimeZone     string            `json:"default_time_zone"`
	TimeZoneDataVersion string            `json:"time_zone_data_version"`
	MatchMode           string            `json:"match_mode"`
	DefaultOnMiss       bool              `json:"default_on_miss"`
	HighestLayer        string            `json:"highest_layer"`
	Supported           []string          `json:"supported"`
	Required            []string          `json:"required"`
	Parents             []artifactParent  `json:"parents"`
	Capabilities        []string          `json:"capabilities"`
	Limits              artifactLimits    `json:"limits"`
	Messages            []artifactMessage `json:"messages"`
}

type artifactParent struct {
	Locale string `json:"locale"`
	Parent string `json:"parent"`
}

type artifactMessage struct {
	Key            string             `json:"key"`
	Revision       string             `json:"revision"`
	Description    string             `json:"description"`
	Output         string             `json:"output"`
	Override       string             `json:"override"`
	AllowEmpty     bool               `json:"allow_empty"`
	Public         bool               `json:"public"`
	ContractDigest string             `json:"contract_digest"`
	SourceDigest   string             `json:"source_digest"`
	Arguments      []artifactArgument `json:"arguments"`
	Markup         []string           `json:"markup"`
	Templates      []artifactTemplate `json:"templates"`
}

type artifactArgument struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Nullable bool     `json:"nullable"`
	Values   []string `json:"values"`
}

type artifactTemplate struct {
	Locale string `json:"locale"`
	Layer  string `json:"layer"`
	Text   string `json:"text"`
}

type artifactLocaleLimits struct {
	MaxChoices       int `json:"max_choices"`
	MaxHeaderBytes   int `json:"max_header_bytes"`
	MaxRanges        int `json:"max_ranges"`
	MaxSupported     int `json:"max_supported"`
	MaxTagBytes      int `json:"max_tag_bytes"`
	MaxFallbackDepth int `json:"max_fallback_depth"`
}

type artifactLimits struct {
	Locale              artifactLocaleLimits `json:"locale"`
	MaxModules          int                  `json:"max_modules"`
	MaxMessages         int                  `json:"max_messages"`
	MaxTranslations     int                  `json:"max_translations"`
	MaxLocales          int                  `json:"max_locales"`
	MaxArguments        int                  `json:"max_arguments"`
	MaxEnumValues       int                  `json:"max_enum_values"`
	MaxMarkupNames      int                  `json:"max_markup_names"`
	MaxCatalogItems     int                  `json:"max_catalog_items"`
	MaxIdentifierBytes  int                  `json:"max_identifier_bytes"`
	MaxRevisionBytes    int                  `json:"max_revision_bytes"`
	MaxDescriptionBytes int                  `json:"max_description_bytes"`
	MaxEnumValueBytes   int                  `json:"max_enum_value_bytes"`
	MaxArgumentBytes    int                  `json:"max_argument_bytes"`
	MaxBigIntegerBits   int                  `json:"max_big_integer_bits"`
	MaxTemplateBytes    int                  `json:"max_template_bytes"`
	MaxCatalogBytes     int                  `json:"max_catalog_bytes"`
	MaxDeclarations     int                  `json:"max_declarations"`
	MaxSelectors        int                  `json:"max_selectors"`
	MaxVariants         int                  `json:"max_variants"`
	MaxLocalDepth       int                  `json:"max_local_depth"`
	MaxTemplateParts    int                  `json:"max_template_parts"`
	MaxMarkupDepth      int                  `json:"max_markup_depth"`
	MaxOutputBytes      int                  `json:"max_output_bytes"`
	MaxOutputParts      int                  `json:"max_output_parts"`
	MaxExplainSteps     int                  `json:"max_explain_steps"`
}

func artifactFromSnapshot(snapshot *Snapshot) artifactDocument {
	document, _ := artifactFromSnapshotContext(context.Background(), snapshot)
	return document
}

func artifactFromSnapshotContext(ctx context.Context, snapshot *Snapshot) (artifactDocument, error) {
	if err := ctx.Err(); err != nil {
		return artifactDocument{}, err
	}
	document := artifactDocument{
		Artifact:          ArtifactVersion,
		Profile:           snapshot.profile,
		Engine:            EngineVersion,
		LocaleData:        LocaleDataVersion,
		TimeZoneDataModel: TimeZoneDataModel,
		SemanticDigest:    snapshot.digest,
		Snapshot: artifactSnapshot{
			Revision:            snapshot.revision,
			SourceLocale:        snapshot.sourceLocale,
			DefaultLocale:       snapshot.defaultLocale,
			DefaultTimeZone:     snapshot.defaultTimeZone,
			TimeZoneDataVersion: snapshot.timeZoneDataVersion,
			MatchMode:           snapshot.matchMode.String(),
			DefaultOnMiss:       snapshot.defaultOnMiss,
			HighestLayer:        snapshot.highestLayer.String(),
			Supported:           slices.Clone(snapshot.supported),
			Required:            slices.Clone(snapshot.required),
			Parents:             make([]artifactParent, 0, len(snapshot.parents)),
			Capabilities:        make([]string, 0, len(snapshot.capabilities)),
			Limits:              encodeArtifactLimits(snapshot.limits),
			Messages:            make([]artifactMessage, 0, len(snapshot.records)),
		},
	}
	for _, locale := range sortedMapKeys(snapshot.parents) {
		if err := ctx.Err(); err != nil {
			return artifactDocument{}, err
		}
		document.Snapshot.Parents = append(document.Snapshot.Parents, artifactParent{Locale: locale, Parent: snapshot.parents[locale]})
	}
	for _, capability := range []Capability{CapabilityDateTime, CapabilityUnit} {
		if snapshot.capabilities[capability] {
			document.Snapshot.Capabilities = append(document.Snapshot.Capabilities, capability.String())
		}
	}
	for _, key := range snapshot.Keys() {
		if err := ctx.Err(); err != nil {
			return artifactDocument{}, err
		}
		record := snapshot.records[key]
		message := artifactMessage{
			Key:            string(key),
			Revision:       record.descriptor.Revision,
			Description:    record.descriptor.Description,
			Output:         record.descriptor.Output.String(),
			Override:       record.descriptor.Override.String(),
			AllowEmpty:     record.descriptor.AllowEmpty,
			Public:         record.descriptor.Public,
			ContractDigest: record.contractHash,
			SourceDigest:   record.sourceDigest,
			Arguments:      make([]artifactArgument, 0, len(record.descriptor.Arguments)),
			Markup:         slices.Clone(record.descriptor.Markup),
			Templates:      make([]artifactTemplate, 0, len(record.templates)),
		}
		for _, argument := range record.descriptor.Arguments {
			message.Arguments = append(message.Arguments, artifactArgument{
				Name: argument.Name, Type: argument.Type.String(), Required: argument.Required,
				Nullable: argument.Nullable, Values: nonNilStrings(argument.Values),
			})
		}
		for _, locale := range sortedMapKeys(record.templates) {
			template := record.templates[locale]
			message.Templates = append(message.Templates, artifactTemplate{Locale: locale, Layer: template.layer.String(), Text: template.text})
		}
		if message.Markup == nil {
			message.Markup = []string{}
		}
		document.Snapshot.Messages = append(document.Snapshot.Messages, message)
	}
	if document.Snapshot.Supported == nil {
		document.Snapshot.Supported = []string{}
	}
	if document.Snapshot.Required == nil {
		document.Snapshot.Required = []string{}
	}
	return document, ctx.Err()
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return slices.Clone(values)
}

func snapshotFromArtifact(ctx context.Context, document artifactDocument, catalogCeiling Limits, observer Observer) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	if document.Artifact != ArtifactVersion {
		return nil, fmt.Errorf("%w: artifact version %q", ErrIncompatibleArtifact, boundedProblemText(document.Artifact))
	}
	if document.Profile != GrammarProfile || document.Engine != EngineVersion || document.LocaleData != LocaleDataVersion || document.TimeZoneDataModel != TimeZoneDataModel {
		return nil, fmt.Errorf("%w: profile or data identity is unsupported", ErrIncompatibleArtifact)
	}
	if !validDigest(document.SemanticDigest) {
		return nil, fmt.Errorf("%w: semantic digest is invalid", ErrInvalidArtifact)
	}
	encoded := document.Snapshot
	limits, err := checkedLimits(decodeArtifactLimits(encoded.Limits))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	if err := requireCatalogLimitCeiling(limits, catalogCeiling); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	if err := preflightArtifactSnapshot(ctx, encoded, limits); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	problems := &problemSet{}
	if encoded.Revision == "" || !utf8.ValidString(encoded.Revision) || len(encoded.Revision) > limits.MaxRevisionBytes {
		problems.add(ProblemInvalid, "snapshot.revision", "revision is missing, invalid, or too large")
	}
	_, sourceLocale, sourceErr := canonicalLocale(encoded.SourceLocale, limits.Locale.MaxTagBytes)
	if sourceErr != nil {
		problems.addError(ProblemInvalid, "snapshot.source_locale", sourceErr)
	}
	_, defaultLocale, defaultErr := canonicalLocale(encoded.DefaultLocale, limits.Locale.MaxTagBytes)
	if defaultErr != nil {
		problems.addError(ProblemInvalid, "snapshot.default_locale", defaultErr)
	}
	defaultTimeZone, zoneErr := canonicalTimeZone(encoded.DefaultTimeZone)
	if zoneErr != nil {
		problems.addError(ProblemInvalid, "snapshot.default_time_zone", zoneErr)
	}
	if !utf8.ValidString(encoded.TimeZoneDataVersion) || len(encoded.TimeZoneDataVersion) > limits.MaxRevisionBytes {
		problems.add(ProblemInvalid, "snapshot.time_zone_data_version", "time-zone data version is invalid or too large")
	} else if defaultTimeZone != "" && defaultTimeZone != "UTC" && encoded.TimeZoneDataVersion == "" {
		problems.add(ProblemMissing, "snapshot.time_zone_data_version", "non-UTC formatting requires a time-zone data version")
	}
	matchMode, matchOK := parseMatchMode(encoded.MatchMode)
	if !matchOK {
		problems.add(ProblemInvalid, "snapshot.match_mode", encoded.MatchMode)
	}
	highestLayer, layerOK := parseLayer(encoded.HighestLayer)
	if !layerOK {
		problems.add(ProblemInvalid, "snapshot.highest_layer", encoded.HighestLayer)
	}
	if len(encoded.Supported) > limits.MaxLocales || len(encoded.Required) > limits.MaxLocales || len(encoded.Parents) > limits.MaxLocales {
		problems.add(ProblemLimit, "snapshot.locales", "locale material exceeds configured limits")
	}
	supported := canonicalLocaleList(encoded.Supported, limits.Locale.MaxTagBytes, "snapshot.supported", problems)
	required := canonicalLocaleList(encoded.Required, limits.Locale.MaxTagBytes, "snapshot.required", problems)
	supportedSet := make(map[string]bool, len(supported))
	for _, locale := range supported {
		supportedSet[locale] = true
	}
	for index, locale := range required {
		if !supportedSet[locale] {
			problems.add(ProblemInvalid, problemPath("snapshot.required[%d]", index), "locale is not supported")
		}
	}
	parents := make([]LocaleEdge, 0, len(encoded.Parents))
	for _, parent := range encoded.Parents {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
		}
		parents = append(parents, LocaleEdge{Locale: parent.Locale, Parent: parent.Parent})
	}
	resolver, resolverErr := NewResolver(LocalePolicy{
		Supported: supported, Default: defaultLocale, Parents: parents, Mode: matchMode,
		DefaultOnMiss: encoded.DefaultOnMiss, Limits: limits.Locale, Observer: observer,
	})
	if resolverErr != nil {
		problems.addError(ProblemInvalid, "snapshot.locale_policy", resolverErr)
	}
	parentMap := map[string]string{}
	if resolver != nil {
		parentMap = maps.Clone(resolver.parents)
	}
	capabilities := make(map[Capability]bool, len(encoded.Capabilities))
	for index, name := range encoded.Capabilities {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
		}
		capability, ok := parseCapability(name)
		if !ok {
			problems.add(ProblemUnsupported, problemPath("snapshot.capabilities[%d]", index), name)
			continue
		}
		if capabilities[capability] {
			problems.add(ProblemDuplicate, problemPath("snapshot.capabilities[%d]", index), name)
		}
		capabilities[capability] = true
	}
	if len(encoded.Messages) > limits.MaxMessages {
		problems.add(ProblemLimit, "snapshot.messages", strconv.Itoa(len(encoded.Messages)))
	}
	allowedLocales := maps.Clone(supportedSet)
	if sourceLocale != "" {
		allowedLocales[sourceLocale] = true
	}
	if defaultLocale != "" {
		allowedLocales[defaultLocale] = true
	}
	for child, parent := range parentMap {
		allowedLocales[child] = true
		allowedLocales[parent] = true
	}
	records := make(map[Key]*messageRecord, len(encoded.Messages))
	totalTranslations := 0
	for index, encodedMessage := range encoded.Messages {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
		}
		path := problemPath("snapshot.messages[%d]", index)
		key := Key(encodedMessage.Key)
		if !validKey(key, limits.MaxIdentifierBytes) {
			problems.add(ProblemInvalid, path+".key", encodedMessage.Key)
		}
		if _, exists := records[key]; exists {
			problems.add(ProblemDuplicate, path+".key", encodedMessage.Key)
			continue
		}
		record, err := artifactMessageRecord(ctx, encodedMessage, key, sourceLocale, allowedLocales, capabilities, highestLayer, limits, path, problems)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
		}
		records[key] = record
		totalTranslations += len(record.templates)
	}
	if len(records) == 0 {
		problems.add(ProblemMissing, "snapshot.messages", "catalog has no messages")
	}
	if totalTranslations > limits.MaxTranslations {
		problems.add(ProblemLimit, "snapshot.translations", strconv.Itoa(totalTranslations))
	}
	if err := problems.err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	snapshot := &Snapshot{
		revision:            encoded.Revision,
		profile:             document.Profile,
		sourceLocale:        sourceLocale,
		defaultLocale:       defaultLocale,
		defaultTimeZone:     defaultTimeZone,
		timeZoneDataVersion: encoded.TimeZoneDataVersion,
		supported:           slices.Clone(supported),
		required:            slices.Clone(required),
		parents:             parentMap,
		records:             records,
		resolver:            resolver,
		capabilities:        capabilities,
		matchMode:           matchMode,
		defaultOnMiss:       encoded.DefaultOnMiss,
		limits:              limits,
		observer:            observer,
		highestLayer:        highestLayer,
	}
	snapshot.formatRequirements, err = snapshotFormattingRequirements(snapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	if bytes := snapshotCatalogBytes(snapshot); bytes > limits.MaxCatalogBytes {
		return nil, fmt.Errorf("%w: %w: catalog bytes exceed %d", ErrInvalidArtifact, ErrLimitExceeded, limits.MaxCatalogBytes)
	}
	requiredProblems := &problemSet{}
	validateRequiredLocales(snapshot, requiredProblems)
	if err := requiredProblems.err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArtifact, err)
	}
	snapshot.digest = snapshotDigest(snapshot)
	if snapshot.digest != document.SemanticDigest {
		return nil, fmt.Errorf("%w: semantic digest does not match the compiled snapshot", ErrInvalidArtifact)
	}
	return snapshot, nil
}

func preflightArtifactSnapshot(ctx context.Context, snapshot artifactSnapshot, limits Limits) error {
	problems := &problemSet{}
	tooLong := func(path, value string, maximum int) {
		if len(value) > maximum {
			problems.add(ProblemLimit, path, strconv.Itoa(len(value)))
		}
	}
	if len(snapshot.Supported) > limits.MaxLocales || len(snapshot.Required) > limits.MaxLocales || len(snapshot.Parents) > limits.MaxLocales {
		problems.add(ProblemLimit, "snapshot.locales", "locale material exceeds configured limits")
	}
	if len(snapshot.Capabilities) > 2 {
		problems.add(ProblemLimit, "snapshot.capabilities", strconv.Itoa(len(snapshot.Capabilities)))
	}
	if len(snapshot.Messages) > limits.MaxMessages {
		problems.add(ProblemLimit, "snapshot.messages", strconv.Itoa(len(snapshot.Messages)))
	}
	if err := problems.err(); err != nil {
		return err
	}
	tooLong("snapshot.revision", snapshot.Revision, limits.MaxRevisionBytes)
	tooLong("snapshot.source_locale", snapshot.SourceLocale, limits.Locale.MaxTagBytes)
	tooLong("snapshot.default_locale", snapshot.DefaultLocale, limits.Locale.MaxTagBytes)
	tooLong("snapshot.default_time_zone", snapshot.DefaultTimeZone, 255)
	tooLong("snapshot.time_zone_data_version", snapshot.TimeZoneDataVersion, limits.MaxRevisionBytes)
	tooLong("snapshot.match_mode", snapshot.MatchMode, limits.MaxIdentifierBytes)
	tooLong("snapshot.highest_layer", snapshot.HighestLayer, limits.MaxIdentifierBytes)
	counter := artifactCounter{maxBytes: limits.MaxCatalogBytes, maxItems: limits.MaxCatalogItems}
	add := func(path string, values ...string) bool {
		if counter.add(values...) {
			return true
		}
		problems.add(ProblemLimit, path, "compiled catalog material exceeds configured bounds")
		return false
	}
	if !add("snapshot", snapshot.Revision, snapshot.SourceLocale, snapshot.DefaultLocale, snapshot.DefaultTimeZone, snapshot.TimeZoneDataVersion, snapshot.MatchMode, snapshot.HighestLayer) {
		return problems.err()
	}
	for index, value := range snapshot.Supported {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !add(problemPath("snapshot.supported[%d]", index), value) {
			return problems.err()
		}
		tooLong(problemPath("snapshot.supported[%d]", index), value, limits.Locale.MaxTagBytes)
	}
	for index, value := range snapshot.Required {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !add(problemPath("snapshot.required[%d]", index), value) {
			return problems.err()
		}
		tooLong(problemPath("snapshot.required[%d]", index), value, limits.Locale.MaxTagBytes)
	}
	for index, parent := range snapshot.Parents {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !add(problemPath("snapshot.parents[%d]", index), parent.Locale, parent.Parent) {
			return problems.err()
		}
		tooLong(problemPath("snapshot.parents[%d].locale", index), parent.Locale, limits.Locale.MaxTagBytes)
		tooLong(problemPath("snapshot.parents[%d].parent", index), parent.Parent, limits.Locale.MaxTagBytes)
	}
	for index, capability := range snapshot.Capabilities {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !add(problemPath("snapshot.capabilities[%d]", index), capability) {
			return problems.err()
		}
		tooLong(problemPath("snapshot.capabilities[%d]", index), capability, limits.MaxIdentifierBytes)
	}
	totalTranslations := 0
	for messageIndex, message := range snapshot.Messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := problemPath("snapshot.messages[%d]", messageIndex)
		if !validKey(Key(message.Key), limits.MaxIdentifierBytes) {
			problems.add(ProblemInvalid, path+".key", message.Key)
		}
		tooLong(path+".revision", message.Revision, limits.MaxRevisionBytes)
		tooLong(path+".description", message.Description, limits.MaxDescriptionBytes)
		tooLong(path+".output", message.Output, limits.MaxIdentifierBytes)
		tooLong(path+".override", message.Override, limits.MaxIdentifierBytes)
		if !validDigest(message.ContractDigest) {
			problems.add(ProblemInvalid, path+".contract_digest", "digest is invalid")
		}
		if !validDigest(message.SourceDigest) {
			problems.add(ProblemInvalid, path+".source_digest", "digest is invalid")
		}
		if len(message.Arguments) > limits.MaxArguments || len(message.Markup) > limits.MaxMarkupNames {
			problems.add(ProblemLimit, path, "message schema exceeds configured limits")
			return problems.err()
		}
		if totalTranslations > limits.MaxTranslations-len(message.Templates) {
			problems.add(ProblemLimit, "snapshot.translations", strconv.Itoa(totalTranslations+len(message.Templates)))
			return problems.err()
		}
		totalTranslations += len(message.Templates)
		if !add(path, message.Key, message.Revision, message.Description, message.Output, message.Override, message.ContractDigest, message.SourceDigest) {
			return problems.err()
		}
		descriptorArguments := make([]ArgumentSpec, 0, len(message.Arguments))
		argumentTypesValid := true
		for argumentIndex, argument := range message.Arguments {
			if err := ctx.Err(); err != nil {
				return err
			}
			argumentPath := problemPath("%s.arguments[%d]", path, argumentIndex)
			tooLong(argumentPath+".name", argument.Name, limits.MaxIdentifierBytes)
			tooLong(argumentPath+".type", argument.Type, limits.MaxIdentifierBytes)
			if len(argument.Values) > limits.MaxEnumValues {
				problems.add(ProblemLimit, argumentPath+".values", strconv.Itoa(len(argument.Values)))
				return problems.err()
			}
			if !add(argumentPath, argument.Name, argument.Type) {
				return problems.err()
			}
			argumentType, valid := parseArgumentType(argument.Type)
			if !valid {
				argumentTypesValid = false
				problems.add(ProblemInvalid, argumentPath+".type", argument.Type)
			}
			descriptorArguments = append(descriptorArguments, ArgumentSpec{
				Name: argument.Name, Type: argumentType, Required: argument.Required,
				Nullable: argument.Nullable, Values: slices.Clone(argument.Values),
			})
			for valueIndex, value := range argument.Values {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !add(problemPath("%s.values[%d]", argumentPath, valueIndex), value) {
					return problems.err()
				}
				tooLong(problemPath("%s.values[%d]", argumentPath, valueIndex), value, limits.MaxEnumValueBytes)
			}
		}
		for markupIndex, markup := range message.Markup {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !add(problemPath("%s.markup[%d]", path, markupIndex), markup) {
				return problems.err()
			}
			tooLong(problemPath("%s.markup[%d]", path, markupIndex), markup, limits.MaxIdentifierBytes)
		}
		output, outputValid := parseOutputKind(message.Output)
		override, overrideValid := parseOverridePolicy(message.Override)
		if !outputValid {
			problems.add(ProblemInvalid, path+".output", message.Output)
		}
		if !overrideValid {
			problems.add(ProblemInvalid, path+".override", message.Override)
		}
		if argumentTypesValid && outputValid && overrideValid && validDigest(message.ContractDigest) {
			markup := slices.Clone(message.Markup)
			slices.Sort(markup)
			descriptor := Descriptor{
				Key: Key(message.Key), Revision: message.Revision, Description: message.Description,
				Arguments: descriptorArguments, Output: output, Markup: markup, Override: override,
				AllowEmpty: message.AllowEmpty, Public: message.Public,
			}
			if descriptorDigest(descriptor) != message.ContractDigest {
				problems.add(ProblemSchema, path+".contract_digest", "digest does not match the descriptor")
			}
		}
		for templateIndex, template := range message.Templates {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !add(problemPath("%s.templates[%d]", path, templateIndex), template.Locale, template.Layer, template.Text) {
				return problems.err()
			}
			templatePath := problemPath("%s.templates[%d]", path, templateIndex)
			tooLong(templatePath+".locale", template.Locale, limits.Locale.MaxTagBytes)
			tooLong(templatePath+".layer", template.Layer, limits.MaxIdentifierBytes)
			tooLong(templatePath+".text", template.Text, limits.MaxTemplateBytes)
		}
	}
	return problems.err()
}

func artifactMessageRecord(
	ctx context.Context,
	encoded artifactMessage,
	key Key,
	sourceLocale string,
	allowedLocales map[string]bool,
	capabilities map[Capability]bool,
	highestLayer Layer,
	limits Limits,
	path string,
	problems *problemSet,
) (*messageRecord, error) {
	if encoded.Revision == "" || !utf8.ValidString(encoded.Revision) || len(encoded.Revision) > limits.MaxRevisionBytes {
		problems.add(ProblemInvalid, path+".revision", "message revision is missing, invalid, or too large")
	}
	if strings.TrimSpace(encoded.Description) == "" || !utf8.ValidString(encoded.Description) || len(encoded.Description) > limits.MaxDescriptionBytes {
		problems.add(ProblemInvalid, path+".description", "description is missing, invalid, or too large")
	}
	output, outputOK := parseOutputKind(encoded.Output)
	if !outputOK {
		problems.add(ProblemInvalid, path+".output", encoded.Output)
	}
	override, overrideOK := parseOverridePolicy(encoded.Override)
	if !overrideOK {
		problems.add(ProblemInvalid, path+".override", encoded.Override)
	}
	if len(encoded.Arguments) > limits.MaxArguments || len(encoded.Markup) > limits.MaxMarkupNames || len(encoded.Templates) > limits.MaxTranslations {
		problems.add(ProblemLimit, path, "message material exceeds configured limits")
	}
	argumentSpecs := make([]ArgumentSpec, 0, len(encoded.Arguments))
	for index, encodedArgument := range encoded.Arguments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		argumentType, ok := parseArgumentType(encodedArgument.Type)
		if !ok {
			problems.add(ProblemInvalid, problemPath("%s.arguments[%d].type", path, index), encodedArgument.Type)
		}
		argumentSpecs = append(argumentSpecs, ArgumentSpec{
			Name: encodedArgument.Name, Type: argumentType, Required: encodedArgument.Required,
			Nullable: encodedArgument.Nullable, Values: slices.Clone(encodedArgument.Values),
		})
	}
	arguments := validateArgumentSpecs(argumentSpecs, capabilities, limits, path+".arguments", problems)
	markup := validateMarkupAllowlist(encoded.Markup, output, limits.MaxIdentifierBytes, limits.MaxMarkupNames, path+".markup", problems)
	descriptor := Descriptor{
		Key: key, Revision: encoded.Revision, Description: encoded.Description, Arguments: arguments,
		Output: output, Markup: markup, Override: override, AllowEmpty: encoded.AllowEmpty, Public: encoded.Public,
	}
	contractDigest := descriptorDigest(descriptor)
	if !validDigest(encoded.ContractDigest) || encoded.ContractDigest != contractDigest {
		problems.add(ProblemSchema, path+".contract_digest", "contract digest does not match the descriptor")
	}
	if !validDigest(encoded.SourceDigest) {
		problems.add(ProblemInvalid, path+".source_digest", "source digest is invalid")
	}
	record := &messageRecord{
		descriptor: descriptor, contractHash: contractDigest, sourceDigest: encoded.SourceDigest,
		allowEmpty: encoded.AllowEmpty, templates: make(map[string]compiledTranslation, len(encoded.Templates)),
	}
	seenLocales := make(map[string]string, len(encoded.Templates))
	for index, encodedTemplate := range encoded.Templates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		templatePath := problemPath("%s.templates[%d]", path, index)
		_, locale, err := canonicalLocale(encodedTemplate.Locale, limits.Locale.MaxTagBytes)
		if err != nil {
			problems.addError(ProblemInvalid, templatePath+".locale", err)
			continue
		}
		if previous, exists := seenLocales[locale]; exists {
			code := ProblemDuplicate
			if previous != encodedTemplate.Locale {
				code = ProblemCollision
			}
			problems.add(code, templatePath+".locale", locale)
			continue
		}
		seenLocales[locale] = encodedTemplate.Locale
		if !allowedLocales[locale] {
			problems.add(ProblemInvalid, templatePath+".locale", "locale is outside the declared graph")
			continue
		}
		layer, ok := parseLayer(encodedTemplate.Layer)
		if !ok || layer > highestLayer {
			problems.add(ProblemInvalid, templatePath+".layer", encodedTemplate.Layer)
			continue
		}
		if layer == LayerApplication && override&OverrideApplication == 0 || layer == LayerTenant && override&OverrideTenant == 0 {
			problems.add(ProblemSecurity, templatePath+".layer", "message does not permit this override layer")
			continue
		}
		text := encodedTemplate.Text
		if text == "" && !encoded.AllowEmpty {
			problems.add(ProblemMissing, templatePath+".text", "empty template is not allowed")
			continue
		}
		if !utf8.ValidString(text) || hasUnsafeAuthoredBidiControls(text) {
			problems.add(ProblemSecurity, templatePath+".text", "template is invalid or contains directional controls")
			continue
		}
		if len(text) > limits.MaxTemplateBytes {
			problems.add(ProblemLimit, templatePath+".text", strconv.Itoa(len(text)))
			continue
		}
		compiled, ok := compileTranslation(locale, layer, text, descriptor, capabilities, limits, templatePath+".text", problems)
		if ok {
			record.templates[locale] = compiled
		}
	}
	if _, ok := record.templates[sourceLocale]; !ok {
		problems.add(ProblemMissing, path+".templates", "source locale template is missing")
	}
	return record, nil
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded := make([]byte, 32)
	if _, err := hex.Decode(decoded, []byte(value)); err != nil {
		return false
	}
	return hex.EncodeToString(decoded) == value
}

func parseMatchMode(value string) (MatchMode, bool) {
	switch value {
	case MatchLookup.String():
		return MatchLookup, true
	case MatchBestFit.String():
		return MatchBestFit, true
	default:
		return 0, false
	}
}

func parseLayer(value string) (Layer, bool) {
	switch value {
	case LayerModule.String():
		return LayerModule, true
	case LayerApplication.String():
		return LayerApplication, true
	case LayerTenant.String():
		return LayerTenant, true
	default:
		return 0, false
	}
}

func parseCapability(value string) (Capability, bool) {
	switch value {
	case CapabilityDateTime.String():
		return CapabilityDateTime, true
	case CapabilityUnit.String():
		return CapabilityUnit, true
	default:
		return 0, false
	}
}

func parseOutputKind(value string) (OutputKind, bool) {
	switch value {
	case OutputPlain.String():
		return OutputPlain, true
	case OutputRich.String():
		return OutputRich, true
	default:
		return 0, false
	}
}

func parseOverridePolicy(value string) (OverridePolicy, bool) {
	switch value {
	case OverrideDenied.String():
		return OverrideDenied, true
	case OverrideApplication.String():
		return OverrideApplication, true
	case OverrideTenant.String():
		return OverrideTenant, true
	case OverrideAny.String():
		return OverrideAny, true
	default:
		return 0, false
	}
}

func parseArgumentType(value string) (ArgumentType, bool) {
	for argumentType := TypeText; argumentType <= TypeEnum; argumentType++ {
		if argumentType.String() == value {
			return argumentType, true
		}
	}
	return 0, false
}

func encodeArtifactLimits(limits Limits) artifactLimits {
	return artifactLimits{
		Locale: artifactLocaleLimits{
			MaxChoices: limits.Locale.MaxChoices, MaxHeaderBytes: limits.Locale.MaxHeaderBytes,
			MaxRanges: limits.Locale.MaxRanges, MaxSupported: limits.Locale.MaxSupported,
			MaxTagBytes: limits.Locale.MaxTagBytes, MaxFallbackDepth: limits.Locale.MaxFallbackDepth,
		},
		MaxModules: limits.MaxModules, MaxMessages: limits.MaxMessages, MaxTranslations: limits.MaxTranslations,
		MaxLocales: limits.MaxLocales, MaxArguments: limits.MaxArguments, MaxEnumValues: limits.MaxEnumValues,
		MaxMarkupNames: limits.MaxMarkupNames, MaxCatalogItems: limits.MaxCatalogItems,
		MaxIdentifierBytes: limits.MaxIdentifierBytes, MaxRevisionBytes: limits.MaxRevisionBytes,
		MaxDescriptionBytes: limits.MaxDescriptionBytes, MaxEnumValueBytes: limits.MaxEnumValueBytes,
		MaxArgumentBytes: limits.MaxArgumentBytes, MaxBigIntegerBits: limits.MaxBigIntegerBits,
		MaxTemplateBytes: limits.MaxTemplateBytes, MaxCatalogBytes: limits.MaxCatalogBytes,
		MaxDeclarations: limits.MaxDeclarations, MaxSelectors: limits.MaxSelectors,
		MaxVariants: limits.MaxVariants, MaxLocalDepth: limits.MaxLocalDepth,
		MaxTemplateParts: limits.MaxTemplateParts,
		MaxMarkupDepth:   limits.MaxMarkupDepth, MaxOutputBytes: limits.MaxOutputBytes,
		MaxOutputParts: limits.MaxOutputParts, MaxExplainSteps: limits.MaxExplainSteps,
	}
}

func decodeArtifactLimits(limits artifactLimits) Limits {
	return Limits{
		Locale: LocaleLimits{
			MaxChoices: limits.Locale.MaxChoices, MaxHeaderBytes: limits.Locale.MaxHeaderBytes,
			MaxRanges: limits.Locale.MaxRanges, MaxSupported: limits.Locale.MaxSupported,
			MaxTagBytes: limits.Locale.MaxTagBytes, MaxFallbackDepth: limits.Locale.MaxFallbackDepth,
		},
		MaxModules: limits.MaxModules, MaxMessages: limits.MaxMessages, MaxTranslations: limits.MaxTranslations,
		MaxLocales: limits.MaxLocales, MaxArguments: limits.MaxArguments, MaxEnumValues: limits.MaxEnumValues,
		MaxMarkupNames: limits.MaxMarkupNames, MaxCatalogItems: limits.MaxCatalogItems,
		MaxIdentifierBytes: limits.MaxIdentifierBytes, MaxRevisionBytes: limits.MaxRevisionBytes,
		MaxDescriptionBytes: limits.MaxDescriptionBytes, MaxEnumValueBytes: limits.MaxEnumValueBytes,
		MaxArgumentBytes: limits.MaxArgumentBytes, MaxBigIntegerBits: limits.MaxBigIntegerBits,
		MaxTemplateBytes: limits.MaxTemplateBytes, MaxCatalogBytes: limits.MaxCatalogBytes,
		MaxDeclarations: limits.MaxDeclarations, MaxSelectors: limits.MaxSelectors,
		MaxVariants: limits.MaxVariants, MaxLocalDepth: limits.MaxLocalDepth,
		MaxTemplateParts: limits.MaxTemplateParts,
		MaxMarkupDepth:   limits.MaxMarkupDepth, MaxOutputBytes: limits.MaxOutputBytes,
		MaxOutputParts: limits.MaxOutputParts, MaxExplainSteps: limits.MaxExplainSteps,
	}
}
