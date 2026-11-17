package app

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

const viewerLimit int64 = 2 << 20

type Viewer struct {
	Name           string
	Data           []byte
	Lines          []string
	Top, Left      int
	Query          string
	Notice         string
	SearchError    bool
	Hex, Truncated bool
	Pending        rune
}

func (a *App) showText(name, text string) {
	a.viewer = &Viewer{Name: name, Data: []byte(text)}
	a.viewer.buildLines()
}

func (v *Viewer) buildLines() {
	text := string(v.Data)
	if v.Hex {
		text = hex.Dump(v.Data)
	} else {
		text = strings.ToValidUTF8(text, "�")
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\t", "    ")
	}
	v.Lines = strings.Split(text, "\n")
	v.Top = min(v.Top, max(0, len(v.Lines)-1))
}

func (a *App) viewCurrent() {
	e := a.current()
	if e.IsDir {
		a.openCurrent()
		return
	}
	var data []byte
	var err error
	if a.archive != nil {
		data, err = a.archive.Read(e.Path, viewerLimit+1)
	} else {
		info, statErr := os.Stat(e.Path)
		if statErr != nil {
			a.message(statErr.Error(), true)
			return
		}
		if !info.Mode().IsRegular() {
			a.message("Viewer only opens regular files", true)
			return
		}
		var f *os.File
		f, err = os.Open(e.Path)
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(f, viewerLimit+1))
			f.Close()
		}
	}
	if err != nil {
		a.message(err.Error(), true)
		return
	}
	truncated := int64(len(data)) > viewerLimit
	if truncated {
		data = data[:viewerLimit]
	}
	a.viewer = &Viewer{Name: e.Name, Data: data, Truncated: truncated, Hex: bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)}
	a.viewer.buildLines()
}

func (a *App) drawViewer() {
	v := a.viewer
	w, h := a.Screen.Size()
	mode := "TEXT"
	if v.Hex {
		mode = "HEX"
	}
	a.line(0, clip(" Mdir viewer · "+mode+" · "+v.Name, w), barStyle.Foreground(white))
	for row := 0; row < h-3; row++ {
		i := v.Top + row
		if i >= len(v.Lines) {
			break
		}
		line := v.Lines[i]
		style := baseStyle
		if v.Query != "" && strings.Contains(strings.ToLower(line), strings.ToLower(v.Query)) {
			style = selectedStyle
		}
		a.put(0, row+1, w, skipCells(displayText(line), v.Left), style)
	}
	label := fmt.Sprintf(" Line %d/%d · col %d · %s bytes", v.Top+1, len(v.Lines), v.Left+1, humanSize(int64(len(v.Data))))
	if v.Truncated {
		label += " · first 2 MiB only (e opens full file)"
	}
	a.line(h-2, label, pathStyle)
	if v.Notice != "" {
		style := pathStyle
		if v.SearchError {
			style = baseStyle.Foreground(red)
		}
		a.line(h-2, v.Notice, style)
	}
	a.line(h-1, " j/k scroll  h/l sideways  gg/G  / find  n/N  H text/hex  q back", barStyle.Foreground(white))
}

func skipCells(s string, cells int) string {
	g := uniseg.NewGraphemes(s)
	used := 0
	for g.Next() {
		start, _ := g.Positions()
		if used >= cells {
			return s[start:]
		}
		used += g.Width()
	}
	return ""
}

func (a *App) viewerKey(ev *tcell.EventKey) {
	v := a.viewer
	_, h := a.Screen.Size()
	page := max(1, h-3)
	switch ev.Key() {
	case tcell.KeyEscape:
		a.viewer = nil
		return
	case tcell.KeyDown:
		v.Top++
	case tcell.KeyUp:
		v.Top--
	case tcell.KeyLeft:
		v.Left -= 4
	case tcell.KeyRight:
		v.Left += 4
	case tcell.KeyPgDn, tcell.KeyCtrlF:
		v.Top += page
	case tcell.KeyPgUp, tcell.KeyCtrlB:
		v.Top -= page
	case tcell.KeyCtrlD:
		v.Top += page / 2
	case tcell.KeyCtrlU:
		v.Top -= page / 2
	case tcell.KeyHome:
		v.Top = 0
	case tcell.KeyEnd:
		v.Top = len(v.Lines) - 1
	case tcell.KeyRune:
		pending := v.Pending
		v.Pending = 0
		switch ev.Rune() {
		case 'q':
			a.viewer = nil
			return
		case 'j':
			v.Top++
		case 'k':
			v.Top--
		case 'h':
			v.Left -= 4
		case 'l':
			v.Left += 4
		case 'g':
			if pending == 'g' {
				v.Top = 0
			} else {
				v.Pending = 'g'
			}
		case 'G':
			v.Top = max(0, len(v.Lines)-page)
		case '0':
			v.Left = 0
		case 'H':
			v.Hex = !v.Hex
			v.buildLines()
			v.Left = 0
		case '/':
			a.ask("Find in viewer", "Text contains", v.Query, func(query string) { v.Query = query; a.viewerFind(1, true) })
		case 'n':
			a.viewerFind(1, false)
		case 'N':
			a.viewerFind(-1, false)
		}
	}
	v.Top = min(max(0, v.Top), max(0, len(v.Lines)-1))
	v.Left = max(0, v.Left)
}

