package port

import (
	"encoding/json"
	"testing"

	"github.com/frostgrove/vv/crud/query"
)

func TestNarrowingAnEntityOrCountAlsoClearsJSONCursorPresence(t *testing.T) {
	for _, narrow := range []struct {
		name string
		fn   func(*query.Request)
	}{
		{"count", NarrowForCount},
		{"entity", NarrowForEntity},
	} {
		t.Run(narrow.name, func(t *testing.T) {
			var req query.Request
			if err := json.Unmarshal([]byte(`{"after":"opaque"}`), &req); err != nil {
				t.Fatal(err)
			}
			narrow.fn(&req)
			if _, err := req.Compile(widgetMeta, nil); err != nil {
				t.Fatalf("Compile() after %s narrowing = %v", narrow.name, err)
			}
		})
	}
}
