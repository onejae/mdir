package files

import (
	"archive/tar"
	"archive/zip"
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
)

func TestArchiveRoundTrip(t *testing.T) {
	for _, format := range []string{"zip", "tar", "tgz"} {
		t.Run(format, func(t *testing.T) {
			base := t.TempDir()
			src := filepath.Join(base, "source")
			putFile(t, filepath.Join(src, "nested", "contents.txt"), "hello archive")
			putFile(t, filepath.Join(src, "top.txt"), "top")
			if err := os.Mkdir(filepath.Join(src, "empty"), 0750); err != nil {
				t.Fatal(err)
			}
			stamp := time.Unix(1600000000, 0)
			if err := os.Chtimes(filepath.Join(src, "nested", "contents.txt"), stamp, stamp); err != nil {
				t.Fatal(err)
			}
			archivePath := filepath.Join(base, "test."+format)
			if err := CreateArchive(context.Background(), format, []string{src}, archivePath); err != nil {
				t.Fatal(err)
			}
			a, err := OpenArchive(archivePath)
			if err != nil {
				t.Fatal(err)
			}
			root, err := a.List("")
			if err != nil || len(root) != 1 || root[0].Path != "source" || !root[0].IsDir {
				t.Fatalf("root = %v, %v", root, err)
			}
			entries, err := a.List("source")
			if err != nil || !reflect.DeepEqual(entryNames(entries), []string{"empty", "nested", "top.txt"}) {
				t.Fatalf("nested list = %v, %v", entryNames(entries), err)
			}
			data, err := a.Read("source/nested/contents.txt", 5)
			if err != nil || string(data) != "hello" {
				t.Fatalf("bounded read = %q, %v", data, err)
			}
			dest := t.TempDir()
			if err := a.Extract(context.Background(), nil, dest); err != nil {
				t.Fatal(err)
			}
			if got := fileContents(t, filepath.Join(dest, "source", "nested", "contents.txt")); got != "hello archive" {
				t.Fatalf("extracted = %q", got)
			}
			info, err := os.Stat(filepath.Join(dest, "source", "nested", "contents.txt"))
			if err != nil || info.Mode().Perm() != 0640 || !info.ModTime().Equal(stamp) {
				t.Fatalf("extracted metadata = %v, %v", info, err)
			}
			empty, err := os.Stat(filepath.Join(dest, "source", "empty"))
			if err != nil || !empty.IsDir() || empty.Mode().Perm() != 0750 {
				t.Fatalf("empty directory = %v, %v", empty, err)
			}
			if err := a.Extract(context.Background(), nil, dest); !errors.Is(err, fs.ErrExist) {
				t.Fatalf("repeated extract must refuse files: %v", err)
			}
			selected := t.TempDir()
			if err := a.Extract(context.Background(), []string{"source/nested"}, selected); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(selected, "source", "top.txt")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("extracted unselected sibling: %v", err)
			}
			if got := fileContents(t, filepath.Join(selected, "source", "nested", "contents.txt")); got != "hello archive" {
				t.Fatalf("selected directory missing child: %q", got)
			}
		})
	}
}

type testArchiveItem struct {
	name, data, target string
	kind               byte
}

