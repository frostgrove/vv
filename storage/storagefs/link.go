package storagefs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/frostgrove/vv/storage"
)

const minimumSigningKeyBytes = 32

func linkConfig(config *Config) (*url.URL, []byte, time.Duration, error) {
	configured := config.BaseURL != "" || len(config.SigningKey) != 0 || config.MaxLinkTTL != 0
	if !configured {
		return nil, nil, 0, nil
	}
	if config.BaseURL == "" || len(config.SigningKey) < minimumSigningKeyBytes {
		return nil, nil, 0, storage.NewError("construct", storage.KindInvalid, fmt.Errorf("temporary links require a base URL and at least %d signing-key bytes", minimumSigningKeyBytes))
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, nil, 0, storage.NewError("construct", storage.KindInvalid, fmt.Errorf("temporary-link base URL is invalid"))
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	if parsed.Path != "/" {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	}
	maxTTL := config.MaxLinkTTL
	if maxTTL == 0 {
		maxTTL = storage.MaxTemporaryURLTTL
	}
	if maxTTL < time.Second || maxTTL > storage.MaxTemporaryURLTTL || maxTTL%time.Second != 0 {
		return nil, nil, 0, storage.NewError("construct", storage.KindInvalid, fmt.Errorf("temporary-link maximum TTL is outside 1s..%s", storage.MaxTemporaryURLTTL))
	}
	key := append([]byte(nil), config.SigningKey...)
	return parsed, key, maxTTL, nil
}

func (this *Backend) TemporaryURL(ctx context.Context, namespace storage.Namespace, key storage.Key, options storage.TemporaryURLOptions) (storage.Link, error) {
	if err := contextError(ctx); err != nil {
		return storage.Link{}, storage.NewError("temporary URL", storage.KindCancelled, err)
	}
	if this == nil || this.baseURL == nil {
		return storage.Link{}, storage.NewError("temporary URL", storage.KindUnsupported, fmt.Errorf("filesystem link signer is not configured"))
	}
	ttl := options.ExpiresIn
	if ttl == 0 {
		ttl = storage.DefaultTemporaryURLTTL
	}
	if ttl < time.Second || ttl > this.maxLinkTTL || ttl%time.Second != 0 {
		return storage.Link{}, storage.NewError("temporary URL", storage.KindInvalid, fmt.Errorf("temporary URL expiry exceeds filesystem policy"))
	}
	expiresAt := this.now().UTC().Add(ttl)
	expiry := strconv.FormatInt(expiresAt.UnixNano(), 10)
	copyURL := *this.baseURL
	query := make(url.Values, 4)
	query.Set("namespace", namespace.Value())
	query.Set("key", key.Value())
	query.Set("expires", expiry)
	query.Set("token", this.sign(namespace.Value(), key.Value(), expiry))
	copyURL.RawQuery = query.Encode()
	return storage.NewLink(copyURL.String(), expiresAt)
}

func (this *Backend) sign(namespace, key, expiry string) string {
	mac := hmac.New(sha256.New, this.signingKey)
	_, _ = mac.Write([]byte(namespace))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(key))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(expiry))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (this *Backend) linkHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if this == nil || this.baseURL == nil {
			http.NotFound(response, request)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if request.URL.Path != this.baseURL.Path {
			http.NotFound(response, request)
			return
		}
		if !strings.EqualFold(request.Host, this.baseURL.Host) {
			http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		namespaceRaw, keyRaw, expiryRaw, token, ok := signedQuery(request.URL.Query())
		if !ok || !this.validSignature(namespaceRaw, keyRaw, expiryRaw, token) {
			http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		expiryNanos, err := strconv.ParseInt(expiryRaw, 10, 64)
		if err != nil {
			http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		now := this.now().UTC()
		expiresAt := time.Unix(0, expiryNanos).UTC()
		if !now.Before(expiresAt) {
			http.Error(response, http.StatusText(http.StatusGone), http.StatusGone)
			return
		}
		if expiresAt.Sub(now) > this.maxLinkTTL {
			http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		namespace, err := storage.ParseNamespace(namespaceRaw)
		if err != nil {
			http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		key, err := storage.ParseKey(keyRaw)
		if err != nil {
			http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		body, info, err := this.Open(request.Context(), namespace, key)
		if err != nil {
			writeLinkError(response, err)
			return
		}
		defer body.Close()

		response.Header().Set("Cache-Control", "private, no-store")
		response.Header().Set("Content-Disposition", "attachment")
		response.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		response.Header().Set("Content-Type", info.ContentType)
		response.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		if !info.ModifiedAt.IsZero() {
			response.Header().Set("Last-Modified", info.ModifiedAt.UTC().Format(http.TimeFormat))
		}
		response.WriteHeader(http.StatusOK)
		if request.Method == http.MethodHead {
			return
		}
		_, _ = io.Copy(response, body)
	})
}

func (this *Backend) validSignature(namespace, key, expiry, token string) bool {
	provided, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	expected, err := base64.RawURLEncoding.DecodeString(this.sign(namespace, key, expiry))
	return err == nil && hmac.Equal(provided, expected)
}

func signedQuery(values url.Values) (namespace, key, expiry, token string, ok bool) {
	if len(values) != 4 {
		return "", "", "", "", false
	}
	for _, name := range []string{"namespace", "key", "expires", "token"} {
		if len(values[name]) != 1 || values[name][0] == "" {
			return "", "", "", "", false
		}
	}
	return values["namespace"][0], values["key"][0], values["expires"][0], values["token"][0], true
}

func writeLinkError(response http.ResponseWriter, err error) {
	switch storage.KindOf(err) {
	case storage.KindNotFound:
		http.Error(response, http.StatusText(http.StatusNotFound), http.StatusNotFound)
	case storage.KindForbidden:
		http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
	case storage.KindCancelled:
		http.Error(response, http.StatusText(http.StatusRequestTimeout), http.StatusRequestTimeout)
	default:
		http.Error(response, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}
