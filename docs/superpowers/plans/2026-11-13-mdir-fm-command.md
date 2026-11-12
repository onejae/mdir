# mdir-fm Command Implementation Plan

> Follow the approved command-name design. Apply the surgical edits in this
> session, then review the resulting changes and run the existing checks.

**Goal:** Expose the approved `mdir-fm` command from the `onejae/mdir` project.

**Architecture:** Rename the CLI entry-point directory and build output. Align
the module/import paths with the repository while retaining existing application
branding and stored configuration paths.

**Tech Stack:** Go, Make, Python PTY smoke test.

## Task 1: Rename the command and update references

- [x] Move `cmd/mdir/main.go` to `cmd/mdir-fm/main.go`; change CLI usage, error,
  and version strings to `mdir-fm`.
- [x] Set `module github.com/onejae/mdir` in `go.mod`; replace imports starting
  with `mdir/internal/` with `github.com/onejae/mdir/internal/` in the CLI and app.
- [x] Change the Makefile build command to
  `go build -buildvcs=false -o bin/mdir-fm ./cmd/mdir-fm`; let `clean` remove both
  the new output and the legacy `bin/mdir` artifact.
- [x] Update `README.md` launch paths and explain the public command name.
  Update the default binary in `scripts/smoke.py` to `bin/mdir-fm`.
- [x] Update shell subprocess labels and the return prompt in
  `internal/app/commands.go` to `mdir-fm`.

## Task 2: Verify and review

- [x] Run `rtk proxy gofmt -l cmd internal`; expect no output.
- [x] Run `rtk proxy make test check build` with writable Go caches; expect all
  existing tests, vet, and build to pass. Record any environment blocker.
- [x] Run `rtk proxy ./bin/mdir-fm -version` and `rtk proxy ./bin/mdir-fm -h`;
  expect `mdir-fm 0.1.0` and usage beginning with `mdir-fm`.
- [x] Run `rtk proxy python3 scripts/smoke.py`; expect its PTY checks to pass.
- [x] Compare the edited files against the saved source snapshot; check the
  command/module references and confirm configuration/state paths are preserved.
- [x] Report the rename, verification, and unresolved publication findings.

Verification passed using temporary Go caches and a local module proxy populated
with the pinned dependencies from proxy.golang.org. `go mod verify` also passed;
`go.sum` and dependency versions were preserved. The command-name review found
no new material issues. The earlier move/archive findings remain unresolved, so
no commit or publication was performed.
