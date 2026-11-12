package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
)

type TreeRow struct {
	Path  string
	Depth int
	Link  bool
}
type Tree struct {
	Rows           []TreeRow
	Expanded       map[string]bool
	Cursor, Offset int
	Query          string
	Pending        rune
}

func (a *App) openTree() {
	if a.archive != nil {
		a.message("MCD tree is available for local directories", true)
		return
	}
	current, err := filepath.EvalSymlinks(a.Cwd)
	if err != nil {
		a.message("Tree: "+err.Error(), true)
		return
	}
	t := &Tree{Expanded: make(map[string]bool)}
	for dir := current; ; dir = filepath.Dir(dir) {
		t.Expanded[dir] = true
		if dir == "/" {
			break
		}
	}
	a.tree = t
	a.rebuildTree(current)
	_, height := a.Screen.Size()
	t.Offset = max(0, t.Cursor-max(1, (height-4)/2))
}

func (a *App) rebuildTree(focus string) {
	t := a.tree
	t.Rows = nil
	var add func(string, int, bool)
	var scanErr error
	add = func(dir string, depth int, link bool) {
		t.Rows = append(t.Rows, TreeRow{dir, depth, link})
		if !t.Expanded[dir] || link {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if scanErr == nil {
				scanErr = err
			}
			return
		}
		sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name()) })
		for _, e := range entries {
			if !a.hidden && strings.HasPrefix(e.Name(), ".") && !t.Expanded[filepath.Join(dir, e.Name())] {
				continue
			}
			full := filepath.Join(dir, e.Name())
			isLink := e.Type()&os.ModeSymlink != 0
			isDir := e.IsDir()
			if isLink {
				if info, err := os.Stat(full); err == nil {
					isDir = info.IsDir()
				}
			}
			if isDir {
				add(full, depth+1, isLink)
			}
		}
	}
	add("/", 0, false)
	for i, row := range t.Rows {
		if row.Path == focus {
			t.Cursor = i
			break
		}
	}
	t.Cursor = min(max(0, t.Cursor), max(0, len(t.Rows)-1))
	if scanErr != nil {
		a.message("Tree: "+scanErr.Error(), true)
	}
}

func (a *App) drawTree() {
	t := a.tree
	w, h := a.Screen.Size()
	for y := 0; y < h; y++ {
		a.line(y, "", treeStyle)
	}
	a.line(0, " Mdir III · Change Directory", pathStyle)
	visible := h - 4
	if t.Cursor < t.Offset {
		t.Offset = t.Cursor
	}
	if t.Cursor >= t.Offset+visible {
		t.Offset = t.Cursor - visible + 1
	}
	for row := 0; row < visible; row++ {
		i := t.Offset + row
		if i >= len(t.Rows) {
			break
		}
		e := t.Rows[i]
		style := treeStyle
		if i == t.Cursor {
			style = treeStyle.Foreground(black).Background(yellow)
		}
		mark := "+"
		if t.Expanded[e.Path] {
			mark = "−"
		}
		if e.Link {
			mark = "@"
		}
		name := filepath.Base(e.Path)
		if e.Path == "/" {
			name = "/"
		}
		indent := strings.Repeat("│  ", min(e.Depth, max(0, (w-20)/3)))
		text := indent + "├─" + mark + " " + name
		a.fill(0, row+1, w, style)
		a.put(0, row+1, w, clip(text, w), style)
	}
	if len(t.Rows) > 0 {
		a.line(h-3, " "+t.Rows[t.Cursor].Path, treeStyle)
	}
	a.line(h-2, " j/k move  h collapse  l expand  Enter open  / search  q back", barStyle.Foreground(white))
	a.drawStatus(h - 1)
}

func (a *App) treeKey(ev *tcell.EventKey) {
	t := a.tree
	_, h := a.Screen.Size()
	if len(t.Rows) == 0 {
		a.tree = nil
		return
	}
	row := t.Rows[t.Cursor]
	expand := func() {
		if row.Link {
			a.message("Press Enter to follow this directory symlink", false)
			return
		}
		t.Expanded[row.Path] = true
		a.rebuildTree(row.Path)
	}
	collapse := func() {
		if t.Expanded[row.Path] {
			delete(t.Expanded, row.Path)
			a.rebuildTree(row.Path)
		} else {
			parent := filepath.Dir(row.Path)
			for i, r := range t.Rows {
				if r.Path == parent {
					t.Cursor = i
					break
				}
			}
		}
	}
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyF10:
		a.tree = nil
		return
	case tcell.KeyEnter:
		a.changeDir(row.Path)
		return
	case tcell.KeyDown:
		t.Cursor++
	case tcell.KeyUp:
		t.Cursor--
	case tcell.KeyLeft:
		collapse()
	case tcell.KeyRight:
		expand()
	case tcell.KeyHome:
		t.Cursor = 0
	case tcell.KeyEnd:
		t.Cursor = len(t.Rows) - 1
	case tcell.KeyCtrlD:
		t.Cursor += max(1, (h-4)/2)
	case tcell.KeyCtrlU:
		t.Cursor -= max(1, (h-4)/2)
	case tcell.KeyCtrlF, tcell.KeyPgDn:
		t.Cursor += max(1, h-4)
	case tcell.KeyCtrlB, tcell.KeyPgUp:
		t.Cursor -= max(1, h-4)
	case tcell.KeyRune:
		pending := t.Pending
		t.Pending = 0
		switch ev.Rune() {
		case 'q':
			a.tree = nil
			return
		case 'j':
			t.Cursor++
		case 'k':
			t.Cursor--
		case 'h':
			collapse()
		case 'l':
			expand()
		case 'g':
			if pending == 'g' {
				t.Cursor = 0
			} else {
				t.Pending = 'g'
			}
		case 'G':
			t.Cursor = len(t.Rows) - 1
		case 'r':
			a.rebuildTree(row.Path)
		case '/':
			a.ask("Find in tree", "Visible directory name contains", t.Query, func(query string) { t.Query = query; a.treeFind(1) })
		case 'n':
			a.treeFind(1)
		case 'N':
			a.treeFind(-1)
		}
	}
	t.Cursor = min(max(0, t.Cursor), max(0, len(t.Rows)-1))
}

func (a *App) treeFind(direction int) {
	t := a.tree
	if t.Query == "" {
		return
	}
	for step := 1; step <= len(t.Rows); step++ {
		i := (t.Cursor + direction*step + len(t.Rows)*2) % len(t.Rows)
		if strings.Contains(strings.ToLower(filepath.Base(t.Rows[i].Path)), strings.ToLower(t.Query)) {
			t.Cursor = i
			return
		}
	}
	a.message(fmt.Sprintf("No visible directory matching %q; expand branches with l", t.Query), true)
}
