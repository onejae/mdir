# Mdir Linux Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan
> task by task in this session. The user has approved implementation; no further
> approval or Git action is implied.

**Goal:** Deliver a runnable Linux Mdir with classic DOS colors and Vim navigation.

**Architecture:** Keep filesystem and archive operations separate from terminal
state. Use a single UI event loop and interrupt events for background jobs. Render
directly to tcell so the original column layout and palette remain controllable.

**Tech stack:** Go, tcell v2, uniseg, Go standard archive and filesystem packages.

## Task 1: Bootstrap and filesystem model

Files: `go.mod`, `Makefile`, `cmd/mdir/main.go`, `internal/files/files.go`,
`internal/files/files_test.go`.

- [x] Pin terminal dependencies and configure build/test commands with writable
  Go caches supplied by the execution environment.
- [x] Add directory listing, typed entries, sort and filtering. Preserve directory
  priority and handle dangling symlinks without failing the whole listing.
- [x] Implement copy, move, rename and delete using lstat, exclusive destination
  creation, recursive-copy guards, context cancellation and explicit errors.
- [x] Test normal operations, collisions, descendant destinations, symbolic links,
  and cancellation with `go test ./internal/files`.

## Task 2: Archives, search and persistence

Files: `internal/files/archive.go`, `internal/files/search.go`, archive/search
tests, `internal/app/config.go`, `internal/app/config_test.go`.

- [x] List ZIP/TAR/TGZ members as virtual directory entries, bound viewer reads,
  and extract with a rooted destination, traversal checks, exclusive files and
  rejection of archive links/special files.
- [x] Create archives from selected paths; preserve names, directories and content;
  refuse self inclusion and collisions. Round-trip ZIP, TAR and TGZ in tests.
- [x] Implement recursive filename search and mount discovery for Linux.
- [x] Persist comments/history and load optional launcher commands using XDG paths;
  avoid all environment-file content access during development and verification.

## Task 3: Terminal application

Files: `internal/app/app.go`, `render.go`, `input.go`, `actions.go`, `viewer.go`,
`tree.go`, `commands.go`.

- [x] Implement lifecycle, metadata-only browsing and scalable column layout.
- [x] Add exact DOS color roles and Unicode cell-width rendering.
- [x] Implement Vim/arrow/Alt/function-key input; isolate prompt input from commands.
- [x] Add selection, sort, help/menu, copy/move/rename/delete/mkdir, metadata/chmod,
  comments, quick directories and mount chooser.
- [x] Add tree navigation, archive browsing/extraction/creation, filename search,
  text/hex viewer, external editor/program/shell suspension, job cancellation.
- [x] Verify UI with SimulationScreen key events and visible cell assertions.

## Task 4: Delivery and verification

Files: `README.md`, `config.example.json`, `.gitignore`, `scripts/smoke.py`, tests.

- [x] Document installation, keymap, command syntax, supported archive formats,
  configuration, collision behavior and feature-parity limits.
- [x] Run `go test -race ./...`, `go vet ./...`, and build `bin/mdir`.
- [x] Run a bounded pseudo-terminal smoke test for browsing, file operations,
  viewer, tree, resizing and exit; inspect a captured screen.
- [x] Review requirements against implemented routes and test results, correcting
  any material gaps. Report exact verification and launch command to the user.
