package files

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Chmod changes the opened inode, refusing a symlink substituted at the path.
// O_PATH also permits changing an owned file whose current mode is 000.
func Chmod(path string, mode os.FileMode) error {
	f, err := os.OpenFile(path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return chmodOpened(f, mode)
}

func chmodOpened(f *os.File, mode os.FileMode) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to change permissions through a symlink: %s", f.Name())
	}
	bits := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		bits |= unix.S_ISUID
	}
	if mode&os.ModeSetgid != 0 {
		bits |= unix.S_ISGID
	}
	if mode&os.ModeSticky != 0 {
		bits |= unix.S_ISVTX
	}
	err = unix.Fchmodat(int(f.Fd()), "", bits, unix.AT_EMPTY_PATH)
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		// Older kernels lack fchmodat2/AT_EMPTY_PATH. This procfs link refers to
		// the held descriptor, so replacing the original path cannot redirect it.
		return os.Chmod(fdPath(f), mode)
	}
	return err
}
