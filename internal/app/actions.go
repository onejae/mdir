package app

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onejae/mdir/internal/files"
)

func (a *App) refresh() {
	if err := a.reload(a.current().Path); err != nil {
		a.message(err.Error(), true)
	} else {
		a.message("Refreshed", false)
	}
}

func (a *App) localOnly() bool {
	if a.archive != nil {
		a.message("Extract archive members before editing them", true)
		return false
	}
	return true
}

func (a *App) openCurrent() {
	e := a.current()
	if e.Name == ".." {
		a.parent()
		return
	}
	if a.archive != nil {
		if e.IsDir {
			a.archivePrefix, a.cursor, a.offset = strings.TrimSuffix(e.Path, "/")+"/", 0, 0
			a.selected = make(map[string]bool)
			a.refresh()
		} else {
			a.viewCurrent()
		}
		return
	}
	if e.IsDir {
		a.changeDir(e.Path)
		return
	}
	if files.IsArchive(e.Name) {
		archive, err := files.OpenArchive(e.Path)
		if err != nil {
			a.message(err.Error(), true)
			return
		}
		a.archive, a.archivePrefix, a.cursor, a.offset = archive, "", 0, 0
		a.selected = make(map[string]bool)
		if err := a.reload(""); err != nil {
			a.archive = nil
			a.message(err.Error(), true)
		}
		return
	}
	if e.Mode.IsRegular() && e.Mode.Perm()&0111 != 0 {
		a.openExternal()
		return
	}
	a.viewCurrent()
}

func (a *App) parent() {
	if a.archive != nil {
		if a.archivePrefix == "" {
			focus := a.archive.Path
			a.archive, a.cursor, a.offset = nil, 0, 0
			a.selected = make(map[string]bool)
			if err := a.reload(focus); err != nil {
				a.message(err.Error(), true)
			}
		} else {
			prev := strings.TrimSuffix(a.archivePrefix, "/")
			a.archivePrefix = path.Dir(prev)
			if a.archivePrefix == "." {
				a.archivePrefix = ""
			} else {
				a.archivePrefix += "/"
			}
			a.cursor, a.offset, a.selected = 0, 0, make(map[string]bool)
			if err := a.reload(prev); err != nil {
				a.message(err.Error(), true)
			}
		}
		return
	}
	a.changeDir(filepath.Dir(a.Cwd))
}

func (a *App) transfer(move bool) {
	if a.archive != nil {
		if move {
			a.localOnly()
		} else {
			a.extract(false)
		}
		return
	}
	entries := a.targets()
	if len(entries) == 0 {
		return
	}
	verb := "Copy"
	if move {
		verb = "Move"
	}
	a.ask(fmt.Sprintf("%s %d item(s)", verb, len(entries)), "Destination directory or new filename (existing files are protected)", a.Cwd+string(os.PathSeparator), func(value string) {
		if value == "" {
			return
		}
		dest := a.expandPath(value)
		info, err := os.Stat(dest)
		isDir := err == nil && info.IsDir()
		if len(entries) > 1 && !isDir {
			a.message("Multiple items need an existing destination directory", true)
			return
		}
		if strings.HasSuffix(value, "/") && !isDir {
			a.message("Destination directory does not exist", true)
			return
		}
		var completed []pathChange
		a.startJob(verb, func(ctx context.Context) (string, error) {
			for i, e := range entries {
				target := dest
				if isDir {
					target = filepath.Join(dest, e.Name)
				}
				var err error
				if move {
					err = files.Move(ctx, e.Path, target)
				} else {
					err = files.Copy(ctx, e.Path, target)
				}
				if err != nil {
					return "", fmt.Errorf("%s: %d/%d completed: %w", verb, i, len(entries), err)
				}
				completed = append(completed, pathChange{e.Path, target})
			}
			return fmt.Sprintf("%s completed: %d item(s)", verb, len(entries)), nil
		}, func(err error) {
			a.transferComments(completed, move)
			if err == nil {
				a.selected = make(map[string]bool)
			}
		})
	})
}

