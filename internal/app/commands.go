package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func (a *App) command(input string) {
	input = strings.TrimSpace(strings.TrimPrefix(input, ":"))
	if input == "" {
		return
	}
	if strings.HasPrefix(input, "!") {
		a.runShell(strings.TrimPrefix(input, "!"))
		return
	}
	name, arg, _ := strings.Cut(input, " ")
	arg = strings.TrimSpace(arg)
	switch name {
	case "q", "quit":
		a.quit = true
	case "help":
		a.help()
	case "cd":
		if arg == "" {
			arg = "~"
		}
		a.changeDir(arg)
	case "tree":
		a.openTree()
	case "jump":
		a.quickDir()
	case "mounts":
		a.mounts()
	case "hidden":
		a.toggleHidden()
	case "refresh":
		a.refresh()
	case "reverse":
		a.reverse = !a.reverse
		a.refresh()
	case "sort":
		if arg == "" {
			a.sortMenu()
			return
		}
		if arg != "name" && arg != "ext" && arg != "size" && arg != "time" {
			a.message("Sort: name, ext, size, or time", true)
			return
		}
		a.sortBy = arg
		a.refresh()
	case "mkdir":
		a.makeDir()
		a.acceptArgument(arg)
	case "touch":
		a.newFile()
		a.acceptArgument(arg)
	case "copy":
		a.transfer(false)
		a.acceptArgument(arg)
	case "move":
		a.transfer(true)
		a.acceptArgument(arg)
	case "rename":
		a.renameSelected()
		a.acceptArgument(arg)
	case "delete":
		a.deleteSelected()
	case "view":
		a.viewCurrent()
	case "edit":
		a.editCurrent()
	case "open":
		a.openExternal()
	case "info":
		a.info()
	case "comment":
		a.comment()
		a.acceptArgument(arg)
	case "chmod":
		a.chmodSelected()
		a.acceptArgument(arg)
	case "pack":
		a.archiveMenu()
	case "extract":
		a.extract(false)
		a.acceptArgument(arg)
	case "extract-all":
		a.extract(true)
		a.acceptArgument(arg)
	case "select":
		a.selectPattern()
		a.acceptArgument(arg)
	case "find":
		a.findFiles()
		a.acceptArgument(arg)
	case "shell":
		a.interactiveShell()
	default:
		a.message("Unknown command: "+name+". :help lists commands; use ! for shell commands.", true)
	}
}

func (a *App) acceptArgument(arg string) {
	if arg != "" && a.prompt != nil {
		p := a.prompt
		a.prompt = nil
		p.Accept(arg)
	}
}

func (a *App) editCurrent() {
	if !a.localOnly() {
		return
	}
	e := a.current()
	info, err := os.Stat(e.Path)
	if err != nil {
		a.message(err.Error(), true)
		return
	}
	if !info.Mode().IsRegular() {
		a.message("Select a regular file to edit", true)
		return
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		if _, err := exec.LookPath("vim"); err == nil {
			editor = "vim"
		} else {
			editor = "vi"
		}
	}
	// The filename is a positional argument; names are never interpolated into shell code.
	cmd := exec.Command("/bin/sh", "-c", `exec `+editor+` "$1"`, "mdir-fm-editor", e.Path)
	a.external(cmd, false)
}

func (a *App) openExternal() {
	if !a.localOnly() {
		return
	}
	e := a.current()
	info, err := os.Stat(e.Path)
	if err != nil {
		a.message(err.Error(), true)
		return
	}
	if info.IsDir() {
		a.changeDir(e.Path)
		return
	}
	if !info.Mode().IsRegular() {
		a.message("Only regular files can be opened externally", true)
		return
	}
	if info.Mode().Perm()&0111 != 0 {
		a.external(exec.Command(e.Path), true)
	} else {
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		if _, err := exec.LookPath(opener); err != nil {
			a.message(opener+" is unavailable; use F3 to view or e to edit", true)
			return
		}
		a.external(exec.Command(opener, e.Path), false)
	}
}

func (a *App) runShell(command string) {
	if strings.TrimSpace(command) == "" {
		return
	}
	// User-entered/configured shell text is intentional. Current path is passed
	// separately for launchers as $1 and selected paths as $2, $3, ... .
	args := []string{"-c", command, "mdir-fm", a.Cwd}
	for _, e := range a.targets() {
		if a.archive == nil {
			args = append(args, e.Path)
		}
	}
	a.external(exec.Command("/bin/sh", args...), true)
}

func (a *App) interactiveShell() {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	a.external(exec.Command(shell), false)
}

func (a *App) external(cmd *exec.Cmd, pause bool) {
	if a.busy != "" {
		return
	}
	if err := a.Screen.Suspend(); err != nil {
		a.message(err.Error(), true)
		return
	}
	cmd.Dir = a.Cwd
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	if pause {
		fmt.Print("\n[mdir-fm] Press Enter to return…")
		var b [1]byte
		for {
			n, readErr := os.Stdin.Read(b[:])
			if readErr != nil || n == 1 && (b[0] == '\n' || b[0] == '\r') {
				break
			}
		}
	}
	if resumeErr := a.Screen.Resume(); resumeErr != nil {
		a.quit = true
		a.message(resumeErr.Error(), true)
		return
	}
	a.Screen.Sync()
	if reloadErr := a.reload(a.current().Path); reloadErr != nil {
		a.message(reloadErr.Error(), true)
		return
	}
	if err != nil {
		a.message(fmt.Sprintf("%s: %v", filepath.Base(cmd.Path), err), true)
	} else {
		a.message("Returned from "+filepath.Base(cmd.Path), false)
	}
}
