// Package vvcfg loads a configuration struct from a file and the environment.
//
// The decoding is cleanenv's — YAML, TOML, JSON, .env and environment tags, all
// of it. What this package adds is the three things an application actually
// needs around it: a stated precedence for finding the file, an error return
// rather than a panic, and a validation hook that runs before anything else
// starts.
//
// That last one is the point. A configuration that is wrong should stop the
// process at start-up, not surface as a confusing failure once traffic arrives
// (D-021).
package vvcfg

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/frostgrove/vv/utils/vvflag"
	"github.com/ilyakaznacheev/cleanenv"
)

// ErrNoPath reports that neither --config-path nor CONFIG_PATH named a file.
var ErrNoPath = errors.New("vvcfg: no configuration path: pass --config-path or set CONFIG_PATH")

// Validator is implemented by a configuration that can refuse itself. Load
// calls it after decoding and returns what it returns.
type Validator interface {
	Validate() error
}

// EnvironmentApplier is for a nested configuration type whose environment
// representation needs deliberate handling beyond cleanenv's scalar tags.
// Load discovers it anywhere in T after cleanenv has applied ordinary fields.
// Implementations should overlay only variables they own.
type EnvironmentApplier interface {
	ApplyEnvironment() error
}

// PrefixedEnvironmentApplier receives the cleanenv env-prefix accumulated on
// the field that contains it. It is how a nested type can extend its own
// scalar environment surface without making two independently prefixed blocks
// read the same variables.
type PrefixedEnvironmentApplier interface {
	ApplyEnvironmentPrefix(string) error
}

// Find returns the configuration path, in the order a deployment expects to be
// able to override it: the --config-path flag first, then CONFIG_PATH.
//
// args should not include the program name.
func Find(args []string) (string, error) {
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

// Load reads the file at path into a T and validates it.
func Load[T any](path string) (*T, error) {
	if path == "" {
		return nil, ErrNoPath
	}
	// Stat rather than letting the decoder fail, so "the file is not there" and
	// "the file is there and unreadable" are different messages. The original
	// tested only for IsNotExist, so a permission error arrived as a decode
	// failure and sent people looking in the wrong place.
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("vvcfg: %w", err)
	}
	var cfg T
	if err := cleanenv.ReadConfig(path, &cfg); err != nil {
		return nil, fmt.Errorf("vvcfg: reading %s: %w", path, err)
	}
	if err := applyEnvironment(reflect.ValueOf(&cfg), ""); err != nil {
		return nil, fmt.Errorf("vvcfg: reading environment for %s: %w", path, err)
	}
	if v, ok := any(&cfg).(Validator); ok {
		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("vvcfg: %s is not valid: %w", path, err)
		}
	}
	return &cfg, nil
}

// LoadEnvironment builds a configuration from environment variables alone.
// It is the counterpart to Load for container deployments that intentionally
// ship no configuration file. Validation and nested environment appliers run
// exactly as they do after a file, so this is not a weaker startup path.
func LoadEnvironment[T any]() (*T, error) {
	var cfg T
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		return nil, fmt.Errorf("vvcfg: reading environment: %w", err)
	}
	if err := applyEnvironment(reflect.ValueOf(&cfg), ""); err != nil {
		return nil, fmt.Errorf("vvcfg: reading environment: %w", err)
	}
	if v, ok := any(&cfg).(Validator); ok {
		if err := v.Validate(); err != nil {
			return nil, fmt.Errorf("vvcfg: environment is not valid: %w", err)
		}
	}
	return &cfg, nil
}

// applyEnvironment walks the public shape of a configuration and invokes an
// EnvironmentApplier at its boundary. It deliberately stops at that boundary:
// the implementation owns its nested values and avoids applying the same
// prefix to a replica of itself.
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
		// PkgPath marks an unexported field. Reflect cannot safely expose it
		// through Interface, and configuration is an exported declaration.
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

// Auto finds the path and loads it. It is Find and Load in the order a main
// function wants them.
func Auto[T any](args []string) (*T, error) {
	path, err := Find(args)
	if err != nil {
		if errors.Is(err, ErrNoPath) {
			return LoadEnvironment[T]()
		}
		return nil, err
	}
	return Load[T](path)
}

// Must turns a load into a panic, for a main function that has nothing better
// to do with the error:
//
//	cfg := vvcfg.Must(vvcfg.Auto[Config](os.Args[1:]))
func Must[T any](cfg *T, err error) *T {
	if err != nil {
		panic(err)
	}
	return cfg
}
