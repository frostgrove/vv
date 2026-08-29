package storage_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/storage"
)

func TestCleanupIsExplicitBoundedAndDefaultsToOneBatch(t *testing.T) {
	want := storage.CleanupResult{Removed: 7, More: true}
	var gotNamespace storage.Namespace
	var gotOptions storage.CleanupOptions
	backend := &fakeBackend{cleanup: func(_ context.Context, namespace storage.Namespace, options storage.CleanupOptions) (storage.CleanupResult, error) {
		gotNamespace, gotOptions = namespace, options
		return want, nil
	}}
	got, err := newStore(backend).CleanupExpired(context.Background(), storage.CleanupOptions{})
	if err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}
	if got != want || gotNamespace.Value() != "documents" || gotOptions.Limit != storage.DefaultCleanupLimit {
		t.Fatalf("CleanupExpired = result %#v namespace %q options %#v", got, gotNamespace.Value(), gotOptions)
	}
	if backend.calls != 1 {
		t.Fatalf("CleanupExpired made %d backend calls, want one", backend.calls)
	}
}

func TestAnInvalidCleanupBatchNeverReachesTheBackend(t *testing.T) {
	for _, limit := range []int{-1, storage.MaxCleanupLimit + 1} {
		backend := &fakeBackend{}
		result, err := newStore(backend).CleanupExpired(context.Background(), storage.CleanupOptions{Limit: limit})
		if !errors.Is(err, storage.ErrInvalid) {
			t.Errorf("CleanupExpired(limit %d) error = %v, want ErrInvalid", limit, err)
		}
		if result != (storage.CleanupResult{}) || backend.calls != 0 {
			t.Errorf("CleanupExpired(limit %d) = %#v after %d calls", limit, result, backend.calls)
		}
	}

	backend := &fakeBackend{}
	var nilContext context.Context
	if _, err := newStore(backend).CleanupExpired(nilContext, storage.CleanupOptions{}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("CleanupExpired(nil context) error = %v, want ErrInvalid", err)
	}
	if backend.calls != 0 {
		t.Fatalf("CleanupExpired(nil context) made %d backend calls", backend.calls)
	}
}

func TestCleanupReturnsNoProgressWhenTheBackendFails(t *testing.T) {
	backend := &fakeBackend{cleanup: func(context.Context, storage.Namespace, storage.CleanupOptions) (storage.CleanupResult, error) {
		return storage.CleanupResult{Removed: 3, More: true}, storage.NewError("list stages", storage.KindTemporary, errors.New("request failed"))
	}}
	result, err := newStore(backend).CleanupExpired(context.Background(), storage.CleanupOptions{})
	if !errors.Is(err, storage.ErrTemporary) || result != (storage.CleanupResult{}) {
		t.Fatalf("failed CleanupExpired = result %#v error %v", result, err)
	}
}

func TestTemporaryURLUsesTheCommonDefaultAndOnlyTheExplicitAccessorRevealsIt(t *testing.T) {
	key := mustKey(t, "images/photo.png")
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	secretURL := "https://objects.example.test/download?signature=secret-sentinel"
	var gotNamespace storage.Namespace
	var gotKey storage.Key
	var gotOptions storage.TemporaryURLOptions
	backend := &fakeBackend{temporary: func(_ context.Context, namespace storage.Namespace, key storage.Key, options storage.TemporaryURLOptions) (storage.Link, error) {
		gotNamespace, gotKey, gotOptions = namespace, key, options
		return storage.NewLink(secretURL, now.Add(options.ExpiresIn))
	}}

	link, err := newStore(backend).TemporaryURL(context.Background(), key, storage.TemporaryURLOptions{})
	if err != nil {
		t.Fatalf("TemporaryURL: %v", err)
	}
	if gotNamespace.Value() != "documents" || gotKey != key || gotOptions.ExpiresIn != storage.DefaultTemporaryURLTTL {
		t.Fatalf("TemporaryURL boundary namespace=%q key=%q options=%#v", gotNamespace.Value(), gotKey.Value(), gotOptions)
	}
	if link.URL() != secretURL || !link.ExpiresAt().Equal(now.Add(storage.DefaultTemporaryURLTTL)) {
		t.Fatalf("Link explicit values URL=%q expiry=%s", link.URL(), link.ExpiresAt())
	}
	for _, formatted := range []string{fmt.Sprint(link), fmt.Sprintf("%s", link), fmt.Sprintf("%q", link), fmt.Sprintf("%+v", link)} {
		if strings.Contains(formatted, "secret-sentinel") || strings.Contains(formatted, secretURL) {
			t.Fatalf("ordinary Link formatting disclosed bearer URL: %q", formatted)
		}
	}
	if got := fmt.Sprint(link); got != "[temporary storage URL]" {
		t.Fatalf("Link.String() = %q", got)
	}
	assertFormattingRedacted(t, link, secretURL, "secret-sentinel")
}

