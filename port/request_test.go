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
			var request query.Request
			if err := json.Unmarshal([]byte(`{"after":"opaque"}`), &request); err != nil {
				t.Fatal(err)
			}
			narrow.fn(&request)
			if _, err := request.Compile(widgetMeta, nil); err != nil {
				t.Fatalf("Compile() after %s narrowing = %v", narrow.name, err)
			}
		})
	}
}
