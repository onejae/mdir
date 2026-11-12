package files

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestChmodUnreadableFileAndDirectory(t *testing.T) {
	base := t.TempDir()
	file, dir := filepath.Join(base, "file"), filepath.Join(base, "dir")
	putFile(t, file, "owned file")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, dir} {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if err := Chmod(dir, 0750); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]os.FileMode{file: 0644, dir: 0750} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != expected {
			t.Fatalf("%s permissions = %v, error = %v", path, info, err)
		}
	}
}

func TestChmodRejectsSymlink(t *testing.T) {
	base := t.TempDir()
	target, link := filepath.Join(base, "target"), filepath.Join(base, "link")
	putFile(t, target, "keep permissions")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Chmod(link, 0777); err == nil {
		t.Fatal("changed permissions through a symlink")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("symlink target permissions changed: %v, %v", info, err)
	}
}

func TestChmodRemainsOnOpenedInodeAfterReplacement(t *testing.T) {
	base := t.TempDir()
	original, moved, target := filepath.Join(base, "original"), filepath.Join(base, "moved"), filepath.Join(base, "target")
	putFile(t, original, "original inode")
	putFile(t, target, "must remain unchanged")
	f, err := os.OpenFile(original, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, original); err != nil {
		t.Fatal(err)
	}
	if err := chmodOpened(f, 0600); err != nil {
		t.Fatal(err)
	}
	for path, expected := range map[string]os.FileMode{moved: 0600, target: 0640} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != expected {
			t.Fatalf("%s permissions = %v, error = %v", path, info, err)
		}
	}
}