func (a *App) renameSelected() {
	if !a.localOnly() {
		return
	}
	e := a.current()
	if e.Name == ".." || e.Name == "" {
		return
	}
	a.ask("Rename", "New name", e.Name, func(name string) {
		if name == "" || name == e.Name {
			return
		}
		if filepath.Base(name) != name || name == "." || name == ".." {
			a.message("Enter a filename, not a path", true)
			return
		}
		dest := filepath.Join(a.Cwd, name)
		a.startJob("Rename", func(ctx context.Context) (string, error) {
			return "Renamed to " + name, files.Move(ctx, e.Path, dest)
		}, func(err error) {
			if err != nil {
				return
			}
			a.transferComments([]pathChange{{e.Path, dest}}, true)
			if err := a.reload(dest); err != nil {
				a.message(err.Error(), true)
			}
		})
	})
}

func (a *App) deleteSelected() {
	if !a.localOnly() {
		return
	}
	entries := a.targets()
	if len(entries) == 0 {
		return
	}
	detail := fmt.Sprintf("%d item(s), including %s", len(entries), entries[0].Name)
	a.confirm("Permanently delete", detail, func() {
		a.startJob("Deleting", func(ctx context.Context) (string, error) {
			for i, e := range entries {
				if err := files.Remove(ctx, e.Path); err != nil {
					return "", fmt.Errorf("%d/%d deleted: %w", i, len(entries), err)
				}
			}
			return fmt.Sprintf("Deleted %d item(s)", len(entries)), nil
		}, func(err error) {
			if err == nil {
				a.selected = make(map[string]bool)
			}
			var paths []string
			for _, e := range entries {
				paths = append(paths, e.Path)
			}
			a.pruneComments(paths)
		})
	})
}

func (a *App) makeDir() {
	if !a.localOnly() {
		return
	}
	a.ask("Create directory", "Directory name or path", "", func(name string) {
		if name == "" {
			return
		}
		full := a.expandPath(name)
		if err := os.Mkdir(full, 0755); err != nil {
			a.message(err.Error(), true)
			return
		}
		if err := a.reload(full); err != nil {
			a.message(err.Error(), true)
		} else {
			a.message("Directory created", false)
		}
	})
}

func (a *App) newFile() {
	if !a.localOnly() {
		return
	}
	a.ask("Create file", "Filename", "", func(name string) {
		if name == "" {
			return
		}
		full := a.expandPath(name)
		f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			a.message(err.Error(), true)
			return
		}
		if err := f.Close(); err != nil {
			a.message(err.Error(), true)
			return
		}
		if err := a.reload(full); err != nil {
			a.message(err.Error(), true)
		} else {
			a.message("File created; e edits it", false)
		}
	})
}

func (a *App) chmodSelected() {
	if !a.localOnly() {
		return
	}
	entries := a.targets()
	if len(entries) == 0 {
		return
	}
	a.ask("Unix permissions", "Octal mode, e.g. 644 or 755 (symlinks are refused)", fmt.Sprintf("%03o", entries[0].Mode.Perm()), func(value string) {
		mode, err := strconv.ParseUint(value, 8, 12)
		if err != nil || mode > 0777 {
			a.message("Use octal permissions from 000 to 777", true)
			return
		}
		for _, e := range entries {
			if e.IsLink {
				a.message("Select symlink targets explicitly to change permissions", true)
				return
			}
		}
		for _, e := range entries {
			if err := files.Chmod(e.Path, os.FileMode(mode)); err != nil {
				a.message(err.Error(), true)
				return
			}
		}
		a.refresh()
	})
}

func (a *App) toggleHidden() { a.hidden = !a.hidden; a.refresh() }

func (a *App) sortMenu() {
	choices := []Choice{}
	for _, key := range []string{"name", "ext", "size", "time"} {
		choices = append(choices, Choice{"Sort by " + key, func() { a.sortBy = key; a.refresh() }})
	}
	choices = append(choices, Choice{"Reverse order", func() { a.reverse = !a.reverse; a.refresh() }})
	a.menu = &Menu{Title: "Sort", Choices: choices}
}

func (a *App) selectPattern() {
	a.ask("Select matching files", "Wildcard pattern, e.g. *.go", "*", func(pattern string) {
		if _, err := filepath.Match(pattern, ""); err != nil {
			a.message(err.Error(), true)
			return
		}
		for _, e := range a.entries {
			if match, _ := filepath.Match(pattern, e.Name); match && e.Name != ".." {
				a.selected[e.Path] = true
			}
		}
	})
}

