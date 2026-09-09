package errs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

type nilCatalogueFS struct{}

func (*nilCatalogueFS) Open(string) (fs.File, error) {
	panic("a typed nil filesystem was used")
}

type failingCatalogueFS struct {
	fs.FS
	name string
}

type readErrorCatalogueFS struct {
	fs.FS
	name string
}

func (this readErrorCatalogueFS) Open(name string) (fs.File, error) {
	file, err := this.FS.Open(name)
	if err != nil || name != this.name {
		return file, err
	}
	return readErrorCatalogueFile{File: file}, nil
}

type readErrorCatalogueFile struct{ fs.File }

func (readErrorCatalogueFile) Read([]byte) (int, error) {
	return 0, errors.New("injected read failure")
}

func (this failingCatalogueFS) Open(name string) (fs.File, error) {
	if name == this.name {
		return nil, errors.New("injected read failure")
	}
	return this.FS.Open(name)
}

type closeErrorCatalogueFS struct {
	fs.FS
	name string
}

func (this closeErrorCatalogueFS) Open(name string) (fs.File, error) {
	file, err := this.FS.Open(name)
	if err != nil || name != this.name {
		return file, err
	}
	return closeErrorCatalogueFile{File: file}, nil
}

type closeErrorCatalogueFile struct {
	fs.File
}

func (this closeErrorCatalogueFile) Close() error {
	if err := this.File.Close(); err != nil {
		return err
	}
	return errors.New("injected close failure")
}

type blockedCatalogueFS struct {
	fs.FS
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (this *blockedCatalogueFS) Open(name string) (fs.File, error) {
	file, err := this.FS.Open(name)
	if err != nil || name != "messages" {
		return file, err
	}
	directory, ok := file.(fs.ReadDirFile)
	if !ok {
		_ = file.Close()
		return nil, errors.New("fixture directory is not readable")
	}
	return &blockedCatalogueDir{ReadDirFile: directory, owner: this}, nil
}

type blockedCatalogueDir struct {
	fs.ReadDirFile
	owner *blockedCatalogueFS
}

func (this *blockedCatalogueDir) ReadDir(count int) ([]fs.DirEntry, error) {
	this.owner.once.Do(func() { close(this.owner.entered) })
	<-this.owner.release
	return this.ReadDirFile.ReadDir(count)
}

func TestTheCatalogueLimitsAreStable(t *testing.T) {
	if MaxCatalogueFileBytes != 1<<20 || MaxCatalogueBytes != 16<<20 || MaxCatalogueFiles != 128 || MaxCatalogueDirectoryEntries != 4096 ||
		MaxCatalogueEntries != 10_000 || MaxMessageKeyBytes != 256 ||
		MaxMessageTemplateBytes != 16<<10 || MaxLocaleBytes != 128 {
		t.Fatalf("catalogue limits are file=%d total=%d files=%d directory=%d entries=%d key=%d template=%d locale=%d",
			MaxCatalogueFileBytes, MaxCatalogueBytes, MaxCatalogueFiles, MaxCatalogueDirectoryEntries, MaxCatalogueEntries,
			MaxMessageKeyBytes, MaxMessageTemplateBytes, MaxLocaleBytes)
	}
}

func TestCatalogueFilesRejectAmbiguousOrNonTextJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{"invalid UTF-8", []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, "UTF-8"},
		{"a duplicate member", []byte(`{"unique":"first","\u0075nique":"second"}`), "duplicate"},
		{"an array", []byte(`[]`), "flat object"},
		{"a scalar", []byte(`"message"`), "flat object"},
		{"a trailing value", []byte(`{} {}`), "one JSON object"},
		{"a nested object", []byte(`{"unique":{"text":"taken"}}`), "not a string"},
		{"an array value", []byte(`{"unique":["taken"]}`), "not a string"},
		{"a number value", []byte(`{"unique":1}`), "not a string"},
		{"a null value", []byte(`{"unique":null}`), "not a string"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadMessages(StandardCodes(), fstest.MapFS{
				"messages/default.json": &fstest.MapFile{Data: tc.raw},
			}, "messages")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("loading %s answered %v, want an error containing %q", tc.name, err, tc.want)
			}
		})
	}
}

