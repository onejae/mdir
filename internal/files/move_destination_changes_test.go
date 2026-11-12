package files

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestCrossDeviceMovePreservesSourceWhenDestinationChanges(t *testing.T) {
	for _, directory := range []bool{false, true} {
		kind := "file"
		if directory {
			kind = "directory"
		}
		for _, change := range []string{"removed", "replaced", "rewritten"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				base := t.TempDir()
				src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
				sourceFile, destinationFile := src, dst
				if directory {
					sourceFile, destinationFile = filepath.Join(src, "file"), filepath.Join(dst, "file")
				}
				putFile(t, sourceFile, "only copy")
				changed := false
				ctx := filesystemHookContext{Context: context.Background(), hook: func() {
					if changed {
						return
					}
					data, err := os.ReadFile(destinationFile)
					if err != nil || string(data) != "only copy" {
						return
					}
					changed = true
					switch change {
					case "removed":
						if err := os.Remove(destinationFile); err != nil {
							t.Fatal(err)
						}
					case "replaced":
						if err := os.Rename(destinationFile, destinationFile+".parked"); err != nil {
							t.Fatal(err)
						}
						putFile(t, destinationFile, "replacement")
					case "rewritten":
						putFile(t, destinationFile, "bad data!")
					}
				}}
				err := moveWithRename(ctx, src, dst, func(string, string) error { return unix.EXDEV })
				if !changed {
					t.Fatal("did not exercise destination change")
				}
				if err == nil {
					t.Error("move reported success after destination changed")
				}
				if got := fileContents(t, sourceFile); got != "only copy" {
					t.Fatalf("intact source was not preserved: %q", got)
				}
				switch change {
				case "removed":
					if _, err := os.Lstat(destinationFile); !errors.Is(err, fs.ErrNotExist) {
						t.Fatalf("removed destination was recreated: %v", err)
					}
				case "replaced":
					if got := fileContents(t, destinationFile); got != "replacement" {
						t.Fatalf("replacement was changed: %q", got)
					}
				case "rewritten":
					if got := fileContents(t, destinationFile); got != "bad data!" {
						t.Fatalf("external destination edit was changed: %q", got)
					}
				}
			})
		}
	}
}

func TestCrossDeviceMovePreservesSourceSymlinkWhenDestinationChanges(t *testing.T) {
	for _, change := range []string{"removed", "replaced"} {
		t.Run(change, func(t *testing.T) {
			base := t.TempDir()
			src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
			if err := os.Symlink("original-target", src); err != nil {
				t.Fatal(err)
			}
			changed := false
			ctx := filesystemHookContext{Context: context.Background(), hook: func() {
				if changed {
					return
				}
				if target, err := os.Readlink(dst); err != nil || target != "original-target" {
					return
				}
				changed = true
				if err := os.Remove(dst); err != nil {
					t.Fatal(err)
				}
				if change == "replaced" {
					if err := os.Symlink("replacement-target", dst); err != nil {
						t.Fatal(err)
					}
				}
			}}
			if err := moveWithRename(ctx, src, dst, func(string, string) error { return unix.EXDEV }); err == nil {
				t.Error("move reported success after destination symlink changed")
			}
			if !changed {
				t.Fatal("did not exercise destination symlink change")
			}
			if target, err := os.Readlink(src); err != nil || target != "original-target" {
				t.Fatalf("source symlink was not preserved: %q, %v", target, err)
			}
		})
	}
}

