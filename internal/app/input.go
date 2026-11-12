package app

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

func (a *App) HandleKey(ev *tcell.EventKey) {
	if a.busy != "" {
		if ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyCtrlC {
			a.cancel()
			a.message("Canceling…", false)
		}
		return
	}
	w, h := a.Screen.Size()
	if w < 32 || h < 10 {
		if ev.Key() == tcell.KeyEscape {
			a.prompt, a.menu = nil, nil
		}
		if ev.Key() == tcell.KeyCtrlQ || ev.Key() == tcell.KeyRune && ev.Rune() == 'q' && a.prompt == nil && a.menu == nil && a.viewer == nil && a.tree == nil {
			a.quit = true
		}
		return
	}
	if a.prompt != nil {
		a.promptKey(ev)
		return
	}
	if a.menu != nil {
		a.menuKey(ev)
		return
	}
	if a.viewer != nil {
		a.viewerKey(ev)
		return
	}
	if a.tree != nil {
		a.treeKey(ev)
		return
	}
	if ev.Key() != tcell.KeyRune || ev.Modifiers()&tcell.ModAlt != 0 {
		a.pending, a.count = 0, ""
	}
	rows, columns, _ := a.layout()
	switch ev.Key() {
	case tcell.KeyEscape:
		a.pending, a.count = 0, ""
		a.selected = make(map[string]bool)
		a.message("Selection cleared", false)
		return
	case tcell.KeyCtrlQ:
		a.quit = true
		return
	case tcell.KeyCtrlD:
		a.move(max(1, rows*columns/2))
		return
	case tcell.KeyCtrlU:
		a.move(-max(1, rows*columns/2))
		return
	case tcell.KeyCtrlF, tcell.KeyPgDn:
		a.move(rows * columns)
		return
	case tcell.KeyCtrlB, tcell.KeyPgUp:
		a.move(-rows * columns)
		return
	case tcell.KeyDown:
		a.move(1)
		return
	case tcell.KeyUp:
		a.move(-1)
		return
	case tcell.KeyLeft, tcell.KeyBackspace, tcell.KeyBackspace2:
		a.parent()
		return
	case tcell.KeyRight, tcell.KeyEnter:
		a.openCurrent()
		return
	case tcell.KeyHome:
		a.cursor = 0
		return
	case tcell.KeyEnd:
		a.cursor = max(0, len(a.entries)-1)
		return
	case tcell.KeyTab:
		a.move(rows)
		return
	case tcell.KeyBacktab:
		a.move(-rows)
		return
	case tcell.KeyCtrlR:
		a.refresh()
		return
	case tcell.KeyCtrlA:
		a.selectAll(false)
		return
	case tcell.KeyInsert:
		a.toggleSelection()
		return
	case tcell.KeyDelete:
		a.deleteSelected()
		return
	case tcell.KeyF1:
		a.help()
		return
	case tcell.KeyF10:
		a.openTree()
		return
	case tcell.KeyF11:
		a.quickDir()
		return
	case tcell.KeyF12:
		a.mainMenu()
		return
	}
	if ev.Key() >= tcell.KeyF2 && ev.Key() <= tcell.KeyF9 {
		key := fmt.Sprintf("F%d", int(ev.Key()-tcell.KeyF1)+1)
		if launcher, ok := a.Config.Launchers[key]; ok {
			a.runShell(launcher.Command)
			return
		}
		switch ev.Key() {
		case tcell.KeyF2:
			a.renameSelected()
		case tcell.KeyF3:
			a.viewCurrent()
		case tcell.KeyF4:
			a.editCurrent()
		case tcell.KeyF5:
			a.transfer(false)
		case tcell.KeyF6:
			a.transfer(true)
		case tcell.KeyF7:
			a.makeDir()
		case tcell.KeyF8:
			a.deleteSelected()
		case tcell.KeyF9:
			a.archiveMenu()
		}
		return
	}
	if ev.Key() != tcell.KeyRune {
		return
	}
	r := ev.Rune()
	if ev.Modifiers()&tcell.ModAlt != 0 {
		switch unicode.ToLower(r) {
		case 'c':
			a.transfer(false)
		case 'm':
			a.transfer(true)
		case 'd':
			a.deleteSelected()
		case 'r':
			a.renameSelected()
		case 'k':
			a.makeDir()
		case 'v':
			a.viewCurrent()
		case 'g':
			a.editCurrent()
		case 's':
			a.sortMenu()
		case 'z':
			a.toggleHidden()
		case 'h':
			a.chmodSelected()
		case 'a', 'e':
			a.archiveMenu()
		case 'f':
			a.findFiles()
		case 'x':
			a.quit = true
		}
		return
	}
	if r >= '0' && r <= '9' && (r != '0' || a.count != "") {
		if len(a.count) < 6 {
			a.count += string(r)
		}
		return
	}
	count := 1
	if a.count != "" {
		count, _ = strconv.Atoi(a.count)
		a.count = ""
	}
	pending := a.pending
	a.pending = 0
	if pending == r {
		switch r {
		case 'g':
			a.cursor = 0
			return
		case 'y':
			a.copyToClipboard(false)
			return
		case 'd':
			a.copyToClipboard(true)
			return
		}
	}
	switch r {
	case 'j':
		a.move(count)
	case 'k':
		a.move(-count)
	case 'h':
		a.parent()
	case 'l':
		a.openCurrent()
	case 'g', 'y', 'd':
		a.pending = r
	case 'G':
		a.cursor = max(0, len(a.entries)-1)
	case '0':
		a.cursor = a.offset
	case ' ':
		a.toggleSelection()
		a.move(1)
	case 'v':
		a.toggleSelection()
	case 'V':
		a.selectAll(true)
	case '*':
		a.selectAll(true)
	case '+':
		a.selectPattern()
	case '/':
		a.ask("Find in this directory", "Filename contains", a.query, func(s string) { a.query = s; a.findNext(1, true) })
	case 'n':
		a.findNext(1, false)
	case 'N':
		a.findNext(-1, false)
	case ':':
		a.ask("Command", "Try :help, cd PATH, sort size, or !SHELL COMMAND", "", a.command)
	case '?':
		a.help()
	case 'q':
		a.quit = true
	case '.':
		a.toggleHidden()
	case 's':
		a.sortMenu()
	case 'r':
		a.renameSelected()
	case 'c':
		a.transfer(false)
	case 'm':
		a.transfer(true)
	case 'x':
		a.deleteSelected()
	case 'p':
		a.paste()
	case 't':
		a.openTree()
	case 'e':
		a.editCurrent()
	case 'o':
		a.openExternal()
	case 'f':
		a.findFiles()
	case 'i':
		a.info()
	case 'C':
		a.comment()
	case '~':
		a.changeDir("~")
	case '\\':
		a.changeDir("/")
	case '!':
		a.ask("Shell", "Command to run in "+a.Cwd, "", a.runShell)
	}
}

