package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/frostgrove/vv/i18n"
)

const (
	publicationSchema              = "frostgrove.i18n.publication/v1"
	publicationPointerName         = "current.json"
	publicationGenerationsName     = "generations"
	publicationManifestRole        = "manifest"
	publicationTypeScriptRole      = "typescript"
	publicationManifestName        = "messages.public.json"
	publicationTypeScriptName      = "messages.d.ts"
	maximumPublicationPointerBytes = 64 << 10
	maximumPublicationFiles        = 16
)

type publicationFile struct {
	Role   string `json:"role"`
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type publicationPointer struct {
	Schema     string            `json:"schema"`
	Generation string            `json:"generation"`
	Address    string            `json:"address"`
	Files      []publicationFile `json:"files"`
}

type publicationContent struct {
	role    string
	name    string
	content []byte
}

type publicationHooks struct {
	beforeRootCreate       func() error
	beforeGenerationCommit func() error
	afterGeneration        func() error
	beforeCommit           func() error
	afterCommit            func() error
}

var errPublicationFileChanged = errors.New("publication file changed while opening")
var errPublicationPointerChanged = errors.New("publication pointer changed before commit")

func writePublicPublication(ctx context.Context, rootPath string, exported i18n.PublicExport, check bool, hooks publicationHooks) error {
	return writePublicPublicationBounded(ctx, rootPath, exported, check, hooks, maximumCommandInput)
}

func writePublicPublicationBounded(ctx context.Context, rootPath string, exported i18n.PublicExport, check bool, hooks publicationHooks, maximum int) (resultErr error) {
	if ctx == nil {
		return errors.New("publication context is nil")
	}
	if maximum < 1 || maximum > maximumCommandOutputBytes {
		return fmt.Errorf("publication limit %d is outside supported bounds", maximum)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pointer, contents, err := publicPublicationBounded(ctx, exported, maximum)
	if err != nil {
		return err
	}
	pointerBytes, err := encodePublicationPointer(pointer)
	if err != nil {
		return err
	}
	if len(pointerBytes) > maximum {
		return fmt.Errorf("publication pointer exceeds %d bytes", maximum)
	}
	if check {
		current, _, raw, err := readPublicPublicationRawBounded(ctx, rootPath, maximum)
		if err != nil {
			return err
		}
		if !bytes.Equal(raw, pointerBytes) || !equalPublicationPointers(current, pointer) {
			return fmt.Errorf("publication %q is stale", rootPath)
		}
		return nil
	}
	if err := ensurePublisherLockSupported(); err != nil {
		return err
	}
	root, err := openPublicationRootContext(ctx, rootPath, true, hooks.beforeRootCreate)
	if err != nil {
		return err
	}
	defer root.Close()
	lock, err := acquirePublisherLock(ctx, root)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, lock.close())
	}()
	if err := validatePublicationPointerTarget(root); err != nil {
		return err
	}
	current, _, raw, readErr := readPublicPublicationFromRootBounded(ctx, root, maximum)
	currentMatches := readErr == nil && bytes.Equal(raw, pointerBytes) && equalPublicationPointers(current, pointer)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ensurePublicationDirectory(root, publicationGenerationsName); err != nil {
		return err
	}
	generationsRoot, err := openRootedDirectory(root, publicationGenerationsName)
	if err != nil {
		return err
	}
	defer generationsRoot.Close()
	if currentMatches {
		if _, err := verifyPublicationGenerationInRootBounded(ctx, generationsRoot, pointer, maximum); err != nil {
			return err
		}
		if err := syncRootDirectory(generationsRoot); err != nil {
			return err
		}
		if err := validatePinnedRootedDirectory(root, publicationGenerationsName, generationsRoot); err != nil {
			return err
		}
		return syncRootDirectory(root)
	}
	if err := publishGenerationBounded(ctx, generationsRoot, pointer, contents, hooks, lock, maximum); err != nil {
		return err
	}
	if hooks.afterGeneration != nil {
		if err := hooks.afterGeneration(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return commitPublicationPointer(ctx, root, generationsRoot, pointerBytes, hooks, lock)
}

func readPublicPublication(ctx context.Context, rootPath string) (publicationPointer, map[string][]byte, error) {
	pointer, files, _, err := readPublicPublicationRaw(ctx, rootPath)
	return pointer, files, err
}

func readPublicPublicationRaw(ctx context.Context, rootPath string) (publicationPointer, map[string][]byte, []byte, error) {
	return readPublicPublicationRawBounded(ctx, rootPath, maximumCommandInput)
}

func readPublicPublicationRawBounded(ctx context.Context, rootPath string, maximum int) (publicationPointer, map[string][]byte, []byte, error) {
	if ctx == nil {
		return publicationPointer{}, nil, nil, errors.New("publication context is nil")
	}
	if maximum < 1 || maximum > maximumCommandOutputBytes {
		return publicationPointer{}, nil, nil, fmt.Errorf("publication limit %d is outside supported bounds", maximum)
	}
	if err := ctx.Err(); err != nil {
		return publicationPointer{}, nil, nil, err
	}
	root, err := openPublicationRootContext(ctx, rootPath, false, nil)
	if err != nil {
		return publicationPointer{}, nil, nil, err
	}
	defer root.Close()
	return readPublicPublicationFromRootBounded(ctx, root, maximum)
}

func readPublicPublicationFromRoot(ctx context.Context, root *os.Root) (publicationPointer, map[string][]byte, []byte, error) {
	return readPublicPublicationFromRootBounded(ctx, root, maximumCommandInput)
}

func readPublicPublicationFromRootBounded(ctx context.Context, root *os.Root, maximum int) (publicationPointer, map[string][]byte, []byte, error) {
	var raw []byte
	var err error
	for range 256 {
		pointerMaximum := min(int64(maximum), int64(maximumPublicationPointerBytes))
		raw, err = readRootedRegularFile(ctx, root, publicationPointerName, pointerMaximum)
		if err == nil || !errors.Is(err, errPublicationFileChanged) {
			break
		}
		if err := ctx.Err(); err != nil {
			return publicationPointer{}, nil, nil, err
		}
	}
	if err != nil {
		return publicationPointer{}, nil, nil, err
	}
	pointer, err := decodePublicationPointer(raw)
	if err != nil {
		return publicationPointer{}, nil, nil, err
	}
	files, err := verifyPublicationGenerationBounded(ctx, root, pointer, maximum)
	if err != nil {
		return publicationPointer{}, nil, nil, err
	}
	return pointer, files, raw, nil
}

func publicPublication(exported i18n.PublicExport) (publicationPointer, []publicationContent, error) {
	return publicPublicationBounded(context.Background(), exported, maximumCommandInput)
}

func publicPublicationBounded(ctx context.Context, exported i18n.PublicExport, maximum int) (publicationPointer, []publicationContent, error) {
	if maximum < 1 || maximum > maximumCommandOutputBytes {
		return publicationPointer{}, nil, fmt.Errorf("publication limit %d is outside supported bounds", maximum)
	}
	if !validSHA256Address(exported.Address) {
		return publicationPointer{}, nil, errors.New("public export has an invalid content address")
	}
	address, err := i18n.ExpectedPublicExportAddressContext(ctx, exported.Manifest, exported.TypeScript)
	if err != nil {
		return publicationPointer{}, nil, err
	}
	if exported.Address != address {
		return publicationPointer{}, nil, errors.New("public export content does not match its address")
	}
	contents := []publicationContent{
		{role: publicationManifestRole, name: publicationManifestName, content: slices.Clone(exported.Manifest)},
		{role: publicationTypeScriptRole, name: publicationTypeScriptName, content: slices.Clone(exported.TypeScript)},
	}
	files := make([]publicationFile, len(contents))
	for index, content := range contents {
		if len(content.content) > maximum {
			return publicationPointer{}, nil, fmt.Errorf("publication file %q exceeds %d bytes", content.name, maximum)
		}
		files[index] = publicationFile{
			Role: content.role, Name: content.name, Bytes: int64(len(content.content)), SHA256: sha256Address(content.content),
		}
	}
	generation := publicationGeneration(exported.Address, contents)
	return publicationPointer{Schema: publicationSchema, Generation: generation, Address: exported.Address, Files: files}, contents, nil
}

func publicationGeneration(address string, contents []publicationContent) string {
	hash := sha256.New()
	writePublicationDigestField(hash, "domain", []byte("frostgrove.i18n.publication-generation/v1"))
	writePublicationDigestField(hash, "address", []byte(address))
	for _, content := range contents {
		writePublicationDigestField(hash, "role", []byte(content.role))
		writePublicationDigestField(hash, "name", []byte(content.name))
		writePublicationDigestField(hash, "content", content.content)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func writePublicationDigestField(destination io.Writer, name string, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(name)))
	_, _ = destination.Write(size[:])
	_, _ = io.WriteString(destination, name)
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = destination.Write(size[:])
	_, _ = destination.Write(value)
}

func sha256Address(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validSHA256Address(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	digest := value[len("sha256:"):]
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(digest) == digest
}

func encodePublicationPointer(pointer publicationPointer) ([]byte, error) {
	if err := validatePublicationPointer(pointer); err != nil {
		return nil, err
	}
	raw, err := encodeCommandJSON("publication pointer", pointer)
	if err != nil {
		return nil, err
	}
	if len(raw) > maximumPublicationPointerBytes {
		return nil, fmt.Errorf("publication pointer exceeds %d bytes", maximumPublicationPointerBytes)
	}
	return raw, nil
}

func decodePublicationPointer(raw []byte) (publicationPointer, error) {
	if len(raw) == 0 || len(raw) > maximumPublicationPointerBytes {
		return publicationPointer{}, fmt.Errorf("publication pointer exceeds %d bytes", maximumPublicationPointerBytes)
	}
	if err := preflightPublicationPointer(raw); err != nil {
		return publicationPointer{}, err
	}
	var pointer publicationPointer
	if err := jsonv2.Unmarshal(raw, &pointer, jsonv2.RejectUnknownMembers(true), jsontext.AllowDuplicateNames(false)); err != nil {
		return publicationPointer{}, fmt.Errorf("decode publication pointer: %w", err)
	}
	if err := validatePublicationPointer(pointer); err != nil {
		return publicationPointer{}, err
	}
	canonical, err := encodePublicationPointer(pointer)
	if err != nil {
		return publicationPointer{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return publicationPointer{}, errors.New("publication pointer is not canonical")
	}
	return pointer, nil
}

func preflightPublicationPointer(raw []byte) error {
	decoder := stdjson.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != stdjson.Delim('{') {
		return errors.New("publication pointer is not a JSON object")
	}
	members := 0
	for decoder.More() {
		members++
		if members > 16 {
			return errors.New("publication pointer exceeds the member limit")
		}
		nameToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode publication pointer member: %w", err)
		}
		name, ok := nameToken.(string)
		if !ok {
			return errors.New("publication pointer member name is invalid")
		}
		if name != "files" {
			var value stdjson.RawMessage
			if err := decoder.Decode(&value); err != nil {
				return fmt.Errorf("decode publication pointer member: %w", err)
			}
			continue
		}
		array, err := decoder.Token()
		if err != nil || array != stdjson.Delim('[') {
			return errors.New("publication pointer file table is not an array")
		}
		files := 0
		for decoder.More() {
			files++
			if files > maximumPublicationFiles {
				return errors.New("publication pointer exceeds the file limit")
			}
			var value stdjson.RawMessage
			if err := decoder.Decode(&value); err != nil {
				return fmt.Errorf("decode publication pointer file: %w", err)
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != stdjson.Delim(']') {
			return errors.New("publication pointer file table is not closed")
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != stdjson.Delim('}') {
		return errors.New("publication pointer is not closed")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("publication pointer has trailing data")
	}
	return nil
}

func validatePublicationPointer(pointer publicationPointer) error {
	if pointer.Schema != publicationSchema {
		return fmt.Errorf("unsupported publication schema %q", pointer.Schema)
	}
	if !validSHA256Address(pointer.Generation) || !validSHA256Address(pointer.Address) {
		return errors.New("publication pointer has an invalid content address")
	}
	if len(pointer.Files) != 2 || len(pointer.Files) > maximumPublicationFiles {
		return errors.New("publication pointer has an invalid file table")
	}
	want := []struct {
		role string
		name string
	}{{publicationManifestRole, publicationManifestName}, {publicationTypeScriptRole, publicationTypeScriptName}}
	for index, file := range pointer.Files {
		if file.Role != want[index].role || file.Name != want[index].name || !fs.ValidPath(file.Name) || filepath.Base(file.Name) != file.Name || strings.Contains(file.Name, `\`) {
			return errors.New("publication pointer has an invalid file table")
		}
		if file.Bytes < 0 || file.Bytes > maximumCommandOutputBytes || !validSHA256Address(file.SHA256) {
			return errors.New("publication pointer has invalid file bounds or digest")
		}
	}
	return nil
}

func equalPublicationPointers(left, right publicationPointer) bool {
	if left.Schema != right.Schema || left.Generation != right.Generation || left.Address != right.Address || len(left.Files) != len(right.Files) {
		return false
	}
	for index := range left.Files {
		if left.Files[index] != right.Files[index] {
			return false
		}
	}
	return true
}

func publicationGenerationDirectory(generation string) string {
	return publicationGenerationsName + "/" + publicationGenerationName(generation)
}

func publicationGenerationName(generation string) string {
	return "sha256-" + strings.TrimPrefix(generation, "sha256:")
}

func publishGenerationBounded(ctx context.Context, generationsRoot *os.Root, pointer publicationPointer, contents []publicationContent, hooks publicationHooks, lock *publisherLock, maximum int) error {
	final := publicationGenerationName(pointer.Generation)
	if _, err := verifyPublicationGenerationInRootBounded(ctx, generationsRoot, pointer, maximum); err == nil {
		return syncRootDirectory(generationsRoot)
	} else if !errors.Is(err, fs.ErrNotExist) {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if info, statErr := generationsRoot.Lstat(final); statErr == nil && info.Mode().IsDir() {
			return fmt.Errorf("existing publication generation is invalid: %w", err)
		}
	}
	temporary, err := createPublicationTemporaryDirectory(generationsRoot)
	if err != nil {
		return err
	}
	keepTemporary := true
	defer func() {
		if keepTemporary {
			_ = generationsRoot.RemoveAll(temporary)
		}
	}()
	for _, content := range contents {
		if err := writePublicationFile(ctx, generationsRoot, temporary+"/"+content.name, content.content); err != nil {
			return err
		}
	}
	if err := syncRootPath(generationsRoot, temporary); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if hooks.beforeGenerationCommit != nil {
		if err := hooks.beforeGenerationCommit(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := lock.validate(); err != nil {
		return err
	}
	if err := generationsRoot.Rename(temporary, final); err != nil {
		if _, verifyErr := verifyPublicationGenerationInRootBounded(ctx, generationsRoot, pointer, maximum); verifyErr != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			return errors.Join(fmt.Errorf("publish generation: %w", err), verifyErr)
		}
		return syncRootDirectory(generationsRoot)
	}
	keepTemporary = false
	if err := syncRootDirectory(generationsRoot); err != nil {
		return err
	}
	_, err = verifyPublicationGenerationInRootBounded(ctx, generationsRoot, pointer, maximum)
	return err
}

func verifyPublicationGeneration(ctx context.Context, root *os.Root, pointer publicationPointer) (map[string][]byte, error) {
	return verifyPublicationGenerationBounded(ctx, root, pointer, maximumCommandInput)
}

func verifyPublicationGenerationBounded(ctx context.Context, root *os.Root, pointer publicationPointer, maximum int) (map[string][]byte, error) {
	generationsRoot, err := openRootedDirectory(root, publicationGenerationsName)
	if err != nil {
		return nil, err
	}
	defer generationsRoot.Close()
	return verifyPublicationGenerationInRootBounded(ctx, generationsRoot, pointer, maximum)
}

func verifyPublicationGenerationInRootBounded(ctx context.Context, generationsRoot *os.Root, pointer publicationPointer, maximum int) (map[string][]byte, error) {
	if maximum < 1 || maximum > maximumCommandOutputBytes {
		return nil, fmt.Errorf("publication limit %d is outside supported bounds", maximum)
	}
	if err := validatePublicationPointer(pointer); err != nil {
		return nil, err
	}
	generationRoot, err := openRootedDirectory(generationsRoot, publicationGenerationName(pointer.Generation))
	if err != nil {
		return nil, err
	}
	defer generationRoot.Close()
	entries, err := readRootDirectoryNames(generationRoot)
	if err != nil {
		return nil, err
	}
	if !slices.Equal(entries, []string{publicationTypeScriptName, publicationManifestName}) {
		return nil, errors.New("publication generation contains an unexpected file set")
	}
	files := make(map[string][]byte, len(pointer.Files))
	contents := make([]publicationContent, len(pointer.Files))
	for index, descriptor := range pointer.Files {
		if descriptor.Bytes > int64(maximum) {
			return nil, fmt.Errorf("publication file %q exceeds %d bytes", descriptor.Name, maximum)
		}
		content, err := readRootedRegularFile(ctx, generationRoot, descriptor.Name, int64(maximum))
		if err != nil {
			return nil, err
		}
		if int64(len(content)) != descriptor.Bytes || sha256Address(content) != descriptor.SHA256 {
			return nil, fmt.Errorf("publication file %q does not match its pointer", descriptor.Name)
		}
		files[descriptor.Role] = content
		contents[index] = publicationContent{role: descriptor.Role, name: descriptor.Name, content: content}
	}
	address, err := i18n.ExpectedPublicExportAddressContext(ctx, files[publicationManifestRole], files[publicationTypeScriptRole])
	if err != nil {
		return nil, err
	}
	if address != pointer.Address {
		return nil, errors.New("publication files do not match the public export address")
	}
	if publicationGeneration(pointer.Address, contents) != pointer.Generation {
		return nil, errors.New("publication generation does not match its pointer")
	}
	return files, nil
}

func createPublicationTemporaryDirectory(root *os.Root) (string, error) {
	var random [12]byte
	for range 32 {
		if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
			return "", fmt.Errorf("generate temporary publication name: %w", err)
		}
		name := ".tmp-" + hex.EncodeToString(random[:])
		if err := root.Mkdir(name, 0o755); err == nil {
			return name, nil
		} else if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create temporary publication generation: %w", err)
		}
	}
	return "", errors.New("cannot allocate a temporary publication generation")
}

func writePublicationFile(ctx context.Context, root *os.Root, name string, content []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create publication file: %w", err)
	}
	writeErr := writeFileContext(ctx, file, content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.Join(writeErr, syncErr, closeErr)
	}
	return nil
}

func commitPublicationPointer(ctx context.Context, root, generationsRoot *os.Root, content []byte, hooks publicationHooks, lock *publisherLock) error {
	return commitPublicationPointerAttempt(ctx, root, generationsRoot, content, hooks, lock)
}

func commitPublicationPointerAttempt(ctx context.Context, root, generationsRoot *os.Root, content []byte, hooks publicationHooks, lock *publisherLock) error {
	mode, identity, existed, err := inspectPublicationTarget(root, publicationPointerName, publicationPointerName)
	if err != nil {
		return err
	}
	temporary, file, err := createTemporary(root, publicationPointerName, mode)
	if err != nil {
		return err
	}
	keepTemporary := true
	defer func() {
		if keepTemporary {
			_ = root.Remove(temporary)
		}
	}()
	if err := writeFileContext(ctx, file, content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync publication pointer: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close publication pointer: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if hooks.beforeCommit != nil {
		if err := hooks.beforeCommit(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePinnedRootedDirectory(root, publicationGenerationsName, generationsRoot); err != nil {
		return err
	}
	if err := validatePublicationPointerCommitTarget(root, identity, existed); err != nil {
		return err
	}
	if err := lock.validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(temporary, publicationPointerName); err != nil {
		return fmt.Errorf("commit publication pointer: %w", err)
	}
	keepTemporary = false
	var hookErr error
	if hooks.afterCommit != nil {
		hookErr = hooks.afterCommit()
	}
	return errors.Join(hookErr, syncRootDirectory(root))
}

func validatePublicationPointerCommitTarget(root *os.Root, identity fs.FileInfo, existed bool) error {
	info, err := root.Lstat(publicationPointerName)
	if !existed {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect publication pointer before commit: %w", err)
		}
		return fmt.Errorf("%w: target appeared", errPublicationPointerChanged)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: target disappeared", errPublicationPointerChanged)
	}
	if err != nil {
		return fmt.Errorf("inspect publication pointer before commit: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || !os.SameFile(identity, info) {
		return fmt.Errorf("%w: target identity was replaced", errPublicationPointerChanged)
	}
	return nil
}

func validatePublicationPointerTarget(root *os.Root) error {
	info, err := root.Lstat(publicationPointerName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect publication pointer: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("publication pointer is not a regular file")
	}
	return nil
}

func openPublicationRoot(path string, create bool) (*os.Root, error) {
	return openPublicationRootContext(context.Background(), path, create, nil)
}

func openPublicationRootBeforeCreate(path string, create bool, beforeCreate func() error) (*os.Root, error) {
	return openPublicationRootContext(context.Background(), path, create, beforeCreate)
}

func openPublicationRootContext(ctx context.Context, path string, create bool, beforeCreate func() error) (*os.Root, error) {
	if path == "" {
		return nil, errors.New("publication root is empty")
	}
	root, err := openStableAbsoluteRoot(ctx, path, create, beforeCreate)
	if err != nil {
		return nil, fmt.Errorf("open publication root: %w", err)
	}
	return root, nil
}

func ensurePublicationDirectory(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	created := false
	if errors.Is(err, fs.ErrNotExist) {
		if err := root.Mkdir(name, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create publication directory: %w", err)
		}
		created = true
		info, err = root.Lstat(name)
	}
	if err != nil {
		return fmt.Errorf("inspect publication directory: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsDir() {
		return errors.New("publication generations path is not a regular directory")
	}
	if created {
		return syncRootDirectory(root)
	}
	return nil
}

func openRootedDirectory(root *os.Root, name string) (*os.Root, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsDir() {
		return nil, fmt.Errorf("publication path %q is not a regular directory", name)
	}
	opened, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	openedInfo, err := opened.Stat(".")
	if err != nil || !os.SameFile(info, openedInfo) {
		_ = opened.Close()
		return nil, fmt.Errorf("publication directory %q changed while opening", name)
	}
	return opened, nil
}

func validatePinnedRootedDirectory(root *os.Root, name string, pinned *os.Root) error {
	current, err := root.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect pinned publication directory %q: %w", name, err)
	}
	identity, err := pinned.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect pinned publication directory handle %q: %w", name, err)
	}
	if current.Mode()&fs.ModeSymlink != 0 || !current.Mode().IsDir() || !os.SameFile(identity, current) {
		return fmt.Errorf("publication directory %q changed before pointer commit", name)
	}
	return nil
}

func readRootedRegularFile(ctx context.Context, root *os.Root, name string, maximum int64) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("publication context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maximum {
		return nil, fmt.Errorf("publication file %q is not a bounded regular file", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%w: %q", errPublicationFileChanged, name)
	}
	raw, err := readContextBounded(ctx, file, maximum)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func readRootDirectoryNames(root *os.Root) ([]string, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	names, readErr := directory.Readdirnames(maximumPublicationFiles + 1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if len(names) > maximumPublicationFiles {
		return nil, errors.New("publication generation exceeds the file limit")
	}
	slices.Sort(names)
	return names, nil
}