func TestCatalogueDiagnosticsAreDeterministic(t *testing.T) {
	var first string
	for i, raw := range []string{
		`{"z":false,"a":1}`,
		`{"a":1,"z":false}`,
	} {
		_, err := LoadMessages(StandardCodes(), files(map[string]string{"default.json": raw}), "messages")
		if err == nil {
			t.Fatalf("load %d accepted two non-string values", i)
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("equivalent invalid objects reported %q and %q", first, err)
		}
	}
	if !strings.Contains(first, `"a"`) {
		t.Fatalf("the diagnostic chose %q instead of the first sorted key", first)
	}
}

func TestMessageDeclarationsRejectInvalidNamesAndTemplates(t *testing.T) {
	tests := []struct {
		name     string
		locale   string
		key      string
		template string
	}{
		{"an empty key", "en", "", "message"},
		{"invalid UTF-8 in a key", "en", string([]byte{0xff}), "message"},
		{"an overlong key", "en", strings.Repeat("k", MaxMessageKeyBytes+1), "message"},
		{"invalid UTF-8 in a locale", string([]byte{0xff}), "unique", "message"},
		{"an overlong locale", strings.Repeat("l", MaxLocaleBytes+1), "unique", "message"},
		{"an empty placeholder", "en", "unique", "value {}"},
		{"an unmatched opening brace", "en", "unique", "value {name"},
		{"an unmatched closing brace", "en", "unique", "value name}"},
		{"a nested placeholder", "en", "unique", "value {{name}}"},
		{"an overlong template", "en", "unique", strings.Repeat("m", MaxMessageTemplateBytes+1)},
		{"invalid UTF-8 in a template", "en", "unique", string([]byte{0xff})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMessages(StandardCodes())
			if err := m.Add("seed", "unique", "unchanged"); err != nil {
				t.Fatal(err)
			}
			before := m.Locales()
			if err := m.Add(tc.locale, tc.key, tc.template); err == nil {
				t.Fatalf("Add accepted locale=%q key=%q template=%q", tc.locale, tc.key, tc.template)
			}
			if got := m.Locales(); !reflect.DeepEqual(got, before) {
				t.Fatalf("the failed Add changed locales from %v to %v", before, got)
			}
			if got := say(t, m, Violation{Code: CodeUnique}, "seed"); got != "unchanged" {
				t.Fatalf("the failed Add changed the existing message to %q", got)
			}
		})
	}

	m := NewMessages(StandardCodes())
	if err := m.Add(strings.Repeat("л", MaxLocaleBytes/2), strings.Repeat("к", MaxMessageKeyBytes/2), strings.Repeat("m", MaxMessageTemplateBytes)); err != nil {
		t.Fatalf("the exact byte limits were refused: %v", err)
	}
	if err := m.Add("日本語", "пользователь.почта.unique", "адрес {значение} уже занят"); err != nil {
		t.Fatalf("valid non-ASCII names were refused: %v", err)
	}
	if err := m.Add("../en", "billing email.unique", "value {first name}"); err != nil {
		t.Fatalf("opaque names reachable through direct Add were refused: %v", err)
	}
	got, ok := m.Message(context.Background(), Violation{
		Path: Path{Named("billing email")}, Code: CodeUnique, Params: P{"first name": "Ada"},
	}, "../en")
	if !ok || got != "value Ada" {
		t.Fatalf("the opaque key, locale and parameter resolved to (%q, %v)", got, ok)
	}
}

