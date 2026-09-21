# mergecanary

**Your parallel AI agents' branches merge cleanly. Do they still work *together*?**
mergecanary merges every in-flight git worktree branch into a throwaway worktree, runs your real
build/tests on the result, and if it breaks, tells you which branch caused it.

It never touches your branches, worktrees or working directories.

## Quickstart (about 30 seconds)

Requires Go 1.22+, git, and bash (for the demo fixture only).

```bash
go build -o mergecanary ./cmd/mergecanary
bash testdata/make-fixture.sh /tmp/demo      # a tiny Go repo + 3 agent worktrees
cd /tmp/demo/repo
/path/to/mergecanary watch --once --check "go test ./..."
```

The fixture has three branches that each pass their own tests and merge cleanly into `main`:
`agent-1` adds code that calls `getUser(id)`, `agent-2` adds an unrelated feature, and `agent-3`
renames `getUser` to `fetchUser`. A plain merge check says all is well. Running the tests on the
combination says otherwise. This is real output from that run (exit code 1):

```
mergecanary  FAIL
base main@c970758   3 branches   checked 18:59:18   8 check runs in 6.2s

  FAIL      agent-1  agent-3 breaks agent-1
  PASS      agent-2
  FAIL      agent-3  agent-3 breaks agent-1

  agent-3 breaks agent-1
    # fixture [fixture.test]
    .\profile.go:4:23: undefined: getUser
    FAIL	fixture [build failed]
    FAIL
```

After `agent-3` commits a small shim that keeps the old name working, the same command reports
(exit code 0):

```
mergecanary  PASS
base main@c970758   3 branches   checked 18:59:25   1 check run in 1s

  PASS      agent-1
  PASS      agent-2
  PASS      agent-3
```

(The `.\profile.go` path is from a Windows run; on Linux and macOS it prints `./profile.go`.)

## Usage

```
mergecanary watch --check CMD [flags]

  --check CMD       command to run on the merged result, e.g. "npm test" (required)
  --base BRANCH     base branch every other branch is merged onto (default main)
  --repo PATH       path inside the git repository (default .)
  --interval N      re-check at least this often; seconds or a duration (default 10s)
  --timeout N       kill a check run after this long; 0 = no limit (default 5m)
  --once            run a single pass and exit
  --json            print reports as JSON
```

Without `--once` it keeps running: it re-checks every `--interval`, and sooner when the HEAD of any
worktree (or the base branch) moves. On a terminal the board redraws in place; when piped, it
prints one block per finished pass. Ctrl-C exits with status 130.

**Exit codes with `--once`:** `0` everything passes, `1` a merge conflict or a failing check,
`2` a usage or configuration error (bad flags, not a git repo, unknown base).

**`--json`** prints the report as JSON: one compact line per pass while watching, indented with
`--once`. Top-level fields: `ok`, `base`, `base_sha`, `branches` (each with `name`, `path`, `sha`,
`status`), `conflicts`, `check`, `findings` (each with `set`, `breaker`, `victim`, `summary`,
`output`), `check_runs`, `checked_at`, `duration_ms`.

## How it works

1. Lists worktrees with `git worktree list --porcelain`. Worktrees on a branch other than the base
   are the in-flight branches.
2. Creates a temporary detached worktree at the base, merges each branch's committed tip into it,
   and runs your check there. A branch that conflicts is skipped and reported with the files, and
   with the earlier branch (or the base) it conflicts with.
3. If the check fails, it first confirms the base alone passes. Then it looks for the smallest
   failing set: each branch alone, then each pair of the rest. If neither explains the failure, it
   shrinks the full set one branch at a time while it keeps failing.
4. For a failing pair it names a breaker and a victim by checking whether the failure output
   mentions files changed by only one of the two branches (that branch is the victim). If that is
   not clear, it says "X and Y fail together" instead of guessing.
5. The temporary worktree is removed when a pass finishes, on error, on panic, and on
   SIGINT/SIGTERM.

Everything shells out to `git`; the binary uses only the Go standard library.

## Limitations

- **It is only as good as your check command.** If your tests do not exercise the interaction,
  mergecanary will not see it. It does not analyze code itself.
- **The check runs in a fresh checkout.** Untracked files (`node_modules`, `.env`, build caches
  inside the repo) are not there, and the worktree is cleaned between runs. A command like
  `npm ci && npm test` may be needed.
- **Extra merges cost time.** A green pass runs the check once. A failing pass runs it once for
  the full merge, once for the base, then up to n singles and n(n-1)/2 pairs (8 runs for 3
  branches). Checks are not run in parallel.
- **Only committed work is merged.** Uncommitted changes in a worktree are ignored. Worktrees with
  a detached HEAD are skipped.
- **The breaker/victim label is a heuristic** over the failure text, not proof.
- **A flaky check can blame the wrong branch.** There is no retry.
- **Merge order is alphabetical by branch name**, and the temp worktree starts from the current
  tip of the base each pass.
- **A hard kill (SIGKILL, power loss) can leave a temp worktree** under your system temp directory;
  it is not swept automatically yet.
- **Git side effects:** while running, git records the temporary worktree in the repo's
  `.git/worktrees`, and the merge commits it makes are unreferenced objects that `git gc` will
  collect. Git hooks are disabled for the commands mergecanary runs inside its worktree.
- **On Windows** the check command runs through `cmd.exe`. The colored in-place board has been
  run on Linux; enabling ANSI colors in a Windows console has not been tested.

## How it differs from other approaches

Some tools stop agents from overlapping by having them claim files or areas. Others compare code
statically, such as function signatures, to predict clashes, or make the merge itself smarter.
Those help before and during the merge. mergecanary answers a different question afterwards: it
takes the branches as they are, combines them, and runs the build and tests you already trust, so a
break that only shows up when the pieces meet is caught, and traced to the branch that caused it.
It works alongside any of them.

## Development

```bash
go vet ./...
go test ./...
go test -race ./...   # needs cgo; CI runs it on Linux and macOS
```

The integration tests build the fixture with `testdata/make-fixture.sh` and need `git` and `bash`
(and `go` on the PATH for the fixture's own check command).

See [DESIGN.md](DESIGN.md) for how it is put together and [ROADMAP.md](ROADMAP.md) for what is not built yet. MIT licensed.