func (a *App) copyToClipboard(cut bool) {
	if !a.localOnly() {
		return
	}
	a.clipboard = nil
	for _, e := range a.targets() {
		a.clipboard = append(a.clipboard, e.Path)
	}
	a.cut = cut
	a.message(fmt.Sprintf("%d item(s) ready to %s; p pastes here", len(a.clipboard), map[bool]string{false: "copy", true: "move"}[cut]), false)
}

func (a *App) paste() {
	if !a.localOnly() || len(a.clipboard) == 0 {
		return
	}
	sources := append([]string(nil), a.clipboard...)
	dest, cut := a.Cwd, a.cut
	var completed []pathChange
	a.startJob("Pasting", func(ctx context.Context) (string, error) {
		for i, src := range sources {
			target := filepath.Join(dest, filepath.Base(src))
			var err error
			if cut {
				err = files.Move(ctx, src, target)
			} else {
				err = files.Copy(ctx, src, target)
			}
			if err != nil {
				return "", fmt.Errorf("%d/%d pasted: %w", i, len(sources), err)
			}
			completed = append(completed, pathChange{src, target})
		}
		return fmt.Sprintf("Pasted %d item(s)", len(sources)), nil
	}, func(err error) {
		a.transferComments(completed, cut)
		if cut {
			remaining := a.clipboard[:0]
			for _, src := range a.clipboard {
				moved := false
				for _, change := range completed {
					if change.source == src {
						moved = true
						break
					}
				}
				if !moved {
					remaining = append(remaining, src)
				}
			}
			a.clipboard = remaining
		}
	})
}

func (a *App) archiveMenu() {
	if a.archive != nil {
		a.menu = &Menu{Title: "Archive", Choices: []Choice{
			{"Extract selected/current", func() { a.extract(false) }},
			{"Extract whole archive", func() { a.extract(true) }},
		}}
		return
	}
	a.menu = &Menu{Title: "Create archive from selection", Choices: []Choice{
		{"ZIP (.zip)", func() { a.pack("zip") }},
		{"TAR (.tar)", func() { a.pack("tar") }},
		{"Gzip TAR (.tar.gz)", func() { a.pack("tgz") }},
	}}
}

func (a *App) pack(format string) {
	if !a.localOnly() {
		return
	}
	var sources []string
	for _, e := range a.targets() {
		sources = append(sources, e.Path)
	}
	if len(sources) == 0 {
		return
	}
	ext := map[string]string{"zip": ".zip", "tar": ".tar", "tgz": ".tar.gz"}[format]
	a.ask("Create "+strings.ToUpper(format), "Archive filename", filepath.Base(sources[0])+ext, func(name string) {
		if name == "" {
			return
		}
		dest := a.expandPath(name)
		a.startJob("Creating archive", func(ctx context.Context) (string, error) {
			return "Archive created: " + dest, files.CreateArchive(ctx, format, sources, dest)
		}, nil)
	})
}

func (a *App) extract(all bool) {
	if a.archive == nil {
		a.message("Open an archive first", true)
		return
	}
	archive := a.archive
	var names []string
	if !all {
		for _, e := range a.targets() {
			names = append(names, e.Path)
		}
		if len(names) == 0 {
			a.message("Select an archive member first", true)
			return
		}
	}
	a.ask("Extract archive", "Destination directory (archive paths are preserved)", a.Cwd+"/extracted", func(value string) {
		if value == "" {
			return
		}
		dest := a.expandPath(value)
		a.startJob("Extracting", func(ctx context.Context) (string, error) {
			return "Extracted to " + dest, archive.Extract(ctx, names, dest)
		}, nil)
	})
}

func (a *App) quickDir() {
	choices := []Choice{{"Enter a directory…", func() { a.ask("Change directory", "Path (~ for home)", a.Cwd, a.changeDir) }}}
	for _, dir := range a.State.History {
		choices = append(choices, Choice{dir, func() { a.changeDir(dir) }})
	}
	a.menu = &Menu{Title: "Quick directory", Choices: choices}
}

func (a *App) mounts() {
	paths, err := files.Mounts()
	if err != nil {
		a.message(err.Error(), true)
		return
	}
	choices := []Choice{}
	for _, dir := range paths {
		choices = append(choices, Choice{dir, func() { a.changeDir(dir) }})
	}
	a.menu = &Menu{Title: "Mounted filesystems", Choices: choices}
}

