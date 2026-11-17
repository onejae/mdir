# Homebrew installation

The requested installation command is `brew install onejae/tap/mdir` on macOS
and Linux. The installed executable remains `mdir-fm`, preserving the existing
command and XDG configuration/state paths.

Use a source formula in `onejae/homebrew-tap`, named `Formula/mdir.rb`. It builds
a tagged, SHA-256-verified source archive using Homebrew's Go dependency. This
keeps the initial release smaller than maintaining binary bottles. A generator
in the application repository downloads a published tag's archive, computes its
checksum, and writes the complete formula for the tap. The repository includes
the formula template and precise publication instructions; no guessed checksum
or unversioned branch archive is published.

macOS support must preserve no-overwrite moves, descriptor-anchored directory
operations, refusal to chmod symlinks, and copy/move/archive change detection.
Use native macOS operations where Linux APIs differ. Platform helpers should
remain confined to the filesystem package. The UI uses macOS `open` or Linux
`xdg-open` and obtains mounted filesystems through the host platform.

Darwin does not provide Linux's permission-independent `O_PATH` descriptor.
If an unreadable file cannot be pinned safely for chmod, return an error and
leave it unchanged; document that limitation instead of using a pathname-based
fallback that could change a replacement inode.
On Darwin, checking a directory destination's ancestry also requires read access
to each ancestor; reject the operation if that safety check cannot complete.

Validate with the existing race-enabled tests, vet, and PTY smoke test on Linux;
compile both applications and tests for macOS amd64/arm64. Add CI to execute
tests and the PTY smoke on actual macOS and Linux runners, and a tap workflow
that installs and tests the formula. Cross-compilation alone is not runtime
verification on macOS.

The source repository currently has no version tags. Prepare the first tagged
release as `v0.1.0`, matching the CLI version. Commit, push, tag publication,
and publication of the tap require the user's explicit Git permission after
the changes are reviewable. Local checks do not make the install command live.