func TestCodeDeclarationsRejectInvalidValuesWithoutPanicking(t *testing.T) {
	var nilCodes *Codes
	if err := nilCodes.Add("valid", KindValidation, "message"); err == nil {
		t.Fatal("a nil Codes accepted Add")
	}

	for _, tc := range []struct {
		code    Code
		kind    Kind
		message string
	}{
		{"", KindValidation, "message"},
		{Code(string([]byte{0xff})), KindValidation, "message"},
		{Code(strings.Repeat("k", MaxMessageKeyBytes+1)), KindValidation, "message"},
		{"valid", Kind(255), "message"},
		{"valid", KindValidation, "broken {template"},
		{"valid", KindValidation, strings.Repeat("m", MaxMessageTemplateBytes+1)},
	} {
		codes := NewCodes()
		if err := codes.Add(tc.code, tc.kind, tc.message); err == nil {
			t.Fatalf("Add accepted code=%q kind=%d message=%q", tc.code, tc.kind, tc.message)
		}
		if _, ok := codes.KindOf(tc.code); ok {
			t.Fatalf("the refused code %q was installed", tc.code)
		}
	}
	codes := NewCodes()
	code := Code(strings.Repeat("к", MaxMessageKeyBytes/2))
	if err := codes.Add(code, KindValidation, "message"); err != nil {
		t.Fatalf("a non-ASCII code at the byte limit was refused: %v", err)
	}
	if err := codes.Add("vendor.email_taken", KindConflict, "already registered"); err != nil {
		t.Fatalf("a namespaced custom code was refused: %v", err)
	}
	if err := codes.Add("vendor email taken", KindConflict, "already registered"); err != nil {
		t.Fatalf("an opaque custom code was refused: %v", err)
	}
}

func TestNilMessagesAndFilesystemsAreRefusedWithoutPanicking(t *testing.T) {
	var nilMessages *Messages
	if got, ok := nilMessages.Message(context.Background(), Violation{Code: CodeUnique}, "en"); ok || got != "" {
		t.Fatalf("a nil Messages answered (%q, %v)", got, ok)
	}
	if locales := nilMessages.Locales(); locales != nil {
		t.Fatalf("a nil Messages listed %v", locales)
	}
	if missing := nilMessages.Missing("en"); missing != nil {
		t.Fatalf("a nil Messages reported %v", missing)
	}
	if err := nilMessages.Add("en", "unique", "message"); err == nil {
		t.Fatal("a nil Messages accepted Add")
	}
	if err := nilMessages.Load(fstest.MapFS{}, "."); err == nil {
		t.Fatal("a nil Messages accepted Load")
	}

	m := NewMessages(StandardCodes())
	if err := m.Load(nil, "messages"); err == nil {
		t.Fatal("a nil fs.FS was accepted")
	}
	var typedNil *nilCatalogueFS
	if err := m.Load(typedNil, "messages"); err == nil {
		t.Fatal("a typed nil fs.FS was accepted")
	}
	if loaded, err := LoadMessages(StandardCodes(), nil, "messages"); err == nil || loaded != nil {
		t.Fatalf("LoadMessages with a nil fs.FS answered (%v, %v)", loaded, err)
	}
}

func TestZeroValueCodesAndMessagesRemainUsable(t *testing.T) {
	var codes Codes
	if err := codes.Add("custom", KindValidation, "default"); err != nil {
		t.Fatalf("adding to a zero Codes: %v", err)
	}
	if kind, ok := codes.KindOf("custom"); !ok || kind != KindValidation {
		t.Fatalf("the zero Codes answered (%v, %v)", kind, ok)
	}

	var messages Messages
	if err := messages.Add("en", "custom", "catalogue"); err != nil {
		t.Fatalf("adding to a zero Messages: %v", err)
	}
	if got := say(t, &messages, Violation{Code: "custom"}, "en"); got != "catalogue" {
		t.Fatalf("the zero Messages resolved %q", got)
	}
}

