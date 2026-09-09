package i18n

import (
	"context"
	"strings"
)

type boundedTextBuilder struct {
	value    strings.Builder
	maximum  int
	overflow bool
	ctx      context.Context
	err      error
}

func newBoundedTextBuilder(maximum int) *boundedTextBuilder {
	builder := &boundedTextBuilder{maximum: maximum}
	if maximum > 0 {
		builder.value.Grow(min(maximum, 32<<10))
	}
	return builder
}

func newContextBoundedTextBuilder(ctx context.Context, maximum int) *boundedTextBuilder {
	builder := newBoundedTextBuilder(maximum)
	builder.ctx = ctx
	return builder
}

func (b *boundedTextBuilder) Write(value []byte) (int, error) {
	if b.reserve(len(value)) {
		_, _ = b.value.Write(value)
	}
	return len(value), nil
}

func (b *boundedTextBuilder) WriteString(value string) (int, error) {
	if b.reserve(len(value)) {
		_, _ = b.value.WriteString(value)
	}
	return len(value), nil
}

func (b *boundedTextBuilder) reserve(size int) bool {
	if b.err != nil {
		return false
	}
	if b.ctx != nil {
		if err := b.ctx.Err(); err != nil {
			b.err = err
			return false
		}
	}
	if b.overflow || size < 0 || size > b.maximum-b.value.Len() {
		b.overflow = true
		return false
	}
	return true
}

func (b *boundedTextBuilder) Len() int {
	return b.value.Len()
}

func (b *boundedTextBuilder) Overflow() bool {
	return b.overflow
}

func (b *boundedTextBuilder) String() string {
	return b.value.String()
}

func (b *boundedTextBuilder) Err() error {
	return b.err
}
