package vvcfg

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrNotASize = errors.New("vvcfg: not a size: write one like 25MiB")

type Bytes int64

const (
	kibibyte = 1 << 10
	mebibyte = 1 << 20
	gibibyte = 1 << 30
)

var sizeUnits = []struct {
	suffix string
	scale  int64
}{
	{"kib", kibibyte},
	{"mib", mebibyte},
	{"gib", gibibyte},
	{"kb", 1_000},
	{"mb", 1_000_000},
	{"gb", 1_000_000_000},
	{"b", 1},
	{"", 1},
}

var binarySizeUnits = []struct {
	suffix string
	scale  int64
}{
	{"GiB", gibibyte},
	{"MiB", mebibyte},
	{"KiB", kibibyte},
}

func ParseBytes(written string) (Bytes, error) {
	text := strings.ToLower(strings.TrimSpace(written))
	if text == "" {
		return 0, fmt.Errorf("%w: the size is empty", ErrNotASize)
	}
	for _, unit := range sizeUnits {
		if !strings.HasSuffix(text, unit.suffix) {
			continue
		}
		digits := strings.TrimSpace(text[:len(text)-len(unit.suffix)])
		if digits == "" {
			continue
		}
		amount, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return 0, ErrNotASize
		}
		if amount < 0 {
			return 0, fmt.Errorf("%w: a size is not negative", ErrNotASize)
		}
		if amount > (1<<63-1)/unit.scale {
			return 0, fmt.Errorf("%w: it does not fit in an int64", ErrNotASize)
		}
		return Bytes(amount * unit.scale), nil
	}
	return 0, ErrNotASize
}

func (this *Bytes) UnmarshalText(text []byte) error {
	parsed, err := ParseBytes(string(text))
	if err != nil {
		return err
	}
	*this = parsed
	return nil
}

func (this Bytes) String() string {
	for _, unit := range binarySizeUnits {
		if this != 0 && int64(this)%unit.scale == 0 {
			return strconv.FormatInt(int64(this)/unit.scale, 10) + unit.suffix
		}
	}
	return strconv.FormatInt(int64(this), 10) + "B"
}
