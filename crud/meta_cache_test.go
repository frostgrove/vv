package crud

import (
	"reflect"
	"sync"
	"testing"
)

type cacheFieldsA struct {
	A01, A02, A03, A04, A05, A06, A07, A08 int
}
type cacheFieldsB struct {
	B01, B02, B03, B04, B05, B06, B07, B08 int
}
type cacheProbeModel struct {
	ID int64 `db:"id,pk"`
	cacheFieldsA
	cacheFieldsB
}

func TestConcurrentFirstSchemaUseReturnsOneSchema(t *testing.T) {
	typ := reflect.TypeFor[cacheProbeModel]()
	schemaCache.Delete(typ)

	const callers = 128
	start := make(chan struct{})
	results := make(chan *Schema, callers)
	errors := make(chan error, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			<-start
			schema, err := schemaOfType(typ)
			results <- schema
			errors <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errors)

	for err := range errors {
		if err != nil {
			t.Fatalf("the schema failed to build: %v", err)
		}
	}
	var winner *Schema
	for schema := range results {
		if winner == nil {
			winner = schema
			continue
		}
		if schema != winner {
			t.Fatal("concurrent first use returned two schema identities")
		}
	}
	if winner == nil || winner.Type != typ {
		t.Fatalf("schema winner = %#v, want %v", winner, typ)
	}
}
