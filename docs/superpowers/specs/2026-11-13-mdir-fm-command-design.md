# mdir-fm command name

The user approved `mdir-fm` as the public command name. The source repository is
`https://github.com/onejae/mdir`.

Build `bin/mdir-fm` from `cmd/mdir-fm`. Use `github.com/onejae/mdir` as the Go
module path and update internal imports accordingly. CLI usage, version output,
error prefixes, shell return messages, README launch examples, and the PTY smoke
test must use the new executable name.

Retain the Mdir interface branding and the existing `mdir` configuration/state
directories so saved settings and comments remain available. Preserve historical
design documents. This change does not implement packaging, macOS support, or
the previously reported filesystem fixes. The user's conditional commit approval
does not apply while those review findings remain unresolved.

Verify with the existing race tests, vet, build, CLI help/version checks, and PTY
smoke test. No new test framework or unit tests are needed for this naming change.
