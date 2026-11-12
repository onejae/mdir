# Publication fixes

The public command is `mdir-fm`, the repository is `onejae/mdir`, and the user
selected MIT. Complete the existing publication review by fixing the two
reproduced filesystem defects and verifying the resulting code before the
conditionally authorized commit.

- [x] Add deterministic regression tests for destination removal, replacement,
  and rewriting during cross-device moves; establish the failures first.
- [x] Record and verify the completed destination before removing each source
  entry. Preserve sources when destination changes are detected. Retain the
  existing protection for changed source files, hardlinks, and partial batches.
- [x] Add archive regression tests for truncation and rewriting during reads;
  validate the opened source after reading and discard failed archive output.
- [x] Add the selected MIT license and README license reference. Exclude local
  environment and tool configuration from future Git additions.
- [x] Run the focused regressions, race suite, vet, build, dependency verification,
  CLI checks, and PTY smoke test. Review the complete diff.
- [x] Inspect the target repository and prepare the initial commit once the
  review finds no unresolved blocking issues. Report any actual access blocker.

The fixes detect concurrent changes and stop with an error; cross-filesystem
moves are not atomic transactions, and no claim is made that arbitrary writes
after the final validation or after an operation completes can be prevented.

The focused regressions, full race tests, vet, build, module verification,
version output, and real PTY smoke test passed. Independent review identified
and verified a fix for an empty-directory permission regression; no remaining
findings were reported. The target repository had no Git refs at the final
read-only check. The source folder is now initialized with the `main` branch.
