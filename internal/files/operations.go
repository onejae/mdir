package files

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func copyData(ctx context.Context, dst io.Writer, src io.Reader) error {
	_, err := io.CopyBuffer(dst, contextReader{ctx, src}, make([]byte, 128*1024))
	if err != nil {
		return err
	}
	return ctx.Err()
}

// Copy creates dst as the exact destination path, never replacing any entry.
// On error, completed children may remain; an incomplete regular file is removed.
func Copy(ctx context.Context, src, dst string) error {
	return copyWithManifest(ctx, src, dst, nil)
}

func copyWithManifest(ctx context.Context, src, dst string, manifest *copiedEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if err := checkDestination(src, dst, info); err != nil {
		return err
	}
	parent, err := os.OpenRoot(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer parent.Close()
	if manifest != nil {
		manifest.destinationParent, err = parent.Stat(".")
		if err != nil {
			return err
		}
	}
	if err := checkRootDestination(src, filepath.Base(dst), info, parent); err != nil {
		return err
	}
	return copyEntry(ctx, sourceEntry{name: src}, parent, filepath.Base(dst), info, manifest)
}

func checkRootDestination(src, name string, info fs.FileInfo, parent *os.Root) error {
	if !info.IsDir() {
		return nil
	}
	f, err := parent.Open(".")
	if err != nil {
		return err
	}
	// Walk the actual destination ancestry. Its displayed path may have been
	// renamed or replaced since validation, so lexical paths cannot prove that
	// the opened destination is outside the source tree.
	defer func() { f.Close() }()
	seen := map[fileIdentity]bool{}
	for {
		current, err := f.Stat()
		if err != nil {
			return err
		}
		if os.SameFile(info, current) {
			return fmt.Errorf("destination is inside source: %s/%s", src, name)
		}
		id := identityOf(current)
		if seen[id] {
			return fmt.Errorf("destination ancestry changed during validation")
		}
		seen[id] = true
		ancestor, err := openAt(f, "..", ancestorOpenFlags)
		if err != nil {
			return err
		}
		above, err := ancestor.Stat()
		if err != nil {
			ancestor.Close()
			return err
		}
		if os.SameFile(current, above) {
			ancestor.Close()
			return nil
		}
		f.Close()
		f = ancestor
	}
}

// checkDestination resolves existing ancestors before checking self-copy.
func checkDestination(src, dst string, info fs.FileInfo) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("destination exists: %s: %w", dst, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	source, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dst))
	if err != nil {
		return err
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return err
	}
	target := filepath.Join(parent, filepath.Base(dst))
	rel, err := filepath.Rel(source, target)
	if err != nil {
		return err
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return fmt.Errorf("destination is inside source: %s", dst)
	}
	return nil
}

func openNoFollow(path string, directory bool) (*os.File, error) {
	// A regular file can be replaced by a FIFO between lstat and open. Opening
	// nonblocking lets the subsequent fstat reject it instead of hanging here.
	flags := os.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	return os.OpenFile(path, flags, 0)
}

