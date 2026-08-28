// Package storagefs implements storage.Backend on a private filesystem tree.
package storagefs

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/frostgrove/vv/storage"
)

const (
	DefaultFileMode fs.FileMode = 0o600
	DefaultDirMode  fs.FileMode = 0o700

	privateDirectory = ".vv-storage-v1"
	orphanWorkTTL    = storage.MaxStageTTL
	workRandomBytes  = 18
)

// Config declares the filesystem root and, optionally, the HTTP endpoint used
// for temporary bearer links. Root must be absolute so a later chdir cannot
// change which tree the backend owns.
type Config struct {
	Root       string
	FileMode   fs.FileMode
	DirMode    fs.FileMode
	Sync       bool
	BaseURL    string
	SigningKey []byte
	MaxLinkTTL time.Duration
}

// Backend is a filesystem implementation of storage.Backend. It owns only its
// os.Root handle; callers still own every source and returned body.
type Backend struct {
	root       *os.Root
	fileMode   fs.FileMode
	dirMode    fs.FileMode
	syncWrites bool
	baseURL    *url.URL
	signingKey []byte
	maxLinkTTL time.Duration
	now        func() time.Time

	placeRemove func(string) error
	placeSync   func(string) error
	claimRemove func(string) error

	closeOnce sync.Once
	closeErr  error
}

var _ storage.Backend = (*Backend)(nil)

var caseSafeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// New opens an explicitly configured root. It creates the root when absent,
// but never resolves a relative path against the process working directory.
func New(config *Config) (*Backend, error) {
	if config == nil {
		return nil, storage.NewError("construct", storage.KindInvalid, fmt.Errorf("config is nil"))
	}
	if !supportedOperatingSystem(runtime.GOOS) {
		return nil, storage.NewError("construct", storage.KindUnsupported, fmt.Errorf("filesystem storage requires Unix link and rename semantics"))
	}
	if config.Root == "" || !filepath.IsAbs(config.Root) {
		return nil, storage.NewError("construct", storage.KindInvalid, fmt.Errorf("filesystem root is not absolute"))
	}
	fileMode := config.FileMode
	if fileMode == 0 {
		fileMode = DefaultFileMode
	}
	dirMode := config.DirMode
	if dirMode == 0 {
		dirMode = DefaultDirMode
	}
	if fileMode != fileMode.Perm() || fileMode&0o600 != 0o600 || fileMode&0o022 != 0 {
		return nil, storage.NewError("construct", storage.KindInvalid, fmt.Errorf("file mode must grant owner read/write and no group/world write"))
	}
	if dirMode != dirMode.Perm() || dirMode&0o700 != 0o700 || dirMode&0o022 != 0 {
		return nil, storage.NewError("construct", storage.KindInvalid, fmt.Errorf("directory mode must grant owner access and no group/world write"))
	}

	baseURL, signingKey, maxLinkTTL, err := linkConfig(config)
	if err != nil {
		return nil, err
	}

	rootName := filepath.Clean(config.Root)
	if err := os.MkdirAll(rootName, dirMode); err != nil {
		return nil, filesystemError("construct", err)
	}
	rootInfo, err := os.Lstat(rootName)
	if err != nil {
		return nil, filesystemError("construct", err)
	}
	if !safeOwnedDirectory(rootInfo) {
		return nil, storage.NewError("construct", storage.KindInvalid, fmt.Errorf("filesystem root must be a non-writable real directory"))
	}
	root, err := os.OpenRoot(rootName)
	if err != nil {
		return nil, filesystemError("construct", err)
	}
	b := &Backend{
		root:       root,
		fileMode:   fileMode,
		dirMode:    dirMode,
		syncWrites: config.Sync,
		baseURL:    baseURL,
		signingKey: signingKey,
		maxLinkTTL: maxLinkTTL,
		now:        time.Now,
	}
	for _, directory := range []string{
		privateDirectory,
		path.Join(privateDirectory, "objects"),
		path.Join(privateDirectory, "staging"),
		path.Join(privateDirectory, "work"),
	} {
		if err := b.root.MkdirAll(directory, b.dirMode); err != nil {
			_ = root.Close()
			return nil, filesystemError("construct", err)
		}
		info, err := b.root.Lstat(directory)
		if err != nil || !safeOwnedDirectory(info) {
			_ = root.Close()
			if err != nil {
				return nil, filesystemError("construct", err)
			}
			return nil, storage.NewError("construct", storage.KindInvalid, fmt.Errorf("private storage directory is unsafe"))
		}
	}
	return b, nil
}

