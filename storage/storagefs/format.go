package storagefs

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/frostgrove/vv/storage"
)

const privateHeaderSize = 16 << 10

const maxConsecutiveEmptyReads = 100

var privateMagic = [8]byte{'V', 'V', 'S', 'T', 'O', 'R', '0', '1'}

var errInvalidPrivateFormat = fmt.Errorf("invalid private object format")

type privateHeader struct {
	Version     int               `json:"version"`
	Size        int64             `json:"size"`
	ContentType string            `json:"content_type"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	ModifiedAt  int64             `json:"modified_at"`
	ExpiresAt   int64             `json:"expires_at,omitempty"`
}

func (this privateHeader) info() storage.Info {
	var metadata storage.Metadata
	if len(this.Metadata) != 0 {
		metadata = make(storage.Metadata, len(this.Metadata))
		for key, value := range this.Metadata {
			metadata[key] = value
		}
	}
	return storage.Info{
		Size:        this.Size,
		ContentType: this.ContentType,
		Metadata:    metadata,
		ModifiedAt:  time.Unix(0, this.ModifiedAt).UTC(),
	}
}

func (this *Backend) writePrivateFile(ctx context.Context, file *os.File, source io.Reader, expectedSize *int64, contentType string, metadata storage.Metadata, expiresAt time.Time) (storage.Info, error) {
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if source == nil {
		return storage.Info{}, storage.NewError("write", storage.KindInvalid, fmt.Errorf("source is nil"))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if expectedSize != nil && *expectedSize < 0 {
		return storage.Info{}, storage.NewError("write", storage.KindInvalid, fmt.Errorf("declared size is negative"))
	}

	preflight, err := encodePrivateHeader(privateHeader{
		Version:     1,
		Size:        math.MaxInt64,
		ContentType: contentType,
		Metadata:    cloneMetadata(metadata),
		ModifiedAt:  math.MaxInt64,
		ExpiresAt:   math.MaxInt64,
	})
	if err != nil {
		return storage.Info{}, storage.NewError("write", storage.KindInternal, err)
	}
	if len(preflight) > privateHeaderSize-12 {
		return storage.Info{}, storage.NewError("write", storage.KindInvalid, fmt.Errorf("private header exceeds fixed bound"))
	}
	if _, err := file.Seek(privateHeaderSize, io.SeekStart); err != nil {
		return storage.Info{}, filesystemError("write", err)
	}
	sourceReader := &contextSourceReader{ctx: ctx, source: source}
	var copySource io.Reader = sourceReader
	if expectedSize != nil && *expectedSize < math.MaxInt64 {
		copySource = io.LimitReader(sourceReader, *expectedSize+1)
	}
	written, err := io.CopyBuffer(file, copySource, make([]byte, 64<<10))
	if err != nil {
		return storage.Info{}, filesystemError("write", err)
	}
	if expectedSize != nil && written != *expectedSize {
		return storage.Info{}, storage.NewError("write", storage.KindSource, fmt.Errorf("source size does not match declaration"))
	}
	if written > math.MaxInt64-privateHeaderSize {
		return storage.Info{}, storage.NewError("write", storage.KindSource, fmt.Errorf("source is too large"))
	}
	if err := contextError(ctx); err != nil {
		return storage.Info{}, storage.NewError("write", storage.KindCancelled, err)
	}
	modifiedAt := this.now().UTC()
	header := privateHeader{
		Version:     1,
		Size:        written,
		ContentType: contentType,
		Metadata:    cloneMetadata(metadata),
		ModifiedAt:  modifiedAt.UnixNano(),
	}
	if !expiresAt.IsZero() {
		header.ExpiresAt = expiresAt.UnixNano()
	}
	encoded, err := encodePrivateHeader(header)
	if err != nil {
		return storage.Info{}, storage.NewError("write", storage.KindInternal, err)
	}
	if len(encoded) > privateHeaderSize-12 {
		return storage.Info{}, storage.NewError("write", storage.KindInvalid, fmt.Errorf("private header exceeds fixed bound"))
	}
	block := make([]byte, privateHeaderSize)
	copy(block[:8], privateMagic[:])
	binary.BigEndian.PutUint32(block[8:12], uint32(len(encoded)))
	copy(block[12:], encoded)
	if _, err := file.WriteAt(block, 0); err != nil {
		return storage.Info{}, filesystemError("write", err)
	}
	if err := file.Truncate(privateHeaderSize + written); err != nil {
		return storage.Info{}, filesystemError("write", err)
	}
	if err := file.Chmod(this.fileMode); err != nil {
		return storage.Info{}, filesystemError("write", err)
	}
	if this.syncWrites {
		if err := file.Sync(); err != nil {
			return storage.Info{}, filesystemError("write", err)
		}
	}
	if err := file.Close(); err != nil {
		return storage.Info{}, filesystemError("write", err)
	}
	closed = true
	return header.info(), nil
}

func encodePrivateHeader(header privateHeader) ([]byte, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(header); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'}), nil
}

func (this *Backend) openPrivateFile(name string) (*os.File, privateHeader, error) {
	file, err := this.root.Open(name)
	if err != nil {
		return nil, privateHeader{}, err
	}
	fail := func(err error) (*os.File, privateHeader, error) {
		_ = file.Close()
		return nil, privateHeader{}, err
	}
	stat, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !stat.Mode().IsRegular() {
		return fail(fmt.Errorf("%w: object is not a regular file", errInvalidPrivateFormat))
	}
	block := make([]byte, privateHeaderSize)
	if _, err := io.ReadFull(file, block); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fail(fmt.Errorf("%w: header is truncated", errInvalidPrivateFormat))
		}
		return fail(err)
	}
	if !bytes.Equal(block[:8], privateMagic[:]) {
		return fail(fmt.Errorf("%w: magic is invalid", errInvalidPrivateFormat))
	}
	headerLength := int(binary.BigEndian.Uint32(block[8:12]))
	if headerLength <= 0 || headerLength > len(block)-12 {
		return fail(fmt.Errorf("%w: header length is invalid", errInvalidPrivateFormat))
	}
	decoder := json.NewDecoder(bytes.NewReader(block[12 : 12+headerLength]))
	decoder.DisallowUnknownFields()
	var header privateHeader
	if err := decoder.Decode(&header); err != nil {
		return fail(fmt.Errorf("%w: header JSON: %v", errInvalidPrivateFormat, err))
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fail(fmt.Errorf("%w: header has trailing data", errInvalidPrivateFormat))
	}
	if err := validatePrivateHeader(header, stat.Size()); err != nil {
		return fail(fmt.Errorf("%w: %v", errInvalidPrivateFormat, err))
	}
	return file, header, nil
}

func validatePrivateHeader(header privateHeader, physicalSize int64) error {
	if header.Version != 1 || header.Size < 0 || header.ModifiedAt <= 0 || header.ExpiresAt < 0 || header.ContentType == "" {
		return fmt.Errorf("private object header values are invalid")
	}
	if header.Size > math.MaxInt64-privateHeaderSize || physicalSize != privateHeaderSize+header.Size {
		return fmt.Errorf("private object size is invalid")
	}
	if len(header.ContentType) > 255 || !utf8.ValidString(header.ContentType) || len(header.Metadata) > storage.MaxMetadataEntries {
		return fmt.Errorf("private object metadata is invalid")
	}
	if _, _, err := mime.ParseMediaType(header.ContentType); err != nil {
		return fmt.Errorf("private object metadata is invalid")
	}
	total := 0
	for key, value := range header.Metadata {
		if len(key) == 0 || len(key) > storage.MaxMetadataKeyBytes || !validPrivateMetadataKey(key) || strings.HasPrefix(key, "vv-") || strings.HasPrefix(key, "x-amz-") {
			return fmt.Errorf("private object metadata is invalid")
		}
		if len(value) > storage.MaxMetadataValueBytes || !utf8.ValidString(value) || privateValueHasControl(value) {
			return fmt.Errorf("private object metadata is invalid")
		}
		total += len(key) + len(value)
		if total > storage.MaxMetadataTotalBytes {
			return fmt.Errorf("private object metadata is invalid")
		}
	}
	return nil
}

func validPrivateMetadataKey(key string) bool {
	for i := range len(key) {
		c := key[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '-' || c == '_' || c == '.') {
			continue
		}
		return false
	}
	return true
}

func privateValueHasControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

type contextSourceReader struct {
	ctx        context.Context
	source     io.Reader
	emptyReads int
}

func (this *contextSourceReader) Read(buffer []byte) (int, error) {
	if err := contextError(this.ctx); err != nil {
		return 0, err
	}
	n, err := this.source.Read(buffer)
	if contextErr := contextError(this.ctx); contextErr != nil {
		return n, contextErr
	}
	if n == 0 && err == nil && len(buffer) != 0 {
		this.emptyReads++
		if this.emptyReads >= maxConsecutiveEmptyReads {
			return 0, &sourceReadError{err: io.ErrNoProgress}
		}
	} else if n > 0 {
		this.emptyReads = 0
	}
	if err != nil && err != io.EOF {
		return n, &sourceReadError{err: err}
	}
	return n, err
}

type sourceReadError struct{ err error }

func (this *sourceReadError) Error() string { return "storage source read failed" }
func (this *sourceReadError) Unwrap() error { return this.err }

type objectBody struct {
	ctx       context.Context
	file      *os.File
	remaining int64
}

func (this *objectBody) Read(buffer []byte) (int, error) {
	if err := contextError(this.ctx); err != nil {
		return 0, storage.NewError("read", storage.KindCancelled, err)
	}
	if this.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(buffer)) > this.remaining {
		buffer = buffer[:this.remaining]
	}
	n, err := this.file.Read(buffer)
	this.remaining -= int64(n)
	if err == io.EOF && this.remaining > 0 {
		return n, filesystemError("read", io.ErrUnexpectedEOF)
	}
	if err != nil && err != io.EOF {
		return n, filesystemError("read", err)
	}
	return n, err
}

func (this *objectBody) Close() error {
	return filesystemError("close", this.file.Close())
}

func cloneMetadata(metadata storage.Metadata) map[string]string {
	if metadata == nil {
		return nil
	}
	copy := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copy[key] = value
	}
	return copy
}

var _ fs.File = (*os.File)(nil)