func copyEntry(ctx context.Context, src sourceEntry, parent *os.Root, name string, info fs.FileInfo, manifest *copiedEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if manifest != nil {
		manifest.name, manifest.info = name, info
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := src.readlink()
		if err != nil {
			return err
		}
		current, err := src.lstat()
		if err != nil {
			return err
		}
		if !sameFileVersion(info, current) {
			return fmt.Errorf("source changed during copy: %s", src.name)
		}
		// The open parent is held throughout this call and the target itself is
		// copied verbatim, never followed.
		if err := createLink(parent, name, target, info.ModTime()); err != nil {
			return err
		}
		if manifest != nil {
			return recordCopiedLink(parent, name, target, manifest)
		}
		return nil
	case info.IsDir():
		source, err := src.open(true)
		if err != nil {
			return err
		}
		defer source.Close()
		actual, err := source.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(info, actual) {
			return fmt.Errorf("source changed during copy: %s", src.name)
		}
		if err := parent.Mkdir(name, 0700); err != nil {
			return err
		}
		dest, err := openChildRoot(parent, name)
		if err != nil {
			return err
		}
		defer dest.Close()
		children, err := source.ReadDir(-1)
		if err != nil {
			return err
		}
		sourceRoot, err := rootFromFile(source)
		if err != nil {
			return err
		}
		defer sourceRoot.Close()
		for _, child := range children {
			childSource := sourceEntry{parent: sourceRoot, name: child.Name()}
			childInfo, err := childSource.lstat()
			if err != nil {
				return err
			}
			// Access children through the held directory descriptor, so a renamed
			// source directory cannot redirect the traversal.
			var childManifest *copiedEntry
			if manifest != nil {
				childManifest = &copiedEntry{}
			}
			if err := copyEntry(ctx, childSource, dest, child.Name(), childInfo, childManifest); err != nil {
				return err
			}
			if manifest != nil {
				manifest.children = append(manifest.children, childManifest)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := dest.Open(".")
		if err != nil {
			return err
		}
		defer f.Close()
		if err := setMetadata(f, info.Mode(), info.ModTime()); err != nil {
			return err
		}
		if manifest != nil {
			manifest.destination, err = f.Stat()
			if err != nil {
				return err
			}
			return checkCopiedDestination(parent, name, manifest.destination)
		}
		return nil
	case info.Mode().IsRegular():
		source, err := src.open(false)
		if err != nil {
			return err
		}
		defer source.Close()
		actual, err := source.Stat()
		if err != nil {
			return err
		}
		if !actual.Mode().IsRegular() || !sameFileVersion(info, actual) {
			return fmt.Errorf("source changed during copy: %s", src.name)
		}
		if err := writeExclusiveFile(ctx, parent, name, source, info.Mode(), info.ModTime(), manifest); err != nil {
			return err
		}
		current, err := source.Stat()
		if err != nil {
			return err
		}
		if !sameFileVersion(actual, current) {
			return fmt.Errorf("source changed during copy: %s", src.name)
		}
		return nil
	default:
		return fmt.Errorf("unsupported file type: %s", src.name)
	}
}

// OpenRoot follows symlinks inside its tree. Recursive mutations instead open
// one child with O_NOFOLLOW, then anchor a new root to that descriptor.
func openChildRoot(parent *os.Root, name string) (*os.Root, error) {
	f, err := openChildDirectory(parent, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return rootFromFile(f)
}

func openChildDirectory(parent *os.Root, name string) (*os.File, error) {
	dir, err := parent.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return openAt(dir, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW)
}

func setMetadata(f *os.File, mode fs.FileMode, modTime time.Time) error {
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if modTime.IsZero() {
		return nil
	}
	return setFileTimes(f, modTime)
}

func createLink(parent *os.Root, name, target string, modTime time.Time) error {
	dir, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := unix.Symlinkat(target, int(dir.Fd()), name); err != nil {
		return err
	}
	times := []unix.Timespec{unix.NsecToTimespec(modTime.UnixNano()), unix.NsecToTimespec(modTime.UnixNano())}
	return unix.UtimesNanoAt(int(dir.Fd()), name, times, unix.AT_SYMLINK_NOFOLLOW)
}

func writeExclusive(ctx context.Context, root *os.Root, name string, src io.Reader, mode fs.FileMode, modTime time.Time) error {
	return writeExclusiveFile(ctx, root, name, src, mode, modTime, nil)
}

func writeExclusiveFile(ctx context.Context, root *os.Root, name string, src io.Reader, mode fs.FileMode, modTime time.Time, manifest *copiedEntry) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	access := os.O_WRONLY
	if manifest != nil {
		// Keep read access while checking the copy, even after final permissions
		// would prevent reopening the destination for reading.
		access = os.O_RDWR
	}
	f, err := root.OpenFile(name, access|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	created, statErr := f.Stat()
	if statErr != nil {
		f.Close()
		return statErr
	}
	complete := false
	defer func() {
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil && !complete {
			// Never remove a replacement created by another process.
			if current, statErr := root.Lstat(name); statErr == nil && os.SameFile(created, current) {
				_ = root.Remove(name)
			}
		}
	}()
	var output io.Writer = f
	var checksum hash.Hash
	if manifest != nil {
		checksum = sha256.New()
		output = io.MultiWriter(f, checksum)
	}
	if err = copyData(ctx, output, src); err != nil {
		return err
	}
	if err = setMetadata(f, mode, modTime); err != nil {
		return err
	}
	if manifest != nil {
		// A later integrity error must not delete an externally modified output
		// or discard a completed copy when verification is canceled.
		complete = true
		manifest.destination, err = verifyWrittenFile(ctx, f, checksum.Sum(nil))
		if err != nil {
			return err
		}
		return checkCopiedDestination(root, name, manifest.destination)
	}
	return nil
}

// Move atomically refuses an existing destination. Only a cross-device rename
// falls back to copying, and the original is removed only after a complete copy.
func Move(ctx context.Context, src, dst string) error {
	return moveWithRename(ctx, src, dst, renameNoReplace)
}

// The rename function is an argument so cross-device and racing-destination
// behavior can be exercised without requiring a second mounted filesystem.
func moveWithRename(ctx context.Context, src, dst string, rename func(string, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if err := checkDestination(src, dst, info); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = rename(src, dst)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.EXDEV) {
		return fmt.Errorf("move %s to %s: %w", src, dst, err)
	}
	manifest := &copiedEntry{}
	if err := copyWithManifest(ctx, src, dst, manifest); err != nil {
		return err
	}
	current, err := os.Lstat(src)
	if err != nil || !os.SameFile(info, current) {
		return fmt.Errorf("copy completed, but source changed before removal: %s", src)
	}
	parent, err := os.OpenRoot(filepath.Dir(src))
	if err != nil {
		return fmt.Errorf("copy completed, but original could not be opened for removal: %w", err)
	}
	defer parent.Close()
	if err := removeCopiedEntry(ctx, parent, filepath.Base(src), manifest, dst); err != nil {
		return fmt.Errorf("copy completed, but original could not be removed: %w", err)
	}
	return nil
}

// Remove never traverses symbolic links and refuses the filesystem root.
func Remove(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name := filepath.Base(path); name == "." || name == ".." {
		return fmt.Errorf("refusing to remove . or ..")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if abs == string(filepath.Separator) {
		return fmt.Errorf("refusing to remove filesystem root")
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if info.IsDir() {
		rootInfo, err := os.Stat(string(filepath.Separator))
		if err != nil {
			return err
		}
		if os.SameFile(info, rootInfo) {
			return fmt.Errorf("refusing to remove filesystem root")
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return err
		}
		if resolved == string(filepath.Separator) {
			return fmt.Errorf("refusing to remove filesystem root")
		}
	}
	parent, err := os.OpenRoot(filepath.Dir(abs))
	if err != nil {
		return err
	}
	defer parent.Close()
	return removeEntry(ctx, parent, filepath.Base(abs))
}

func removeEntry(ctx context.Context, parent *os.Root, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return parent.Remove(name)
	}
	// Opening with O_NOFOLLOW prevents a directory-to-link race from redirecting
	// a recursive deletion, including a link which stays inside the root.
	f, err := openChildDirectory(parent, name)
	if err != nil {
		return err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, actual) {
		return fmt.Errorf("directory changed during removal: %s", name)
	}
	dir, err := rootFromFile(f)
	if err != nil {
		return err
	}
	defer dir.Close()
	children, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := removeEntry(ctx, dir, child.Name()); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if !os.SameFile(info, current) {
		return fmt.Errorf("directory changed during removal: %s", name)
	}
	return parent.Remove(name)
}
