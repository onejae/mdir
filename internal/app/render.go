package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/onejae/mdir/internal/files"
	"github.com/rivo/uniseg"
)

var (
	black         = tcell.NewHexColor(0x000000)
	gray          = tcell.NewHexColor(0xaaaaaa)
	white         = tcell.NewHexColor(0xffffff)
	cyan          = tcell.NewHexColor(0x00aaaa)
	brightCyan    = tcell.NewHexColor(0x55ffff)
	red           = tcell.NewHexColor(0xff5555)
	yellow        = tcell.NewHexColor(0xffff55)
	green         = tcell.NewHexColor(0x55ff55)
	magenta       = tcell.NewHexColor(0xff55ff)
	blue          = tcell.NewHexColor(0x5555ff)
	baseStyle     = tcell.StyleDefault.Foreground(gray).Background(black)
	barStyle      = baseStyle.Foreground(black).Background(cyan)
	pathStyle     = baseStyle.Foreground(black).Background(gray)
	selectedStyle = baseStyle.Foreground(yellow)
	cursorStyle   = baseStyle.Foreground(black).Background(red)
	treeStyle     = baseStyle.Foreground(white).Background(blue)
	dialogStyle   = baseStyle.Foreground(white).Background(cyan)
)

// displayText neutralizes controls without changing the actual filesystem name.
func displayText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, s)
}

func clip(s string, width int) string {
	s = displayText(s)
	if width <= 0 {
		return ""
	}
	if uniseg.StringWidth(s) <= width {
		return s
	}
	var b strings.Builder
	g := uniseg.NewGraphemes(s)
	used := 0
	for g.Next() {
		if used+g.Width() > width-1 {
			break
		}
		b.WriteString(g.Str())
		used += g.Width()
	}
	return b.String() + "…"
}

func (a *App) fill(x, y, width int, style tcell.Style) {
	w, h := a.Screen.Size()
	if y < 0 || y >= h {
		return
	}
	for col := max(0, x); col < min(w, x+width); col++ {
		a.Screen.SetContent(col, y, ' ', nil, style)
	}
}

func (a *App) put(x, y, width int, text string, style tcell.Style) {
	w, h := a.Screen.Size()
	if y < 0 || y >= h || x >= w || width <= 0 {
		return
	}
	g := uniseg.NewGraphemes(displayText(text))
	end := min(w, x+width)
	for g.Next() {
		runes := g.Runes()
		cells := g.Width()
		if cells == 0 {
			continue
		}
		if x+cells > end {
			break
		}
		if x >= 0 {
			a.Screen.SetContent(x, y, runes[0], runes[1:], style)
		}
		x += cells
	}
}

func (a *App) line(y int, text string, style tcell.Style) {
	w, _ := a.Screen.Size()
	a.fill(0, y, w, style)
	a.put(0, y, w, text, style)
}

func (a *App) Draw() {
	w, h := a.Screen.Size()
	a.Screen.SetStyle(baseStyle)
	a.Screen.Clear()
	a.Screen.HideCursor()
	if w < 32 || h < 10 {
		a.put(0, 0, w, "Mdir — enlarge terminal (32×10)", selectedStyle)
		a.Screen.Show()
		return
	}
	if a.viewer != nil {
		a.drawViewer()
	} else if a.tree != nil {
		a.drawTree()
	} else {
		a.drawFiles()
	}
	if a.menu != nil {
		a.drawMenu()
	}
	if a.prompt != nil {
		a.drawPrompt()
	}
	a.Screen.Show()
}

