package crudgrpc

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/grpc/metadata"

	"github.com/frostgrove/vv/port"
)

func TestLocaleSourcePrecedenceIsStable(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"x-locale", "es",
		"accept-language", "de",
		"grpc-accept-language", "fr, en;q=0.5",
		"grpc-accept-language", "it",
	))
	if got := port.LocaleFrom(withRequestLocale(ctx)); got != "fr" {
		t.Fatalf("metadata precedence selected %q, want fr", got)
	}
	explicit := port.WithLocale(ctx, "ja")
	if got := port.LocaleFrom(withRequestLocale(explicit)); got != "ja" {
		t.Fatalf("explicit locale lost to metadata: %q", got)
	}
}

func TestLocaleKeysAreReturnedAsIndependentCopies(t *testing.T) {
	first := LocaleKeys()
	second := LocaleKeys()
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("locale key counts = %d/%d, want 3/3", len(first), len(second))
	}
	first[0] = "private-locale"
	if second[0] != "grpc-accept-language" || LocaleKeys()[0] != "grpc-accept-language" {
		t.Fatalf("mutating one result changed another: %v / %v", first, second)
	}
}

func TestMutatingReturnedLocaleKeysCannotRaceRequestParsing(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("grpc-accept-language", "fr"))
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 32 {
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			keys := LocaleKeys()
			for index := range keys {
				keys[index] = "private-locale"
			}
		}()
		go func() {
			defer workers.Done()
			<-start
			if got := port.LocaleFrom(withRequestLocale(ctx)); got != "fr" {
				t.Errorf("request selected %q, want fr", got)
			}
		}()
	}
	close(start)
	workers.Wait()
}
