# Design

A short map of how mergecanary is put together. Everything described here exists in the code.

## The problem

Two branches can each pass their tests and merge without a textual conflict, and still be broken
together: one renames a function, the other adds a call to the old name. Git cannot see this;
only building and testing the combination can.

## One pass

```
worktrees.List ──> integrate.Build ──> check.Run ──(fails)──> bisect.Find ──> Report
 (git worktree      (temp worktree,     (your command,          (which branch /
  list --porcelain)  merge each branch)  with a timeout)         pair breaks it)
```

`engine.Analyze` runs one pass and returns a `Report`. `engine.Watch` calls it in a loop.
The board and `--json` both render the same `Report`.

## Packages

| Package | Job |
|---|---|
| `cmd/mergecanary` | Flag parsing, exit codes, signal handling, `--once` / watch / `--json` output. |
| `internal/gitcmd` | The only place that runs `git`. Returns stdout, wraps failures with stderr. |
| `internal/worktrees` | Parses `git worktree list --porcelain`; picks the in-flight branches. |
| `internal/integrate` | The temporary detached worktree: create, reset to base, merge a branch, attribute a merge conflict, remove. `Run` guarantees removal. |
| `internal/check` | Runs the check command through the platform shell with a timeout; kills the whole process tree; keeps the last 64 KB of output. |
| `internal/bisect` | Finds minimal failing sets of branches, and names a breaker and victim for a pair. Knows nothing about git. |
| `internal/engine` | Ties the above together into a pass; the watch loop and its cheap change detector. |
| `internal/board` | Renders a `Report` as text; terminal detection and in-place redraw. |
| `internal/testutil` | Test-only: builds the fixture and snapshots repo state. |

## Safety rules and how they are enforced

- **Nothing of the user's is written.** All merges happen in a temporary detached worktree in the
  system temp directory. Tests snapshot every branch ref and every checkout's HEAD and status
  (including ignored files) before and after a pass and require them to be identical.
- **Always cleaned up.** `integrate.Run` removes the worktree on return, on panic (the panic keeps
  propagating), and when SIGINT/SIGTERM cancels its context. Cleanup uses its own context, so a
  cancelled caller cannot skip it. A hard kill cannot be handled (see ROADMAP).
- **No user side effects from git itself.** Commands inside the worktree run with hooks disabled
  and a synthetic author; nothing is written to the user's git config.
- **Reproducible verdicts.** Every reset also runs `git clean -ffdx` in the temp worktree, so
  leftovers from one check cannot affect the next.

## How a failure is explained

1. The full merge fails its check.
2. The base alone is checked. If it fails, no branch is blamed.
3. Each branch alone, then each pair of the branches that passed alone, is merged onto the base and
   checked. Failing sets are reported. A set that cannot be merged is skipped, not counted as a
   failure. If nothing is found, the full set is shrunk one branch at a time while it keeps failing.
4. For a failing pair, the failure output is matched against the files each branch changed. If it
   points at files of exactly one branch, that branch is the victim and the other the breaker.
   Otherwise the pair is reported as failing together, without a breaker.

## Watching

`engine.Fingerprint` (base tip plus each in-flight branch and its HEAD) is compared about once a
second. A pass runs when it changes, or when `--interval` has passed since the last one finished.
The fingerprint is taken before a pass, so a branch that moves during a pass triggers another.
Each pass uses a fresh temporary worktree.
