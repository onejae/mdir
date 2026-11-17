package files

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyThroughSearchOnlyDestinationAncestor(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	putFile(t, filepath.Join(source, "file"), "contents")
	ancestor := filepath.Join(base, "search-only")
	parent := filepath.Join(ancestor, "parent")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ancestor, 0100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ancestor, 0700) })
	destination := filepath.Join(parent, "copy")
	if err := Copy(context.Background(), source, destination); err != nil {
		t.Fatal(err)
	}
	if got := fileContents(t, filepath.Join(destination, "file")); got != "contents" {
		t.Fatalf("copied file = %q", got)
	}
}
