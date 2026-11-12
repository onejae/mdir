package files

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The callback injects a filesystem change at a deterministic cancellation
// checkpoint without sleeps, large fixtures, or timing-dependent goroutines.
type filesystemHookContext struct {
	context.Context
	hook func()
}

func (c filesystemHookContext) Err() error {
	c.hook()
	return c.Context.Err()
}

func TestCrossDeviceMovePreservesNewSourceChildren(t *testing.T) {
	base := t.TempDir()
	src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
	putFile(t, filepath.Join(src, "copied"), "old data")
	added := false
	ctx := filesystemHookContext{Context: context.Background(), hook: func() {
		if added {
			return
		}
		data, err := os.ReadFile(filepath.Join(dst, "copied"))
		if err == nil && string(data) == "old data" {
			added = true
			putFile(t, filepath.Join(src, "new"), "new data")
		}
	}}
	err := moveWithRename(ctx, src, dst, func(string, string) error { return unix.EXDEV })
	if !added {
		t.Fatal("did not exercise the source change")
	}
	if err == nil {
		t.Fatal("move reported complete despite a new source child")
	}
	if got := fileContents(t, filepath.Join(src, "new")); got != "new data" {
		t.Fatalf("new source child changed: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(dst, "new")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("test child was unexpectedly copied: %v", err)
	}
}

func TestCrossDeviceMovePreservesUpdatedSourceFile(t *testing.T) {
	base := t.TempDir()
	src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
	putFile(t, src, "old data")
	updated := false
	ctx := filesystemHookContext{Context: context.Background(), hook: func() {
		if updated {
			return
		}
		data, err := os.ReadFile(dst)
		if err == nil && string(data) == "old data" {
			updated = true
			putFile(t, src, "new data")
		}
	}}
	err := moveWithRename(ctx, src, dst, func(string, string) error { return unix.EXDEV })
	if !updated {
		t.Fatal("did not exercise the source update")
	}
	if err == nil {
		t.Fatal("move reported complete despite an updated source file")
	}
	if got := fileContents(t, src); got != "new data" {
		t.Fatalf("updated source lost: %q", got)
	}
}

func TestExtractRetainsIndexedArchiveWhenPathIsReplaced(t *testing.T) {
	original := makeArchive(t, "zip", []testArchiveItem{{name: "file", data: "expected"}})
	replacement := makeArchive(t, "zip", nil)
	a, err := OpenArchive(original)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "extract")
	swapped := false
	ctx := filesystemHookContext{Context: context.Background(), hook: func() {
		if swapped {
			return
		}
		if _, err := os.Stat(dest); err == nil {
			swapped = true
			if err := os.Rename(replacement, original); err != nil {
				t.Fatal(err)
			}
		}
	}}
	err = a.Extract(ctx, nil, dest)
	if !swapped {
		t.Fatal("did not exercise archive replacement")
	}
	if err == nil {
		if got := fileContents(t, filepath.Join(dest, "file")); got != "expected" {
			t.Fatalf("successful extraction omitted indexed contents: %q", got)
		}
	}
}

func TestArchiveRejectsInvalidGzipChecksum(t *testing.T) {
	base := t.TempDir()
	src, name := filepath.Join(base, "file"), filepath.Join(base, "corrupt.tgz")
	putFile(t, src, "payload")
	if err := CreateArchive(context.Background(), "tgz", []string{src}, name); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	compressed[len(compressed)-8] ^= 0xff
	if err := os.WriteFile(name, compressed, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := OpenArchive(name)
	if err == nil && a.Extract(context.Background(), nil, filepath.Join(base, "extracted")) == nil {
		t.Fatal("archive with invalid gzip checksum was accepted")
	}
}

func TestCrossDeviceMoveSupportsHardlinkedSourceFiles(t *testing.T) {
	base := t.TempDir()
	src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
	putFile(t, filepath.Join(src, "a"), "shared inode")
	if err := os.Link(filepath.Join(src, "a"), filepath.Join(src, "b")); err != nil {
		t.Fatal(err)
	}
	if err := moveWithRename(context.Background(), src, dst, func(string, string) error { return unix.EXDEV }); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if got := fileContents(t, filepath.Join(dst, name)); got != "shared inode" {
			t.Fatalf("moved hardlink contents = %q", got)
		}
	}
	if _, err := os.Lstat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("completed move left original: %v", err)
	}
}