func TestAFailedLateCatalogueFileLeavesTheReceiverUnchanged(t *testing.T) {
	tests := []struct {
		name string
		fsys fs.FS
	}{
		{
			"invalid contents",
			files(map[string]string{
				"a-fr.json": `{"check":"nouveau"}`,
				"z.json":    `{"unique":false}`,
			}),
		},
		{
			"a conflicting declaration",
			files(map[string]string{
				"a-fr.json": `{"check":"nouveau"}`,
				"en.json":   `{"unique":"replacement"}`,
			}),
		},
		{
			"an open failure",
			failingCatalogueFS{
				FS: files(map[string]string{
					"anew.json": `{"check":"new"}`,
					"z.json":    `{"unique":"new"}`,
				}),
				name: "messages/z.json",
			},
		},
		{
			"a read failure",
			readErrorCatalogueFS{
				FS: files(map[string]string{
					"anew.json": `{"check":"new"}`,
					"z.json":    `{"unique":"new"}`,
				}),
				name: "messages/z.json",
			},
		},
		{
			"a close failure",
			closeErrorCatalogueFS{
				FS: files(map[string]string{
					"anew.json": `{"check":"new"}`,
					"z.json":    `{"unique":"new"}`,
				}),
				name: "messages/z.json",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMessages(StandardCodes())
			if err := m.Add("en", "unique", "original"); err != nil {
				t.Fatal(err)
			}
			before := m.Locales()
			if err := m.Load(tc.fsys, "messages"); err == nil {
				t.Fatal("the broken reload succeeded")
			}
			if got := m.Locales(); !reflect.DeepEqual(got, before) {
				t.Fatalf("the failed reload changed locales from %v to %v", before, got)
			}
			if got := say(t, m, Violation{Code: CodeUnique}, "en"); got != "original" {
				t.Fatalf("the failed reload changed the original message to %q", got)
			}
			if got := say(t, m, Violation{Code: CodeCheck}, "a-fr"); got != "this value is not allowed" {
				t.Fatalf("the failed reload installed an early file: %q", got)
			}
		})
	}
}

