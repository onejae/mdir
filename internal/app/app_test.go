package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/onejae/mdir/internal/files"
)

func testApp(t *testing.T) (*App, tcell.SimulationScreen) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"alpha.txt": "first\nsecond\nlast\n", "beta.go": "package main\n", "한글 문서.txt": "안녕하세요\n", ".hidden": "hidden", "folder/inside.txt": "inside"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(100, 25)
	t.Cleanup(s.Fini)
	a, err := New(s, dir, Config{}, filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return a, s
}

func typeText(a *App, text string) {
	for _, r := range text {
		a.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
}
func key(a *App, k tcell.Key) { a.HandleKey(tcell.NewEventKey(k, 0, tcell.ModNone)) }

func focus(t *testing.T, a *App, name string) {
	t.Helper()
	for i, e := range a.entries {
		if e.Name == name {
			a.cursor = i
			return
		}
	}
	t.Fatalf("missing entry %q", name)
}

func input(a *App, text string) { key(a, tcell.KeyCtrlU); typeText(a, text); key(a, tcell.KeyEnter) }

func finishJob(t *testing.T, a *App) {
	t.Helper()
	timer := time.AfterFunc(5*time.Second, func() { a.Screen.PostEventWait(tcell.NewEventInterrupt("test timeout")) })
	defer timer.Stop()
	for a.busy != "" {
		event := a.Screen.PollEvent()
		if ev, ok := event.(*tcell.EventInterrupt); ok {
			if tag, ok := ev.Data().(string); ok && tag == "test timeout" {
				t.Fatal("job timed out")
			}
		}
		a.handleEvent(event)
	}
}

func TestVimNavigationSelectionSearch(t *testing.T) {
	a, _ := testApp(t)
	typeText(a, "j")
	if a.current().Name != "folder" {
		t.Fatalf("j: %s", a.current().Name)
	}
	typeText(a, "l")
	if filepath.Base(a.Cwd) != "folder" {
		t.Fatal("l did not open directory")
	}
	typeText(a, "h")
	if a.current().Name != "folder" {
		t.Fatal("h did not return cursor to departed folder")
	}
	typeText(a, "gg2j")
	if a.current().Name != "alpha.txt" {
		t.Fatalf("count: %s", a.current().Name)
	}
	typeText(a, " ")
	if len(a.selected) != 1 || a.current().Name != "beta.go" {
		t.Fatal("selection did not advance")
	}
	key(a, tcell.KeyEscape)
	if len(a.selected) != 0 {
		t.Fatal("Esc did not clear selection")
	}
	typeText(a, "/한글")
	key(a, tcell.KeyEnter)
	if a.current().Name != "한글 문서.txt" {
		t.Fatal("Unicode search failed")
	}
	typeText(a, "ggG")
	if a.cursor != len(a.entries)-1 {
		t.Fatal("G did not reach end")
	}
	typeText(a, ".")
	focus(t, a, ".hidden")
}

func TestPromptTreatsVimKeysAsText(t *testing.T) {
	a, _ := testApp(t)
	before := a.cursor
	var result string
	a.ask("Test", "Filename", "", func(s string) { result = s })
	typeText(a, "hjkl / 한글")
	key(a, tcell.KeyLeft)
	typeText(a, "X")
	key(a, tcell.KeyEnter)
	if result != "hjkl / 한X글" {
		t.Fatalf("prompt result %q", result)
	}
	if a.cursor != before || a.Cwd == "/" {
		t.Fatal("prompt triggered browser commands")
	}
}

func TestDeleteRequiresExplicitConfirm(t *testing.T) {
	a, _ := testApp(t)
	focus(t, a, "alpha.txt")
	path := a.current().Path
	typeText(a, "x")
	if a.menu == nil || a.menu.Cursor != 0 {
		t.Fatal("delete must default to cancel")
	}
	key(a, tcell.KeyEnter)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("cancel deleted file")
	}
	typeText(a, "xj")
	key(a, tcell.KeyEnter)
	finishJob(t, a)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("confirmed deletion failed")
	}
}

