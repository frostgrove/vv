package jobsmemory

import "testing"

func TestMemoryDescriptionPublishesExactZeroExternalResources(t *testing.T) {
	backend, err := NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	profile := backend.Description().ResourceProfile()
	resources, err := profile.Resolve(256)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.IsDeclared() || !resources.IsComplete() || !resources.IsEmpty() || !profile.Lifecycle().IsEmpty() {
		t.Fatalf("memory resources = runtime %v lifecycle %v", resources, profile.Lifecycle())
	}
}
