package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestMoveAndRenameCarryComments(t *testing.T) {
	a, _ := testApp(t)
	source := filepath.Join(a.Cwd, "alpha.txt")
	dest := filepath.Join(a.Cwd, "folder", "alpha.txt")
	a.State.Comments[source] = "file note"
	focus(t, a, "alpha.txt")
	a.command("move " + dest)
	finishJob(t, a)
	if a.State.Comments[source] != "" || a.State.Comments[dest] != "file note" {
		t.Fatalf("move comments: %#v", a.State.Comments)
	}
	focus(t, a, "folder")
	a.command("rename renamed folder")
	finishJob(t, a)
	renamed := filepath.Join(a.Cwd, "renamed folder", "alpha.txt")
	if a.State.Comments[dest] != "" || a.State.Comments[renamed] != "file note" {
		t.Fatalf("directory rename comments: %#v", a.State.Comments)
	}
	state, err := loadState(a.StateFile)
	if err != nil || state.Comments[renamed] != "file note" {
		t.Fatal("updated comments not persisted")
	}
}

func TestDeletingDirectoryPrunesOnlyItsComments(t *testing.T) {
	a, _ := testApp(t)
	inside := filepath.Join(a.Cwd, "folder", "inside.txt")
	outside := filepath.Join(a.Cwd, "alpha.txt")
	a.State.Comments[inside] = "inside note"
	a.State.Comments[outside] = "outside note"
	focus(t, a, "folder")
	typeText(a, "xj")
	key(a, tcell.KeyEnter)
	finishJob(t, a)
	if a.State.Comments[inside] != "" || a.State.Comments[outside] != "outside note" {
		t.Fatalf("delete comments: %#v", a.State.Comments)
	}
}

func TestPartialMoveUpdatesOnlyCompletedComments(t *testing.T) {
	a, _ := testApp(t)
	dest := filepath.Join(a.Cwd, "target")
	if err := os.Mkdir(dest, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "beta.go"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha.txt", "beta.go"} {
		full := filepath.Join(a.Cwd, name)
		a.selected[full] = true
		a.State.Comments[full] = name + " note"
	}
	a.command("move " + dest)
	finishJob(t, a)
	if !a.statusError {
		t.Fatal("second move should have collided")
	}
	if a.State.Comments[filepath.Join(a.Cwd, "alpha.txt")] != "" || a.State.Comments[filepath.Join(dest, "alpha.txt")] != "alpha.txt note" {
		t.Fatal("completed item comment not moved")
	}
	if a.State.Comments[filepath.Join(a.Cwd, "beta.go")] != "beta.go note" || a.State.Comments[filepath.Join(dest, "beta.go")] != "" {
		t.Fatal("failed item comment moved")
	}
}

func TestCutPasteCarriesComments(t *testing.T) {
	a, _ := testApp(t)
	source := filepath.Join(a.Cwd, "alpha.txt")
	a.State.Comments[source] = "clipboard note"
	focus(t, a, "alpha.txt")
	typeText(a, "dd")
	focus(t, a, "folder")
	typeText(a, "lp")
	finishJob(t, a)
	if a.State.Comments[source] != "" || a.State.Comments[filepath.Join(a.Cwd, "alpha.txt")] != "clipboard note" {
		t.Fatal("cut/paste comments not moved")
	}
}

func TestPermissionDialogRefusesReplacementSymlink(t *testing.T) {
	a, _ := testApp(t)
	focus(t, a, "alpha.txt")
	a.chmodSelected()
	source := filepath.Join(a.Cwd, "alpha.txt")
	target := filepath.Join(a.Cwd, "beta.go")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, source); err != nil {
		t.Fatal(err)
	}
	input(a, "600")
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatal("permissions changed through replacement symlink")
	}
	if !a.statusError {
		t.Fatal("symlink refusal not reported")
	}
}

func TestPartialDeletePreservesUnreadableComments(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires Unix permission enforcement")
	}
	a, _ := testApp(t)
	first, second := filepath.Join(a.Cwd, "aa-delete"), filepath.Join(a.Cwd, "ab-blocked")
	for _, dir := range []string{first, second} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "child"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		a.State.Comments[filepath.Join(dir, "child")] = dir
	}
	a.refresh()
	if err := os.Chmod(second, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(second, 0700)
	a.selected[first], a.selected[second] = true, true
	a.deleteSelected()
	a.menu.Cursor = 1
	key(a, tcell.KeyEnter)
	finishJob(t, a)
	if !a.statusError {
		t.Fatal("inaccessible directory deletion unexpectedly succeeded")
	}
	if a.State.Comments[filepath.Join(first, "child")] != "" || a.State.Comments[filepath.Join(second, "child")] != second {
		t.Fatal("partial delete lost or retained incorrect comments")
	}
}