func (a *App) drawFiles() {
	w, h := a.Screen.Size()
	labels := []string{"Rename", "View", "Edit", "Copy", "Move", "Mkdir", "Delete", "Pack"}
	a.line(0, "", barStyle)
	for i, label := range labels {
		key := fmt.Sprintf("F%d", i+2)
		if launcher, ok := a.Config.Launchers[key]; ok {
			label = launcher.Label
		}
		x, end := i*w/8, (i+1)*w/8
		a.put(x, 0, end-x, clip(key+" "+label, end-x-1), barStyle)
	}
	path := a.Cwd
	if a.archive != nil {
		path = a.archive.Path + " :: /" + a.archivePrefix
	}
	a.line(1, clip(" Path "+path, w), pathStyle)
	a.line(2, strings.Repeat("─", w), baseStyle.Foreground(cyan))
	header := " MDIR / LINUX  ·  VIM "
	if a.archive != nil {
		header += "· ARCHIVE "
	}
	a.put(2, 2, w-4, header, baseStyle.Foreground(brightCyan))
	rows, columns, columnWidth := a.layout()
	pageSize := rows * columns
	if a.cursor < a.offset || a.cursor >= a.offset+pageSize {
		a.offset = (a.cursor / pageSize) * pageSize
	}
	a.offset = max(0, a.offset)
	for col := 0; col < columns; col++ {
		x := col * columnWidth
		width := columnWidth - 1
		if col == columns-1 {
			width = w - x
		}
		for row := 0; row < rows; row++ {
			index := a.offset + col*rows + row
			if index >= len(a.entries) {
				break
			}
			e := a.entries[index]
			style := entryStyle(e)
			metadataStyle := baseStyle
			marker := " "
			if a.selected[e.Path] {
				style, marker = selectedStyle, "*"
				metadataStyle = selectedStyle
			}
			if index == a.cursor {
				style = cursorStyle
				metadataStyle = cursorStyle
			}
			a.fill(x, 3+row, width, style)
			name := e.Name
			if e.IsDir && name != ".." {
				name += "/"
			} else if e.IsLink {
				name += "@"
			}
			if width >= 38 {
				nameWidth := width - 24
				a.put(x, 3+row, nameWidth+1, marker+clip(name, nameWidth), style)
				size := humanSize(e.Size)
				if e.IsDir {
					size = "<DIR>"
				}
				a.put(x+width-23, 3+row, 8, fmt.Sprintf("%8s", size), metadataStyle)
				a.put(x+width-14, 3+row, 14, stamp(e.ModTime), metadataStyle)
			} else {
				a.put(x, 3+row, width, marker+clip(name, width-1), style)
			}
		}
		if col < columns-1 {
			for row := 0; row < rows; row++ {
				a.put(x+columnWidth-1, 3+row, 1, "│", baseStyle.Foreground(cyan))
			}
		}
	}
	var total int64
	var dirs, fileCount int
	for _, e := range a.entries {
		if e.Name == ".." {
			continue
		}
		if e.IsDir {
			dirs++
		} else {
			fileCount++
			total += e.Size
		}
	}
	summary := fmt.Sprintf(" %d Files  %d Dirs  %s bytes  %d selected", fileCount, dirs, humanSize(total), len(a.selected))
	var fs syscall.Statfs_t
	if syscall.Statfs(a.Cwd, &fs) == nil {
		summary += "  Free " + humanSize(int64(fs.Bavail)*fs.Bsize)
	}
	a.line(h-4, summary, pathStyle)
	current := a.current()
	info := " " + current.Mode.String() + "  " + current.Name
	if comment := a.State.Comments[current.Path]; comment != "" {
		info += "  ·  " + comment
	}
	a.line(h-3, clip(info, w), baseStyle.Foreground(brightCyan))
	a.line(h-2, " F1 Help  F10 Tree  F11 Jump  F12 Menu  / Find  : Command  q Quit", barStyle.Foreground(white))
	a.drawStatus(h - 1)
}

