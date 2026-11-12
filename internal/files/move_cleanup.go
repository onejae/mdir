package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// A cross-device move deletes only entries actually copied. Re-enumerating the
// source during cleanup would also delete children created after copying.
type copiedEntry struct {
	name              string
	info              fs.FileInfo
	destination       fs.FileInfo
	destinationParent fs.FileInfo
	children          []*copiedEntry
}

func verifyWrittenFile(ctx context.Context, f *os.File, expected []byte) (fs.FileInfo, error) {
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	actual := sha256.New()
	if err := copyData(ctx, actual, f); err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !sameFileVersion(before, after) || !bytes.Equal(expected, actual.Sum(nil)) {
		return nil, fmt.Errorf("destination changed while verifying copy: %s", f.Name())
	}
	return after, nil
}

func checkCopiedDestination(parent *os.Root, name string, expected fs.FileInfo) error {
	current, err := parent.Lstat(name)
	if err != nil {
		return fmt.Errorf("destination changed after copy: %s: %w", name, err)
	}
	if !sameFileVersion(expected, current) {
		return fmt.Errorf("destination changed after copy: %s", name)
	}
	return nil
}

func recordCopiedLink(parent *os.Root, name, target string, copied *copiedEntry) error {
	before, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	dir, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	actual, err := os.Readlink(filepath.Join(fdPath(dir), name))
	if err != nil {
		return err
	}
	if actual != target {
		return fmt.Errorf("destination link changed during copy: %s", name)
	}
	if err := checkCopiedDestination(parent, name, before); err != nil {
		return err
	}
	copied.destination = before
	return nil
}

func sameFileVersion(before, after fs.FileInfo) bool {
	if !sameFileAttributes(before, after) {
		return false
	}
	// Reading may update atime, which is deliberately excluded. ctime catches
	// same-size rewrites even if the writer restores the old modification time.
	oldStat, oldOK := before.Sys().(*syscall.Stat_t)
	newStat, newOK := after.Sys().(*syscall.Stat_t)
	return oldOK && newOK && oldStat.Ctim == newStat.Ctim
}

func sameFileAttributes(before, after fs.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) && before.Mode() == after.Mode() && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

type fileIdentity struct{ device, inode uint64 }
type copiedVersion struct{ original, current fs.FileInfo }

type copiedDestination struct {
	parent      *os.Root
	name        string
	info        fs.FileInfo
	checkParent func() error
}

func (d copiedDestination) check() error {
	if err := d.checkParent(); err != nil {
		return err
	}
	return checkCopiedDestination(d.parent, d.name, d.info)
}

func removeCopiedEntry(ctx context.Context, parent *os.Root, name string, copied *copiedEntry, dst string) error {
	destinationParent, err := os.OpenRoot(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer destinationParent.Close()
	actual, err := destinationParent.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(copied.destinationParent, actual) {
		return fmt.Errorf("destination parent changed after copy: %s", dst)
	}
	destination := copiedDestination{destinationParent, filepath.Base(dst), copied.destination, func() error {
		// A held root may survive being renamed. Check that the user's current
		// destination path still reaches the original parent as well.
		current, err := os.Stat(filepath.Dir(dst))
		if err != nil {
			return err
		}
		if !os.SameFile(copied.destinationParent, current) {
			return fmt.Errorf("destination parent changed after copy: %s", dst)
		}
		return nil
	}}
	return removeCopiedTree(ctx, parent, name, copied, map[fileIdentity]copiedVersion{}, destination)
}

func removeCopiedTree(ctx context.Context, parent *os.Root, name string, copied *copiedEntry, versions map[fileIdentity]copiedVersion, destination copiedDestination) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := destination.check(); err != nil {
		return err
	}
	current, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if !os.SameFile(copied.info, current) {
		return fmt.Errorf("source changed after copy: %s", name)
	}
	if !copied.info.IsDir() {
		return removeCopiedLeaf(parent, name, copied.info, current, versions, destination.check)
	}
	f, err := openChildDirectory(parent, name)
	if err != nil {
		return err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(copied.info, actual) {
		return fmt.Errorf("source directory changed after copy: %s", name)
	}
	dir, err := os.OpenRoot(fdPath(f))
	if err != nil {
		return err
	}
	defer dir.Close()
	destFile, err := openChildDirectory(destination.parent, destination.name)
	if err != nil {
		return err
	}
	destinationInfo, err := destFile.Stat()
	if err != nil {
		destFile.Close()
		return err
	}
	if !sameFileVersion(copied.destination, destinationInfo) {
		destFile.Close()
		return fmt.Errorf("destination directory changed after copy: %s", destination.name)
	}
	dest, err := os.OpenRoot(fdPath(destFile))
	destFile.Close()
	if err != nil {
		return err
	}
	defer dest.Close()
	for _, child := range copied.children {
		childDestination := copiedDestination{dest, child.name, child.destination, destination.check}
		if err := removeCopiedTree(ctx, dir, child.name, child, versions, childDestination); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := destination.check(); err != nil {
		return err
	}
	current, err = parent.Lstat(name)
	if err != nil {
		return err
	}
	if !os.SameFile(copied.info, current) {
		return fmt.Errorf("source directory changed after copy: %s", name)
	}
	// Remove on a directory is rmdir: it fails if an unrecorded child exists.
	// Directory timestamps necessarily change as our recorded children leave.
	return parent.Remove(name)
}

func removeCopiedLeaf(parent *os.Root, name string, copied, current fs.FileInfo, versions map[fileIdentity]copiedVersion, checkDestination func() error) error {
	stat := copied.Sys().(*syscall.Stat_t)
	id := fileIdentity{uint64(stat.Dev), stat.Ino}
	expected := copied
	if previous, ok := versions[id]; ok {
		if !sameFileVersion(copied, previous.original) {
			return fmt.Errorf("hardlinked source changed between copies: %s", name)
		}
		expected = previous.current
	}
	if !sameFileVersion(expected, current) {
		return fmt.Errorf("source changed after copy: %s", name)
	}
	parentFile, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer parentFile.Close()
	fd, err := unix.Openat(int(parentFile.Fd()), name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return err
	}
	if !sameFileVersion(expected, before) {
		return fmt.Errorf("source changed before removal: %s", name)
	}
	if err := checkDestination(); err != nil {
		return err
	}
	if err := unix.Unlinkat(int(parentFile.Fd()), name, 0); err != nil {
		return err
	}
	after, err := f.Stat()
	if err != nil {
		return err
	}
	beforeStat := before.Sys().(*syscall.Stat_t)
	afterStat := after.Sys().(*syscall.Stat_t)
	if !sameFileAttributes(before, after) || beforeStat.Nlink != afterStat.Nlink+1 {
		return fmt.Errorf("source inode changed during removal: %s", name)
	}
	if afterStat.Nlink > 0 {
		// Our own unlink changes ctime for every other hardlink to this inode.
		// Keep that expected change separate from the original copied version.
		versions[id] = copiedVersion{original: copied, current: after}
	}
	return nil
}
