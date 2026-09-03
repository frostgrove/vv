package runtime_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/runtime/runtimecheck"
)

// tolerated names the files that still activate a component with an empty
// fx.Invoke, with the reason each is still here. D-092 forbids the pattern; a
// file listed here is debt, not an exemption, and an entry that no longer
// matches must be deleted rather than left to rot.
var tolerated = map[string]string{}

func TestNothingInTheTreeIsActivatedByAnEmptyInvoke(t *testing.T) {
	found, err := runtimecheck.EmptyInvokeActivations("..")
	if err != nil {
		t.Fatal(err)
	}

	var unexpected []string
	files := make([]string, 0, len(found))
	for _, activation := range found {
		files = append(files, activation.File)
		if _, allowed := tolerated[activation.File]; !allowed {
			unexpected = append(unexpected, activation.String())
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("these activate a component with an empty fx.Invoke, which D-092 forbids: %s",
			strings.Join(unexpected, ", "))
	}
	for file, reason := range tolerated {
		if !slices.Contains(files, file) {
			t.Fatalf("%s no longer has an empty fx.Invoke; delete its entry, which still says %q", file, reason)
		}
	}
}