func TestTemporaryURLRejectsBadExpiryAndBadBackendResults(t *testing.T) {
	key := mustKey(t, "images/photo.png")
	for _, ttl := range []time.Duration{time.Millisecond, 1500 * time.Millisecond, storage.MaxTemporaryURLTTL + time.Second} {
		backend := &fakeBackend{}
		link, err := newStore(backend).TemporaryURL(context.Background(), key, storage.TemporaryURLOptions{ExpiresIn: ttl})
		if !errors.Is(err, storage.ErrInvalid) || !reflect.DeepEqual(link, storage.Link{}) {
			t.Errorf("TemporaryURL(ttl %s) = link %#v error %v", ttl, link, err)
		}
		if backend.calls != 0 {
			t.Errorf("TemporaryURL(ttl %s) made %d backend calls", ttl, backend.calls)
		}
	}

	backend := &fakeBackend{}
	if _, err := newStore(backend).TemporaryURL(context.Background(), storage.Key{}, storage.TemporaryURLOptions{}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("TemporaryURL(zero key) error = %v, want ErrInvalid", err)
	}
	if backend.calls != 0 {
		t.Fatalf("TemporaryURL(zero key) made %d backend calls", backend.calls)
	}

	backend = &fakeBackend{temporary: func(context.Context, storage.Namespace, storage.Key, storage.TemporaryURLOptions) (storage.Link, error) {
		return storage.Link{}, nil
	}}
	link, err := newStore(backend).TemporaryURL(context.Background(), key, storage.TemporaryURLOptions{})
	if !errors.Is(err, storage.ErrInternal) || !reflect.DeepEqual(link, storage.Link{}) {
		t.Fatalf("TemporaryURL with invalid backend result = link %#v error %v", link, err)
	}

	secret := "https://objects.example.test/key?signature=must-not-leak"
	backend = &fakeBackend{temporary: func(context.Context, storage.Namespace, storage.Key, storage.TemporaryURLOptions) (storage.Link, error) {
		return storage.Link{}, storage.NewError("presign", storage.KindUnavailable, errors.New(secret))
	}}
	link, err = newStore(backend).TemporaryURL(context.Background(), key, storage.TemporaryURLOptions{})
	if !errors.Is(err, storage.ErrUnavailable) || !reflect.DeepEqual(link, storage.Link{}) {
		t.Fatalf("failed TemporaryURL = link %#v error %v", link, err)
	}
	if strings.Contains(fmt.Sprint(err), secret) || strings.Contains(fmt.Sprint(err), "must-not-leak") {
		t.Fatalf("TemporaryURL error disclosed its URL: %q", err)
	}
}

func TestNewLinkAcceptsOnlyAnAbsoluteHTTPBearerWithAnExpiry(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	valid := []string{
		"https://objects.example.test/key?signature=value",
		"http://127.0.0.1:8080/files/token",
	}
	for _, raw := range valid {
		link, err := storage.NewLink(raw, now)
		if err != nil {
			t.Errorf("NewLink(%q): %v", raw, err)
			continue
		}
		if link.URL() != raw || !link.ExpiresAt().Equal(now) {
			t.Errorf("NewLink(%q) = URL %q expiry %s", raw, link.URL(), link.ExpiresAt())
		}
	}

	invalid := []struct {
		raw    string
		expiry time.Time
	}{
		{"/relative", now},
		{"file:///srv/private/object", now},
		{"https:///missing-host", now},
		{"https://user:password@objects.example.test/key", now},
		{"https://objects.example.test/key", time.Time{}},
	}
	for _, tc := range invalid {
		link, err := storage.NewLink(tc.raw, tc.expiry)
		if !errors.Is(err, storage.ErrInternal) || !reflect.DeepEqual(link, storage.Link{}) {
			t.Errorf("NewLink(%q, %s) = link %#v error %v", tc.raw, tc.expiry, link, err)
		}
		if tc.raw != "" && strings.Contains(fmt.Sprint(err), tc.raw) {
			t.Errorf("NewLink(%q) disclosed rejected URL in %q", tc.raw, err)
		}
	}
}
