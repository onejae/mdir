# Publishing the Homebrew tap

`brew install onejae/tap/mdir` resolves to `Formula/mdir.rb` in the public GitHub
repository `onejae/homebrew-tap`. The formula installs the `mdir-fm` executable.
See Homebrew's [tap guide](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap).

This directory prepares that separate repository; adding it to `onejae/mdir`
alone does not publish the tap. `mdir.rb.in` is a template, not an installable
formula. Generate the real formula from the published source archive so its
SHA-256 matches the bytes Homebrew will download.

## First release

1. Review and commit the macOS/Linux changes. Publish the source commit and wait
   for `.github/workflows/ci.yml` to pass on all four runners. The local Linux
   tests and Darwin cross-compilation cannot replace native macOS tests.
2. Tag that tested commit `v0.1.0` (matching `mdir-fm -version`) and publish the tag:

   ```sh
   git tag -a v0.1.0 -m "mdir-fm 0.1.0"
   git push origin v0.1.0
   ```

3. Create or check out `onejae/homebrew-tap` as a separate public repository.
   From the `mdir` source checkout, prepare its contents:

   ```sh
   mkdir -p ../homebrew-tap/Formula ../homebrew-tap/.github/workflows
   cp packaging/homebrew/tap/README.md ../homebrew-tap/README.md
   cp packaging/homebrew/tap/.github/workflows/tests.yml ../homebrew-tap/.github/workflows/tests.yml
   python3 scripts/homebrew-formula.py v0.1.0 --output ../homebrew-tap/Formula/mdir.rb
   ```

   The generator refuses invalid tags, missing archives, and a mismatched CLI
   version. A failed download leaves an existing output file unchanged.

4. Before publishing the tap, use a machine with Homebrew to test the local
   checkout (replace the path below with its absolute path):

   ```sh
   brew tap onejae/tap /absolute/path/to/homebrew-tap
   brew install --build-from-source onejae/tap/mdir
   brew test onejae/tap/mdir
   brew audit --strict onejae/tap/mdir
   mdir-fm
   ```

   The formula's test opens a real terminal, creates a directory, quits, and
   verifies the final-directory handoff. Test both macOS and Linux.

5. Commit and push the tap contents to `onejae/homebrew-tap` with default branch
   `main`, then verify its Homebrew workflow. In a fresh Homebrew environment:

   ```sh
   brew install onejae/tap/mdir
   mdir-fm -version
   ```

   If using the local tap from step 4, change its Git remote to
   `https://github.com/onejae/homebrew-tap.git` before using `brew update`.

Commit, push, tag publication, and repository creation are separate publication
actions and require explicit permission when performed by an agent.

## Later releases

Update the CLI version, pass native CI, and publish a new immutable version tag.
Run `scripts/homebrew-formula.py` for that tag, review the URL and SHA-256 change
in `Formula/mdir.rb`, test it, and publish the updated tap. Do not move an
existing release tag. No cross-repository token or automated push is required.
