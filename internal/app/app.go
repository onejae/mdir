package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/onejae/mdir/internal/files"
)

type Prompt struct {
	Title, Label string
	Text         []rune
	Pos          int
	Accept       func(string)
}

type Choice struct {
	Label  string
	Action func()
}

type Menu struct {
	Title   string
	Choices []Choice
	Cursor  int
}

type jobResult struct {
	err     error
	message string
	after   func(error)
}

type App struct {
	Screen          tcell.Screen
	Cwd             string
	Config          Config
	State           State
	StateFile       string
	entries         []files.Entry
	cursor, offset  int
	selected        map[string]bool
	hidden, reverse bool
	sortBy          string
	query, status   string
	statusError     bool
	prompt          *Prompt
	menu            *Menu
	viewer          *Viewer
	tree            *Tree
	archive         *files.Archive
	archivePrefix   string
	pending         rune
	count           string
	quit            bool
	busy            string
	cancel          context.CancelFunc
	ctx             context.Context
	clipboard       []string
	cut             bool
}

func New(screen tcell.Screen, dir string, cfg Config, statePath string) (*App, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	s, stateErr := loadState(statePath)
	if stateErr != nil {
		return nil, fmt.Errorf("load history/comments: %w", stateErr)
	}
	a := &App{Screen: screen, Cwd: abs, Config: cfg, State: s,
		StateFile: statePath, selected: make(map[string]bool), sortBy: "name", ctx: context.Background()}
	if err := a.reload(""); err != nil {
		return nil, err
	}
	a.remember(abs)
	a.message("Welcome to Mdir.  ? help  ·  j/k move  h parent  l open  : commands", false)
	return a, nil
}

func (a *App) Run(ctx context.Context) error {
	a.ctx = ctx
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			a.Screen.PostEventWait(tcell.NewEventInterrupt("quit"))
		case <-done:
		}
	}()
	for !a.quit {
		a.Draw()
		ev := a.Screen.PollEvent()
		if ev == nil {
			break
		}
		a.handleEvent(ev)
	}
	return saveState(a.StateFile, a.State)
}

func (a *App) handleEvent(event tcell.Event) {
	switch ev := event.(type) {
	case *tcell.EventResize:
		a.Screen.Sync()
	case *tcell.EventKey:
		a.HandleKey(ev)
	case *tcell.EventMouse:
		a.handleMouse(ev)
	case *tcell.EventInterrupt:
		switch result := ev.Data().(type) {
		case string:
			if result == "quit" {
				if a.cancel != nil {
					a.cancel()
				}
				if a.busy == "" {
					a.quit = true
				}
			}
		case jobResult:
			a.busy, a.cancel = "", nil
			if a.ctx.Err() != nil {
				a.quit = true
			}
			reloadErr := a.reload("")
			if result.err != nil {
				a.message(result.err.Error(), true)
			} else if reloadErr != nil {
				a.message(reloadErr.Error(), true)
			} else {
				a.message(result.message, false)
			}
			if result.after != nil {
				result.after(result.err)
			}
		}
	}
}

func (a *App) startJob(label string, work func(context.Context) (string, error), after func(error)) {
	if a.busy != "" {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.busy, a.cancel = label, cancel
	a.message(label+" — Esc to cancel", false)
	go func() {
		message, err := work(ctx)
		cancel()
		if errors.Is(err, context.Canceled) {
			err = fmt.Errorf("canceled; completed items were kept")
		}
		a.Screen.PostEventWait(tcell.NewEventInterrupt(jobResult{err, message, after}))
	}()
}

func (a *App) reload(focus string) error {
	if a.archive != nil {
		entries, err := a.archive.List(a.archivePrefix)
		if err != nil {
			return err
		}
		visible := entries[:0]
		for _, entry := range entries {
			if a.hidden || !strings.HasPrefix(entry.Name, ".") {
				visible = append(visible, entry)
			}
		}
		entries = visible
		files.Sort(entries, a.sortBy, a.reverse)
		a.entries = append([]files.Entry{{Name: "..", IsDir: true}}, entries...)
	} else {
		entries, err := files.List(a.Cwd, a.hidden, a.sortBy, a.reverse)
		if err != nil {
			return err
		}
		parent := files.Entry{Name: "..", Path: filepath.Dir(a.Cwd), IsDir: true}
		a.entries = append([]files.Entry{parent}, entries...)
	}
	if focus != "" {
		for i, e := range a.entries {
			if e.Path == focus || e.Name == focus {
				a.cursor = i
				break
			}
		}
	}
	a.cursor = min(max(0, a.cursor), max(0, len(a.entries)-1))
	valid := make(map[string]bool)
	for _, entry := range a.entries {
		if a.selected[entry.Path] {
			valid[entry.Path] = true
		}
	}
	a.selected = valid
	return nil
}

func (a *App) changeDir(dir string) {
	dir = a.expandPath(dir)
	entries, err := files.List(dir, a.hidden, a.sortBy, a.reverse)
	if err != nil {
		a.message(err.Error(), true)
		return
	}
	previous := a.Cwd
	a.Cwd, a.archive, a.archivePrefix, a.tree = filepath.Clean(dir), nil, "", nil
	a.entries = append([]files.Entry{{Name: "..", Path: filepath.Dir(a.Cwd), IsDir: true}}, entries...)
	a.cursor, a.offset, a.selected = 0, 0, make(map[string]bool)
	if filepath.Dir(previous) == a.Cwd {
		for i, e := range a.entries {
			if e.Path == previous {
				a.cursor = i
				break
			}
		}
	}
	a.remember(a.Cwd)
	a.message("", false)
}

func (a *App) remember(dir string) {
	history := []string{dir}
	for _, prev := range a.State.History {
		if prev != dir && len(history) < 40 {
			history = append(history, prev)
		}
	}
	a.State.History = history
}

func (a *App) expandPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.Cwd, path)
	}
	return filepath.Clean(path)
}

func (a *App) current() files.Entry {
	if a.cursor >= 0 && a.cursor < len(a.entries) {
		return a.entries[a.cursor]
	}
	return files.Entry{}
}

func (a *App) targets() []files.Entry {
	var entries []files.Entry
	for _, e := range a.entries {
		if e.Name != ".." && a.selected[e.Path] {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 && a.current().Name != ".." && a.current().Name != "" {
		entries = append(entries, a.current())
	}
	return entries
}

func (a *App) message(text string, isError bool) { a.status, a.statusError = text, isError }

func (a *App) ask(title, label, initial string, accept func(string)) {
	a.pending, a.count = 0, ""
	a.prompt = &Prompt{title, label, []rune(initial), len([]rune(initial)), accept}
}

func (a *App) confirm(title, detail string, action func()) {
	a.menu = &Menu{Title: title + " · " + detail, Choices: []Choice{
		{Label: "Cancel", Action: func() {}}, {Label: "Confirm", Action: action},
	}}
}

func (a *App) move(delta int) {
	a.cursor = min(max(0, a.cursor+delta), max(0, len(a.entries)-1))
}

func (a *App) layout() (rows, columns, columnWidth int) {
	w, h := a.Screen.Size()
	rows = max(1, h-7)
	columns = max(1, min(4, w/40))
	columnWidth = max(1, w/columns)
	return
}

func humanSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d", size)
	}
	value := float64(size)
	for _, unit := range []string{"K", "M", "G", "T", "P"} {
		value /= 1024
		if value < 1024 {
			return fmt.Sprintf("%.1f%s", value, unit)
		}
	}
	return fmt.Sprintf("%.1fE", value/1024)
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("06-01-02 15:04")
}
