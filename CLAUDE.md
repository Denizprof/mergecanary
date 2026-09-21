# mergecanary

Go CLI that answers: "My parallel AI agents' branches merge cleanly. Do they still work TOGETHER?"
It watches all git worktrees of a repo, merges every in-flight branch into a throwaway
worktree, runs the user's check command (tests/build), and if that fails, bisects to find
which branch (or pair) caused the break.

## Hard rules
- Never modify the user's real branches, worktrees, or working directories. All merging
  happens in a temporary worktree that is always cleaned up (exit, SIGINT, panic).
- Shell out to `git`; no git library.
- Single static binary, standard library only.
- Do not claim anything works until it has been run. No invented features, benchmarks or stats
  in docs.
- Keep it small. Anything outside the v0.1 scope goes in ROADMAP.md, not in code.

## Tests
```
go vet ./...
go test ./...
go test -race ./...
bash testdata/make-fixture.sh "$(mktemp -d)"   # build the demo fixture by hand
```
Integration tests build the fixture with `testdata/make-fixture.sh` (needs bash, git, go).

## Definition of done
Never mark a task done without running the tests and showing output.