func (a *App) findFiles() {
	if !a.localOnly() {
		return
	}
	root := a.Cwd
	a.ask("Find files recursively", "Filename substring or wildcard; up to 1000 results", "", func(pattern string) {
		if pattern == "" {
			return
		}
		var found []files.Entry
		var searchErr error
		a.startJob("Searching "+root, func(ctx context.Context) (string, error) {
			found, searchErr = files.Search(ctx, root, pattern, 1000)
			if len(found) == 0 || ctx.Err() != nil {
				return fmt.Sprintf("Found %d matches", len(found)), searchErr
			}
			return fmt.Sprintf("Found %d matches", len(found)), nil
		}, func(err error) {
			if err != nil {
				return
			}
			choices := []Choice{}
			for _, e := range found {
				label, _ := filepath.Rel(root, e.Path)
				choices = append(choices, Choice{label, func() {
					a.changeDir(filepath.Dir(e.Path))
					if err := a.reload(e.Path); err != nil {
						a.message(err.Error(), true)
					}
				}})
			}
			if len(choices) > 0 {
				a.menu = &Menu{Title: fmt.Sprintf("Find: %s (%d)", pattern, len(found)), Choices: choices}
			}
			if searchErr != nil {
				message := "Partial search results: " + searchErr.Error()
				if len(found) == 1000 {
					message += " (first 1000 matches only)"
				}
				a.message(message, true)
			} else if len(found) == 1000 {
				a.message("Showing the first 1000 matches; narrow the search for more", false)
			}
		})
	})
}

func (a *App) comment() {
	if !a.localOnly() {
		return
	}
	e := a.current()
	if e.Name == ".." || e.Name == "" {
		return
	}
	a.ask("File comment", e.Name, a.State.Comments[e.Path], func(text string) {
		if text == "" {
			delete(a.State.Comments, e.Path)
		} else {
			a.State.Comments[e.Path] = text
		}
		if err := saveState(a.StateFile, a.State); err != nil {
			a.message("Comment could not be saved: "+err.Error(), true)
		} else {
			a.message("Comment saved", false)
		}
	})
}

func (a *App) info() {
	e := a.current()
	text := fmt.Sprintf("Name: %s\nPath: %s\nSize: %d bytes\nMode: %s (%03o)\nModified: %s\nDirectory: %t\nSymlink: %t\n\nComment: %s", e.Name, e.Path, e.Size, e.Mode.String(), e.Mode.Perm(), e.ModTime, e.IsDir, e.IsLink, a.State.Comments[e.Path])
	if e.IsLink && a.archive == nil {
		if target, err := os.Readlink(e.Path); err == nil {
			text += "\nLink target: " + target
		}
	}
	a.showText("Properties", text)
}

func (a *App) mainMenu() {
	a.menu = &Menu{Title: "Mdir commands", Choices: []Choice{
		{"Copy                         c / Alt-c / F5", func() { a.transfer(false) }},
		{"Move                         m / Alt-m / F6", func() { a.transfer(true) }},
		{"Rename                       r / Alt-r / F2", a.renameSelected},
		{"Delete                       x / Alt-d / F8", a.deleteSelected},
		{"Create directory             Alt-k / F7", a.makeDir},
		{"Create new file              :touch", a.newFile},
		{"View                         F3", a.viewCurrent},
		{"Edit                         e / F4", a.editCurrent},
		{"Open / execute externally    o", a.openExternal},
		{"Archive commands             F9", a.archiveMenu},
		{"Directory tree               t / F10", a.openTree},
		{"Quick directory              F11", a.quickDir},
		{"Mounted filesystems          :mounts", a.mounts},
		{"Find files recursively       f", a.findFiles},
		{"Sort                         s", a.sortMenu},
		{"Toggle hidden files          . / Alt-z", a.toggleHidden},
		{"Select by wildcard           +", a.selectPattern},
		{"Permissions                  Alt-h", a.chmodSelected},
		{"File comment                 C", a.comment},
		{"Properties                   i", a.info},
		{"Help                         ? / F1", a.help},
		{"Quit                         q / Alt-x", func() { a.quit = true }},
	}}
}
