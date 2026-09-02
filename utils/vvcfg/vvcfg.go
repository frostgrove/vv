package vvcfg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/ilyakaznacheev/cleanenv"
)

var ErrNoPath = errors.New("vvcfg: no configuration path: pass --config-path or set CONFIG_PATH")

type redactedDecodeError struct {
	operation string
	cause     error
}

func (this *redactedDecodeError) Error() string    { return this.operation + ": details redacted" }
func (this *redactedDecodeError) Unwrap() error    { return this.cause }
func (this *redactedDecodeError) GoString() string { return this.Error() }
func (this *redactedDecodeError) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, this.Error())
}

func hideDecodeCause(operation string, cause error) error {
	return &redactedDecodeError{operation: operation, cause: cause}
}

type EnvironmentApplier interface {
	ApplyEnvironment() error
}

type PrefixedEnvironmentApplier interface {
	ApplyEnvironmentPrefix(string) error
}

func Load[T any](path string) (*T, error) {
	if path == "" {
		return nil, ErrNoPath
	}
	config, _, err := load[T](Source{Path: path}, path, PathFromCaller)
	return config, err
}

func LoadStrict[T any](path string) (*T, *Report, error) {
	if path == "" {
		return nil, nil, ErrNoPath
	}
	source := Source{Path: path, Strict: true}
	return load[T](source, path, PathFromCaller)
}

func LoadFrom[T any](source Source) (*T, *Report, error) {
	path, origin, err := source.Resolve()
	if err != nil {
		return nil, nil, err
	}
	return load[T](source, path, origin)
}

func MustLoad[T any](paths ...string) *T {
	source := DefaultSource()
	if len(paths) > 0 {
		source = Source{Path: filepath.Join(paths...)}
	}
	config, _, err := LoadFrom[T](source)
	if err != nil {
		panic(err)
	}
	return config
}

func load[T any](source Source, path string, origin PathOrigin) (*T, *Report, error) {
	var config T
	if err := decodeInto(&config, path); err != nil {
		return nil, nil, err
	}
	file, unreadable := inspectFile(path)
	if unreadable != nil && source.Strict {
		return nil, nil, unreadable
	}
	if err := applyEnvironment(reflect.ValueOf(&config), ""); err != nil {
		return nil, nil, fmt.Errorf("vvcfg: reading environment for %s: %w", describeSubject(path), err)
	}

	format := ""
	if file != nil {
		format = file.format
	}
	declared := describe(reflect.TypeOf(&config).Elem(), format)
	report := buildReport(declared, file, unreadable, path, origin)

	failures := source.refusals(declared, report, path)
	if err := ValidateTree(&config); err != nil {
		failures = append(failures, fmt.Errorf("vvcfg: %s is not valid: %w", describeSubject(path), err))
	}
	if joined := errors.Join(failures...); joined != nil {
		return nil, report, joined
	}
	return &config, report, nil
}

func decodeInto(config any, path string) error {
	if path == "" {
		if err := cleanenv.ReadEnv(config); err != nil {
			return hideDecodeCause("vvcfg: reading environment", err)
		}
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("vvcfg: %w", err)
	}
	if err := cleanenv.ReadConfig(path, config); err != nil {
		return hideDecodeCause("vvcfg: reading "+path, err)
	}
	return nil
}

func inspectFile(path string) (*document, error) {
	if path == "" {
		return nil, nil
	}
	file, err := readDocument(path)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (this Source) refusals(declared *schema, report *Report, path string) []error {
	var failures []error
	if this.Strict {
		if len(report.UnknownKeys) > 0 {
			failures = append(failures, &UnknownKeysError{Path: describeSubject(path), Keys: report.UnknownKeys})
		}
		if len(report.UnusedEnvironment) > 0 {
			failures = append(failures, &UnusedEnvironmentError{Variables: report.UnusedEnvironment})
		}
	}
	for _, required := range this.RequireEnvironment {
		node, declaredHere := declared.byPath[required]
		if !declaredHere {
			failures = append(failures, fmt.Errorf("%w: %s", ErrUndeclaredPath, required))
			continue
		}
		if origin, _ := report.OriginOf(required); origin != OriginEnvironment {
			failures = append(failures, &EnvironmentSourceError{Path: required, Variables: node.names})
		}
	}
	return failures
}

func describeSubject(path string) string {
	if path == "" {
		return "the environment"
	}
	return path
}

func applyEnvironment(value reflect.Value, prefix string) error {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		if value.CanInterface() {
			if applier, ok := value.Interface().(PrefixedEnvironmentApplier); ok {
				return applier.ApplyEnvironmentPrefix(prefix)
			}
			if applier, ok := value.Interface().(EnvironmentApplier); ok {
				return applier.ApplyEnvironment()
			}
		}
		return applyEnvironment(value.Elem(), prefix)
	}
	if value.CanAddr() && value.Addr().CanInterface() {
		if applier, ok := value.Addr().Interface().(PrefixedEnvironmentApplier); ok {
			return applier.ApplyEnvironmentPrefix(prefix)
		}
		if applier, ok := value.Addr().Interface().(EnvironmentApplier); ok {
			return applier.ApplyEnvironment()
		}
	}
	if value.Kind() != reflect.Struct {
		return nil
	}
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		if value.Type().Field(index).PkgPath != "" {
			continue
		}
		fieldPrefix, _ := value.Type().Field(index).Tag.Lookup("env-prefix")
		if err := applyEnvironment(field, prefix+fieldPrefix); err != nil {
			return err
		}
	}
	return nil
}
