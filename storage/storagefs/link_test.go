package storagefs

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/storage"
)

func TestTemporaryURLHandlerStreamsOnlyRawObjectBytes(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{
		BaseURL:    "https://files.example.test/private/file",
		SigningKey: bytes.Repeat([]byte("s"), 32),
		MaxLinkTTL: time.Hour,
	})
	now := time.Date(2026, 8, 27, 15, 0, 0, 0, time.UTC)
	backend.now = func() time.Time { return now }
	key := mustKey(t, "avatars/user-one.png")
	payload := []byte("\x89PNG raw image bytes")
	if _, err := store.Put(t.Context(), key, bytes.NewReader(payload), storage.PutOptions{ContentType: "image/png"}); err != nil {
		t.Fatal(err)
	}
	link, err := store.TemporaryURL(t.Context(), key, storage.TemporaryURLOptions{ExpiresIn: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if link.String() != "[temporary storage URL]" || link.ExpiresAt() != now.Add(10*time.Minute) {
		t.Fatalf("unexpected link projection: %s %s", link, link.ExpiresAt())
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, link.URL(), nil)
	backend.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%q", response.Code, response.Body.String())
	}
	if !bytes.Equal(response.Body.Bytes(), payload) {
		t.Fatalf("handler body = %q, want exact raw bytes", response.Body.Bytes())
	}
	if response.Header().Get("Content-Type") != "image/png" || response.Header().Get("Content-Length") != "20" {
		t.Fatalf("unexpected response headers: %v", response.Header())
	}
	if response.Header().Get("Content-Disposition") != "attachment" ||
		response.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" ||
		response.Header().Get("Referrer-Policy") != "no-referrer" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unsafe download response headers: %v", response.Header())
	}
	headResponse := httptest.NewRecorder()
	headRequest := httptest.NewRequest(http.MethodHead, link.URL(), nil)
	backend.Handler().ServeHTTP(headResponse, headRequest)
	if headResponse.Code != http.StatusOK || headResponse.Body.Len() != 0 || headResponse.Header().Get("Content-Length") != "20" {
		t.Fatalf("HEAD response = status %d, length %q, body %q", headResponse.Code, headResponse.Header().Get("Content-Length"), headResponse.Body.String())
	}
}

func TestTemporaryURLRejectsTamperingExpiryAndAmbiguity(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{
		BaseURL:    "https://files.example.test/file",
		SigningKey: bytes.Repeat([]byte("k"), 32),
		MaxLinkTTL: time.Hour,
	})
	now := time.Date(2026, 8, 27, 15, 0, 0, 0, time.UTC)
	backend.now = func() time.Time { return now }
	key := mustKey(t, "avatars/one")
	if _, err := store.Put(t.Context(), key, strings.NewReader("one"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	link, err := store.TemporaryURL(t.Context(), key, storage.TemporaryURLOptions{ExpiresIn: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("key", func(t *testing.T) {
		tampered := mutateURL(t, link.URL(), func(query url.Values) { query.Set("key", "avatars/two") })
		assertLinkStatus(t, backend, tampered, http.StatusForbidden)
	})
	t.Run("signature", func(t *testing.T) {
		tampered := mutateURL(t, link.URL(), func(query url.Values) { query.Set("token", strings.Repeat("A", 43)) })
		assertLinkStatus(t, backend, tampered, http.StatusForbidden)
	})
	t.Run("duplicate", func(t *testing.T) {
		tampered := mutateURL(t, link.URL(), func(query url.Values) { query.Add("key", "avatars/one") })
		assertLinkStatus(t, backend, tampered, http.StatusForbidden)
	})
	t.Run("expired", func(t *testing.T) {
		now = now.Add(2 * time.Minute)
		assertLinkStatus(t, backend, link.URL(), http.StatusGone)
	})
}

func TestOneSecondTemporaryURLKeepsItsFractionalSecond(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{
		BaseURL:    "https://files.example.test/file",
		SigningKey: bytes.Repeat([]byte("f"), 32),
		MaxLinkTTL: time.Hour,
	})
	issuedAt := time.Date(2026, 8, 27, 15, 0, 0, 900_000_000, time.UTC)
	now := issuedAt
	backend.now = func() time.Time { return now }
	key := mustKey(t, "fractional/second")
	if _, err := store.Put(t.Context(), key, strings.NewReader("alive"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	link, err := store.TemporaryURL(t.Context(), key, storage.TemporaryURLOptions{ExpiresIn: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if want := issuedAt.Add(time.Second); !link.ExpiresAt().Equal(want) {
		t.Fatalf("expiry = %s, want %s", link.ExpiresAt(), want)
	}
	now = issuedAt.Add(999 * time.Millisecond)
	assertLinkStatus(t, backend, link.URL(), http.StatusOK)
	now = issuedAt.Add(time.Second)
	assertLinkStatus(t, backend, link.URL(), http.StatusGone)
}

func TestTemporaryURLIsAnOptionalBoundedCapability(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{})
	key := mustKey(t, "documents/file")
	if backend.Capabilities().TemporaryURL {
		t.Fatal("unconfigured backend advertised temporary URLs")
	}
	if _, err := store.TemporaryURL(t.Context(), key, storage.TemporaryURLOptions{}); !errors.Is(err, storage.ErrUnsupported) {
		t.Fatalf("TemporaryURL error = %v, want ErrUnsupported", err)
	}

	configured, configuredStore, _ := newTestStore(t, Config{
		BaseURL:    "https://files.example.test/file",
		SigningKey: bytes.Repeat([]byte("z"), 32),
		MaxLinkTTL: time.Minute,
	})
	if !configured.Capabilities().TemporaryURL {
		t.Fatal("configured backend did not advertise temporary URLs")
	}
	if _, err := configuredStore.TemporaryURL(t.Context(), key, storage.TemporaryURLOptions{ExpiresIn: 2 * time.Minute}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("over-limit TemporaryURL error = %v, want ErrInvalid", err)
	}
}

func TestHandlerDoesNotServePrivateFormatOrAnotherPath(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{
		BaseURL:    "https://files.example.test/file",
		SigningKey: bytes.Repeat([]byte("q"), 32),
	})
	key := mustKey(t, "documents/file")
	if _, err := store.Put(t.Context(), key, strings.NewReader("visible"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	link, err := store.TemporaryURL(t.Context(), key, storage.TemporaryURLOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wrongPath, err := url.Parse(link.URL())
	if err != nil {
		t.Fatal(err)
	}
	wrongPath.Path = "/another"
	assertLinkStatus(t, backend, wrongPath.String(), http.StatusNotFound)

	wrongHostRequest := httptest.NewRequest(http.MethodGet, link.URL(), nil)
	wrongHostRequest.Host = "authenticated-app.example.test"
	wrongHostResponse := httptest.NewRecorder()
	backend.Handler().ServeHTTP(wrongHostResponse, wrongHostRequest)
	if wrongHostResponse.Code != http.StatusForbidden {
		t.Fatalf("altered Host status = %d, want 403", wrongHostResponse.Code)
	}

	request := httptest.NewRequest(http.MethodPost, link.URL(), strings.NewReader("ignored"))
	response := httptest.NewRecorder()
	backend.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", response.Code)
	}
	if _, err := io.ReadAll(response.Result().Body); err != nil {
		t.Fatal(err)
	}
}

func mutateURL(t *testing.T, raw string, mutate func(url.Values)) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	mutate(query)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func assertLinkStatus(t *testing.T, backend *Backend, rawURL string, want int) {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, rawURL, nil)
	backend.Handler().ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("status = %d, want %d; body=%q", response.Code, want, response.Body.String())
	}
}
