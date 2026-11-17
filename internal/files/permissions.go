package files

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Chmod changes the opened inode, refusing a symlink substituted at the path.
// Linux also permits changing an owned file whose current mode is 000.
func Chmod(path string, mode os.FileMode) error {
	f, err := openPinned(path)
	if err != nil {
		return fmt.Errorf("cannot safely open entry for permission change: %w", err)
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
	return chmodFile(f, mode)
}

func openPinned(path string) (*os.File, error) {
	return os.OpenFile(path, pinnedOpenFlags|unix.O_CLOEXEC, 0)
}

func openPinnedAt(parent *os.File, name string) (*os.File, error) {
	return openAt(parent, name, pinnedOpenFlags)
}