func TestLoadMergesAnAddThatWinsWhileFilesAreStaged(t *testing.T) {
	m := NewMessages(StandardCodes())
	blocked := &blockedCatalogueFS{
		FS:      files(map[string]string{"en.json": `{"check":"loaded"}`}),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	loaded := make(chan error, 1)
	go func() { loaded <- m.Load(blocked, "messages") }()
	<-blocked.entered
	if err := m.Add("en", "unique", "added while loading"); err != nil {
		t.Fatal(err)
	}
	if got := say(t, m, Violation{Code: CodeUnique}, "en"); got != "added while loading" {
		t.Fatalf("Message could not read the concurrent Add while Load staged files: %q", got)
	}
	close(blocked.release)
	if err := <-loaded; err != nil {
		t.Fatalf("Load failed after the concurrent Add: %v", err)
	}
	if got := say(t, m, Violation{Code: CodeUnique}, "en"); got != "added while loading" {
		t.Fatalf("Load lost the concurrent Add and resolved %q", got)
	}
	if got := say(t, m, Violation{Code: CodeCheck}, "en"); got != "loaded" {
		t.Fatalf("the staged file was not committed: %q", got)
	}
}

func TestAConflictingAddWinsWhileFilesAreStaged(t *testing.T) {
	m := NewMessages(StandardCodes())
	blocked := &blockedCatalogueFS{
		FS:      files(map[string]string{"en.json": `{"unique":"loaded"}`}),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	loaded := make(chan error, 1)
	go func() { loaded <- m.Load(blocked, "messages") }()
	<-blocked.entered
	if err := m.Add("en", "unique", "added while loading"); err != nil {
		t.Fatal(err)
	}
	if got := say(t, m, Violation{Code: CodeUnique}, "en"); got != "added while loading" {
		t.Fatalf("Message could not read the winning Add while Load staged files: %q", got)
	}
	close(blocked.release)
	if err := <-loaded; !errors.Is(err, ErrMessageRedeclared) {
		t.Fatalf("Load answered %v, want ErrMessageRedeclared", err)
	}
	if got := say(t, m, Violation{Code: CodeUnique}, "en"); got != "added while loading" {
		t.Fatalf("the failed Load replaced the winning Add with %q", got)
	}
}

func TestMissingMeasuresTranslationsWithoutChangingMessageFallback(t *testing.T) {
	codes := NewCodes()
	for _, code := range []Code{CodeUnique, CodeCheck} {
		if err := codes.Add(code, KindValidation, "vocabulary default"); err != nil {
			t.Fatal(err)
		}
	}
	m := NewMessages(codes)
	if err := m.Add("", "unique", "default translation"); err != nil {
		t.Fatal(err)
	}
	if got := say(t, m, Violation{Code: CodeUnique}, "ru"); got != "default translation" {
		t.Fatalf("Message no longer falls through to the default catalogue: %q", got)
	}
	if missing := m.Missing("ru"); !reflect.DeepEqual(missing, []Code{CodeCheck, CodeUnique}) {
		t.Fatalf("default.json hid missing Russian translations: %v", missing)
	}
	if missing := m.Missing(""); !reflect.DeepEqual(missing, []Code{CodeCheck}) {
		t.Fatalf("the default locale reports %v missing", missing)
	}

	if err := m.Add("en", "unique", "English translation"); err != nil {
		t.Fatal(err)
	}
	if missing := m.Missing("en-GB"); !reflect.DeepEqual(missing, []Code{CodeCheck}) {
		t.Fatalf("the en base locale did not cover en-GB: %v", missing)
	}
	if got := say(t, m, Violation{Code: CodeUnique}, "en-GB"); got != "English translation" {
		t.Fatalf("Message no longer falls through to a base locale: %q", got)
	}
}

func TestLocaleMatchingIsDeliberatelyCaseSensitive(t *testing.T) {
	m := NewMessages(StandardCodes())
	if err := m.Add("pt-BR", "unique", "endereco ocupado"); err != nil {
		t.Fatal(err)
	}
	if got := say(t, m, Violation{Code: CodeUnique}, "pt-BR"); got != "endereco ocupado" {
		t.Fatalf("the exact locale resolved %q", got)
	}
	if got := say(t, m, Violation{Code: CodeUnique}, "pt-br"); got != "this value is already taken" {
		t.Fatalf("a differently cased opaque locale resolved %q", got)
	}
}

func TestCatalogueInputBoundsAreEnforced(t *testing.T) {
	tooLarge := fstest.MapFS{
		"messages/default.json": &fstest.MapFile{Data: []byte(strings.Repeat("x", MaxCatalogueFileBytes+1))},
	}
	if _, err := LoadMessages(StandardCodes(), tooLarge, "messages"); err == nil {
		t.Fatal("a file over MaxCatalogueFileBytes loaded")
	}

	tooManyFiles := fstest.MapFS{}
	for i := 0; i <= MaxCatalogueFiles; i++ {
		tooManyFiles[fmt.Sprintf("messages/%03d.json", i)] = &fstest.MapFile{Data: []byte(`{}`)}
	}
	if _, err := LoadMessages(StandardCodes(), tooManyFiles, "messages"); err == nil {
		t.Fatal("more than MaxCatalogueFiles loaded")
	}

	tooManyDirectoryEntries := fstest.MapFS{}
	for i := 0; i <= MaxCatalogueDirectoryEntries; i++ {
		tooManyDirectoryEntries[fmt.Sprintf("messages/%04d.txt", i)] = &fstest.MapFile{}
	}
	if _, err := LoadMessages(StandardCodes(), tooManyDirectoryEntries, "messages"); err == nil {
		t.Fatal("more than MaxCatalogueDirectoryEntries were scanned")
	}

	var entries strings.Builder
	entries.WriteByte('{')
	for i := 0; i <= MaxCatalogueEntries; i++ {
		if i > 0 {
			entries.WriteByte(',')
		}
		fmt.Fprintf(&entries, "%q:%q", fmt.Sprintf("key_%d", i), "message")
	}
	entries.WriteByte('}')
	if _, err := LoadMessages(StandardCodes(), files(map[string]string{"default.json": entries.String()}), "messages"); err == nil {
		t.Fatal("more than MaxCatalogueEntries loaded")
	}

	largeFiles := map[string]string{}
	largeTemplate := strings.Repeat("x", MaxMessageTemplateBytes-64)
	for file := 0; file < 17; file++ {
		var body strings.Builder
		body.WriteByte('{')
		for entry := 0; entry < 63; entry++ {
			if entry > 0 {
				body.WriteByte(',')
			}
			fmt.Fprintf(&body, "%q:%q", fmt.Sprintf("key_%d", entry), largeTemplate)
		}
		body.WriteByte('}')
		if body.Len() > MaxCatalogueFileBytes {
			t.Fatalf("the total-size fixture made a %d-byte file", body.Len())
		}
		largeFiles[fmt.Sprintf("%02d.json", file)] = body.String()
	}
	if _, err := LoadMessages(StandardCodes(), files(largeFiles), "messages"); err == nil {
		t.Fatal("more than MaxCatalogueBytes loaded")
	}
}

func TestCatalogueInputBoundsAcceptTheirExactEdges(t *testing.T) {
	base := `{"exact":"edge"}`
	exactFile := []byte(base + strings.Repeat(" ", MaxCatalogueFileBytes-len(base)))
	exactBytes := fstest.MapFS{}
	for i := 0; i < MaxCatalogueBytes/MaxCatalogueFileBytes; i++ {
		exactBytes[fmt.Sprintf("messages/%03d.json", i)] = &fstest.MapFile{Data: exactFile}
	}
	if _, err := LoadMessages(nil, exactBytes, "messages"); err != nil {
		t.Fatalf("exact file and total byte limits were refused: %v", err)
	}

	exactFiles := fstest.MapFS{}
	for i := 0; i < MaxCatalogueFiles; i++ {
		exactFiles[fmt.Sprintf("messages/%03d.json", i)] = &fstest.MapFile{Data: []byte(`{}`)}
	}
	if _, err := LoadMessages(nil, exactFiles, "messages"); err != nil {
		t.Fatalf("exact file-count limit was refused: %v", err)
	}

	exactDirectory := fstest.MapFS{}
	for i := 0; i < MaxCatalogueDirectoryEntries; i++ {
		exactDirectory[fmt.Sprintf("messages/%04d.txt", i)] = &fstest.MapFile{}
	}
	if _, err := LoadMessages(nil, exactDirectory, "messages"); err != nil {
		t.Fatalf("exact directory-entry limit was refused: %v", err)
	}

	var body strings.Builder
	body.WriteByte('{')
	for i := 0; i < MaxCatalogueEntries; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, "%q:%q", fmt.Sprintf("key_%d", i), "value")
	}
	body.WriteByte('}')
	if _, err := LoadMessages(nil, files(map[string]string{"default.json": body.String()}), "messages"); err != nil {
		t.Fatalf("exact entry-count limit was refused: %v", err)
	}

	exactMemory := NewMessages(nil)
	remaining := MaxCatalogueBytes
	for i := 0; remaining > 0; i++ {
		key := fmt.Sprintf("exact_%04d", i)
		textBytes := min(MaxMessageTemplateBytes, remaining-len(key))
		if textBytes < 0 {
			t.Fatalf("cannot construct the exact in-memory boundary with %d bytes left", remaining)
		}
		if err := exactMemory.Add("", key, strings.Repeat("x", textBytes)); err != nil {
			t.Fatalf("exact in-memory byte limit was refused at entry %d: %v", i, err)
		}
		remaining -= len(key) + textBytes
	}
	if err := exactMemory.Add("", "one_more", "x"); err == nil {
		t.Fatal("one byte beyond the exact in-memory limit was accepted")
	}
}

