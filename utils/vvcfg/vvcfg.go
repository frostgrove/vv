package vvcfg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/frostgrove/vv/utils/vvflag"
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

var DefaultCfgPath = "./config/app.yml"

type Validator interface {
	Validate() error
}

type EnvironmentApplier interface {
	ApplyEnvironment() error
}

type PrefixedEnvironmentApplier interface {
	ApplyEnvironmentPrefix(string) error
}

func find(args []string) (string, error) {
	if p, err := vvflag.Or(args, "config-path", ""); err != nil {
		return "", err
	} else if p != "" {
		return p, nil
	}
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		return p, nil
	}
	return "", ErrNoPath
}

func Load[T any](path string) (*T, error) {
	if path == "" {
		return nil, ErrNoPath
	}

	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("vvcfg: %w", err)
	}
	var config T
	if err := cleanenv.ReadConfig(path, &config); err != nil {
		return nil, hideDecodeCause("vvcfg: reading "+path, err)
	}
	if err := applyEnvironment(reflect.ValueOf(&config), ""); err != nil {
		return nil, fmt.Errorf("vvcfg: reading environment for %s: %w", path, err)
	}
	if v, ok := any(&config).(Validator); ok {
		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("vvcfg: %s is not valid: %w", path, err)
		}
	}
	return &config, nil
}

func loadEnvironment[T any]() (*T, error) {
	var config T
	if err := cleanenv.ReadEnv(&config); err != nil {
		return nil, hideDecodeCause("vvcfg: reading environment", err)
	}
	if err := applyEnvironment(reflect.ValueOf(&config), ""); err != nil {
		return nil, fmt.Errorf("vvcfg: reading environment: %w", err)
	}
	if v, ok := any(&config).(Validator); ok {
		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("vvcfg: environment is not valid: %w", err)
		}
	}
	return &config, nil
}

func applyEnvironment(v reflect.Value, prefix string) error {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		if v.CanInterface() {
			if a, ok := v.Interface().(PrefixedEnvironmentApplier); ok {
				return a.ApplyEnvironmentPrefix(prefix)
			}
			if a, ok := v.Interface().(EnvironmentApplier); ok {
				return a.ApplyEnvironment()
			}
		}
		return applyEnvironment(v.Elem(), prefix)
	}
	if v.CanAddr() && v.Addr().CanInterface() {
		if a, ok := v.Addr().Interface().(PrefixedEnvironmentApplier); ok {
			return a.ApplyEnvironmentPrefix(prefix)
		}
		if a, ok := v.Addr().Interface().(EnvironmentApplier); ok {
			return a.ApplyEnvironment()
		}
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)

		if v.Type().Field(i).PkgPath != "" {
			continue
		}
		fieldPrefix, _ := v.Type().Field(i).Tag.Lookup("env-prefix")
		if err := applyEnvironment(field, prefix+fieldPrefix); err != nil {
			return err
		}
	}
	return nil
}

func MustLoad[T any](paths ...string) *T {
	var config *T
	var err error

	if len(paths) > 0 {
		config, err = Load[T](filepath.Join(paths...))
	} else if path, findErr := find(os.Args[1:]); findErr == nil {
		config, err = Load[T](path)
	} else if !errors.Is(findErr, ErrNoPath) {
		err = findErr
	} else if DefaultCfgPath != "" {
		config, err = Load[T](DefaultCfgPath)
	} else {
		config, err = loadEnvironment[T]()
	}

	if err != nil {
		panic(err)
	}
	return config
}