func (a *App) viewerFind(direction int, include bool) {
	v := a.viewer
	if v.Query == "" {
		return
	}
	start := 1
	if include {
		start = 0
	}
	for step := start; step < len(v.Lines)+start; step++ {
		i := (v.Top + direction*step + len(v.Lines)*2) % len(v.Lines)
		if strings.Contains(strings.ToLower(v.Lines[i]), strings.ToLower(v.Query)) {
			v.Top = i
			v.Left = 0
			v.Notice, v.SearchError = "/"+v.Query, false
			return
		}
	}
	v.Notice, v.SearchError = "No match: "+v.Query, true
}

func (a *App) help() {
	a.showText("Mdir help", `MDIR — classic colors, Vim navigation

NAVIGATION
  j / k, Up / Down     Next / previous item (counts work: 12j)
  h / l, Left / Right  Parent / open directory, archive, or file
  Enter                Open; executable files run in the terminal
  gg / G               First / last item
  Ctrl-d / Ctrl-u      Half-page down / up
  Ctrl-f / Ctrl-b      Page down / up
  Tab / Shift-Tab      Next / previous list column
  / then n / N         Find filename, next / previous match
  ~ / \                Home / filesystem root
  .                    Toggle dotfiles
  Ctrl-r               Refresh

SELECTION & FILES
  Space                Toggle selection and advance
  v                    Toggle current item
  Ctrl-a / V           Select all / invert
  +                    Select wildcard, e.g. *.txt
  Esc                  Cancel dialog / clear selection / cancel running job
  c / m / r / x        Copy / move / rename / delete (delete asks first)
  yy / dd / p          Copy / cut to Mdir clipboard / paste here
  e / o / i / C        Edit / external open / properties / file comment
  f                    Recursive filename search (substring or wildcard)
  s                    Sort: name, extension, size, date; reverse

FUNCTION KEYS (F2–F9 can be replaced by configured launchers)
  F1 Help    F2 Rename   F3 View     F4 Edit    F5 Copy
  F6 Move    F7 Mkdir    F8 Delete   F9 Archive menu
  F10 MCD tree          F11 Quick directory   F12 Menu
  Alt-c/m/r/d/k/v/g/s/z/h = copy/move/rename/delete/mkdir/view/edit/sort/hidden/chmod

TREE
  j/k, gg/G, Ctrl-d/u   Move
  l                    Expand a directory
  h                    Collapse or go to parent
  Enter                Open highlighted directory in the file list
  /, n/N               Search visible tree names
  r                    Refresh; lazily rescans expanded directories
  q / Esc / F10        Return

VIEWER
  j/k, gg/G, Ctrl-d/u/f/b; h/l horizontal scrolling
  / and n/N search; H toggles text/hex; q or Esc returns
  Preview is limited to 2 MiB. Use e from the file list for full-file editing.

ARCHIVES
  Enter opens ZIP, TAR, TAR.GZ or TGZ; h leaves its root.
  c / F5 extracts selected members; F9 also offers whole-archive extraction.
  F9 from a normal directory creates an archive from selected/current items.
  Extraction preserves member paths, rejects path escapes and archive links,
  and refuses existing files. Archives are browsed read-only.

COMMANDS (:)
  cd PATH        Change directory (spaces need no quoting; ~ is supported)
  mkdir NAME     Create directory       touch NAME   Create empty file
  sort name|ext|size|time               reverse      Reverse sort
  hidden         Toggle dotfiles       tree         MCD tree
  mounts         Mounted filesystems   jump         Recent directories
  copy / move / rename / delete        Open the respective dialog
  view / edit / open / info / comment / chmod / pack / extract / extract-all
  find PATTERN   Recursive search      select GLOB  Mark matching names
  !COMMAND       Run shell command     shell        Interactive shell
  help           This help             q / quit     Exit

SAFETY & PLATFORM DIFFERENCES
  Copy/move/rename never silently replace an existing destination.
  Delete is permanent and needs confirmation; cancel is selected by default.
  Canceling batch work leaves completed items in place; partial progress is reported.
  Symlinks are copied as links; recursive operations do not follow them.
  Directory listing reads metadata. Files are read when explicitly viewed/opened.
  DOS floppy utilities and legacy archive formats are not emulated.
`)
}
