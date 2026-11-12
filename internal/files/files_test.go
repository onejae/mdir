package files

import (
	"bufio"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func putFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0640); err != nil {
		t.Fatal(err)
	}
}

func fileContents(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func entryNames(entries []Entry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	return names
}

func TestListMetadataHiddenAndSorting(t *testing.T) {
	dir := t.TempDir()
	putFile(t, filepath.Join(dir, "a.log"), "longer")
	putFile(t, filepath.Join(dir, "b.txt"), "x")
	putFile(t, filepath.Join(dir, ".hidden"), "hidden")
	if err := os.Mkdir(filepath.Join(dir, "z-dir"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("z-dir", filepath.Join(dir, "dir-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(dir, "broken")); err != nil {
		t.Fatal(err)
	}
	entries, err := List(dir, false, "name", false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := entryNames(entries), []string{"dir-link", "z-dir", "a.log", "b.txt", "broken"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	if !entries[0].IsLink || !entries[0].IsDir || entries[0].Mode&os.ModeSymlink == 0 {
		t.Fatalf("symlink must retain lstat metadata: %+v", entries[0])
	}
	entries, err = List(dir, true, "name", true)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Name != "z-dir" || entries[1].Name != "dir-link" || entries[len(entries)-1].Name != ".hidden" {
		t.Fatalf("reverse must retain directories first: %v", entryNames(entries))
	}
	values := []Entry{{Name: "a.z", Size: 2, ModTime: time.Unix(2, 0)}, {Name: "b.a", Size: 1, ModTime: time.Unix(1, 0)}}
	for _, key := range []string{"ext", "size", "time"} {
		sortEntries(values, key, false)
		if values[0].Name != "b.a" {
			t.Errorf("%s sort = %v", key, entryNames(values))
		}
	}
}

func TestSearchRecursiveLimitCancellationAndLinks(t *testing.T) {
	dir := t.TempDir()
	putFile(t, filepath.Join(dir, "nested", "Notes.TXT"), "first")
	putFile(t, filepath.Join(dir, "deep", "sub", "notes.md"), "second")
	outside := t.TempDir()
	putFile(t, filepath.Join(outside, "notes-secret"), "outside")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	entries, err := Search(context.Background(), dir, "NOTES", 0)
	if err != nil || len(entries) != 2 {
		t.Fatalf("substring search: %v, %v", entryNames(entries), err)
	}
	entries, err = Search(context.Background(), dir, "*.txt", 0)
	if err != nil || len(entries) != 1 || entries[0].Name != "Notes.TXT" {
		t.Fatalf("glob search: %v, %v", entryNames(entries), err)
	}
	entries, err = Search(context.Background(), dir, "notes", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("limited search: %v, %v", entries, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Search(ctx, dir, "", 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled search = %v", err)
	}
	if _, err := Search(context.Background(), dir, "[", 0); err == nil {
		t.Fatal("malformed glob accepted")
	}
}

func TestParseMounts(t *testing.T) {
	data := "1 0 0:1 / / rw - tmpfs tmpfs rw\n2 1 0:2 / /with\\040space rw - tmpfs tmpfs rw\n3 1 0:2 / /with\\040space rw - tmpfs tmpfs rw\n4 1 0:3 / /a\\134b rw - tmpfs tmpfs rw\n"
	got, err := parseMounts(bufio.NewScanner(strings.NewReader(data)))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/", `/a\b`, "/with space"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mounts = %q, want %q", got, want)
	}
	if _, err := parseMounts(bufio.NewScanner(strings.NewReader("broken"))); err == nil {
		t.Fatal("malformed mount table accepted")
	}
}

func TestSearchReturnsPartialResultsAndDirectoryErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read directories with no permission")
	}
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	putFile(t, filepath.Join(blocked, "hidden.txt"), "hidden")
	putFile(t, filepath.Join(root, "readable", "found.txt"), "found")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0700) })
	entries, err := Search(context.Background(), root, "*.txt", 0)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("search error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "found.txt" {
		t.Fatalf("search stopped before readable sibling: %v", entryNames(entries))
	}
}

func TestConcurrentCopiesNeverOverwrite(t *testing.T) {
	root := t.TempDir()
	srcA, srcB := filepath.Join(root, "a"), filepath.Join(root, "b")
	putFile(t, srcA, "first source")
	putFile(t, srcB, "second source")
	for i := 0; i < 20; i++ {
		dest := filepath.Join(t.TempDir(), "destination")
		var wg sync.WaitGroup
		wg.Add(2)
		results := make(chan error, 2)
		start := make(chan struct{})
		for _, src := range []string{srcA, srcB} {
			go func() {
				defer wg.Done()
				<-start
				results <- Copy(context.Background(), src, dest)
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		success, collision := 0, 0
		for err := range results {
			if err == nil {
				success++
			} else if errors.Is(err, fs.ErrExist) {
				collision++
			} else {
				t.Fatalf("unexpected race result: %v", err)
			}
		}
		if success != 1 || collision != 1 {
			t.Fatalf("successes = %d, collisions = %d", success, collision)
		}
		if got := fileContents(t, dest); got != "first source" && got != "second source" {
			t.Fatalf("corrupted destination: %q", got)
		}
	}
}

func TestCopyPreservesTreeMetadataAndLinks(t *testing.T) {
	base := t.TempDir()
	src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
	putFile(t, filepath.Join(src, "nested", "item"), "contents")
	if err := os.Symlink("../nested", filepath.Join(src, "nested", "link")); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1600000000, 123456789)
	for _, name := range []string{src, filepath.Join(src, "nested"), filepath.Join(src, "nested", "item")} {
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(src, "nested"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := Copy(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	if got := fileContents(t, filepath.Join(dst, "nested", "item")); got != "contents" {
		t.Fatalf("contents = %q", got)
	}
	for _, name := range []string{".", "nested", "nested/item"} {
		before, err := os.Lstat(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		after, err := os.Lstat(filepath.Join(dst, name))
		if err != nil {
			t.Fatal(err)
		}
		if before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
			t.Errorf("%s metadata before = %v %v; after = %v %v", name, before.Mode(), before.ModTime(), after.Mode(), after.ModTime())
		}
	}
	if target, err := os.Readlink(filepath.Join(dst, "nested", "link")); err != nil || target != "../nested" {
		t.Fatalf("copied link = %q, %v", target, err)
	}
}

func TestCopyAndMoveRefuseCollisionsAndSelfDestination(t *testing.T) {
	for _, operation := range []struct {
		name string
		fn   func(context.Context, string, string) error
	}{{"copy", Copy}, {"move", Move}} {
		t.Run(operation.name, func(t *testing.T) {
			base := t.TempDir()
			src := filepath.Join(base, "src")
			putFile(t, filepath.Join(src, "original"), "keep")
			existing := filepath.Join(base, "existing")
			putFile(t, existing, "untouched")
			if err := operation.fn(context.Background(), src, existing); !errors.Is(err, fs.ErrExist) {
				t.Fatalf("collision = %v", err)
			}
			if got := fileContents(t, existing); got != "untouched" {
				t.Fatalf("overwrote collision: %q", got)
			}
			if err := operation.fn(context.Background(), src, filepath.Join(src, "child")); err == nil {
				t.Fatal("self destination accepted")
			}
			alias := filepath.Join(base, "alias")
			if err := os.Symlink(src, alias); err != nil {
				t.Fatal(err)
			}
			if err := operation.fn(context.Background(), src, filepath.Join(alias, "child")); err == nil {
				t.Fatal("symlink ancestor self destination accepted")
			}
			if err := operation.fn(context.Background(), src, alias); !errors.Is(err, fs.ErrExist) {
				t.Fatalf("link collision = %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := operation.fn(ctx, src, filepath.Join(base, "cancel")); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation = %v", err)
			}
			if got := fileContents(t, filepath.Join(src, "original")); got != "keep" {
				t.Fatalf("source changed: %q", got)
			}
		})
	}
}

func TestMoveAndRemoveDoNotTraverseLinks(t *testing.T) {
	base := t.TempDir()
	src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
	putFile(t, filepath.Join(src, "item"), "keep")
	if err := Move(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source remains: %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(dst, link); err != nil {
		t.Fatal(err)
	}
	if err := Remove(context.Background(), link); err != nil {
		t.Fatal(err)
	}
	if got := fileContents(t, filepath.Join(dst, "item")); got != "keep" {
		t.Fatal("symlink target changed")
	}
	putFile(t, filepath.Join(base, "tree", "child", "item"), "remove")
	if err := os.Symlink(dst, filepath.Join(base, "tree", "outside")); err != nil {
		t.Fatal(err)
	}
	if err := Remove(context.Background(), filepath.Join(base, "tree")); err != nil {
		t.Fatal(err)
	}
	if got := fileContents(t, filepath.Join(dst, "item")); got != "keep" {
		t.Fatal("recursive remove followed link")
	}
	for _, root := range []string{"/", "/..", ".", ".."} {
		if err := Remove(context.Background(), root); err == nil {
			t.Fatalf("unsafe remove accepted: %s", root)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Remove(ctx, dst); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled remove = %v", err)
	}
}

type cancelReader struct {
	cancel context.CancelFunc
	root   string
	read   bool
}

func (r *cancelReader) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		if err := os.WriteFile(filepath.Join(r.root, "external"), []byte("unrelated"), 0600); err != nil {
			return 0, err
		}
		r.cancel()
		return copy(p, "partial"), nil
	}
	return 0, context.Canceled
}

func TestCanceledCopyCleansOnlyIncompleteFile(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = writeExclusive(ctx, root, "incomplete", &cancelReader{cancel: cancel, root: dir}, 0644, time.Now())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("write = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "incomplete")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("incomplete file remains: %v", err)
	}
	if got := fileContents(t, filepath.Join(dir, "external")); got != "unrelated" {
		t.Fatalf("unrelated file removed: %q", got)
	}
}

func TestMoveCrossDeviceFallback(t *testing.T) {
	t.Run("completed copy removes original", func(t *testing.T) {
		base := t.TempDir()
		src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
		putFile(t, filepath.Join(src, "nested", "file"), "cross device")
		err := moveWithRename(context.Background(), src, dst, func(string, string) error { return unix.EXDEV })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(src); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("original remains: %v", err)
		}
		if got := fileContents(t, filepath.Join(dst, "nested", "file")); got != "cross device" {
			t.Fatalf("moved contents = %q", got)
		}
	})
	t.Run("failed copy keeps original", func(t *testing.T) {
		base := t.TempDir()
		src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
		putFile(t, filepath.Join(src, "file"), "keep original")
		if err := unix.Mkfifo(filepath.Join(src, "unsupported"), 0600); err != nil {
			t.Fatal(err)
		}
		err := moveWithRename(context.Background(), src, dst, func(string, string) error { return unix.EXDEV })
		if err == nil {
			t.Fatal("unsupported source type copied")
		}
		if got := fileContents(t, filepath.Join(src, "file")); got != "keep original" {
			t.Fatalf("original changed: %q", got)
		}
		if info, err := os.Lstat(filepath.Join(src, "unsupported")); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("original special entry removed: %v, %v", info, err)
		}
	})
	t.Run("other errors do not copy", func(t *testing.T) {
		base := t.TempDir()
		src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
		putFile(t, src, "keep")
		err := moveWithRename(context.Background(), src, dst, func(string, string) error { return unix.EACCES })
		if !errors.Is(err, unix.EACCES) {
			t.Fatalf("rename failure = %v", err)
		}
		if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("copied after a non-EXDEV error: %v", err)
		}
	})
}

func TestMoveDestinationAppearingAfterPreflight(t *testing.T) {
	for _, crossDevice := range []bool{false, true} {
		base := t.TempDir()
		src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
		putFile(t, src, "original")
		err := moveWithRename(context.Background(), src, dst, func(src, dst string) error {
			if err := os.WriteFile(dst, []byte("concurrent file"), 0600); err != nil {
				return err
			}
			if crossDevice {
				return unix.EXDEV
			}
			return renameNoReplace(src, dst)
		})
		if !errors.Is(err, fs.ErrExist) {
			t.Fatalf("racing destination = %v", err)
		}
		if got := fileContents(t, src); got != "original" {
			t.Fatalf("lost original: %q", got)
		}
		if got := fileContents(t, dst); got != "concurrent file" {
			t.Fatalf("overwrote concurrent file: %q", got)
		}
	}
}
