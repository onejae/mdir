package files

import (
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// A selected source starts with a path. Every child is resolved relative to
// its held parent, so replacing a directory name cannot redirect traversal.
type sourceEntry struct {
	parent *os.Root
	name   string
}

func (s sourceEntry) lstat() (fs.FileInfo, error) {
	if s.parent == nil {
		return os.Lstat(s.name)
	}
	return s.parent.Lstat(s.name)
}

func (s sourceEntry) readlink() (string, error) {
	if s.parent == nil {
		return os.Readlink(s.name)
	}
	return s.parent.Readlink(s.name)
}

func (s sourceEntry) open(directory bool) (*os.File, error) {
	if s.parent == nil {
		return openNoFollow(s.name, directory)
	}
	parent, err := s.parent.Open(".")
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	return openAt(parent, s.name, flags)
}

func openAt(parent *os.File, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, flags|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}
