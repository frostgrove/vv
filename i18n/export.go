package i18n

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
)

const PublicContractSchema = "frostgrove.i18n.public-contract/v1"

const (
	PublicTypeScriptGenerator = "frostgrove.i18n.typescript/v1"
	PublicValueContract       = "frostgrove.i18n.json-scalars/v1"
	PublicTypeScriptTarget    = "ES2020"
	PublicWireFormat          = "json"
)

type PublicExport struct {
	TypeScript []byte
	Manifest   []byte
	Address    string
}

type PublicExportSpec struct {
	MaxOutputBytes int
}

func ExportPublic(snapshot *Snapshot) (PublicExport, error) {
	return ExportPublicContext(context.Background(), snapshot, PublicExportSpec{})
}

func ExportPublicContext(ctx context.Context, snapshot *Snapshot, spec PublicExportSpec) (PublicExport, error) {
	return exportPublicContext(ctx, snapshot, spec, expectedPublicExportAddressContext)
}

func exportPublicContext(ctx context.Context, snapshot *Snapshot, spec PublicExportSpec, address func(context.Context, []byte, []byte) (string, error)) (PublicExport, error) {
	if ctx == nil {
		return PublicExport{}, fmt.Errorf("%w: public export context is nil", ErrInvalidCatalog)
	}
	if err := ctx.Err(); err != nil {
		return PublicExport{}, err
	}
	if snapshot == nil {
		return PublicExport{}, fmt.Errorf("%w: snapshot is nil", ErrInvalidCatalog)
	}
	maximum := spec.MaxOutputBytes
	if maximum == 0 || maximum > snapshot.limits.MaxCatalogBytes {
		maximum = snapshot.limits.MaxCatalogBytes
	}
	if maximum < 1 {
		return PublicExport{}, fmt.Errorf("%w: public export output limit must be positive", ErrLimitExceeded)
	}
	if err := preflightPublicExport(ctx, snapshot, maximum); err != nil {
		return PublicExport{}, err
	}
	messages, err := publicContractMessagesContext(ctx, snapshot)
	if err != nil {
		return PublicExport{}, err
	}
	manifest := publicContractManifest{
		Schema:           PublicContractSchema,
		Profile:          snapshot.Profile(),
		Generator:        PublicTypeScriptGenerator,
		ValueContract:    PublicValueContract,
		TypeScriptTarget: PublicTypeScriptTarget,
		WireFormat:       PublicWireFormat,
		Capabilities:     publicCapabilities(snapshot),
		FormattingParity: false,
		Messages:         messages,
	}
	manifestBytes, err := marshalPublicManifestContext(ctx, manifest, maximum)
	if err != nil {
		return PublicExport{}, err
	}
	typeScript, err := publicTypeScriptContext(ctx, messages, maximum)
	if err != nil {
		return PublicExport{}, err
	}
	if len(manifestBytes) > maximum || len(typeScript) > maximum {
		return PublicExport{}, fmt.Errorf("%w: public contract export exceeds %d bytes", ErrLimitExceeded, maximum)
	}
	addressValue, err := address(ctx, manifestBytes, typeScript)
	if err != nil {
		return PublicExport{}, err
	}
	return PublicExport{
		TypeScript: typeScript,
		Manifest:   manifestBytes,
		Address:    addressValue,
	}, nil
}

