package files

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// O_SYMLINK opens the link itself; combining it with O_NOFOLLOW would reject
// links needed by move cleanup. O_NONBLOCK prevents FIFO substitutions hanging.
// Darwin has no public O_PATH equivalent. These descriptors require read access,
// so chmod of unreadable entries fails closed instead of changing a pathname
// that another process could replace between validation and chmod.
const pinnedOpenFlags = unix.O_RDONLY | unix.O_SYMLINK | unix.O_NONBLOCK
const ancestorOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW

func rootFromFile(f *os.File) (*os.Root, error) {
	// Darwin fdesc_open/dupfdopen duplicates the exact descriptor. The devfs
	// entry is not a traversable directory: never append child names here.
	root, err := os.OpenRoot("/dev/fd/" + strconv.FormatUint(uint64(f.Fd()), 10))
	runtime.KeepAlive(f)
	return root, err
}

func chmodFile(f *os.File, mode os.FileMode) error {
	return f.Chmod(mode)
}

func setFileTimes(f *os.File, stamp time.Time) error {
	// Futimes truncates nanoseconds. Darwin's descriptor-based fsetattrlist
	// preserves them without resolving the file's potentially replaced name.
	attrs := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Commonattr:  unix.ATTR_CMN_MODTIME | unix.ATTR_CMN_ACCTIME,
	}
	// Attributes are packed in bitmap order: modification time, access time.
	ts := unix.Timespec{Sec: stamp.Unix(), Nsec: int64(stamp.Nanosecond())}
	times := [2]unix.Timespec{ts, ts}
	_, _, errno := unix.Syscall6(unix.SYS_FSETATTRLIST, f.Fd(), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&times)), unsafe.Sizeof(times), 0, 0)
	runtime.KeepAlive(f)
	if errno != 0 {
		return &os.PathError{Op: "fsetattrlist", Path: f.Name(), Err: errno}
	}
	return nil
}

func renameNoReplace(src, dst string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_EXCL)
}

func sameChangeTime(before, after *syscall.Stat_t) bool {
	return before.Ctimespec == after.Ctimespec
}

// Mounts reads the native mount table without invoking an external command.
func Mounts() ([]string, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	for attempts, size := 0, n+16; attempts < 4; attempts, size = attempts+1, size*2 {
		stats := make([]unix.Statfs_t, size)
		n, err = unix.Getfsstat(stats, unix.MNT_NOWAIT)
		if err != nil {
			return nil, err
		}
		if n >= len(stats) {
			continue // Mounts were added after the count; retry with headroom.
		}
		seen := map[string]bool{}
		mounts := make([]string, 0, n)
		for _, stat := range stats[:n] {
			name := unix.ByteSliceToString(stat.Mntonname[:])
			if name != "" && !seen[name] {
				seen[name] = true
				mounts = append(mounts, name)
			}
		}
		sort.Strings(mounts)
		return mounts, nil
	}
	return nil, fmt.Errorf("mount table kept growing during enumeration")
}
