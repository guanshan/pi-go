package ai

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func assertTSProviderFixture(t *testing.T, got []byte, name string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", "provider", "ts", name))
	if err != nil {
		t.Fatal(err)
	}
	gotCanonical := canonicalProviderJSON(t, got)
	wantCanonical := canonicalProviderJSON(t, want)
	if !bytes.Equal(gotCanonical, wantCanonical) {
		t.Fatalf("%s TypeScript fixture mismatch\n--- got ---\n%s\n--- want ---\n%s", name, gotCanonical, wantCanonical)
	}
}

func canonicalProviderJSON(t *testing.T, data []byte) []byte {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}