func preflightPublicExport(ctx context.Context, snapshot *Snapshot, maximum int) error {
	remaining := maximum
	consume := func(size int) bool {
		if size < 0 || size > remaining {
			return false
		}
		remaining -= size
		return true
	}
	if !consume(64) {
		return fmt.Errorf("%w: public contract export exceeds %d bytes", ErrLimitExceeded, maximum)
	}
	for key, record := range snapshot.records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !record.descriptor.Public {
			continue
		}
		if !consume(len(key) + len(record.descriptor.Revision) + len(record.contractHash) + len(record.descriptor.Output.String())) {
			return fmt.Errorf("%w: public contract export exceeds %d bytes", ErrLimitExceeded, maximum)
		}
		for _, markup := range record.descriptor.Markup {
			if !consume(len(markup)) {
				return fmt.Errorf("%w: public contract export exceeds %d bytes", ErrLimitExceeded, maximum)
			}
		}
		for _, argument := range record.descriptor.Arguments {
			if !consume(len(argument.Name) + len(argument.Type.String())) {
				return fmt.Errorf("%w: public contract export exceeds %d bytes", ErrLimitExceeded, maximum)
			}
			for _, value := range argument.Values {
				if !consume(len(value)) {
					return fmt.Errorf("%w: public contract export exceeds %d bytes", ErrLimitExceeded, maximum)
				}
			}
		}
	}
	return nil
}

func marshalPublicManifest(manifest publicContractManifest, maximum int) ([]byte, error) {
	return marshalPublicManifestContext(context.Background(), manifest, maximum)
}

func marshalPublicManifestContext(ctx context.Context, manifest publicContractManifest, maximum int) ([]byte, error) {
	limits, err := checkedArtifactLimits(ArtifactLimits{MaxBytes: maximum, MaxDepth: maximumArtifactDepth, MaxMembers: maximumArtifactMembers})
	if err != nil {
		return nil, err
	}
	encoder := boundedArtifactJSON{bytes: make([]byte, 0, min(maximum, 32<<10)), limits: limits, ctx: ctx}
	if err := encoder.value(reflect.ValueOf(manifest), 1); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if errors.Is(err, ErrLimitExceeded) {
			return nil, fmt.Errorf("%w: public manifest exceeds %d bytes", ErrLimitExceeded, maximum)
		}
		return nil, fmt.Errorf("%w: public manifest: %v", ErrInvalidCatalog, err)
	}
	return encoder.bytes, nil
}

type publicContractManifest struct {
	Schema           string                  `json:"schema"`
	Profile          string                  `json:"profile"`
	Generator        string                  `json:"generator"`
	ValueContract    string                  `json:"valueContract"`
	TypeScriptTarget string                  `json:"typeScriptTarget"`
	WireFormat       string                  `json:"wireFormat"`
	Capabilities     []string                `json:"capabilities"`
	FormattingParity bool                    `json:"formattingParity"`
	Messages         []publicContractMessage `json:"messages"`
}

type publicContractMessage struct {
	ID        string                   `json:"id"`
	Revision  string                   `json:"revision"`
	Digest    string                   `json:"digest"`
	Output    string                   `json:"output"`
	Markup    []string                 `json:"markup,omitempty"`
	Arguments []publicContractArgument `json:"arguments"`
	typeName  string
}

type publicContractArgument struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Nullable bool     `json:"nullable"`
	Values   []string `json:"values,omitempty"`
}

func publicContractMessages(snapshot *Snapshot) ([]publicContractMessage, error) {
	return publicContractMessagesContext(context.Background(), snapshot)
}