// Close releases the root directory handle. It is safe to call more than once.
func (b *Backend) Close() error {
	if b == nil || b.root == nil {
		return nil
	}
	b.closeOnce.Do(func() { b.closeErr = b.root.Close() })
	return b.closeErr
}

func (b *Backend) Put(ctx context.Context, namespace storage.Namespace, key storage.Key, source io.Reader, opts storage.PutOptions) (storage.Info, error) {
	if err := contextError(ctx); err != nil {
		return storage.Info{}, storage.NewError("put", storage.KindCancelled, err)
	}
	mode, err := writeMode(opts.Mode)
	if err != nil {
		return storage.Info{}, storage.NewError("put", storage.KindInvalid, err)
	}
	work, workName, err := b.newWorkFile(namespace)
	if err != nil {
		return storage.Info{}, filesystemError("put", err)
	}
	defer func() {
		_ = b.root.Remove(workName)
	}()

	info, err := b.writePrivateFile(ctx, work, source, opts.Size, opts.ContentType, opts.Metadata, time.Time{})
	if err != nil {
		return storage.Info{}, operationError("put", err)
	}
	if _, err := b.place(ctx, workName, objectPath(namespace, key), mode); err != nil {
		return storage.Info{}, operationError("put", err)
	}
	return info, nil
}

func (b *Backend) Open(ctx context.Context, namespace storage.Namespace, key storage.Key) (io.ReadCloser, storage.Info, error) {
	if err := contextError(ctx); err != nil {
		return nil, storage.Info{}, storage.NewError("open", storage.KindCancelled, err)
	}
	file, header, err := b.openPrivateFile(objectPath(namespace, key))
	if err != nil {
		return nil, storage.Info{}, filesystemError("open", err)
	}
	if err := contextError(ctx); err != nil {
		_ = file.Close()
		return nil, storage.Info{}, storage.NewError("open", storage.KindCancelled, err)
	}
	return &objectBody{ctx: ctx, file: file, remaining: header.Size}, header.info(), nil
}

func (b *Backend) Head(ctx context.Context, namespace storage.Namespace, key storage.Key) (storage.Info, error) {
	if err := contextError(ctx); err != nil {
		return storage.Info{}, storage.NewError("head", storage.KindCancelled, err)
	}
	file, header, err := b.openPrivateFile(objectPath(namespace, key))
	if err != nil {
		return storage.Info{}, filesystemError("head", err)
	}
	if err := file.Close(); err != nil {
		return storage.Info{}, filesystemError("head", err)
	}
	return header.info(), nil
}

func (b *Backend) Delete(ctx context.Context, namespace storage.Namespace, key storage.Key) error {
	if err := contextError(ctx); err != nil {
		return storage.NewError("delete", storage.KindCancelled, err)
	}
	name := objectPath(namespace, key)
	removed := true
	if err := b.root.Remove(name); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return filesystemError("delete", err)
		}
		removed = false
	}
	if b.syncWrites && removed {
		if err := b.syncDirectory(path.Dir(name)); err != nil {
			return filesystemError("delete", err)
		}
	}
	return nil
}

