package files

import (
	"bufio"
	"errors"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const pinnedOpenFlags = unix.O_PATH | unix.O_NOFOLLOW
const ancestorOpenFlags = unix.O_PATH | unix.O_DIRECTORY | unix.O_NOFOLLOW

func descriptorPath(f *os.File) string {
	return "/proc/self/fd/" + strconv.FormatUint(uint64(f.Fd()), 10)
}

func rootFromFile(f *os.File) (*os.Root, error) {
	root, err := os.OpenRoot(descriptorPath(f))
	runtime.KeepAlive(f)
	return root, err
}

func chmodFile(f *os.File, mode os.FileMode) error {
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
	err := unix.Fchmodat(int(f.Fd()), "", bits, unix.AT_EMPTY_PATH)
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		// Older kernels lack fchmodat2/AT_EMPTY_PATH. This procfs link refers to
		// the held descriptor, so replacing the original path cannot redirect it.
		err = os.Chmod(descriptorPath(f), mode)
	}
	runtime.KeepAlive(f)
	return err
}

func setFileTimes(f *os.File, stamp time.Time) error {
	err := os.Chtimes(descriptorPath(f), stamp, stamp)
	runtime.KeepAlive(f)
	return err
}

func renameNoReplace(src, dst string) error {
	return unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
}

func sameChangeTime(before, after *syscall.Stat_t) bool {
	return before.Ctim == after.Ctim
}

// Mounts reads the process's Linux mount table, including escaped mountpoints.
func Mounts() ([]string, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseMounts(bufio.NewScanner(f))
}
