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
		head, err := this.Head(request.Context(), namespace, key)
		if err != nil {
			writeLinkError(response, err)
			return
		}
		// A handler that advertised no range support and then answered every
		// Range with the whole object told a resuming download it had resumed and
		// sent it the file again from the top.
		read, status, satisfiable := rangeOf(request, head.Size)
		if !satisfiable {
			response.Header().Set("Accept-Ranges", "bytes")
			response.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(head.Size, 10))
			http.Error(response, http.StatusText(http.StatusRequestedRangeNotSatisfiable), http.StatusRequestedRangeNotSatisfiable)
			return
		}
		body, info, err := this.Open(request.Context(), namespace, key, read)
		if err != nil {
			writeLinkError(response, err)
			return
		}
		defer body.Close()

		length := info.Size - read.Offset
		if read.Length != nil && *read.Length < length {
			length = *read.Length
		}
		response.Header().Set("Accept-Ranges", "bytes")
		response.Header().Set("Cache-Control", "private, no-store")
		response.Header().Set("Content-Disposition", "attachment")
		response.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		response.Header().Set("Content-Type", info.ContentType)
		response.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		if status == http.StatusPartialContent {
			response.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", read.Offset, read.Offset+length-1, info.Size))
		}
		if !info.ModifiedAt.IsZero() {
			response.Header().Set("Last-Modified", info.ModifiedAt.UTC().Format(http.TimeFormat))
		}
		response.WriteHeader(status)
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

// Only the single-range forms, which is what a resuming download and a media
// player send. A multipart range is answered whole rather than wrongly: the
// status stays 200 and no Content-Range is written, so no client is told it got
// something it did not.
func rangeOf(request *http.Request, size int64) (storage.ReadOptions, int, bool) {
	raw := strings.TrimSpace(request.Header.Get("Range"))
	if raw == "" {
		return storage.ReadOptions{}, http.StatusOK, true
	}
	spec, ok := strings.CutPrefix(raw, "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return storage.ReadOptions{}, http.StatusOK, true
	}
	first, last, ok := strings.Cut(spec, "-")
	if !ok {
		return storage.ReadOptions{}, http.StatusOK, true
	}
	if first == "" {
		suffix, err := strconv.ParseInt(last, 10, 64)
		if err != nil || suffix <= 0 {
			return storage.ReadOptions{}, 0, false
		}
		if suffix > size {
			suffix = size
		}
		length := suffix
		return storage.ReadOptions{Offset: size - suffix, Length: &length}, http.StatusPartialContent, true
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil || start < 0 || start >= size {
		return storage.ReadOptions{}, 0, false
	}
	if last == "" {
		length := size - start
		return storage.ReadOptions{Offset: start, Length: &length}, http.StatusPartialContent, true
	}
	end, err := strconv.ParseInt(last, 10, 64)
	if err != nil || end < start {
		return storage.ReadOptions{}, 0, false
	}
	if end >= size {
		end = size - 1
	}
	length := end - start + 1
	return storage.ReadOptions{Offset: start, Length: &length}, http.StatusPartialContent, true
}