func (b *Backend) Stage(ctx context.Context, namespace storage.Namespace, source io.Reader, opts storage.StageOptions) (storage.Staged, error) {
	if err := contextError(ctx); err != nil {
		return storage.Staged{}, storage.NewError("stage", storage.KindCancelled, err)
	}
	ttl := opts.ExpiresIn
	if ttl == 0 {
		ttl = storage.DefaultStageTTL
	}
	if ttl < time.Second || ttl > storage.MaxStageTTL {
		return storage.Staged{}, storage.NewError("stage", storage.KindInvalid, fmt.Errorf("stage expiry is outside filesystem policy"))
	}
	id, err := storage.NewStageID()
	if err != nil {
		return storage.Staged{}, err
	}
	expiresAt := b.now().UTC().Add(ttl)
	work, workName, err := b.newWorkFile(namespace)
	if err != nil {
		return storage.Staged{}, filesystemError("stage", err)
	}
	defer func() {
		_ = b.root.Remove(workName)
	}()

	info, err := b.writePrivateFile(ctx, work, source, opts.Size, opts.ContentType, opts.Metadata, expiresAt)
	if err != nil {
		return storage.Staged{}, operationError("stage", err)
	}
	if _, err := b.place(ctx, workName, stagePath(namespace, id), storage.CreateOnly); err != nil {
		return storage.Staged{}, operationError("stage", err)
	}
	return storage.Staged{ID: id, Info: info, ExpiresAt: expiresAt}, nil
}

func (b *Backend) Promote(ctx context.Context, namespace storage.Namespace, id storage.StageID, key storage.Key, opts storage.PromoteOptions) (result storage.Info, resultErr error) {
	if err := contextError(ctx); err != nil {
		return storage.Info{}, storage.NewError("promote", storage.KindCancelled, err)
	}
	mode, err := writeMode(opts.Mode)
	if err != nil {
		return storage.Info{}, storage.NewError("promote", storage.KindInvalid, err)
	}
	claimedName, err := b.claimStage("promote", namespace, id)
	if err != nil {
		return storage.Info{}, err
	}
	releaseOnReturn := true
	defer func() {
		if releaseOnReturn {
			if err := b.releaseClaim(namespace, id); err != nil {
				result = storage.Info{}
				resultErr = filesystemError("promote", err)
			}
		}
	}()
	file, header, err := b.openPrivateFile(claimedName)
	if err != nil {
		return storage.Info{}, filesystemError("promote", err)
	}
	closeErr := file.Close()
	if closeErr != nil {
		return storage.Info{}, filesystemError("promote", closeErr)
	}
	if header.ExpiresAt == 0 {
		return storage.Info{}, storage.NewError("promote", storage.KindInternal, fmt.Errorf("private stage has no expiry"))
	}
	if !b.now().Before(time.Unix(0, header.ExpiresAt)) {
		consumed, err := b.consumeClaim(namespace, id, b.root.Remove)
		if consumed {
			releaseOnReturn = false
		}
		if err != nil {
			return storage.Info{}, filesystemError("promote", err)
		}
		return storage.Info{}, storage.NewError("promote", storage.KindExpired, fmt.Errorf("stage expired"))
	}
	placed, err := b.placeClaim(ctx, namespace, claimedName, objectPath(namespace, key), mode)
	if placed {
		// Final identity is visible while both protecting links still exist.
		// Consume stage before claim so a second promoter can never observe an
		// unlocked live stage, including after a post-placement sync failure.
		releaseOnReturn = false
		if _, consumeErr := b.consumeClaim(namespace, id, b.removeClaimName); consumeErr != nil {
			return storage.Info{}, filesystemError("promote", consumeErr)
		}
	}
	if err != nil {
		return storage.Info{}, operationError("promote", err)
	}
	return header.info(), nil
}

func (b *Backend) Abort(ctx context.Context, namespace storage.Namespace, id storage.StageID) error {
	return b.abortWithRemove(ctx, namespace, id, b.root.Remove)
}

