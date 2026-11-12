package files

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateArchiveRejectsSourceTruncatedAfterOpen(t *testing.T) {
	for _, format := range []string{"zip", "tar", "tgz"} {
		t.Run(format, func(t *testing.T) {
			base := t.TempDir()
			src, dst := filepath.Join(base, "source"), filepath.Join(base, "archive."+format)
			putFile(t, src, "original payload")
			calls := 0
			changed := false
			ctx := filesystemHookContext{Context: context.Background(), hook: func() {
				calls++
				// CreateArchive and addArchiveEntry check cancellation before
				// opening the source. The third check starts its first read.
				if calls == 3 {
					if err := os.Truncate(src, 0); err != nil {
						t.Fatal(err)
					}
					changed = true
				}
			}}
			err := CreateArchive(ctx, format, []string{src}, dst)
			if !changed {
				t.Fatal("did not exercise source truncation")
			}
			if err == nil || !strings.Contains(err.Error(), "source changed while creating archive") {
				t.Errorf("archive did not report source truncation: %v", err)
			}
			if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("incomplete archive remains: %v", err)
			}
			if got := fileContents(t, src); got != "" {
				t.Fatalf("truncated source was modified: %q", got)
			}
		})
	}
}

func TestCreateArchiveRejectsSourceRewrittenDuringRead(t *testing.T) {
	for _, format := range []string{"zip", "tar", "tgz"} {
		t.Run(format, func(t *testing.T) {
			base := t.TempDir()
			src, dst := filepath.Join(base, "source"), filepath.Join(base, "archive."+format)
			initial := bytes.Repeat([]byte("A"), 256*1024)
			replacement := bytes.Repeat([]byte("B"), len(initial))
			if err := os.WriteFile(src, initial, 0600); err != nil {
				t.Fatal(err)
			}
			stamp := time.Unix(1600000000, 0)
			if err := os.Chtimes(src, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			calls := 0
			changed := false
			ctx := filesystemHookContext{Context: context.Background(), hook: func() {
				calls++
				// The fourth check starts the second 128 KiB read, so the
				// archive has already received data from the original file.
				if calls == 4 {
					if err := os.WriteFile(src, replacement, 0600); err != nil {
						t.Fatal(err)
					}
					changed = true
				}
			}}
			err := CreateArchive(ctx, format, []string{src}, dst)
			if !changed {
				t.Fatal("did not exercise source rewrite")
			}
			if err == nil || !strings.Contains(err.Error(), "source changed while creating archive") {
				t.Errorf("archive did not report source rewrite: %v", err)
			}
			if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("incomplete archive remains: %v", err)
			}
			got, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, replacement) {
				t.Fatal("rewritten source was modified")
			}
		})
	}
}
