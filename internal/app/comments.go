package app

import (
	"os"
	"path/filepath"
	"strings"
)

type pathChange struct{ source, destination string }

func underPath(path, parent string) bool {
	return path == parent || strings.HasPrefix(path, parent+string(os.PathSeparator))
}

// Apply only completed changes, on the event loop, including partial batches.
func (a *App) transferComments(changes []pathChange, move bool) {
	changed := false
	for _, change := range changes {
		additions := make(map[string]string)
		for path, comment := range a.State.Comments {
			if !underPath(path, change.source) {
				continue
			}
			rel, err := filepath.Rel(change.source, path)
			if err != nil {
				continue
			}
			additions[filepath.Join(change.destination, rel)] = comment
			if move {
				delete(a.State.Comments, path)
			}
			changed = true
		}
		for path, comment := range additions {
			a.State.Comments[path] = comment
		}
	}
	if changed {
		a.persistComments()
	}
}

func (a *App) pruneComments(paths []string) {
	changed := false
	for path := range a.State.Comments {
		for _, parent := range paths {
			if underPath(path, parent) {
				if _, err := os.Lstat(path); os.IsNotExist(err) {
					delete(a.State.Comments, path)
					changed = true
				}
				break
			}
		}
	}
	if changed {
		a.persistComments()
	}
}

func (a *App) persistComments() {
	if err := saveState(a.StateFile, a.State); err != nil {
		a.message("File operation finished, but comments could not be saved: "+err.Error(), true)
	}
}