func (b *Backend) abortWithRemove(ctx context.Context, namespace storage.Namespace, id storage.StageID, remove func(string) error) (resultErr error) {
	if err := contextError(ctx); err != nil {
		return storage.NewError("abort", storage.KindCancelled, err)
	}
	claimedName, err := b.claimStage("abort", namespace, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_ = claimedName
	releaseOnReturn := true
	defer func() {
		if releaseOnReturn {
			if err := b.releaseClaim(namespace, id); err != nil {
				resultErr = filesystemError("abort", err)
			}
		}
	}()
	consumed, err := b.consumeClaim(namespace, id, remove)
	if consumed {
		releaseOnReturn = false
	}
	if err != nil {
		return filesystemError("abort", err)
	}
	return nil
}

func (b *Backend) CleanupExpired(ctx context.Context, namespace storage.Namespace, opts storage.CleanupOptions) (storage.CleanupResult, error) {
	if err := contextError(ctx); err != nil {
		return storage.CleanupResult{}, storage.NewError("cleanup", storage.KindCancelled, err)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = storage.DefaultCleanupLimit
	}
	result := storage.CleanupResult{}
	if err := b.cleanupExpiredStages(ctx, namespace, limit, &result); err != nil {
		return result, err
	}
	if result.More {
		return result, nil
	}
	if err := b.cleanupOrphanWork(ctx, namespace, limit, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (b *Backend) cleanupExpiredStages(ctx context.Context, namespace storage.Namespace, limit int, result *storage.CleanupResult) error {
	directory := stageDirectory(namespace)
	dir, err := b.root.Open(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return filesystemError("cleanup", err)
	}
	defer dir.Close()
	dirty := false
	for {
		if err := contextError(ctx); err != nil {
			return storage.NewError("cleanup", storage.KindCancelled, err)
		}
		entries, readErr := dir.ReadDir(64)
		for _, entry := range entries {
			id, ok := stageIDFromName(entry.Name())
			if !ok || entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
				continue
			}
			name := path.Join(directory, entry.Name())
			file, header, openErr := b.openPrivateFile(name)
			if openErr != nil {
				if errors.Is(openErr, fs.ErrNotExist) {
					continue
				}
				if errors.Is(openErr, errInvalidPrivateFormat) {
					// Corrupted entries are not proven owned stages and are deliberately
					// left for an operator rather than deleted by a sweep.
					continue
				}
				return filesystemError("cleanup", openErr)
			}
			if closeErr := file.Close(); closeErr != nil {
				return filesystemError("cleanup", closeErr)
			}
			if header.ExpiresAt == 0 || b.now().Before(time.Unix(0, header.ExpiresAt)) {
				continue
			}
			removed := false
			for _, ownedName := range []string{stagePath(namespace, id), stageClaimPath(namespace, id)} {
				if removeErr := b.root.Remove(ownedName); removeErr != nil {
					if errors.Is(removeErr, fs.ErrNotExist) {
						continue
					}
					return filesystemError("cleanup", removeErr)
				}
				removed = true
			}
			if removed {
				result.Removed++
				dirty = true
				if result.Removed == limit {
					result.More = true
					return b.finishCleanupDirectory(directory, dirty)
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return filesystemError("cleanup", readErr)
		}
	}
	return b.finishCleanupDirectory(directory, dirty)
}

func (b *Backend) cleanupOrphanWork(ctx context.Context, namespace storage.Namespace, limit int, result *storage.CleanupResult) error {
	directory := path.Join(privateDirectory, "work", namespace.Value())
	dir, err := b.root.Open(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return filesystemError("cleanup", err)
	}
	defer dir.Close()
	dirty := false
	cutoff := b.now().Add(-orphanWorkTTL)
	for {
		if err := contextError(ctx); err != nil {
			return storage.NewError("cleanup", storage.KindCancelled, err)
		}
		entries, readErr := dir.ReadDir(64)
		for _, entry := range entries {
			if !validWorkName(entry.Name()) || entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
				continue
			}
			info, infoErr := entry.Info()
			if errors.Is(infoErr, fs.ErrNotExist) {
				continue
			}
			if infoErr != nil {
				return filesystemError("cleanup", infoErr)
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			if removeErr := b.root.Remove(path.Join(directory, entry.Name())); removeErr != nil {
				if errors.Is(removeErr, fs.ErrNotExist) {
					continue
				}
				return filesystemError("cleanup", removeErr)
			}
			result.Removed++
			dirty = true
			if result.Removed == limit {
				result.More = true
				return b.finishCleanupDirectory(directory, dirty)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return filesystemError("cleanup", readErr)
		}
	}
	return b.finishCleanupDirectory(directory, dirty)
}

func (b *Backend) finishCleanupDirectory(directory string, dirty bool) error {
	if b.syncWrites && dirty {
		if err := b.syncDirectory(directory); err != nil {
			return filesystemError("cleanup", err)
		}
	}
	return nil
}

func (b *Backend) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		CreateOnly:   true,
		Replace:      true,
		Staging:      true,
		TemporaryURL: b != nil && b.baseURL != nil,
	}
}

func (b *Backend) newWorkFile(namespace storage.Namespace) (*os.File, string, error) {
	directory := path.Join(privateDirectory, "work", namespace.Value())
	if err := b.root.MkdirAll(directory, b.dirMode); err != nil {
		return nil, "", err
	}
	for range 16 {
		var random [workRandomBytes]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, "", err
		}
		name := path.Join(directory, encodeCaseSafe(random[:])+".tmp")
		file, err := b.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, b.fileMode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if err := file.Chmod(b.fileMode); err != nil {
			_ = file.Close()
			_ = b.root.Remove(name)
			return nil, "", err
		}
		return file, name, nil
	}
	return nil, "", fmt.Errorf("could not allocate a private work file")
}

func (b *Backend) newWorkLink(namespace storage.Namespace, sourceName string) (string, error) {
	directory := path.Join(privateDirectory, "work", namespace.Value())
	if err := b.root.MkdirAll(directory, b.dirMode); err != nil {
		return "", err
	}
	for range 16 {
		var random [workRandomBytes]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		name := path.Join(directory, encodeCaseSafe(random[:])+".tmp")
		err := b.root.Link(sourceName, name)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return name, nil
	}
	return "", fmt.Errorf("could not allocate a private work link")
}

func validWorkName(name string) bool {
	raw, ok := strings.CutSuffix(name, ".tmp")
	if !ok || len(raw) != caseSafeEncoding.EncodedLen(workRandomBytes) {
		return false
	}
	decoded, err := decodeCaseSafe(raw)
	return err == nil && len(decoded) == workRandomBytes && encodeCaseSafe(decoded) == raw
}

func (b *Backend) place(ctx context.Context, sourceName, destinationName string, mode storage.WriteMode) (bool, error) {
	if err := contextError(ctx); err != nil {
		return false, storage.NewError("place", storage.KindCancelled, err)
	}
	if err := b.root.MkdirAll(path.Dir(destinationName), b.dirMode); err != nil {
		return false, filesystemError("place", err)
	}
	var err error
	switch mode {
	case storage.CreateOnly:
		err = b.root.Link(sourceName, destinationName)
		if errors.Is(err, fs.ErrExist) {
			return false, storage.NewError("place", storage.KindAlreadyExists, err)
		}
	case storage.Replace:
		err = b.root.Rename(sourceName, destinationName)
	default:
		return false, storage.NewError("place", storage.KindInvalid, fmt.Errorf("unknown write mode"))
	}
	if err != nil {
		return false, filesystemError("place", err)
	}
	if mode == storage.CreateOnly {
		// The destination is already complete. Failure to unlink the private
		// work/stage name cannot make that committed object false.
		if b.placeRemove != nil {
			_ = b.placeRemove(sourceName)
		} else {
			_ = b.root.Remove(sourceName)
		}
	}
	if b.syncWrites {
		var syncErr error
		if b.placeSync != nil {
			syncErr = b.placeSync(path.Dir(destinationName))
		} else {
			syncErr = b.syncDirectory(path.Dir(destinationName))
		}
		if syncErr != nil {
			return true, filesystemError("place", syncErr)
		}
	}
	return true, nil
}

// placeClaim establishes the final object without consuming either protecting
// link of a staged upload. CreateOnly can link the claim directly. Replace
// atomically renames a fresh private hard link, leaving stage and claim intact
// until final visibility is known.
func (b *Backend) placeClaim(ctx context.Context, namespace storage.Namespace, sourceName, destinationName string, mode storage.WriteMode) (bool, error) {
	if err := contextError(ctx); err != nil {
		return false, storage.NewError("place", storage.KindCancelled, err)
	}
	if err := b.root.MkdirAll(path.Dir(destinationName), b.dirMode); err != nil {
		return false, filesystemError("place", err)
	}

	var err error
	switch mode {
	case storage.CreateOnly:
		err = b.root.Link(sourceName, destinationName)
		if errors.Is(err, fs.ErrExist) {
			return false, storage.NewError("place", storage.KindAlreadyExists, err)
		}
	case storage.Replace:
		workName, linkErr := b.newWorkLink(namespace, sourceName)
		if linkErr != nil {
			return false, filesystemError("place", linkErr)
		}
		defer func() { _ = b.root.Remove(workName) }()
		if err := contextError(ctx); err != nil {
			return false, storage.NewError("place", storage.KindCancelled, err)
		}
		err = b.root.Rename(workName, destinationName)
	default:
		return false, storage.NewError("place", storage.KindInvalid, fmt.Errorf("unknown write mode"))
	}
	if err != nil {
		return false, filesystemError("place", err)
	}
	if b.syncWrites {
		var syncErr error
		if b.placeSync != nil {
			syncErr = b.placeSync(path.Dir(destinationName))
		} else {
			syncErr = b.syncDirectory(path.Dir(destinationName))
		}
		if syncErr != nil {
			return true, filesystemError("place", syncErr)
		}
	}
	return true, nil
}

func (b *Backend) syncDirectory(name string) error {
	directory, err := b.root.Open(name)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func objectPath(namespace storage.Namespace, key storage.Key) string {
	parts := []string{privateDirectory, "objects", namespace.Value()}
	for _, segment := range strings.Split(key.Value(), "/") {
		parts = append(parts, encodeCaseSafe([]byte(segment)))
	}
	return path.Join(append(parts, "object.vv")...)
}

func stageDirectory(namespace storage.Namespace) string {
	return path.Join(privateDirectory, "staging", namespace.Value())
}

func stagePath(namespace storage.Namespace, id storage.StageID) string {
	return path.Join(stageDirectory(namespace), encodeCaseSafe([]byte(id.Value()))+".stage")
}

func stageClaimPath(namespace storage.Namespace, id storage.StageID) string {
	return path.Join(stageDirectory(namespace), encodeCaseSafe([]byte(id.Value()))+".claim")
}

func stageIDFromName(name string) (storage.StageID, bool) {
	raw, stage := strings.CutSuffix(name, ".stage")
	if !stage {
		raw, stage = strings.CutSuffix(name, ".claim")
	}
	if !stage {
		return storage.StageID{}, false
	}
	decoded, err := decodeCaseSafe(raw)
	if err != nil || encodeCaseSafe(decoded) != raw {
		return storage.StageID{}, false
	}
	id, err := storage.ParseStageID(string(decoded))
	return id, err == nil
}

// claimStage gives one operation exclusive ownership without a process-local
// mutex. The hard-link creation is the election. The public stage link remains
// present for the whole operation; deterministic failure removes only the
// claim, while successful consumption removes stage before claim. These
// monotonic states avoid an absent-name ABA window between concurrent retries.
func (b *Backend) claimStage(operation string, namespace storage.Namespace, id storage.StageID) (string, error) {
	stagedName := stagePath(namespace, id)
	claimedName := stageClaimPath(namespace, id)
	err := b.root.Link(stagedName, claimedName)
	if err == nil {
		if b.syncWrites {
			if err := b.syncDirectory(stageDirectory(namespace)); err != nil {
				if releaseErr := b.releaseClaim(namespace, id); releaseErr != nil {
					return "", filesystemError(operation, releaseErr)
				}
				return "", filesystemError(operation, err)
			}
		}
		return claimedName, nil
	}
	if errors.Is(err, fs.ErrExist) {
		return "", storage.NewError(operation, storage.KindConflict, err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", filesystemError(operation, err)
	}
	claimExists, stateErr := b.privateNameExists(claimedName)
	if stateErr != nil {
		return "", filesystemError(operation, stateErr)
	}
	if claimExists {
		return "", storage.NewError(operation, storage.KindConflict, err)
	}
	return "", storage.NewError(operation, storage.KindNotFound, err)
}

func (b *Backend) privateNameExists(name string) (bool, error) {
	_, err := b.root.Lstat(name)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func encodeCaseSafe(raw []byte) string {
	return strings.ToLower(caseSafeEncoding.EncodeToString(raw))
}

func decodeCaseSafe(encoded string) ([]byte, error) {
	if encoded == "" || encoded != strings.ToLower(encoded) {
		return nil, fmt.Errorf("case-safe name is invalid")
	}
	return caseSafeEncoding.DecodeString(strings.ToUpper(encoded))
}

func supportedOperatingSystem(goos string) bool {
	switch goos {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "linux", "netbsd", "openbsd", "solaris":
		return true
	default:
		return false
	}
}

func (b *Backend) removeClaimName(name string) error {
	if b.claimRemove != nil {
		return b.claimRemove(name)
	}
	return b.root.Remove(name)
}

func (b *Backend) releaseClaim(namespace storage.Namespace, id storage.StageID) error {
	claimedName := stageClaimPath(namespace, id)
	err := b.removeClaimName(claimedName)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if b.syncWrites && err == nil {
		return b.syncDirectory(stageDirectory(namespace))
	}
	return nil
}

// consumeClaim removes the public stage before its exclusive claim. Its bool
// reports that the stage name is already absent; after that point releasing the
// claim would risk making a committed stage identity reusable.
func (b *Backend) consumeClaim(namespace storage.Namespace, id storage.StageID, remove func(string) error) (bool, error) {
	stagedName := stagePath(namespace, id)
	claimedName := stageClaimPath(namespace, id)
	if err := remove(stagedName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := remove(claimedName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return true, err
	}
	if b.syncWrites {
		if err := b.syncDirectory(stageDirectory(namespace)); err != nil {
			return true, err
		}
	}
	return true, nil
}

func writeMode(mode storage.WriteMode) (storage.WriteMode, error) {
	if mode == 0 {
		return storage.CreateOnly, nil
	}
	if mode != storage.CreateOnly && mode != storage.Replace {
		return 0, fmt.Errorf("unknown write mode")
	}
	return mode, nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is nil")
	}
	return ctx.Err()
}

func safeOwnedDirectory(info fs.FileInfo) bool {
	if info == nil || !info.Mode().IsDir() {
		return false
	}
	permissions := info.Mode().Perm()
	return permissions&0o700 == 0o700 && permissions&0o022 == 0
}

func filesystemError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var sourceErr *sourceReadError
	if errors.As(err, &sourceErr) {
		return storage.NewError(operation, storage.KindSource, sourceErr.err)
	}
	if storage.KindOf(err) != "" {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return storage.NewError(operation, storage.KindCancelled, err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return storage.NewError(operation, storage.KindNotFound, err)
	}
	if errors.Is(err, fs.ErrExist) {
		return storage.NewError(operation, storage.KindAlreadyExists, err)
	}
	if errors.Is(err, fs.ErrPermission) {
		return storage.NewError(operation, storage.KindForbidden, err)
	}
	return storage.NewError(operation, storage.KindInternal, err)
}

func operationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if kind := storage.KindOf(err); kind != "" {
		return storage.NewError(operation, kind, err)
	}
	return filesystemError(operation, err)
}

// Handler is declared here so users find filesystem construction and its link
// endpoint on the same concrete backend. It is implemented in link.go.
func (b *Backend) Handler() http.Handler { return b.linkHandler() }