func publicContractMessagesContext(ctx context.Context, snapshot *Snapshot) ([]publicContractMessage, error) {
	messages := make([]publicContractMessage, 0)
	typeNames := make(map[string]string)
	for _, key := range snapshot.Keys() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		descriptor, ok := snapshot.Descriptor(key)
		if !ok || !descriptor.Public {
			continue
		}
		contract, ok := snapshot.ContractRef(key)
		if !ok {
			return nil, fmt.Errorf("%w: public message %q has no contract", ErrInvalidCatalog, key)
		}
		typeName := goExportedIdentifier(string(key), "Message") + "Args"
		if previous, exists := typeNames[typeName]; exists {
			return nil, fmt.Errorf("%w: public TypeScript name %q collides for %q and %q", ErrInvalidCatalog, typeName, previous, key)
		}
		typeNames[typeName] = string(key)
		message := publicContractMessage{
			ID:        string(key),
			Revision:  contract.Revision,
			Digest:    contract.Digest,
			Output:    descriptor.Output.String(),
			Markup:    descriptor.Markup,
			Arguments: make([]publicContractArgument, len(descriptor.Arguments)),
			typeName:  typeName,
		}
		for index, argument := range descriptor.Arguments {
			message.Arguments[index] = publicContractArgument{
				Name:     argument.Name,
				Type:     argument.Type.String(),
				Required: argument.Required,
				Nullable: argument.Nullable,
				Values:   argument.Values,
			}
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func publicTypeScript(messages []publicContractMessage, maximum int) ([]byte, error) {
	return publicTypeScriptContext(context.Background(), messages, maximum)
}

func publicTypeScriptContext(ctx context.Context, messages []publicContractMessage, maximum int) ([]byte, error) {
	source := newContextBoundedTextBuilder(ctx, maximum)
	source.WriteString("// Code generated by github.com/frostgrove/vv/i18n.ExportPublic. Types only; formatting parity is not provided. DO NOT EDIT.\n\n")
	source.WriteString("declare const frostgroveI18nScalar: unique symbol;\n")
	source.WriteString("type FrostgroveI18nScalar<Name extends string> = { readonly [frostgroveI18nScalar]: Name };\n")
	source.WriteString("export type Int64String = string & FrostgroveI18nScalar<\"int64\">;\n")
	source.WriteString("export type UInt64String = string & FrostgroveI18nScalar<\"uint64\">;\n")
	source.WriteString("export type BigIntegerString = string & FrostgroveI18nScalar<\"big-integer\">;\n")
	source.WriteString("export type DecimalString = string & FrostgroveI18nScalar<\"decimal\">;\n")
	source.WriteString("export type CurrencyCode = string & FrostgroveI18nScalar<\"iso-4217\">;\n")
	source.WriteString("export type Instant = string & FrostgroveI18nScalar<\"rfc3339-instant\">;\n")
	source.WriteString("export type CalendarDate = Readonly<{ year: number; month: number; day: number }> & FrostgroveI18nScalar<\"calendar-date\">;\n")
	source.WriteString("export type FormattingParity = false;\n")
	usesMoney, err := publicContractsUseContext(ctx, messages, TypeMoney)
	if err != nil {
		return nil, err
	}
	if usesMoney {
		source.WriteString("export interface MoneyValue { readonly amount: DecimalString; readonly currency: CurrencyCode; }\n")
	}
	source.WriteString("\n")
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fmt.Fprintf(source, "export interface %s {\n", message.typeName)
		for _, argument := range message.Arguments {
			name, err := typeScriptString(argument.Name)
			if err != nil {
				return nil, err
			}
			typeName, err := publicTypeScriptArgumentTypeContext(ctx, argument, maximum)
			if err != nil {
				return nil, err
			}
			optional := ""
			if !argument.Required {
				optional = "?"
			}
			if argument.Nullable {
				typeName += " | null"
			}
			fmt.Fprintf(source, "readonly %s%s: %s;\n", name, optional, typeName)
		}
		source.WriteString("}\n\n")
	}
	source.WriteString("export interface MessageContracts {\n")
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, err := typeScriptString(message.ID)
		if err != nil {
			return nil, err
		}
		revision, err := typeScriptString(message.Revision)
		if err != nil {
			return nil, err
		}
		digest, err := typeScriptString(message.Digest)
		if err != nil {
			return nil, err
		}
		output, err := typeScriptString(message.Output)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(source, "readonly %s: { readonly revision: %s; readonly digest: %s; readonly output: %s; readonly args: %s };\n", id, revision, digest, output, message.typeName)
	}
	source.WriteString("}\n\nexport type MessageID = keyof MessageContracts;\n")
	if err := source.Err(); err != nil {
		return nil, err
	}
	if source.Overflow() {
		return nil, fmt.Errorf("%w: public TypeScript exceeds %d bytes", ErrLimitExceeded, maximum)
	}
	return []byte(source.String()), nil
}

