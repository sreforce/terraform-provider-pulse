package clienttest

import (
	"embed"
	"testing"
)

//go:embed testdata/*.json
var fixtures embed.FS

// Fixture loads a canonical mock contract fixture.
func Fixture(t testing.TB, name string) []byte {
	t.Helper()
	value, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read Pulse mock fixture %q: %v", name, err)
	}
	return append([]byte(nil), value...)
}
