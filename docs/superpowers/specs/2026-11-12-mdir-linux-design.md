# Mdir for Linux

Approved in conversation on 2026-11-12: a fresh Go terminal application inspired
by Korean MDIR III 3.x, with its original colors and Vim navigation.

## Interface

One directory displayed in multiple columns, ordered top to bottom. A compact
terminal uses one column; wider terminals use two or more. A cyan launcher bar,
gray path and status bars, black list, cyan separators, colored file types and a
red cursor reproduce the classic screen. The MCD tree uses a bright blue
background and yellow cursor. Colors use explicit DOS RGB values, with terminal
palette fallback. Unicode filenames, including Korean and combining characters,
must align by terminal cell width. Terminal resize must never panic.

Browse keys: j/k move, h goes to parent, l/Enter opens, gg/G jump, Ctrl-d/u
half-page, Ctrl-f/b page, / searches, n/N repeat, Space selects, : opens a command
prompt, Esc cancels. Arrow keys remain available. F1 is help, F10 tree, F11 quick
directory switching, F12 menu. Alt shortcuts expose file operations. Text entry
uses ordinary editing keys; browse commands never consume typed prompt text.

## Functional scope

- Browse, sort by name/extension/size/date, reverse sort, toggle dotfiles, select
  individual files, select all/invert, and select wildcard matches.
- Copy/move files and directory trees, rename, delete with explicit confirmation,
  create directories/files, inspect metadata, and change Unix permission bits.
  Existing destinations are refused rather than silently overwritten. Symlinks
  are copied as links; directory operations do not follow links recursively.
- MCD tree with lazy expansion, search, and navigation; quick directory history;
  mounted filesystem chooser and free-space display.
- Text/hex file viewer with Vim scrolling and search; external editing through
  VISUAL/EDITOR, falling back to vim then vi. Launch executable files, open other
  files with xdg-open, and run shell commands in the current directory.
- ZIP and TAR (including gzip) directory browsing, viewing archive members,
  extracting selected members or the entire archive, and creating ZIP/TAR/TGZ
  archives. Extraction rejects escaping paths, links, and existing destinations.
- Recursive filename search with per-directory error handling and no recursion
  through symlinks; cancelable background file operations/searches.
- Persistent file comments, recent directories, and configurable function-key
  program launchers stored under the XDG config/state directories. Settings use
  JSON; shell launch commands are explicitly configured by the user.

Original DOS executables, floppy formatting/copying, memory swapping, and legacy
archive codecs are not emulated. Linux mount paths and permissions replace DOS
drive letters and attribute bits. Archive editing in place is outside this build;
extract and recreate archives instead. This is a working reimplementation of the
approved portable feature set, not a claim of every historical MDIR behavior.

## Architecture

Go with tcell v2 and uniseg. `cmd/mdir` handles CLI and terminal lifecycle.
`internal/files` owns filesystem operations, listings, archive handling and
search. `internal/app` owns state, key dispatch, rendering, dialogs, tree, viewer,
and external commands. Persistence is a small JSON store. UI state is mutated on
the event loop; background jobs return through tcell interrupt events.

Filesystem failures appear in the status/dialog without closing the program.
Deletion confirmation defaults to cancel. Job cancellation reports partial work;
success is never displayed after an error. Terminal state is restored on normal
exit and while running external programs. File contents are read only when the
user explicitly opens/views/edits them; directory browsing reads metadata only.

## Verification

Use temporary directories for filesystem tests, including non-overwrite behavior,
symlinks, recursive-copy prevention, cross-directory moves, canceled operations,
ZIP/TAR round trips and hostile archive paths. Use tcell SimulationScreen for
key dispatch, prompts, selection, search, rendering colors, Korean names, and
small/large terminal sizes. Finish with `go test -race ./...`, `go vet ./...`, a
build, and a real pseudo-terminal smoke test against a bounded fixture directory.

## References

- Original screens and archived manual: https://oldpc.tistory.com/144 and
  https://oldpc.tistory.com/17 (viewed 2026-11-12).
- Existing clone considered: https://github.com/la9527/linm.
- Terminal library: https://github.com/gdamore/tcell/tree/v2.13.10.

The original archive was inspected only for documentation and visual reference;
no DOS executable or original source is part of this implementation.