func TestCopyCollisionMoveAndClipboardThroughUI(t *testing.T) {
	a, _ := testApp(t)
	focus(t, a, "alpha.txt")
	typeText(a, "c")
	input(a, "copy.txt")
	finishJob(t, a)
	data, err := os.ReadFile(filepath.Join(a.Cwd, "copy.txt"))
	if err != nil || string(data) != "first\nsecond\nlast\n" {
		t.Fatalf("copy: %q %v", data, err)
	}
	focus(t, a, "beta.go")
	typeText(a, "c")
	input(a, "copy.txt")
	finishJob(t, a)
	if !a.statusError {
		t.Fatal("collision not reported")
	}
	data, _ = os.ReadFile(filepath.Join(a.Cwd, "copy.txt"))
	if string(data) != "first\nsecond\nlast\n" {
		t.Fatal("collision overwrote file")
	}
	focus(t, a, "copy.txt")
	typeText(a, "r")
	input(a, "renamed.txt")
	finishJob(t, a)
	if _, err := os.Stat(filepath.Join(a.Cwd, "copy.txt")); !os.IsNotExist(err) {
		t.Fatal("rename retained old filename")
	}
	focus(t, a, "renamed.txt")
	typeText(a, "dd")
	focus(t, a, "folder")
	typeText(a, "lp")
	finishJob(t, a)
	if _, err := os.Stat(filepath.Join(a.Cwd, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	if len(a.clipboard) != 0 {
		t.Fatal("completed cut clipboard was retained")
	}
}

func TestCommandsCommentsAndPermissions(t *testing.T) {
	a, _ := testApp(t)
	a.command("mkdir new folder")
	if info, err := os.Stat(filepath.Join(a.Cwd, "new folder")); err != nil || !info.IsDir() {
		t.Fatal("mkdir with spaces failed")
	}
	a.command("touch new document.txt")
	focus(t, a, "new document.txt")
	a.command("comment 한글 note")
	s, err := loadState(a.StateFile)
	if err != nil || s.Comments[a.current().Path] != "한글 note" {
		t.Fatalf("comment: %v %#v", err, s)
	}
	a.command("chmod 600")
	info, err := os.Stat(filepath.Join(a.Cwd, "new document.txt"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("chmod failed")
	}
	a.command("select *.txt")
	if len(a.selected) < 2 {
		t.Fatal("wildcard selection failed")
	}
	a.command("sort size")
	if a.sortBy != "size" {
		t.Fatal("sort command failed")
	}
}

func TestViewerAndArchiveNavigation(t *testing.T) {
	a, _ := testApp(t)
	focus(t, a, "alpha.txt")
	key(a, tcell.KeyF3)
	if a.viewer == nil || a.viewer.Hex {
		t.Fatal("text viewer not opened")
	}
	typeText(a, "/last")
	key(a, tcell.KeyEnter)
	if a.viewer.Top != 2 {
		t.Fatal("viewer search failed")
	}
	typeText(a, "H")
	if !a.viewer.Hex || !strings.Contains(a.viewer.Lines[0], "00000000") {
		t.Fatal("hex view failed")
	}
	typeText(a, "q")
	archivePath := filepath.Join(a.Cwd, "demo.zip")
	if err := files.CreateArchive(context.Background(), "zip", []string{filepath.Join(a.Cwd, "folder")}, archivePath); err != nil {
		t.Fatal(err)
	}
	a.refresh()
	focus(t, a, "demo.zip")
	typeText(a, "l")
	if a.archive == nil {
		t.Fatal("archive not entered")
	}
	focus(t, a, "folder")
	typeText(a, "l")
	focus(t, a, "inside.txt")
	key(a, tcell.KeyF3)
	if a.viewer == nil || string(a.viewer.Data) != "inside" {
		t.Fatal("archive member viewer failed")
	}
	typeText(a, "q")
	a.command("extract " + filepath.Join(a.Cwd, "out"))
	finishJob(t, a)
	if data, err := os.ReadFile(filepath.Join(a.Cwd, "out", "folder", "inside.txt")); err != nil || string(data) != "inside" {
		t.Fatalf("extraction failed: %s %v", data, err)
	}
	typeText(a, "hh")
	if a.archive != nil || a.current().Name != "demo.zip" {
		t.Fatal("leaving archive did not restore cursor")
	}
}

func TestTreeAndFindResultNavigation(t *testing.T) {
	a, _ := testApp(t)
	original := a.Cwd
	typeText(a, "t")
	if a.tree == nil || a.tree.Rows[a.tree.Cursor].Path != original {
		t.Fatal("tree not focused on current directory")
	}
	typeText(a, "j")
	key(a, tcell.KeyEnter)
	if a.Cwd != filepath.Join(original, "folder") {
		t.Fatalf("tree enter: %s", a.Cwd)
	}
	typeText(a, "h")
	a.command("find inside")
	finishJob(t, a)
	if a.menu == nil || len(a.menu.Choices) != 1 {
		t.Fatal("recursive results missing")
	}
	key(a, tcell.KeyEnter)
	if a.current().Name != "inside.txt" {
		t.Fatal("find result not focused")
	}
}

func TestPaletteUnicodeAndResize(t *testing.T) {
	a, s := testApp(t)
	s.SetSize(80, 25)
	a.Draw()
	_, _, style, _ := s.GetContent(0, 0)
	_, bg, _ := style.Decompose()
	if bg != cyan {
		t.Fatalf("launcher bar %v", bg)
	}
	_, _, style, _ = s.GetContent(0, 3)
	_, bg, _ = style.Decompose()
	if bg != red {
		t.Fatal("cursor must be DOS bright red")
	}
	focus(t, a, "한글 문서.txt")
	a.Draw()
	x, y := 1, 3+a.cursor
	r, _, _, width := s.GetContent(x, y)
	if r != '한' || width != 2 {
		t.Fatalf("Korean cell: %q width %d", r, width)
	}
	for _, size := range [][2]int{{1, 1}, {10, 4}, {32, 10}, {40, 12}, {160, 48}} {
		s.SetSize(size[0], size[1])
		a.Draw()
		a.mainMenu()
		a.Draw()
		key(a, tcell.KeyEscape)
		a.ask("Prompt", "long filename", strings.Repeat("한", 100), func(string) {})
		a.Draw()
		key(a, tcell.KeyEscape)
	}
	if got := clip("ab한글", 5); got != "ab한…" {
		t.Fatalf("Unicode clip: %q", got)
	}
	if got := displayText("a\x1b[31mb\n"); strings.ContainsAny(got, "\x1b\n") {
		t.Fatal("control characters leaked into rendering")
	}
}

func TestEventLoopSavesHistoryAndQuits(t *testing.T) {
	a, s := testApp(t)
	go func() {
		s.PostEventWait(tcell.NewEventKey(tcell.KeyRune, 'j', 0))
		s.PostEventWait(tcell.NewEventKey(tcell.KeyRune, 'l', 0))
		s.PostEventWait(tcell.NewEventKey(tcell.KeyRune, 'q', 0))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(a.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.History) == 0 || filepath.Base(state.History[0]) != "folder" {
		t.Fatal("history not saved")
	}
}

func TestInterruptedVimOperatorDoesNotExecute(t *testing.T) {
	a, _ := testApp(t)
	focus(t, a, "alpha.txt")
	typeText(a, "d")
	key(a, tcell.KeyDown)
	typeText(a, "d")
	if len(a.clipboard) != 0 {
		t.Fatal("interrupted dd cut files")
	}
}

func TestViewerBoundedAndSpecialFilesRefused(t *testing.T) {
	a, _ := testApp(t)
	path := filepath.Join(a.Cwd, "large.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(viewerLimit)+100)), 0644); err != nil {
		t.Fatal(err)
	}
	a.refresh()
	focus(t, a, "large.txt")
	a.viewCurrent()
	if !a.viewer.Truncated || int64(len(a.viewer.Data)) != viewerLimit {
		t.Fatal("viewer read bound failed")
	}
}

func TestJobCancellationReturnsToBrowser(t *testing.T) {
	a, _ := testApp(t)
	a.startJob("Cancelable test", func(ctx context.Context) (string, error) { <-ctx.Done(); return "", ctx.Err() }, nil)
	key(a, tcell.KeyEscape)
	finishJob(t, a)
	if !a.statusError || !strings.Contains(a.status, "canceled") {
		t.Fatalf("cancellation status %q", a.status)
	}
	typeText(a, "j")
	if a.cursor != 1 {
		t.Fatal("browser stayed blocked after cancellation")
	}
}

func TestMainMenuHasWorkingEntryPoints(t *testing.T) {
	a, _ := testApp(t)
	a.mainMenu()
	if len(a.menu.Choices) < 20 {
		t.Fatal("missing command menu entries")
	}
	for _, choice := range a.menu.Choices {
		if choice.Action == nil {
			t.Fatal(fmt.Sprintf("missing action %s", choice.Label))
		}
	}
}

func TestTreeFocusThroughSymlinkAncestor(t *testing.T) {
	a, _ := testApp(t)
	physical := filepath.Join(a.Cwd, "folder", "nested")
	if err := os.Mkdir(physical, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("folder", filepath.Join(a.Cwd, "alias")); err != nil {
		t.Fatal(err)
	}
	a.changeDir(filepath.Join(a.Cwd, "alias", "nested"))
	a.openTree()
	if a.tree == nil || a.tree.Rows[a.tree.Cursor].Path != physical {
		t.Fatal("tree lost directory behind symlink")
	}
	key(a, tcell.KeyEnter)
	if a.Cwd != physical {
		t.Fatalf("tree entered %s", a.Cwd)
	}
}

func TestViewerNoMatchIsVisible(t *testing.T) {
	a, s := testApp(t)
	focus(t, a, "alpha.txt")
	a.viewCurrent()
	typeText(a, "/absent phrase")
	key(a, tcell.KeyEnter)
	a.Draw()
	w, h := s.Size()
	var row strings.Builder
	for x := 0; x < w; x++ {
		r, _, _, _ := s.GetContent(x, h-2)
		row.WriteRune(r)
	}
	if !strings.Contains(row.String(), "No match: absent phrase") {
		t.Fatalf("missing feedback: %s", row.String())
	}
}

func TestFindLimitRetainsPartialWarning(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires Unix permissions to reject a directory")
	}
	a, _ := testApp(t)
	locked := filepath.Join(a.Cwd, "a-locked")
	if err := os.Mkdir(locked, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0700)
	for i := 0; i < 1001; i++ {
		if err := os.WriteFile(filepath.Join(a.Cwd, fmt.Sprintf("match-%04d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	a.command("find match")
	finishJob(t, a)
	if a.menu == nil || len(a.menu.Choices) != 1000 {
		t.Fatal("limited results missing")
	}
	if !a.statusError || !strings.Contains(a.status, "Partial") || !strings.Contains(a.status, "1000") {
		t.Fatalf("warning lost: %s", a.status)
	}
}

func TestHalfPageAcrossColumnCounts(t *testing.T) {
	a, s := testApp(t)
	for i := 0; i < 100; i++ {
		a.entries = append(a.entries, files.Entry{Name: fmt.Sprintf("item%d", i)})
	}
	for _, size := range []struct{ width, half int }{{40, 9}, {80, 18}, {160, 36}} {
		s.SetSize(size.width, 25)
		a.cursor = 0
		key(a, tcell.KeyCtrlD)
		if a.cursor != size.half {
			t.Fatalf("width %d: half page moved %d, want %d", size.width, a.cursor, size.half)
		}
		key(a, tcell.KeyCtrlU)
		if a.cursor != 0 {
			t.Fatal("half page up did not return")
		}
	}
}

func TestSmallTerminalRefusesHiddenConfirmation(t *testing.T) {
	a, s := testApp(t)
	focus(t, a, "alpha.txt")
	typeText(a, "x")
	s.SetSize(20, 5)
	typeText(a, "j")
	key(a, tcell.KeyEnter)
	if a.busy != "" || a.menu == nil || a.menu.Cursor != 0 {
		t.Fatal("invisible delete confirmation accepted")
	}
	a.menu = nil
	a.ask("Rename", "Filename", "", func(string) {})
	typeText(a, "q")
	if a.quit {
		t.Fatal("typing q into a resized prompt quit the application")
	}
}
