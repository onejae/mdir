// Package files implements the filesystem operations used by the browser.
package files

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Entry struct {
	Name, Path    string
	Size          int64
	Mode          fs.FileMode
	ModTime       time.Time
	IsDir, IsLink bool
}

func entryFor(name, path string, info fs.FileInfo) Entry {
	return Entry{Name: name, Path: path, Size: info.Size(), Mode: info.Mode(), ModTime: info.ModTime(), IsDir: info.IsDir(), IsLink: info.Mode()&os.ModeSymlink != 0}
}

// List returns a directory's children, with directories before files.
func List(dir string, hidden bool, sortBy string, reverse bool) ([]Entry, error) {
	children, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(children))
	for _, child := range children {
		if !hidden && strings.HasPrefix(child.Name(), ".") {
			continue
		}
		info, err := child.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		entry := entryFor(child.Name(), filepath.Join(dir, child.Name()), info)
		if entry.IsLink {
			if target, err := os.Stat(entry.Path); err == nil {
				entry.IsDir = target.IsDir()
			}
		}
		entries = append(entries, entry)
	}
	sortEntries(entries, sortBy, reverse)
	return entries, nil
}

func sortEntries(entries []Entry, sortBy string, reverse bool) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		cmp := 0
		switch sortBy {
		case "size":
			if a.Size < b.Size {
				cmp = -1
			} else if a.Size > b.Size {
				cmp = 1
			}
		case "time":
			cmp = a.ModTime.Compare(b.ModTime)
		case "ext":
			cmp = strings.Compare(strings.ToLower(filepath.Ext(a.Name)), strings.ToLower(filepath.Ext(b.Name)))
		}
		if cmp == 0 {
			cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		if cmp == 0 {
			cmp = strings.Compare(a.Name, b.Name)
		}
		if reverse {
			return cmp > 0
		}
		return cmp < 0
	})
}

// Sort applies the browser's ordering in place, always keeping directories first.
func Sort(entries []Entry, sortBy string, reverse bool) {
	sortEntries(entries, sortBy, reverse)
}

// Search matches a glob or a case-insensitive substring, without walking links.
// A positive limit stops the search after that many matches; zero is unlimited.
func Search(ctx context.Context, root, pattern string, limit int) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pattern = strings.ToLower(pattern)
	glob := strings.ContainsAny(pattern, "*?[")
	if glob {
		if _, err := filepath.Match(pattern, ""); err != nil {
			return nil, err
		}
	}
	entries := []Entry{}
	var problems []error
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			problems = append(problems, walkErr)
			return nil
		}
		if path == root {
			return nil
		}
		name := strings.ToLower(d.Name())
		match := strings.Contains(name, pattern)
		if glob {
			match, _ = filepath.Match(pattern, name)
		}
		if !match {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			problems = append(problems, err)
			return nil
		}
		entry := entryFor(d.Name(), path, info)
		if entry.IsLink {
			if target, err := os.Stat(path); err == nil {
				entry.IsDir = target.IsDir()
			}
		}
		entries = append(entries, entry)
		if limit > 0 && len(entries) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	return entries, errors.Join(append(problems, err)...)
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

func parseMounts(scanner *bufio.Scanner) ([]string, error) {
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	seen := map[string]bool{}
	var mounts []string
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 {
			return nil, fmt.Errorf("malformed mountinfo line")
		}
		mount := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(fields[4])
		if !seen[mount] {
			seen[mount] = true
			mounts = append(mounts, mount)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Strings(mounts)
	return mounts, nil
}