func (a *App) promptKey(ev *tcell.EventKey) {
	p := a.prompt
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		a.prompt = nil
	case tcell.KeyEnter:
		a.prompt = nil
		p.Accept(string(p.Text))
	case tcell.KeyLeft:
		p.Pos = max(0, p.Pos-1)
	case tcell.KeyRight:
		p.Pos = min(len(p.Text), p.Pos+1)
	case tcell.KeyHome, tcell.KeyCtrlA:
		p.Pos = 0
	case tcell.KeyEnd, tcell.KeyCtrlE:
		p.Pos = len(p.Text)
	case tcell.KeyCtrlU:
		p.Text, p.Pos = nil, 0
	case tcell.KeyCtrlK:
		p.Text = p.Text[:p.Pos]
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if p.Pos > 0 {
			p.Text = append(p.Text[:p.Pos-1], p.Text[p.Pos:]...)
			p.Pos--
		}
	case tcell.KeyDelete:
		if p.Pos < len(p.Text) {
			p.Text = append(p.Text[:p.Pos], p.Text[p.Pos+1:]...)
		}
	case tcell.KeyRune:
		if !unicode.IsControl(ev.Rune()) {
			p.Text = append(p.Text, 0)
			copy(p.Text[p.Pos+1:], p.Text[p.Pos:])
			p.Text[p.Pos] = ev.Rune()
			p.Pos++
		}
	}
}