func TestCrossDeviceMoveRechecksDestinationDuringSourceCleanup(t *testing.T) {
	for _, change := range []string{"file", "ancestor", "parent"} {
		t.Run(change, func(t *testing.T) {
			base := t.TempDir()
			destinationParent := filepath.Join(base, "destination-parent")
			if err := os.Mkdir(destinationParent, 0700); err != nil {
				t.Fatal(err)
			}
			src, dst := filepath.Join(base, "src"), filepath.Join(destinationParent, "dst")
			putFile(t, filepath.Join(src, "a"), "first")
			putFile(t, filepath.Join(src, "b"), "second")
			changed := false
			remaining := ""
			ctx := filesystemHookContext{Context: context.Background(), hook: func() {
				if changed {
					return
				}
				_, aErr := os.Lstat(filepath.Join(src, "a"))
				_, bErr := os.Lstat(filepath.Join(src, "b"))
				// File.ReadDir uses filesystem order, so either entry may finish first.
				switch {
				case errors.Is(aErr, fs.ErrNotExist) && bErr == nil:
					remaining = "b"
				case errors.Is(bErr, fs.ErrNotExist) && aErr == nil:
					remaining = "a"
				default:
					return
				}
				changed = true
				if change == "ancestor" {
					if err := os.Rename(dst, dst+".parked"); err != nil {
						t.Fatal(err)
					}
				} else if change == "parent" {
					if err := os.Rename(destinationParent, destinationParent+".parked"); err != nil {
						t.Fatal(err)
					}
				}
				putFile(t, filepath.Join(dst, remaining), "external edit")
			}}
			if err := moveWithRename(ctx, src, dst, func(string, string) error { return unix.EXDEV }); err == nil {
				t.Error("move reported success after destination changed during cleanup")
			}
			if !changed {
				t.Fatal("did not exercise a change after the first source was removed")
			}
			if got := fileContents(t, filepath.Join(src, remaining)); got != map[string]string{"a": "first", "b": "second"}[remaining] {
				t.Fatalf("remaining source was not preserved: %q", got)
			}
			if got := fileContents(t, filepath.Join(dst, remaining)); got != "external edit" {
				t.Fatalf("external destination edit was changed: %q", got)
			}
		})
	}
}

func TestCrossDeviceMovePreservesSourceWhenDestinationChangesAfterMetadata(t *testing.T) {
	base := t.TempDir()
	src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
	putFile(t, src, "only copy")
	stamp := time.Unix(946684800, 0)
	if err := os.Chtimes(src, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	changed := false
	ctx := filesystemHookContext{Context: context.Background(), hook: func() {
		if changed {
			return
		}
		info, err := os.Stat(dst)
		if err == nil && info.ModTime().Equal(stamp) {
			changed = true
			putFile(t, dst, "bad data!")
		}
	}}
	if err := moveWithRename(ctx, src, dst, func(string, string) error { return unix.EXDEV }); err == nil {
		t.Fatal("move reported success after completed destination changed")
	}
	if !changed {
		t.Fatal("did not exercise a change after applying destination metadata")
	}
	if got := fileContents(t, src); got != "only copy" {
		t.Fatalf("source was changed: %q", got)
	}
	if got := fileContents(t, dst); got != "bad data!" {
		t.Fatalf("external destination edit was changed: %q", got)
	}
}

func TestCrossDeviceMoveKeepsCompletedOutputWhenVerificationCanceled(t *testing.T) {
	base := t.TempDir()
	src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
	putFile(t, src, "only copy")
	stamp := time.Unix(946684800, 0)
	if err := os.Chtimes(src, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	background, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceled := false
	ctx := filesystemHookContext{Context: background, hook: func() {
		if canceled {
			return
		}
		info, err := os.Stat(dst)
		if err == nil && info.ModTime().Equal(stamp) {
			canceled = true
			cancel()
		}
	}}
	if err := moveWithRename(ctx, src, dst, func(string, string) error { return unix.EXDEV }); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if !canceled {
		t.Fatal("did not exercise cancellation after applying destination metadata")
	}
	for _, path := range []string{src, dst} {
		if got := fileContents(t, path); got != "only copy" {
			t.Fatalf("completed data changed at %s: %q", path, got)
		}
	}
}

func TestMoveVerificationKeepsOpenReadAccess(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := writeExclusiveFile(context.Background(), root, "output", strings.NewReader("payload"), 0000, time.Time{}, &copiedEntry{}); err != nil {
		t.Fatalf("verification could not read through the original descriptor: %v", err)
	}
	path := filepath.Join(dir, "output")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0000 {
		t.Fatalf("verification changed the requested permissions: %v", info.Mode())
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if got := fileContents(t, path); got != "payload" {
		t.Fatalf("verified output changed: %q", got)
	}
}

func TestCrossDeviceMoveEmptyDirectoryWithoutSearchPermission(t *testing.T) {
	base := t.TempDir()
	src, dst := filepath.Join(base, "src"), filepath.Join(base, "dst")
	if err := os.Mkdir(src, 0400); err != nil {
		t.Fatal(err)
	}
	if err := moveWithRename(context.Background(), src, dst, func(string, string) error { return unix.EXDEV }); err != nil {
		t.Fatalf("move failed for an unchanged readable empty directory: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0400 {
		t.Fatalf("destination permissions changed: %v", info.Mode())
	}
	if _, err := os.Lstat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("completed move left source: %v", err)
	}
}
