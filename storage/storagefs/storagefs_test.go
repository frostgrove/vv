package storagefs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/storage"
)

func TestPutOpenHeadDeleteRoundTripOnePrivateFile(t *testing.T) {
	backend, store, root := newTestStore(t, Config{Sync: true})
	key := mustKey(t, "invoices/2026/statement.pdf")
	payload := []byte("the exact object body")
	metadata := storage.Metadata{"invoice": "2026-001", "source": "form"}
	info, err := store.Put(t.Context(), key, bytes.NewReader(payload), storage.PutOptions{
		Mode:        storage.CreateOnly,
		Size:        storage.ExactSize(int64(len(payload))),
		ContentType: "application/pdf",
		Metadata:    metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata["invoice"] = "mutated"
	if info.Size != int64(len(payload)) || info.ContentType != "application/pdf" || info.Metadata["invoice"] != "2026-001" || info.ModifiedAt.IsZero() {
		t.Fatalf("unexpected write info: %#v", info)
	}

	head, err := store.Head(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	body, opened, err := store.Open(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) || opened.Size != head.Size || opened.Metadata["source"] != "form" {
		t.Fatalf("body/info did not round trip: body=%q open=%#v head=%#v", got, opened, head)
	}

	physicalName := filepath.Join(root, filepath.FromSlash(objectPath(mustNamespace(t, "documents"), key)))
	physical, err := os.ReadFile(physicalName)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(physical, payload) || !bytes.Equal(physical[len(physical)-len(payload):], payload) {
		t.Fatalf("private file did not contain a bounded header followed by exact bytes")
	}
	stat, err := os.Stat(physicalName)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != DefaultFileMode {
		t.Fatalf("private object mode = %o, want %o", stat.Mode().Perm(), DefaultFileMode)
	}

	if err := store.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(t.Context(), key); err != nil {
		t.Fatalf("Delete of an absent object must be idempotent: %v", err)
	}
	if _, err := store.Head(t.Context(), key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Head after Delete error = %v, want ErrNotFound", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAbsentMetadataRemainsNilAcrossEveryReadBoundary(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	key := mustKey(t, "metadata/absent")
	putInfo, err := store.Put(t.Context(), key, strings.NewReader("object"), storage.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if putInfo.Metadata != nil {
		t.Fatalf("Put metadata = %#v, want nil", putInfo.Metadata)
	}
	headInfo, err := store.Head(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if headInfo.Metadata != nil {
		t.Fatalf("Head metadata = %#v, want nil", headInfo.Metadata)
	}
	body, openInfo, err := store.Open(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if openInfo.Metadata != nil {
		t.Fatalf("Open metadata = %#v, want nil", openInfo.Metadata)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}

	staged, err := store.Stage(t.Context(), strings.NewReader("stage"), storage.StageOptions{ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if staged.Info.Metadata != nil {
		t.Fatalf("Stage metadata = %#v, want nil", staged.Info.Metadata)
	}
	promoted, err := store.Promote(t.Context(), staged.ID, mustKey(t, "metadata/promoted"), storage.PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Metadata != nil {
		t.Fatalf("Promote metadata = %#v, want nil", promoted.Metadata)
	}
}

func TestAdversarialPortableMetadataEncodingFitsThePrivateHeader(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	metadata := make(storage.Metadata, 3)
	for i := range 3 {
		metadata[fmt.Sprintf("key%d", i)] = strings.Repeat("<", 500)
	}
	source := &bytesReadCloser{Reader: bytes.NewReader([]byte("body"))}
	info, err := store.Put(t.Context(), mustKey(t, "metadata/adversarial"), source, storage.PutOptions{Metadata: metadata})
	if err != nil {
		t.Fatalf("portable metadata was not representable by storagefs: %v", err)
	}
	if info.Metadata["key0"] != metadata["key0"] {
		t.Fatal("metadata changed while encoding the private header")
	}
	if source.closed.Load() {
		t.Fatal("Put closed the caller-owned source")
	}
}

func TestPrefixKeysCoexist(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	for key, value := range map[string]string{
		"pictures":          "root object",
		"pictures/avatar":   "nested object",
		"pictures/avatar/x": "deep object",
	} {
		_, err := store.Put(t.Context(), mustKey(t, key), strings.NewReader(value), storage.PutOptions{})
		if err != nil {
			t.Fatalf("Put(%q): %v", key, err)
		}
	}
	for key, want := range map[string]string{
		"pictures":          "root object",
		"pictures/avatar":   "nested object",
		"pictures/avatar/x": "deep object",
	} {
		body, _, err := store.Open(t.Context(), mustKey(t, key))
		if err != nil {
			t.Fatalf("Open(%q): %v", key, err)
		}
		got, readErr := io.ReadAll(body)
		_ = body.Close()
		if readErr != nil || string(got) != want {
			t.Fatalf("Open(%q) = %q, %v; want %q", key, got, readErr, want)
		}
	}
}

func TestPhysicalNamesAreInjectiveUnderCaseFolding(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{})
	namespace := mustNamespace(t, "documents")
	firstKey := mustKey(t, "aaa")
	secondKey := mustKey(t, "aaG")
	firstPath := objectPath(namespace, firstKey)
	secondPath := objectPath(namespace, secondKey)
	if firstPath == secondPath || strings.EqualFold(firstPath, secondPath) {
		t.Fatalf("distinct keys collide after case folding: %q / %q", firstPath, secondPath)
	}
	if firstPath != strings.ToLower(firstPath) || secondPath != strings.ToLower(secondPath) {
		t.Fatalf("physical object names are not case-fold-safe: %q / %q", firstPath, secondPath)
	}
	for key, value := range map[storage.Key]string{firstKey: "first", secondKey: "second"} {
		if _, err := store.Put(t.Context(), key, strings.NewReader(value), storage.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	for key, want := range map[storage.Key]string{firstKey: "first", secondKey: "second"} {
		body, _, err := store.Open(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(body)
		_ = body.Close()
		if readErr != nil || string(got) != want {
			t.Fatalf("Open(%s) = %q, %v; want %q", key.Value(), got, readErr, want)
		}
	}

	firstID, err := storage.ParseStageID(strings.Repeat("A", 32))
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := storage.ParseStageID("a" + strings.Repeat("A", 31))
	if err != nil {
		t.Fatal(err)
	}
	firstStage := stagePath(namespace, firstID)
	secondStage := stagePath(namespace, secondID)
	if firstStage == secondStage || strings.EqualFold(firstStage, secondStage) {
		t.Fatalf("distinct stage IDs collide after case folding: %q / %q", firstStage, secondStage)
	}
	for name, want := range map[string]storage.StageID{path.Base(firstStage): firstID, path.Base(secondStage): secondID} {
		got, ok := stageIDFromName(name)
		if !ok || got != want || name != strings.ToLower(name) {
			t.Fatalf("stage name round trip = %q / %v / %v, want %v", name, got, ok, want)
		}
	}
	canonical := strings.TrimSuffix(path.Base(firstStage), ".stage")
	decoded, err := decodeCaseSafe(canonical)
	if err != nil {
		t.Fatal(err)
	}
	noncanonical := ""
	for _, candidateTail := range "abcdefghijklmnopqrstuvwxyz234567" {
		candidate := canonical[:len(canonical)-1] + string(candidateTail)
		candidateDecoded, decodeErr := decodeCaseSafe(candidate)
		if candidate != canonical && decodeErr == nil && bytes.Equal(candidateDecoded, decoded) && encodeCaseSafe(candidateDecoded) != candidate {
			noncanonical = candidate
			break
		}
	}
	if noncanonical == "" {
		t.Fatal("could not construct a noncanonical base32 tail-bit alias")
	}
	if _, ok := stageIDFromName(noncanonical + ".stage"); ok {
		t.Fatal("cleanup accepted a noncanonical filename alias for a stage ID")
	}

	work, workName, err := backend.newWorkFile(namespace)
	if err != nil {
		t.Fatal(err)
	}
	if err := work.Close(); err != nil {
		t.Fatal(err)
	}
	if base := path.Base(workName); base != strings.ToLower(base) || !validWorkName(base) {
		t.Fatalf("work name is not canonical case-safe base32: %q", base)
	}
}

func TestCreateOnlyHasExactlyOneWinner(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	key := mustKey(t, "race/winner")
	const writers = 32
	var start sync.WaitGroup
	start.Add(1)
	results := make(chan error, writers)
	values := make(chan string, writers)
	for i := range writers {
		value := fmt.Sprintf("writer-%02d", i)
		go func() {
			start.Wait()
			_, err := store.Put(context.Background(), key, strings.NewReader(value), storage.PutOptions{Mode: storage.CreateOnly})
			results <- err
			if err == nil {
				values <- value
			}
		}()
	}
	start.Done()
	successes := 0
	for range writers {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, storage.ErrAlreadyExists) {
			t.Fatalf("loser error = %v, want ErrAlreadyExists", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful writers = %d, want 1", successes)
	}
	winner := <-values
	body, _, err := store.Open(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(got) != winner {
		t.Fatalf("committed body = %q, %v; winner wrote %q", got, err, winner)
	}
}

func TestReplaceNeverMixesMetadataAndBytes(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	key := mustKey(t, "atomic/object")
	putGeneration := func(generation byte) error {
		body := bytes.Repeat([]byte{generation}, 64<<10)
		_, err := store.Put(context.Background(), key, bytes.NewReader(body), storage.PutOptions{
			Mode:     storage.Replace,
			Metadata: storage.Metadata{"generation": string([]byte{generation})},
		})
		return err
	}
	if err := putGeneration('A'); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	errs := make(chan error, 1)
	go func() {
		defer close(done)
		for i := range 30 {
			generation := byte('A' + i%2)
			if err := putGeneration(generation); err != nil {
				errs <- err
				return
			}
		}
	}()
	for {
		select {
		case <-done:
			select {
			case err := <-errs:
				t.Fatal(err)
			default:
			}
			return
		default:
		}
		body, info, err := store.Open(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(body)
		_ = body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		generation := info.Metadata["generation"]
		if len(generation) != 1 || len(data) != 64<<10 || bytes.Count(data, []byte(generation)) != len(data) {
			t.Fatalf("mixed generation: metadata=%q first=%q size=%d", generation, data[:1], len(data))
		}
	}
}

func TestAFailedSourceLeavesNoVisibleObjectAndIsNotClosed(t *testing.T) {
	_, store, root := newTestStore(t, Config{})
	key := mustKey(t, "uploads/partial")
	sourceFailure := errors.New("source broke")
	source := &failingReadCloser{failure: sourceFailure}
	_, err := store.Put(t.Context(), key, source, storage.PutOptions{Mode: storage.Replace})
	if !errors.Is(err, storage.ErrSource) || !errors.Is(err, sourceFailure) {
		t.Fatalf("Put error = %v, want source classification and cause", err)
	}
	var storageErr *storage.Error
	if !errors.As(err, &storageErr) || storageErr.Operation != "put" {
		t.Fatalf("Put operation = %#v, want put", storageErr)
	}
	if source.closed.Load() {
		t.Fatal("Put closed the caller-owned source")
	}
	if _, err := store.Head(t.Context(), key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("partial object became visible: %v", err)
	}
	workEntries, err := os.ReadDir(filepath.Join(root, privateDirectory, "work", "documents"))
	if err != nil {
		t.Fatal(err)
	}
	if len(workEntries) != 0 {
		t.Fatalf("failed Put retained private work files: %v", workEntries)
	}
}

func TestAHostilePortableSourceErrorRemainsASourceFailure(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	secret := "private/key/from-reader"
	sourceFailure := storage.NewError("reader "+secret, storage.KindNotFound, errors.New(secret))
	source := &failingReadCloser{failure: sourceFailure}
	_, err := store.Put(t.Context(), mustKey(t, "uploads/hostile-source"), source, storage.PutOptions{Mode: storage.Replace})
	if !errors.Is(err, storage.ErrSource) || errors.Is(err, storage.ErrNotFound) || !errors.Is(err, sourceFailure) {
		t.Fatalf("Put error = %v, want source provenance", err)
	}
	if strings.Contains(fmt.Sprintf("%#v", err), secret) {
		t.Fatalf("Put error disclosed hostile source detail: %v", err)
	}
}

func TestStageDoesNotCloseASuccessfulCallerOwnedSource(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	source := &bytesReadCloser{Reader: bytes.NewReader([]byte("temporary"))}
	staged, err := store.Stage(t.Context(), source, storage.StageOptions{ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if source.closed.Load() {
		t.Fatal("Stage closed the caller-owned source")
	}
	if err := store.Abort(t.Context(), staged.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDeclaredSizeMismatchLeavesNoVisibleObject(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	key := mustKey(t, "uploads/wrong-size")
	_, err := store.Put(t.Context(), key, strings.NewReader("short"), storage.PutOptions{
		Mode: storage.Replace,
		Size: storage.ExactSize(100),
	})
	if !errors.Is(err, storage.ErrSource) {
		t.Fatalf("Put error = %v, want ErrSource", err)
	}
	if _, err := store.Head(t.Context(), key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("size-mismatched object became visible: %v", err)
	}
}

func TestDeclaredSizeStopsReadingAfterTheFirstExcessByte(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	for _, declared := range []int64{0, 1} {
		t.Run(fmt.Sprintf("size-%d", declared), func(t *testing.T) {
			source := bytes.NewReader(bytes.Repeat([]byte{'x'}, 1<<20))
			before := source.Len()
			key := mustKey(t, fmt.Sprintf("uploads/over-limit-%d", declared))
			_, err := store.Put(t.Context(), key, source, storage.PutOptions{
				Mode: storage.Replace,
				Size: storage.ExactSize(declared),
			})
			if !errors.Is(err, storage.ErrSource) {
				t.Fatalf("Put error = %v, want ErrSource", err)
			}
			if consumed := before - source.Len(); consumed != int(declared)+1 {
				t.Fatalf("source bytes consumed = %d, want %d", consumed, declared+1)
			}
			if _, err := store.Head(t.Context(), key); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("oversized source became visible: %v", err)
			}
		})
	}
}

func TestASourceCancellationErrorIsStillASourceErrorWhenTheCallIsLive(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	source := &failingReadCloser{failure: context.Canceled}
	_, err := store.Put(t.Context(), mustKey(t, "uploads/source-cancelled"), source, storage.PutOptions{})
	if !errors.Is(err, storage.ErrSource) || !errors.Is(err, context.Canceled) {
		t.Fatalf("source cancellation error = %v, want ErrSource retaining context.Canceled", err)
	}
}

func TestOperationCancellationWhileASourceReadIsBlockedWinsOverSourceFailure(t *testing.T) {
	for _, stage := range []bool{false, true} {
		name := "put"
		if stage {
			name = "stage"
		}
		t.Run(name, func(t *testing.T) {
			_, store, _ := newTestStore(t, Config{})
			ctx, cancel := context.WithCancel(context.Background())
			sourceFailure := errors.New("source unblocked with failure")
			source := newBlockedReader(sourceFailure)
			result := make(chan error, 1)
			go func() {
				if stage {
					_, err := store.Stage(ctx, source, storage.StageOptions{ExpiresIn: time.Hour})
					result <- err
					return
				}
				_, err := store.Put(ctx, mustKey(t, "cancel/blocked"), source, storage.PutOptions{})
				result <- err
			}()
			<-source.started
			cancel()
			close(source.release)
			err := <-result
			if !errors.Is(err, storage.ErrCancelled) || !errors.Is(err, context.Canceled) || errors.Is(err, storage.ErrSource) {
				t.Fatalf("cancelled blocked read error = %v, want only ErrCancelled", err)
			}
			if errors.Is(err, sourceFailure) {
				t.Fatal("operation cancellation was relabelled with the source failure")
			}
		})
	}
}

func TestAReaderThatNeverMakesProgressFailsInABoundedNumberOfReads(t *testing.T) {
	tests := []struct {
		name  string
		stage bool
		size  *int64
	}{
		{name: "put unknown size"},
		{name: "put known size", size: storage.ExactSize(1)},
		{name: "stage unknown size", stage: true},
		{name: "stage known size", stage: true, size: storage.ExactSize(1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, store, root := newTestStore(t, Config{})
			source := &emptyReader{}
			var err error
			if test.stage {
				_, err = store.Stage(t.Context(), source, storage.StageOptions{Size: test.size, ExpiresIn: time.Hour})
			} else {
				_, err = store.Put(t.Context(), mustKey(t, "uploads/no-progress"), source, storage.PutOptions{Size: test.size})
			}
			if !errors.Is(err, storage.ErrSource) || !errors.Is(err, io.ErrNoProgress) {
				t.Fatalf("operation error = %v, want ErrSource retaining io.ErrNoProgress", err)
			}
			if source.reads != maxConsecutiveEmptyReads {
				t.Fatalf("source reads = %d, want bounded %d", source.reads, maxConsecutiveEmptyReads)
			}
			if !test.stage {
				if _, headErr := store.Head(t.Context(), mustKey(t, "uploads/no-progress")); !errors.Is(headErr, storage.ErrNotFound) {
					t.Fatalf("failed source became visible: %v", headErr)
				}
			}
			for _, relative := range []string{
				stageDirectory(mustNamespace(t, "documents")),
				filepath.Join(privateDirectory, "work", "documents"),
			} {
				entries, readErr := os.ReadDir(filepath.Join(root, filepath.FromSlash(relative)))
				if errors.Is(readErr, os.ErrNotExist) {
					continue
				}
				if readErr != nil || len(entries) != 0 {
					t.Fatalf("temporary residue in %s: entries=%v error=%v", relative, entries, readErr)
				}
			}
		})
	}
}

func TestStagePromoteAbortExpiryAndBoundedCleanup(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{})
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	backend.now = func() time.Time { return now }

	staged, err := store.Stage(t.Context(), strings.NewReader("confirmed"), storage.StageOptions{
		ContentType: "image/png",
		Metadata:    storage.Metadata{"form": "avatar"},
		ExpiresIn:   time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	finalKey := mustKey(t, "avatars/user-1")
	info, err := store.Promote(t.Context(), staged.ID, finalKey, storage.PromoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info.ContentType != "image/png" || info.Metadata["form"] != "avatar" {
		t.Fatalf("promotion lost metadata: %#v", info)
	}
	if err := store.Abort(t.Context(), staged.ID); err != nil {
		t.Fatalf("Abort after Promote must be idempotent: %v", err)
	}
	if _, err := store.Promote(t.Context(), staged.ID, mustKey(t, "avatars/again"), storage.PromoteOptions{}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("second Promote error = %v, want ErrNotFound", err)
	}

	expired, err := store.Stage(t.Context(), strings.NewReader("expired"), storage.StageOptions{ExpiresIn: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if _, err := store.Promote(t.Context(), expired.ID, mustKey(t, "avatars/expired"), storage.PromoteOptions{}); !errors.Is(err, storage.ErrExpired) {
		t.Fatalf("expired Promote error = %v, want ErrExpired", err)
	}

	now = now.Add(time.Hour)
	for i := range 3 {
		if _, err := store.Stage(t.Context(), strings.NewReader(fmt.Sprintf("stage-%d", i)), storage.StageOptions{ExpiresIn: time.Second}); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Second)
	first, err := store.CleanupExpired(t.Context(), storage.CleanupOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first.Removed != 2 || !first.More {
		t.Fatalf("first cleanup = %#v, want 2 removed and More", first)
	}
	second, err := store.CleanupExpired(t.Context(), storage.CleanupOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if second.Removed != 1 || second.More {
		t.Fatalf("second cleanup = %#v, want one final removal", second)
	}
	body, _, err := store.Open(t.Context(), finalKey)
	if err != nil {
		t.Fatalf("cleanup touched a promoted object: %v", err)
	}
	content, readErr := io.ReadAll(body)
	_ = body.Close()
	if readErr != nil || string(content) != "confirmed" {
		t.Fatalf("promoted object after cleanup = %q, %v", content, readErr)
	}
	if err := store.Abort(t.Context(), expired.ID); err != nil {
		t.Fatalf("Abort of already-expired stage must be idempotent: %v", err)
	}
}

func TestCleanupLimitIsConservativeAtTheExactEndOfTheDirectory(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{})
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	backend.now = func() time.Time { return now }
	for range 2 {
		if _, err := store.Stage(t.Context(), strings.NewReader("expired"), storage.StageOptions{ExpiresIn: time.Second}); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Second)
	result, err := store.CleanupExpired(t.Context(), storage.CleanupOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 2 || !result.More {
		t.Fatalf("cleanup = %#v, want two removals and conservative More", result)
	}
	next, err := store.CleanupExpired(t.Context(), storage.CleanupOptions{Limit: 2})
	if err != nil || next != (storage.CleanupResult{}) {
		t.Fatalf("second cleanup = %#v, %v; want empty", next, err)
	}
}

func TestCleanupRemovesOnlyOldCanonicallyNamedWorkResidues(t *testing.T) {
	backend, store, root := newTestStore(t, Config{})
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	backend.now = func() time.Time { return now }
	namespace := mustNamespace(t, "documents")

	oldFile, oldName, err := backend.newWorkFile(namespace)
	if err != nil {
		t.Fatal(err)
	}
	if err := oldFile.Close(); err != nil {
		t.Fatal(err)
	}
	youngFile, youngName, err := backend.newWorkFile(namespace)
	if err != nil {
		t.Fatal(err)
	}
	if err := youngFile.Close(); err != nil {
		t.Fatal(err)
	}
	oldTime := now.Add(-orphanWorkTTL - time.Second)
	for name, timestamp := range map[string]time.Time{oldName: oldTime, youngName: now} {
		absolute := filepath.Join(root, filepath.FromSlash(name))
		if err := os.Chtimes(absolute, timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
	}
	foreignName := filepath.Join(root, filepath.FromSlash(path.Join(privateDirectory, "work", namespace.Value(), "operator-note.tmp")))
	if err := os.WriteFile(foreignName, []byte("leave me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(foreignName, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	result, err := store.CleanupExpired(t.Context(), storage.CleanupOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 1 || result.More {
		t.Fatalf("cleanup = %#v, want one old work residue", result)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(oldName))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old canonical work file still exists: %v", err)
	}
	for _, name := range []string{filepath.Join(root, filepath.FromSlash(youngName)), foreignName} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("cleanup removed an unowned or live file: %v", err)
		}
	}
}

func TestCleanupSurfacesFilesystemErrorsAndLeavesCorruptStages(t *testing.T) {
	t.Run("permission error", func(t *testing.T) {
		backend, store, root := newTestStore(t, Config{})
		now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
		backend.now = func() time.Time { return now }
		staged, err := store.Stage(t.Context(), strings.NewReader("expired"), storage.StageOptions{ExpiresIn: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(2 * time.Second)
		name := filepath.Join(root, filepath.FromSlash(stagePath(mustNamespace(t, "documents"), staged.ID)))
		if err := os.Chmod(name, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(name, DefaultFileMode) })
		_, err = store.CleanupExpired(t.Context(), storage.CleanupOptions{Limit: 10})
		if err == nil {
			t.Skip("this test process can read mode-000 files")
		}
		if !errors.Is(err, storage.ErrForbidden) {
			t.Fatalf("cleanup error = %v, want ErrForbidden", err)
		}
	})

	t.Run("corrupt owned-looking entry", func(t *testing.T) {
		backend, store, root := newTestStore(t, Config{})
		now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
		backend.now = func() time.Time { return now }
		staged, err := store.Stage(t.Context(), strings.NewReader("expired"), storage.StageOptions{ExpiresIn: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(2 * time.Second)
		name := filepath.Join(root, filepath.FromSlash(stagePath(mustNamespace(t, "documents"), staged.ID)))
		if err := os.WriteFile(name, []byte("not a private object"), DefaultFileMode); err != nil {
			t.Fatal(err)
		}
		result, err := store.CleanupExpired(t.Context(), storage.CleanupOptions{Limit: 10})
		if err != nil || result != (storage.CleanupResult{}) {
			t.Fatalf("cleanup = %#v, %v; want corrupt entry left for operator", result, err)
		}
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("cleanup removed corrupt entry: %v", err)
		}
	})
}

func TestPromoteCreateOnlyDoesNotConsumeStageOnCollision(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	key := mustKey(t, "avatars/existing")
	if _, err := store.Put(t.Context(), key, strings.NewReader("existing"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	staged, err := store.Stage(t.Context(), strings.NewReader("new"), storage.StageOptions{ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Promote(t.Context(), staged.ID, key, storage.PromoteOptions{}); !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("Promote error = %v, want ErrAlreadyExists", err)
	}
	replacement := mustKey(t, "avatars/replacement")
	if _, err := store.Promote(t.Context(), staged.ID, replacement, storage.PromoteOptions{}); err != nil {
		t.Fatalf("stage was consumed by losing promotion: %v", err)
	}
}

func TestPromoteReplaceUsesTheStagedBytesAndConsumesTheStage(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	key := mustKey(t, "avatars/current")
	if _, err := store.Put(t.Context(), key, strings.NewReader("old"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	staged, err := store.Stage(t.Context(), strings.NewReader("new"), storage.StageOptions{ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	info, err := store.Promote(t.Context(), staged.ID, key, storage.PromoteOptions{Mode: storage.Replace})
	if err != nil {
		t.Fatal(err)
	}
	body, opened, err := store.Open(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || string(content) != "new" || info.Size != 3 || opened.Size != 3 {
		t.Fatalf("replacement = %q, info=%#v/%#v, read/close=%v/%v", content, info, opened, readErr, closeErr)
	}
	if _, err := store.Promote(t.Context(), staged.ID, mustKey(t, "avatars/duplicate"), storage.PromoteOptions{}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("consumed replacement stage promoted twice: %v", err)
	}
}

func TestPromoteSurfacesAClaimReleaseFailureInsteadOfPromisingRetry(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{})
	key := mustKey(t, "release/existing")
	if _, err := store.Put(t.Context(), key, strings.NewReader("existing"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	staged, err := store.Stage(t.Context(), strings.NewReader("new"), storage.StageOptions{ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	backend.claimRemove = func(string) error { return fs.ErrPermission }
	_, err = store.Promote(t.Context(), staged.ID, key, storage.PromoteOptions{})
	if !errors.Is(err, storage.ErrForbidden) || errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("Promote release failure = %v, want release error instead of collision", err)
	}
	backend.claimRemove = nil
	if err := backend.releaseClaim(mustNamespace(t, "documents"), staged.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Promote(t.Context(), staged.ID, mustKey(t, "release/retry"), storage.PromoteOptions{}); err != nil {
		t.Fatalf("stage was not retryable after a successful release: %v", err)
	}
}

func TestPromoteNeverRestoresAStageAfterFinalPlacementBecomesVisible(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{Sync: true})
	staged, err := store.Stage(t.Context(), strings.NewReader("committed"), storage.StageOptions{ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	backend.claimRemove = func(name string) error {
		if strings.HasSuffix(name, ".stage") {
			return fs.ErrPermission
		}
		return backend.root.Remove(name)
	}
	backend.placeSync = func(string) error { return errors.New("directory sync failed") }
	finalKey := mustKey(t, "committed/first")
	_, err = store.Promote(t.Context(), staged.ID, finalKey, storage.PromoteOptions{})
	if !errors.Is(err, storage.ErrForbidden) {
		t.Fatalf("post-placement cleanup error = %v, want ErrForbidden", err)
	}
	backend.claimRemove = nil
	backend.placeSync = nil
	body, _, err := store.Open(t.Context(), finalKey)
	if err != nil {
		t.Fatalf("visible placement disappeared after sync failure: %v", err)
	}
	content, readErr := io.ReadAll(body)
	_ = body.Close()
	if readErr != nil || string(content) != "committed" {
		t.Fatalf("visible placement = %q, %v", content, readErr)
	}
	if _, err := store.Promote(t.Context(), staged.ID, mustKey(t, "committed/second"), storage.PromoteOptions{}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("committed stage became promotable to a second key: %v", err)
	}
}

func TestAbortConflictsWithAnActiveClaimAndCannotResurrectAStage(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{})
	staged, err := store.Stage(t.Context(), strings.NewReader("temporary"), storage.StageOptions{ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	namespace := mustNamespace(t, "documents")
	claimedName, err := backend.claimStage("test claim", namespace, staged.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Abort(t.Context(), staged.ID); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("Abort during active claim = %v, want ErrConflict", err)
	}
	if _, err := backend.root.Lstat(claimedName); err != nil {
		t.Fatalf("conflicting Abort removed the active claim: %v", err)
	}
	if err := backend.releaseClaim(namespace, staged.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Abort(t.Context(), staged.ID); err != nil {
		t.Fatalf("Abort after claim release: %v", err)
	}
	if _, err := store.Promote(t.Context(), staged.ID, mustKey(t, "abort/resurrected"), storage.PromoteOptions{}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("aborted stage became promotable again: %v", err)
	}
}

func TestTwoFailedPromotersNeverExposeFalseStageAbsence(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{})
	existing := mustKey(t, "claims/existing")
	if _, err := store.Put(t.Context(), existing, strings.NewReader("existing"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	staged, err := store.Stage(t.Context(), strings.NewReader("temporary"), storage.StageOptions{ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}

	releaseStarted := make(chan int, 2)
	allowRelease := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var releaseCount atomic.Int32
	backend.claimRemove = func(name string) error {
		index := int(releaseCount.Add(1) - 1)
		if index >= len(allowRelease) {
			return fmt.Errorf("unexpected claim release %d", index)
		}
		releaseStarted <- index
		<-allowRelease[index]
		return backend.root.Remove(name)
	}

	failed := func() <-chan error {
		result := make(chan error, 1)
		go func() {
			_, err := store.Promote(context.Background(), staged.ID, existing, storage.PromoteOptions{})
			result <- err
		}()
		return result
	}
	for attempt := range 2 {
		result := failed()
		if index := <-releaseStarted; index != attempt {
			t.Fatalf("release index = %d, want %d", index, attempt)
		}
		_, err := store.Promote(t.Context(), staged.ID, mustKey(t, fmt.Sprintf("claims/contender-%d", attempt)), storage.PromoteOptions{})
		if !errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("contender %d = %v, want Conflict without false absence", attempt, err)
		}
		close(allowRelease[attempt])
		if err := <-result; !errors.Is(err, storage.ErrAlreadyExists) {
			t.Fatalf("failed promoter %d = %v, want AlreadyExists", attempt, err)
		}
	}
	backend.claimRemove = nil
	if _, err := store.Promote(t.Context(), staged.ID, mustKey(t, "claims/final"), storage.PromoteOptions{}); err != nil {
		t.Fatalf("stage was not retryable after two failed promoters: %v", err)
	}
}

func TestAbortKeepsStageAfterADeterministicRemoveFailure(t *testing.T) {
	backend, store, _ := newTestStore(t, Config{})
	staged, err := store.Stage(t.Context(), strings.NewReader("temporary"), storage.StageOptions{ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	namespace := mustNamespace(t, "documents")
	err = backend.abortWithRemove(t.Context(), namespace, staged.ID, func(string) error {
		return fs.ErrPermission
	})
	if !errors.Is(err, storage.ErrForbidden) {
		t.Fatalf("failed Abort remove = %v, want ErrForbidden", err)
	}
	if _, err := backend.root.Lstat(stagePath(namespace, staged.ID)); err != nil {
		t.Fatalf("failed Abort did not keep a retryable stage: %v", err)
	}
	if err := store.Abort(t.Context(), staged.ID); err != nil {
		t.Fatalf("Abort retry after released claim: %v", err)
	}
}

func TestAStageHasOnlyOneConcurrentPromotionWinner(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	staged, err := store.Stage(t.Context(), strings.NewReader("once"), storage.StageOptions{ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	const promoters = 16
	start := make(chan struct{})
	results := make(chan error, promoters)
	for i := range promoters {
		key := mustKey(t, fmt.Sprintf("promotions/%02d", i))
		go func() {
			<-start
			_, err := store.Promote(context.Background(), staged.ID, key, storage.PromoteOptions{})
			results <- err
		}()
	}
	close(start)
	winners := 0
	for range promoters {
		err := <-results
		if err == nil {
			winners++
			continue
		}
		if !errors.Is(err, storage.ErrConflict) && !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("losing promotion error = %v, want conflict/not-found", err)
		}
	}
	if winners != 1 {
		t.Fatalf("successful promotions = %d, want 1", winners)
	}
}

func TestOpenBodyObservesCancellation(t *testing.T) {
	_, store, _ := newTestStore(t, Config{})
	key := mustKey(t, "context/body")
	if _, err := store.Put(t.Context(), key, strings.NewReader("content"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	body, _, err := store.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	buffer := make([]byte, 1)
	if _, err := body.Read(buffer); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read after cancellation error = %v", err)
	}
	_ = body.Close()
}

func TestOpenBodyReadAndCloseErrorsAreTypedAndRedacted(t *testing.T) {
	_, store, root := newTestStore(t, Config{})
	key := mustKey(t, "private/secret-object")
	if _, err := store.Put(t.Context(), key, strings.NewReader("content"), storage.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	body, _, err := store.Open(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	owned, ok := body.(*objectBody)
	if !ok {
		t.Fatalf("filesystem body type = %T", body)
	}
	if err := owned.file.Close(); err != nil {
		t.Fatal(err)
	}
	_, readErr := body.Read(make([]byte, 1))
	if !errors.Is(readErr, storage.ErrInternal) {
		t.Fatalf("Read error = %v, want ErrInternal", readErr)
	}
	closeErr := body.Close()
	if !errors.Is(closeErr, storage.ErrInternal) {
		t.Fatalf("Close error = %v, want ErrInternal", closeErr)
	}
	for _, err := range []error{readErr, closeErr} {
		formatted := fmt.Sprintf("%v %+v %#v", err, err, err)
		if strings.Contains(formatted, root) || strings.Contains(formatted, key.Value()) || strings.Contains(formatted, privateDirectory) {
			t.Fatalf("body error disclosed a private path or key: %q", formatted)
		}
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) {
			t.Fatalf("body error did not retain its diagnostic os.PathError: %v", err)
		}
	}
}

func TestOpenRootRefusesAParentSymlinkEscape(t *testing.T) {
	_, store, root := newTestStore(t, Config{})
	namespace := mustNamespace(t, "documents")
	key := mustKey(t, "escape/object")
	outside := t.TempDir()
	objectParent := filepath.Join(root, privateDirectory, "objects", namespace.Value())
	if err := os.MkdirAll(objectParent, 0o700); err != nil {
		t.Fatal(err)
	}
	encodedFirstSegment := base64Segment("escape")
	if err := os.Symlink(outside, filepath.Join(objectParent, encodedFirstSegment)); err != nil {
		t.Fatal(err)
	}
	_, err := store.Put(t.Context(), key, strings.NewReader("must stay contained"), storage.PutOptions{Mode: storage.Replace})
	if err == nil {
		t.Fatal("Put followed a parent symlink outside the configured root")
	}
	outsideEntries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(outsideEntries) != 0 {
		t.Fatalf("Put created files outside root: %v", outsideEntries)
	}
}

func TestConfigRequiresAnAbsoluteRootAndSafeSigningMaterial(t *testing.T) {
	if _, err := New(Config{Root: "relative"}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("relative root error = %v, want ErrInvalid", err)
	}
	root := t.TempDir()
	if _, err := New(Config{Root: root, BaseURL: "https://files.example.test/file", SigningKey: bytes.Repeat([]byte{'x'}, 31)}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("short key error = %v, want ErrInvalid", err)
	}
	if _, err := New(Config{Root: root, FileMode: 0o400}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("read-only file mode error = %v, want ErrInvalid", err)
	}
	if _, err := New(Config{Root: root, FileMode: 0o620}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("group-writable file mode error = %v, want ErrInvalid", err)
	}
	if _, err := New(Config{Root: root, DirMode: 0o720}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("group-writable directory mode error = %v, want ErrInvalid", err)
	}
	if _, err := New(Config{
		Root:       root,
		BaseURL:    "https://files.example.test/file",
		SigningKey: bytes.Repeat([]byte{'x'}, 32),
		MaxLinkTTL: 1500 * time.Millisecond,
	}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("fractional maximum link TTL error = %v, want ErrInvalid", err)
	}

	unsafeRoot := t.TempDir()
	if err := os.Chmod(unsafeRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Root: unsafeRoot}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("world-writable root error = %v, want ErrInvalid", err)
	}

	target := t.TempDir()
	linkName := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(target, linkName); err != nil {
		t.Skipf("cannot create root symlink: %v", err)
	}
	if _, err := New(Config{Root: linkName}); !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("symlink root error = %v, want ErrInvalid", err)
	}
}

func TestOperatingSystemGateRequiresUnixLinkAndRenameSemantics(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "freebsd"} {
		if !supportedOperatingSystem(goos) {
			t.Fatalf("%s unexpectedly unsupported", goos)
		}
	}
	for _, goos := range []string{"windows", "plan9", "js", "wasip1"} {
		if supportedOperatingSystem(goos) {
			t.Fatalf("%s unexpectedly advertised filesystem semantics", goos)
		}
	}
}

func newTestStore(t *testing.T, config Config) (*Backend, storage.Store, string) {
	t.Helper()
	if config.Root == "" {
		config.Root = t.TempDir()
	}
	backend, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	store, err := storage.New(storage.Config{Namespace: "documents", Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	return backend, store, config.Root
}

func mustKey(t *testing.T, raw string) storage.Key {
	t.Helper()
	key, err := storage.ParseKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustNamespace(t *testing.T, raw string) storage.Namespace {
	t.Helper()
	namespace, err := storage.ParseNamespace(raw)
	if err != nil {
		t.Fatal(err)
	}
	return namespace
}

func base64Segment(raw string) string {
	key, _ := storage.ParseKey(raw)
	full := objectPath(mustNamespaceNoTest("documents"), key)
	parts := strings.Split(full, "/")
	return parts[len(parts)-2]
}

func mustNamespaceNoTest(raw string) storage.Namespace {
	namespace, err := storage.ParseNamespace(raw)
	if err != nil {
		panic(err)
	}
	return namespace
}

type failingReadCloser struct {
	reads   int
	failure error
	closed  atomic.Bool
}

type bytesReadCloser struct {
	*bytes.Reader
	closed atomic.Bool
}

type emptyReader struct{ reads int }

func (r *emptyReader) Read([]byte) (int, error) {
	r.reads++
	return 0, nil
}

type blockedReader struct {
	started chan struct{}
	release chan struct{}
	err     error
	once    sync.Once
}

func newBlockedReader(err error) *blockedReader {
	return &blockedReader{started: make(chan struct{}), release: make(chan struct{}), err: err}
}

func (r *blockedReader) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return 0, r.err
}

func (r *bytesReadCloser) Close() error {
	r.closed.Store(true)
	return nil
}

func (r *failingReadCloser) Read(buffer []byte) (int, error) {
	if r.reads == 0 {
		r.reads++
		return copy(buffer, "partial"), nil
	}
	return 0, r.failure
}

func (r *failingReadCloser) Close() error {
	r.closed.Store(true)
	return nil
}