func (a *App) menuKey(ev *tcell.EventKey) {
	m := a.menu
	_, h := a.Screen.Size()
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		a.menu = nil
	case tcell.KeyEnter:
		if len(m.Choices) > 0 {
			a.menu = nil
			m.Choices[m.Cursor].Action()
		}
	case tcell.KeyDown:
		m.Cursor++
	case tcell.KeyUp:
		m.Cursor--
	case tcell.KeyHome:
		m.Cursor = 0
	case tcell.KeyEnd:
		m.Cursor = len(m.Choices) - 1
	case tcell.KeyPgDn, tcell.KeyCtrlF:
		m.Cursor += max(1, h-6)
	case tcell.KeyPgUp, tcell.KeyCtrlB:
		m.Cursor -= max(1, h-6)
	case tcell.KeyCtrlD:
		m.Cursor += max(1, (h-6)/2)
	case tcell.KeyCtrlU:
		m.Cursor -= max(1, (h-6)/2)
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'j':
			m.Cursor++
		case 'k':
			m.Cursor--
		case 'g':
			m.Cursor = 0
		case 'G':
			m.Cursor = len(m.Choices) - 1
		case 'q':
			a.menu = nil
		}
	}
	m.Cursor = min(max(0, m.Cursor), max(0, len(m.Choices)-1))
}

func (a *App) findNext(direction int, includeCurrent bool) {
	if a.query == "" {
		return
	}
	start := 1
	if includeCurrent {
		start = 0
	}
	for step := start; step < len(a.entries)+start; step++ {
		i := (a.cursor + direction*step + len(a.entries)*2) % len(a.entries)
		if strings.Contains(strings.ToLower(a.entries[i].Name), strings.ToLower(a.query)) {
			a.cursor = i
			a.message("/"+a.query, false)
			return
		}
	}
	a.message("No match: "+a.query, true)
}

func (a *App) toggleSelection() {
	e := a.current()
	if e.Name == ".." || e.Name == "" {
		return
	}
	if a.selected[e.Path] {
		delete(a.selected, e.Path)
	} else {
		a.selected[e.Path] = true
	}
}

func (a *App) selectAll(invert bool) {
	for _, e := range a.entries {
		if e.Name == ".." {
			continue
		}
		if invert && a.selected[e.Path] {
			delete(a.selected, e.Path)
		} else {
			a.selected[e.Path] = true
		}
	}
}

func (a *App) handleMouse(ev *tcell.EventMouse) {
	if a.prompt != nil || a.menu != nil || a.busy != "" {
		return
	}
	button := ev.Buttons()
	delta := 0
	if button&tcell.WheelUp != 0 {
		delta = -3
	}
	if button&tcell.WheelDown != 0 {
		delta = 3
	}
	if a.viewer != nil {
		a.viewer.Top = max(0, min(a.viewer.Top+delta, len(a.viewer.Lines)-1))
		return
	}
	if a.tree != nil {
		a.tree.Cursor = max(0, min(a.tree.Cursor+delta, len(a.tree.Rows)-1))
		return
	}
	if delta != 0 {
		a.move(delta)
		return
	}
	if button&tcell.Button1 != 0 {
		x, y := ev.Position()
		rows, columns, width := a.layout()
		if y >= 3 && y < 3+rows {
			index := a.offset + min(x/width, columns-1)*rows + y - 3
			if index < len(a.entries) {
				a.cursor = index
			}
		}
	}
}