func TestExtractRejectsArchiveRewrittenInPlace(t *testing.T) {
	original := makeArchive(t, "zip", []testArchiveItem{{name: "file", data: "expected"}})
	empty, err := os.ReadFile(makeArchive(t, "zip", nil))
	if err != nil {
		t.Fatal(err)
	}
	a, err := OpenArchive(original)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "extract")
	rewritten := false
	ctx := filesystemHookContext{Context: context.Background(), hook: func() {
		if rewritten {
			return
		}
		if _, err := os.Stat(dest); err == nil {
			rewritten = true
			if err := os.WriteFile(original, empty, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}}
	if err := a.Extract(ctx, nil, dest); err == nil {
		t.Fatal("rewritten archive reported complete despite missing selected file")
	}
	if !rewritten {
		t.Fatal("did not exercise archive rewriting")
	}
}

func TestCopyAndCreateRejectSourceReplacedByFIFO(t *testing.T) {
	for _, operation := range []struct {
		name string
		fn   func(context.Context, string, string) error
	}{
		{"copy", Copy},
		{"archive", func(ctx context.Context, src, dst string) error {
			return CreateArchive(ctx, "zip", []string{src}, dst)
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			base := t.TempDir()
			src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
			putFile(t, src, "regular file")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			var swapErr error
			ready := make(chan struct{})
			hooked := filesystemHookContext{Context: ctx, hook: func() {
				calls++
				if calls == 2 {
					swapErr = os.Remove(src)
					if swapErr == nil {
						swapErr = unix.Mkfifo(src, 0600)
					}
					close(ready)
				}
			}}
			done := make(chan error, 1)
			go func() { done <- operation.fn(hooked, src, dst) }()
			<-ready
			select {
			case err := <-done:
				if swapErr != nil {
					t.Fatal(swapErr)
				}
				if err == nil {
					t.Fatal("replacement FIFO accepted")
				}
			case <-time.After(time.Second):
				cancel()
				// Release a broken blocking implementation before failing the test.
				fd, err := unix.Open(src, unix.O_WRONLY|unix.O_NONBLOCK, 0)
				if err == nil {
					_ = unix.Close(fd)
				}
				<-done
				t.Fatal("operation blocked opening a replaced FIFO")
			}
		})
	}
}

func TestArchiveInputsRejectFIFOsWithoutBlocking(t *testing.T) {
	for _, operation := range []string{"open", "read", "extract"} {
		t.Run(operation, func(t *testing.T) {
			name := makeArchive(t, "zip", []testArchiveItem{{name: "file", data: "original"}})
			a, err := OpenArchive(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(name, 0600); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "extract")
			done := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "open":
					_, err = OpenArchive(name)
				case "read":
					_, err = a.Read("file", 100)
				case "extract":
					err = a.Extract(context.Background(), nil, dest)
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("FIFO archive input accepted")
				}
			case <-time.After(time.Second):
				// Restore a regular path before releasing a blocked opener so a
				// broken implementation cannot block a second time on the FIFO.
				blocked := name + ".blocked"
				_ = os.Rename(name, blocked)
				_ = os.WriteFile(name, []byte("not an archive"), 0600)
				fd, err := unix.Open(blocked, unix.O_WRONLY|unix.O_NONBLOCK, 0)
				if err == nil {
					_ = unix.Close(fd)
				}
				<-done
				t.Fatal("archive API blocked on a FIFO")
			}
		})
	}
}
