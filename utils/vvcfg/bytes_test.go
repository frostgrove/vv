package vvcfg

import (
	"errors"
	"strings"
	"testing"
)

type limitsConf struct {
	Upload Bytes `yaml:"upload" env:"UPLOAD_LIMIT" env-default:"1MiB"`
}

func TestASizeIsWrittenWithTheUnitAnOperatorUsesAndReadAsBytes(t *testing.T) {
	for written, expected := range map[string]Bytes{
		"25MiB":  25 * mebibyte,
		"1 KiB":  kibibyte,
		" 3gib ": 3 * gibibyte,
		"1kb":    1_000,
		"2MB":    2_000_000,
		"512B":   512,
		"512":    512,
		"0":      0,
	} {
		parsed, err := ParseBytes(written)
		if err != nil {
			t.Fatalf("%q was refused: %v", written, err)
		}
		if parsed != expected {
			t.Fatalf("%q parsed to %d bytes, want %d", written, parsed, expected)
		}
	}
}

func TestASizeThatIsNotOneIsRefusedAndTheRefusalDoesNotEchoIt(t *testing.T) {
	for _, written := range []string{"", "   ", "gib", "twenty-five", "25 mib mib", "-1MiB", "9223372036854775807GiB", "1.5MiB"} {
		parsed, err := ParseBytes(written)
		if !errors.Is(err, ErrNotASize) {
			t.Fatalf("%q was accepted as %d bytes: %v", written, parsed, err)
		}
		if trimmed := strings.TrimSpace(written); trimmed != "" && strings.Contains(err.Error(), trimmed) {
			t.Fatalf("the refusal of %q repeats the value back: %s", written, err)
		}
	}
}

func TestASizeRendersItselfInTheUnitItDividesBy(t *testing.T) {
	for size, expected := range map[Bytes]string{
		25 * mebibyte: "25MiB",
		kibibyte:      "1KiB",
		gibibyte:      "1GiB",
		1_000:         "1000B",
		0:             "0B",
	} {
		if rendered := size.String(); rendered != expected {
			t.Fatalf("%d bytes rendered as %q, want %q", int64(size), rendered, expected)
		}
	}
}

func TestASizeIsReadFromTheFileTheEnvironmentAndTheDeclaredDefault(t *testing.T) {
	silent := write(t, "{}\n")
	config, report, err := LoadStrict[limitsConf](silent)
	if err != nil {
		t.Fatal(err)
	}
	if config.Upload != mebibyte {
		t.Fatalf("the declared default was not parsed as a size: %s", config.Upload)
	}
	if origin, _ := report.OriginOf("upload"); origin != OriginDefault {
		t.Fatalf("upload came from %s, want the declared default", origin)
	}

	path := write(t, "upload: 25MiB\n")
	config, report, err = LoadStrict[limitsConf](path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Upload != 25*mebibyte {
		t.Fatalf("the file's size did not reach the struct: %s", config.Upload)
	}
	if origin, _ := report.OriginOf("upload"); origin != OriginFile {
		t.Fatalf("upload came from %s, want the file", origin)
	}

	t.Setenv("UPLOAD_LIMIT", "3GiB")
	config, report, err = LoadStrict[limitsConf](path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Upload != 3*gibibyte {
		t.Fatalf("the environment did not override the file: %s", config.Upload)
	}
	if origin, _ := report.OriginOf("upload"); origin != OriginEnvironment {
		t.Fatalf("upload came from %s, want the environment", origin)
	}
}

func TestASizeNobodyCanParseStopsTheLoadInsteadOfBecomingZero(t *testing.T) {
	path := write(t, "upload: twenty-five megabytes\n")
	if _, _, err := LoadStrict[limitsConf](path); err == nil {
		t.Fatal("a size nobody can parse was accepted and the field kept its zero value")
	}
}
