package storage

import (
	"fmt"
	"mime"
	"strings"
	"time"
	"unicode/utf8"
)

func validateNamespace(s string) error {
	if len(s) == 0 || len(s) > 63 {
		return fmt.Errorf("namespace length is outside 1..63")
	}
	for i := range len(s) {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' && i > 0 && i < len(s)-1 {
			continue
		}
		return fmt.Errorf("namespace has a non-portable character")
	}
	return nil
}

func validateKey(s string) error {
	if len(s) == 0 || len(s) > MaxKeyBytes {
		return fmt.Errorf("key length is outside 1..%d", MaxKeyBytes)
	}
	if s[0] == '/' || s[len(s)-1] == '/' {
		return fmt.Errorf("key has a leading or trailing separator")
	}
	for _, segment := range strings.Split(s, "/") {
		if err := validateSegment(segment); err != nil {
			return err
		}
	}
	return nil
}

func validateSegment(segment string) error {
	if len(segment) == 0 || len(segment) > MaxKeySegmentBytes {
		return fmt.Errorf("key segment length is outside 1..%d", MaxKeySegmentBytes)
	}
	if segment == "." || segment == ".." {
		return fmt.Errorf("key has a dot segment")
	}
	if segment[len(segment)-1] == '.' || segment[len(segment)-1] == ' ' {
		return fmt.Errorf("key segment has a non-portable suffix")
	}
	for i := range len(segment) {
		c := segment[i]
		if c < 0x20 || c > 0x7e || strings.ContainsRune(`<>:"\|?*`, rune(c)) {
			return fmt.Errorf("key has a non-portable character")
		}
	}
	base := segment
	if before, _, ok := strings.Cut(base, "."); ok {
		base = before
	}
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return fmt.Errorf("key has a platform-reserved segment")
	}
	return nil
}

func normalizePutOptions(opts PutOptions) (PutOptions, error) {
	mode, err := normalizeMode(opts.Mode)
	if err != nil {
		return PutOptions{}, err
	}
	size, err := copySize(opts.Size)
	if err != nil {
		return PutOptions{}, err
	}
	contentType, err := normalizeContentType(opts.ContentType)
	if err != nil {
		return PutOptions{}, err
	}
	metadata, err := normalizeMetadata(opts.Metadata)
	if err != nil {
		return PutOptions{}, err
	}
	return PutOptions{Mode: mode, Size: size, ContentType: contentType, Metadata: metadata}, nil
}

func normalizeStageOptions(opts StageOptions) (StageOptions, error) {
	size, err := copySize(opts.Size)
	if err != nil {
		return StageOptions{}, err
	}
	contentType, err := normalizeContentType(opts.ContentType)
	if err != nil {
		return StageOptions{}, err
	}
	metadata, err := normalizeMetadata(opts.Metadata)
	if err != nil {
		return StageOptions{}, err
	}
	ttl := opts.ExpiresIn
	if ttl == 0 {
		ttl = DefaultStageTTL
	}
	if ttl < time.Second || ttl > MaxStageTTL {
		return StageOptions{}, fmt.Errorf("stage expiry is outside 1s..%s", MaxStageTTL)
	}
	return StageOptions{Size: size, ContentType: contentType, Metadata: metadata, ExpiresIn: ttl}, nil
}

func normalizeMode(mode WriteMode) (WriteMode, error) {
	if mode == 0 {
		return CreateOnly, nil
	}
	if mode != CreateOnly && mode != Replace {
		return 0, fmt.Errorf("unknown write mode")
	}
	return mode, nil
}

func normalizeTemporaryURLOptions(opts TemporaryURLOptions) (TemporaryURLOptions, error) {
	ttl := opts.ExpiresIn
	if ttl == 0 {
		ttl = DefaultTemporaryURLTTL
	}
	if ttl < time.Second || ttl > MaxTemporaryURLTTL || ttl%time.Second != 0 {
		return TemporaryURLOptions{}, fmt.Errorf("temporary URL expiry must be a whole number of seconds inside 1s..%s", MaxTemporaryURLTTL)
	}
	return TemporaryURLOptions{ExpiresIn: ttl}, nil
}

func normalizeCleanupOptions(opts CleanupOptions) (CleanupOptions, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = DefaultCleanupLimit
	}
	if limit < 1 || limit > MaxCleanupLimit {
		return CleanupOptions{}, fmt.Errorf("cleanup limit is outside 1..%d", MaxCleanupLimit)
	}
	return CleanupOptions{Limit: limit}, nil
}

func copySize(size *int64) (*int64, error) {
	if size == nil {
		return nil, nil
	}
	if *size < 0 {
		return nil, fmt.Errorf("size is negative")
	}
	n := *size
	return &n, nil
}

func normalizeContentType(contentType string) (string, error) {
	if contentType == "" {
		return "application/octet-stream", nil
	}
	if len(contentType) > 255 || !utf8.ValidString(contentType) {
		return "", fmt.Errorf("content type is invalid")
	}
	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		return "", fmt.Errorf("content type is invalid")
	}
	return contentType, nil
}

func normalizeMetadata(metadata Metadata) (Metadata, error) {
	if len(metadata) > MaxMetadataEntries {
		return nil, fmt.Errorf("metadata has more than %d entries", MaxMetadataEntries)
	}
	if len(metadata) == 0 {
		return nil, nil
	}
	out := make(Metadata, len(metadata))
	total := 0
	for key, value := range metadata {
		if len(key) == 0 || len(key) > MaxMetadataKeyBytes || !validMetadataKey(key) {
			return nil, fmt.Errorf("metadata key is invalid")
		}
		if strings.HasPrefix(key, "vv-") || strings.HasPrefix(key, "x-amz-") {
			return nil, fmt.Errorf("metadata key uses a reserved prefix")
		}
		if len(value) > MaxMetadataValueBytes || !utf8.ValidString(value) || hasControl(value) {
			return nil, fmt.Errorf("metadata value is invalid")
		}
		total += len(key) + len(value)
		if total > MaxMetadataTotalBytes {
			return nil, fmt.Errorf("metadata exceeds %d bytes", MaxMetadataTotalBytes)
		}
		out[key] = value
	}
	return out, nil
}

func validMetadataKey(key string) bool {
	for i := range len(key) {
		c := key[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_' || c == '.') {
			continue
		}
		return false
	}
	return true
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func cloneMetadata(metadata Metadata) Metadata {
	if metadata == nil {
		return nil
	}
	out := make(Metadata, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func cloneInfo(info Info) Info {
	info.Metadata = cloneMetadata(info.Metadata)
	return info
}