func entryStyle(e files.Entry) tcell.Style {
	if e.IsLink {
		return baseStyle.Foreground(magenta)
	}
	if e.IsDir {
		return baseStyle.Foreground(white)
	}
	if e.Mode.Perm()&0111 != 0 {
		return baseStyle.Foreground(green)
	}
	if files.IsArchive(e.Name) {
		return baseStyle.Foreground(magenta)
	}
	switch strings.ToLower(filepath.Ext(e.Name)) {
	case ".txt", ".md", ".doc", ".json", ".yaml", ".yml", ".toml", ".go", ".c", ".h", ".py", ".js", ".ts", ".rs", ".html", ".css":
		return baseStyle.Foreground(cyan)
	case ".sh", ".bash", ".zsh":
		return baseStyle.Foreground(yellow)
	}
	return baseStyle
}

func (a *App) drawStatus(y int) {
	style := baseStyle
	if a.statusError {
		style = style.Foreground(red)
	}
	status := a.status
	if a.busy != "" {
		status = a.busy + "  ·  Esc cancels"
		style = selectedStyle
	}
	if status == "" {
		status = fmt.Sprintf(" %s sort · %s · j/k move · h parent · l open · Space select", a.sortBy, map[bool]string{true: "dotfiles shown", false: "dotfiles hidden"}[a.hidden])
	}
	a.line(y, status, style)
}

func (a *App) box(title string, width, height int) (x, y int) {
	w, h := a.Screen.Size()
	width, height = min(width, w-2), min(height, h-2)
	x, y = (w-width)/2, (h-height)/2
	for row := 0; row < height; row++ {
		a.fill(x, y+row, width, dialogStyle)
	}
	a.put(x, y, width, "┌"+strings.Repeat("─", max(0, width-2))+"┐", dialogStyle)
	a.put(x, y+height-1, width, "└"+strings.Repeat("─", max(0, width-2))+"┘", dialogStyle)
	for row := 1; row < height-1; row++ {
		a.put(x, y+row, 1, "│", dialogStyle)
		a.put(x+width-1, y+row, 1, "│", dialogStyle)
	}
	a.put(x+2, y, width-4, clip(" "+title+" ", width-4), dialogStyle.Foreground(yellow))
	return
}

func (a *App) drawMenu() {
	w, h := a.Screen.Size()
	m := a.menu
	width := 50
	for _, choice := range m.Choices {
		width = max(width, uniseg.StringWidth(displayText(choice.Label))+6)
	}
	width = min(w-2, max(width, min(76, uniseg.StringWidth(displayText(m.Title))+6)))
	visible := min(len(m.Choices), h-6)
	x, y := a.box(m.Title, width, visible+4)
	start := max(0, m.Cursor-visible+1)
	for row := 0; row < visible; row++ {
		index := start + row
		if index >= len(m.Choices) {
			break
		}
		style := dialogStyle
		if index == m.Cursor {
			style = cursorStyle
		}
		a.fill(x+2, y+1+row, width-4, style)
		a.put(x+2, y+1+row, width-4, clip(m.Choices[index].Label, width-4), style)
	}
	a.put(x+2, y+visible+2, width-4, "j/k choose · Enter accept · Esc cancel", dialogStyle)
}

func (a *App) drawPrompt() {
	w, _ := a.Screen.Size()
	width := min(w-2, 80)
	p := a.prompt
	x, y := a.box(p.Title, width, 7)
	a.put(x+2, y+1, width-4, clip(p.Label, width-4), dialogStyle)
	inputWidth := width - 6
	a.fill(x+2, y+3, width-4, baseStyle.Foreground(white))
	start := 0
	for start < p.Pos && uniseg.StringWidth(string(p.Text[start:p.Pos])) >= inputWidth {
		start++
	}
	a.put(x+3, y+3, inputWidth, string(p.Text[start:]), baseStyle.Foreground(white))
	a.Screen.ShowCursor(x+3+uniseg.StringWidth(string(p.Text[start:p.Pos])), y+3)
	a.put(x+2, y+5, width-4, "Enter accept · Esc cancel · Ctrl-u clear", dialogStyle)
}
