package files

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestHeldRootSurvivesRenameAndRejectsLinkChildren(t *testing.T) {
	base := t.TempDir()
	original, moved := filepath.Join(base, "original"), filepath.Join(base, "moved")
	putFile(t, filepath.Join(original, "child", "file"), "held contents")
	if err := os.Symlink("child", filepath.Join(original, "alias")); err != nil {
		t.Fatal(err)
	}
	f, err := openNoFollow(original, true)
	if err != nil {
		t.Fatal(err)
	}
	root, err := rootFromFile(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(original, "child", "file"), "replacement")
	child, err := openChildRoot(root, "child")
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	data, err := child.ReadFile("file")
	if err != nil || string(data) != "held contents" {
		t.Fatalf("held root followed replacement: %q, %v", data, err)
	}
	if target, err := root.Readlink("alias"); err != nil || target != "child" {
		t.Fatalf("held root readlink = %q, %v", target, err)
	}
	if unsafeRoot, err := openChildRoot(root, "alias"); err == nil {
		unsafeRoot.Close()
		t.Fatal("recursive mutation followed an internal directory symlink")
	}
}

func TestMetadataRemainsOnOpenedInodeAfterReplacement(t *testing.T) {
	base := t.TempDir()
	original, moved := filepath.Join(base, "original"), filepath.Join(base, "moved")
	putFile(t, original, "held")
	f, err := os.Open(original)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	putFile(t, original, "replacement")
	replacement, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1600000000, 123456789)
	if err := setMetadata(f, 0600, stamp); err != nil {
		t.Fatal(err)
	}
	held, err := os.Stat(moved)
	if err != nil || held.Mode().Perm() != 0600 || !held.ModTime().Equal(stamp) {
		t.Fatalf("held inode metadata = %v, %v", held, err)
	}
	current, err := os.Stat(original)
	if err != nil || !sameFileVersion(replacement, current) {
		t.Fatalf("replacement metadata changed: %v, %v", current, err)
	}
}

func TestRootDestinationChecksHeldAncestryAfterRename(t *testing.T) {
	base := t.TempDir()
	source, destination := filepath.Join(base, "source"), filepath.Join(base, "parent")
	putFile(t, filepath.Join(source, "file"), "source")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.OpenRoot(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := os.Rename(destination, filepath.Join(source, "moved-parent")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := checkRootDestination(source, "copy", info, parent); err == nil || !strings.Contains(err.Error(), "inside source") {
		t.Fatalf("held destination inside renamed source accepted: %v", err)
	}
}

func TestCopyTraversalSurvivesSourceDirectoryReplacement(t *testing.T) {
	base := t.TempDir()
	source, destination := filepath.Join(base, "source"), filepath.Join(base, "destination")
	putFile(t, filepath.Join(source, "child", "file"), "held contents")
	changed := false
	ctx := filesystemHookContext{Context: context.Background(), hook: func() {
		if changed {
			return
		}
		if _, err := os.Stat(destination); err != nil {
			return
		}
		changed = true
		if err := os.Rename(source, source+".moved"); err != nil {
			t.Fatal(err)
		}
		putFile(t, filepath.Join(source, "child", "file"), "replacement")
	}}
	if err := Copy(ctx, source, destination); err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("did not exercise source directory replacement")
	}
	if got := fileContents(t, filepath.Join(destination, "child", "file")); got != "held contents" {
		t.Fatalf("copy followed replacement: %q", got)
	}
}

func TestPinnedSymlinkSurvivesUnlink(t *testing.T) {
	base := t.TempDir()
	link := filepath.Join(base, "link")
	if err := os.Symlink("missing-target", link); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	f, err := openPinnedAt(parent, "link")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || before.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("did not pin the symlink: %v, %v", before, err)
	}
	if err := unix.Unlinkat(int(parent.Fd()), "link", 0); err != nil {
		t.Fatal(err)
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("lost the pinned symlink after unlink: %v, %v", after, err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("symlink remains: %v", err)
	}
}

func TestArchiveTraversalSurvivesSourceDirectoryReplacement(t *testing.T) {
	for _, format := range []string{"zip", "tar", "tgz"} {
		t.Run(format, func(t *testing.T) {
			base := t.TempDir()
			source, destination := filepath.Join(base, "source"), filepath.Join(base, "archive."+format)
			putFile(t, filepath.Join(source, "child", "file"), "held contents")
			calls := 0
			changed := false
			ctx := filesystemHookContext{Context: context.Background(), hook: func() {
				calls++
				// The third checkpoint starts the first child after its parent
				// directory has been opened and its entries enumerated.
				if calls != 3 {
					return
				}
				changed = true
				if err := os.Rename(source, source+".moved"); err != nil {
					t.Fatal(err)
				}
				putFile(t, filepath.Join(source, "child", "file"), "replacement")
			}}
			if err := CreateArchive(ctx, format, []string{source}, destination); err != nil {
				t.Fatal(err)
			}
			if !changed {
				t.Fatal("did not exercise source directory replacement")
			}
			archive, err := OpenArchive(destination)
			if err != nil {
				t.Fatal(err)
			}
			data, err := archive.Read("source/child/file", 1024)
			if err != nil || string(data) != "held contents" {
				t.Fatalf("archive followed replacement: %q, %v", data, err)
			}
		})
	}
}

func TestMountsReturnsSortedUniquePathsIncludingRoot(t *testing.T) {
	mounts, err := Mounts()
	if err != nil {
		t.Fatal(err)
	}
	if !sort.StringsAreSorted(mounts) || len(mounts) == 0 || mounts[0] != "/" {
		t.Fatalf("mounts must be sorted and include root: %v", mounts)
	}
	for i, mount := range mounts {
		if !filepath.IsAbs(mount) || (i > 0 && mounts[i-1] == mount) {
			t.Fatalf("invalid or duplicate mount: %q", mount)
		}
	}
}
