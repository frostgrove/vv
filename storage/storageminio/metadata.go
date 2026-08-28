package storageminio

import (
	"errors"
	"mime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/frostgrove/vv/storage"
	"github.com/minio/minio-go/v7"
)

const (
	minioUserMetadataLimit = 2 * 1024
	minioMetadataPrefix    = "x-amz-meta-"
)

func mergeMetadata(public storage.Metadata, internal map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(public)+len(internal))
	for key, value := range public {
		if strings.HasPrefix(strings.ToLower(key), "vv-") {
			return nil, errors.New("metadata key uses a reserved prefix")
		}
		out[key] = value
	}
	for key, value := range internal {
		out[key] = value
	}
	if minioUserMetadataSize(out) > minioUserMetadataLimit {
		return nil, errors.New("metadata exceeds the MinIO user-metadata limit")
	}
	return out, nil
}

func minioUserMetadataSize(metadata map[string]string) int {
	total := 0
	for key, value := range metadata {
		if !strings.HasPrefix(strings.ToLower(key), minioMetadataPrefix) {
			total += len(minioMetadataPrefix)
		}
		total += len(key) + len(value)
	}
	return total
}

func infoFromObject(object minio.ObjectInfo) (storage.Info, error) {
	if object.Size < 0 {
		return storage.Info{}, errors.New("object size is invalid")
	}
	contentType := strings.TrimSpace(object.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if len(contentType) > 255 {
		return storage.Info{}, errors.New("object content type is invalid")
	}
	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		return storage.Info{}, errors.New("object content type is invalid")
	}
	metadata, err := portableMetadata(object)
	if err != nil {
		return storage.Info{}, err
	}
	return storage.Info{
		Size:        object.Size,
		ContentType: contentType,
		Metadata:    metadata,
		ModifiedAt:  object.LastModified,
		ETag:        object.ETag,
		Version:     object.VersionID,
	}, nil
}

func stageExpiry(object minio.ObjectInfo) (time.Time, bool, error) {
	return markedExpiry(object, stageMarkerKey, stageMarkerValue)
}

func markedExpiry(object minio.ObjectInfo, markerKey, markerValue string) (time.Time, bool, error) {
	metadata, err := rawUserMetadata(object)
	if err != nil {
		return time.Time{}, false, err
	}
	if metadata[markerKey] != markerValue {
		return time.Time{}, false, nil
	}
	raw := metadata[stageExpiryKey]
	if raw == "" {
		return time.Time{}, true, errors.New("stage expiry is absent")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || expiresAt.IsZero() {
		return time.Time{}, true, errors.New("stage expiry is invalid")
	}
	return expiresAt.UTC(), true, nil
}

func portableMetadata(object minio.ObjectInfo) (storage.Metadata, error) {
	raw, err := rawUserMetadata(object)
	if err != nil {
		return nil, err
	}
	out := make(storage.Metadata)
	total := 0
	for key, value := range raw {
		if strings.HasPrefix(key, "vv-") {
			continue
		}
		if len(out) == storage.MaxMetadataEntries {
			return nil, errors.New("object metadata has too many entries")
		}
		if !validMetadataKey(key) || len(key) > storage.MaxMetadataKeyBytes {
			return nil, errors.New("object metadata key is invalid")
		}
		if len(value) > storage.MaxMetadataValueBytes || !utf8.ValidString(value) || hasControl(value) {
			return nil, errors.New("object metadata value is invalid")
		}
		total += len(key) + len(value)
		if total > storage.MaxMetadataTotalBytes {
			return nil, errors.New("object metadata is too large")
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func rawUserMetadata(object minio.ObjectInfo) (map[string]string, error) {
	out := make(map[string]string, len(object.UserMetadata))
	for key, value := range object.UserMetadata {
		key = strings.ToLower(strings.TrimSpace(key))
		key = strings.TrimPrefix(key, "x-amz-meta-")
		if _, exists := out[key]; exists {
			return nil, errors.New("object metadata has an ambiguous key")
		}
		out[key] = value
	}
	if len(out) != 0 {
		return out, nil
	}
	for key, values := range object.Metadata {
		key = strings.ToLower(key)
		if !strings.HasPrefix(key, "x-amz-meta-") || len(values) == 0 {
			continue
		}
		key = strings.TrimPrefix(key, "x-amz-meta-")
		if len(values) != 1 {
			return nil, errors.New("object metadata has an ambiguous value")
		}
		if _, exists := out[key]; exists {
			return nil, errors.New("object metadata has an ambiguous key")
		}
		out[key] = values[0]
	}
	return out, nil
}

func validMetadataKey(key string) bool {
	if key == "" {
		return false
	}
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

func cloneMetadata(metadata storage.Metadata) storage.Metadata {
	if metadata == nil {
		return nil
	}
	out := make(storage.Metadata, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
