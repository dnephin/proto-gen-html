package util

import (
	"testing"
)

// Fully-qualified symbol path resolution tests.
func TestResolverFQ(t *testing.T) {
	tests := map[string]string{
		".world.building.Options": "world/region/building.proto",
		".world.human.Options":    "world/region/human.proto",
	}

	req, err := ReadJSONFile("testdata/resolver.json")
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(req.ProtoFile)
	for symbolPath, want := range tests {
		_, got := resolver.Resolve(symbolPath, nil)
		if got.GetName() != want {
			t.Logf("symbolPath=%q\n", symbolPath)
			t.Fatalf("got %q want %q", got, want)
		}
	}
}