func publicContractsUse(messages []publicContractMessage, argumentType ArgumentType) bool {
	used, _ := publicContractsUseContext(context.Background(), messages, argumentType)
	return used
}

func publicContractsUseContext(ctx context.Context, messages []publicContractMessage, argumentType ArgumentType) (bool, error) {
	want := argumentType.String()
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		for _, argument := range message.Arguments {
			if argument.Type == want {
				return true, nil
			}
		}
	}
	return false, nil
}

func publicTypeScriptArgumentType(argument publicContractArgument, maximum int) (string, error) {
	return publicTypeScriptArgumentTypeContext(context.Background(), argument, maximum)
}

func publicTypeScriptArgumentTypeContext(ctx context.Context, argument publicContractArgument, maximum int) (string, error) {
	switch argument.Type {
	case TypeText.String():
		return "string", nil
	case TypeBool.String():
		return "boolean", nil
	case TypeInteger.String():
		return "Int64String", nil
	case TypeUnsignedInteger.String():
		return "UInt64String", nil
	case TypeBigInteger.String():
		return "BigIntegerString", nil
	case TypeDecimal.String():
		return "DecimalString", nil
	case TypeMoney.String():
		return "MoneyValue", nil
	case TypeDate.String():
		return "CalendarDate", nil
	case TypeInstant.String():
		return "Instant", nil
	case TypeEnum.String():
		values := newContextBoundedTextBuilder(ctx, maximum)
		for index, value := range argument.Values {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			quoted, err := typeScriptString(value)
			if err != nil {
				return "", err
			}
			if index > 0 {
				values.WriteString(" | ")
			}
			values.WriteString(quoted)
		}
		if err := values.Err(); err != nil {
			return "", err
		}
		if values.Overflow() {
			return "", fmt.Errorf("%w: public TypeScript enum exceeds %d bytes", ErrLimitExceeded, maximum)
		}
		return values.String(), nil
	default:
		return "", fmt.Errorf("%w: unsupported public argument type %q", ErrInvalidCatalog, argument.Type)
	}
}

func publicCapabilities(snapshot *Snapshot) []string {
	capabilities := make([]string, 0, len(snapshot.capabilities))
	for capability := range snapshot.capabilities {
		capabilities = append(capabilities, capability.String())
	}
	slices.Sort(capabilities)
	return capabilities
}

func ExpectedPublicExportAddress(manifest, typeScript []byte) string {
	address, _ := expectedPublicExportAddressContext(context.Background(), manifest, typeScript)
	return address
}

func expectedPublicExportAddressContext(ctx context.Context, manifest, typeScript []byte) (string, error) {
	hash := sha256.New()
	for _, field := range []struct {
		name  []byte
		value []byte
	}{
		{name: []byte("domain"), value: []byte("frostgrove.i18n.public-export-address/v1")},
		{name: []byte("manifest"), value: manifest},
		{name: []byte("typescript"), value: typeScript},
	} {
		if err := writePublicDigestBytesContext(ctx, hash.Write, field.name); err != nil {
			return "", err
		}
		if err := writePublicDigestBytesContext(ctx, hash.Write, field.value); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func writePublicDigestBytesContext(ctx context.Context, write func([]byte) (int, error), value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	prefix := strconv.AppendInt(nil, int64(len(value)), 10)
	prefix = append(prefix, ':')
	_, _ = write(prefix)
	for len(value) != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := min(len(value), 32<<10)
		_, _ = write(value[:chunk])
		value = value[chunk:]
	}
	_, _ = write([]byte{'\n'})
	return ctx.Err()
}

func typeScriptString(value string) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("%w: TypeScript string: %v", ErrInvalidCatalog, err)
	}
	return string(encoded), nil
}