func makeArchive(t *testing.T, format string, items []testArchiveItem) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "test."+format)
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if format == "zip" {
		zw := zip.NewWriter(f)
		for _, item := range items {
			header := &zip.FileHeader{Name: item.name, Method: zip.Store}
			header.SetMode(0644)
			if item.kind == tar.TypeSymlink {
				header.SetMode(os.ModeSymlink | 0777)
			} else if item.kind == tar.TypeDir {
				header.Name = strings.TrimSuffix(item.name, "/") + "/"
				header.SetMode(os.ModeDir | 0755)
			}
			w, err := zw.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(item.data)); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		tw := tar.NewWriter(f)
		for _, item := range items {
			kind := item.kind
			if kind == 0 {
				kind = tar.TypeReg
			}
			header := &tar.Header{Name: item.name, Mode: 0644, Size: int64(len(item.data)), Typeflag: kind, Linkname: item.target}
			if err := tw.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if item.data != "" {
				if _, err := tw.Write([]byte(item.data)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestArchiveImplicitDirectories(t *testing.T) {
	for _, format := range []string{"zip", "tar"} {
		t.Run(format, func(t *testing.T) {
			name := makeArchive(t, format, []testArchiveItem{{name: "one/two/file", data: "implicit"}})
			a, err := OpenArchive(name)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := a.List("one")
			if err != nil || len(entries) != 1 || entries[0].Name != "two" || !entries[0].IsDir {
				t.Fatalf("implicit directory = %v, %v", entries, err)
			}
			dest := t.TempDir()
			if err := a.Extract(context.Background(), []string{"one"}, dest); err != nil {
				t.Fatal(err)
			}
			if got := fileContents(t, filepath.Join(dest, "one", "two", "file")); got != "implicit" {
				t.Fatalf("extracted = %q", got)
			}
		})
	}
}

func TestArchiveRejectsUnsafePathsAndCollisions(t *testing.T) {
	cases := map[string][]testArchiveItem{
		"traversal":            {{name: "../escape", data: "bad"}},
		"nested traversal":     {{name: "safe/../escape", data: "bad"}},
		"absolute":             {{name: "/escape", data: "bad"}},
		"windows absolute":     {{name: `C:/escape`, data: "bad"}},
		"backslash":            {{name: `..\escape`, data: "bad"}},
		"duplicate":            {{name: "same", data: "one"}, {name: "same", data: "two"}},
		"file parent":          {{name: "parent", data: "file"}, {name: "parent/child", data: "child"}},
		"implicit parent file": {{name: "parent/child", data: "child"}, {name: "parent", data: "file"}},
		"link parent":          {{name: "parent", kind: tar.TypeSymlink, target: "outside"}, {name: "parent/child", data: "child"}},
	}
	for _, format := range []string{"zip", "tar"} {
		for name, items := range cases {
			t.Run(format+"/"+name, func(t *testing.T) {
				archivePath := makeArchive(t, format, items)
				if _, err := OpenArchive(archivePath); err == nil {
					t.Fatal("unsafe archive accepted")
				}
			})
		}
	}
}

func TestExtractRejectsLinksSpecialFilesAndSymlinkParents(t *testing.T) {
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeFifo} {
		t.Run(string(kind), func(t *testing.T) {
			name := makeArchive(t, "tar", []testArchiveItem{{name: "unsafe", kind: kind, target: "../outside"}})
			a, err := OpenArchive(name)
			if err != nil {
				t.Fatal(err)
			}
			dest := t.TempDir()
			if err := a.Extract(context.Background(), nil, dest); err == nil {
				t.Fatal("link or special file extracted")
			}
			entries, err := os.ReadDir(dest)
			if err != nil || len(entries) != 0 {
				t.Fatalf("unsafe extraction wrote files: %v, %v", entries, err)
			}
		})
	}
	name := makeArchive(t, "zip", []testArchiveItem{{name: "parent/file", data: "bad"}})
	a, err := OpenArchive(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, inside := range []bool{false, true} {
		dest, target := t.TempDir(), t.TempDir()
		if inside {
			target = filepath.Join(dest, "other")
			if err := os.Mkdir(target, 0755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(target, filepath.Join(dest, "parent")); err != nil {
			t.Fatal(err)
		}
		if err := a.Extract(context.Background(), nil, dest); err == nil {
			t.Fatal("extraction followed symlink parent")
		}
		if _, err := os.Stat(filepath.Join(target, "file")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("symlink target written: %v", err)
		}
	}
}

func TestCreateArchiveSafetyAndLinkPreservation(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	putFile(t, filepath.Join(src, "file"), "data")
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(src, alias); err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{filepath.Join(src, "inside.zip"), filepath.Join(alias, "inside.zip")} {
		if err := CreateArchive(context.Background(), "zip", []string{src}, dest); err == nil {
			t.Fatal("archive created inside source")
		}
	}
	existing := filepath.Join(base, "exists.zip")
	putFile(t, existing, "keep")
	if err := CreateArchive(context.Background(), "zip", []string{src}, existing); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("archive collision = %v", err)
	}
	if got := fileContents(t, existing); got != "keep" {
		t.Fatalf("archive overwrote destination: %q", got)
	}
	if err := os.Symlink("../../outside", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"zip", "tar", "tgz"} {
		archivePath := filepath.Join(base, "links."+format)
		if err := CreateArchive(context.Background(), format, []string{src}, archivePath); err != nil {
			t.Fatal(err)
		}
		a, err := OpenArchive(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := a.List("src")
		if err != nil || len(entries) != 2 || entries[1].Name != "link" || !entries[1].IsLink {
			t.Fatalf("archive link = %v, %v", entries, err)
		}
		if err := a.Extract(context.Background(), nil, t.TempDir()); err == nil {
			t.Fatal("created archive link extracted")
		}
		if err := a.Extract(context.Background(), []string{"src/file"}, t.TempDir()); err != nil {
			t.Fatalf("safe selection rejected: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CreateArchive(ctx, "zip", []string{src}, filepath.Join(base, "canceled.zip")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled create = %v", err)
	}
	if err := CreateArchive(context.Background(), "7z", []string{src}, filepath.Join(base, "unknown")); err == nil {
		t.Fatal("unknown format accepted")
	}
}

func TestArchiveCancellationAndGzipSignature(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "file")
	putFile(t, source, "content")
	name := filepath.Join(base, "no-extension")
	if err := CreateArchive(context.Background(), "tgz", []string{source}, name); err != nil {
		t.Fatal(err)
	}
	a, err := OpenArchive(name)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := a.Read("file", 99); err != nil || string(data) != "content" {
		t.Fatalf("gzip signature read = %q, %v", data, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Extract(ctx, nil, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled extract = %v", err)
	}
}

func TestExtractCreatesDestinationAfterArchiveValidation(t *testing.T) {
	name := makeArchive(t, "tar", []testArchiveItem{{name: "folder/file", data: "created"}})
	a, err := OpenArchive(name)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "new", "destination")
	if err := a.Extract(context.Background(), nil, dest); err != nil {
		t.Fatal(err)
	}
	if got := fileContents(t, filepath.Join(dest, "folder", "file")); got != "created" {
		t.Fatalf("new destination contents = %q", got)
	}
	unsafe := makeArchive(t, "tar", []testArchiveItem{{name: "link", kind: tar.TypeSymlink, target: "../outside"}})
	a, err = OpenArchive(unsafe)
	if err != nil {
		t.Fatal(err)
	}
	newDest := filepath.Join(t.TempDir(), "must-not-create")
	if err := a.Extract(context.Background(), nil, newDest); err == nil {
		t.Fatal("unsafe archive extracted")
	}
	if _, err := os.Lstat(newDest); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("destination mutated before validation: %v", err)
	}
}

func TestExtractParentSymlinkSwapDoesNotEscape(t *testing.T) {
	archivePath := makeArchive(t, "zip", []testArchiveItem{{name: "parent/file", data: "inside"}})
	a, err := OpenArchive(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	dest, outside := t.TempDir(), t.TempDir()
	parent := filepath.Join(dest, "parent")
	parked := filepath.Join(dest, "parked")
	if err := os.Mkdir(parent, 0755); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	started := make(chan struct{})
	stop := make(chan struct{})
	go func() {
		defer wg.Done()
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if os.Rename(parent, parked) == nil {
				if os.Symlink(outside, parent) == nil {
					_ = os.Remove(parent)
				}
				_ = os.Rename(parked, parent)
			}
		}
	}()
	<-started
	for i := 0; i < 50; i++ {
		// Either success or a collision/change error is acceptable during the
		// attack; writing outside the extraction root is never acceptable.
		_ = a.Extract(context.Background(), nil, dest)
	}
	close(stop)
	wg.Wait()
	if _, err := os.Lstat(filepath.Join(outside, "file")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("extraction escaped through replaced parent: %v", err)
	}
}

func TestCreateArchiveRejectsUnsafeDescendantNames(t *testing.T) {
	for _, format := range []string{"zip", "tar", "tgz"} {
		t.Run(format, func(t *testing.T) {
			base := t.TempDir()
			source := filepath.Join(base, "source")
			putFile(t, filepath.Join(source, "nested", `bad\name`), "keep source")
			dest := filepath.Join(base, "output."+format)
			if err := CreateArchive(context.Background(), format, []string{source}, dest); err == nil {
				t.Fatal("created an archive with an unsupported descendant name")
			}
			if _, err := os.Lstat(dest); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("partial archive remains: %v", err)
			}
			if got := fileContents(t, filepath.Join(source, "nested", `bad\name`)); got != "keep source" {
				t.Fatalf("source changed: %q", got)
			}
		})
	}
}