func TestRuntimeAddBoundsCatalogueCountAndBytes(t *testing.T) {
	byCount := NewMessages(nil)
	for i := 0; i < MaxCatalogueEntries; i++ {
		if err := byCount.Add("", fmt.Sprintf("key_%d", i), ""); err != nil {
			t.Fatalf("entry %d below MaxCatalogueEntries was refused: %v", i, err)
		}
	}
	if err := byCount.Add("", "one_more", ""); err == nil {
		t.Fatal("Add exceeded MaxCatalogueEntries")
	}

	byBytes := NewMessages(nil)
	template := strings.Repeat("x", MaxMessageTemplateBytes)
	for i := 0; i < 1023; i++ {
		if err := byBytes.Add("", fmt.Sprintf("large_%d", i), template); err != nil {
			t.Fatalf("byte fixture entry %d was refused before the total bound: %v", i, err)
		}
	}
	if err := byBytes.Add("", "over_total", template); err == nil {
		t.Fatal("Add exceeded MaxCatalogueBytes")
	}
}

func TestCodesAndMessagesSupportConcurrentRuntimeUpdates(t *testing.T) {
	const rounds = 200
	codes := NewCodes()
	messages := NewMessages(codes)
	loadedFS := files(map[string]string{"en.json": `{"loaded":"loaded"}`})
	start := make(chan struct{})
	errs := make(chan error, rounds*6)
	var group sync.WaitGroup
	run := func(work func(int)) {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for i := 0; i < rounds; i++ {
				work(i)
			}
		}()
	}

	run(func(i int) {
		code := Code(fmt.Sprintf("dynamic_%d", i))
		if err := codes.Add(code, KindValidation, "default {value}"); err != nil {
			errs <- err
		}
	})
	run(func(i int) {
		key := fmt.Sprintf("dynamic_%d", i)
		if err := messages.Add("live", key, "live {value}"); err != nil {
			errs <- err
		}
	})
	run(func(int) {
		if err := messages.Load(loadedFS, "messages"); err != nil {
			errs <- err
		}
	})
	run(func(i int) {
		code := Code(fmt.Sprintf("dynamic_%d", i))
		got, ok := messages.Message(context.Background(), Violation{Code: code, Params: P{"value": i}}, "live")
		if ok && got != fmt.Sprintf("live %d", i) && got != fmt.Sprintf("default %d", i) {
			errs <- fmt.Errorf("message %d resolved %q", i, got)
		}
	})
	run(func(int) {
		locales := messages.Locales()
		if !sort.StringsAreSorted(locales) {
			errs <- fmt.Errorf("locales are not sorted: %v", locales)
		}
		missing := messages.Missing("en-GB")
		if !sort.SliceIsSorted(missing, func(i, j int) bool { return missing[i] < missing[j] }) {
			errs <- fmt.Errorf("missing codes are not sorted: %v", missing)
		}
	})
	run(func(i int) {
		code := Code(fmt.Sprintf("dynamic_%d", i))
		codes.KindOf(code)
		codes.Message(context.Background(), Violation{Code: code, Params: P{"value": i}}, "live")
	})

	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for i := 0; i < rounds; i++ {
		code := Code(fmt.Sprintf("dynamic_%d", i))
		if kind, ok := codes.KindOf(code); !ok || kind != KindValidation {
			t.Fatalf("the concurrent code %q was lost: (%v, %v)", code, kind, ok)
		}
		got, ok := messages.Message(context.Background(), Violation{Code: code, Params: P{"value": i}}, "live")
		if !ok || got != fmt.Sprintf("live %d", i) {
			t.Fatalf("the concurrent message %q was lost: (%q, %v)", code, got, ok)
		}
	}
	if got := say(t, messages, Violation{Code: "loaded"}, "en"); got != "loaded" {
		t.Fatalf("the repeatedly loaded file resolved %q", got)
	}
}
