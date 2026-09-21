# Contributing

Bug reports and small, focused pull requests are welcome.

- Read [DESIGN.md](DESIGN.md) first; it is short. The scope of v0.1 is deliberately small, and
  new ideas usually belong in [ROADMAP.md](ROADMAP.md) before code.
- Run `go vet ./...` and `go test ./...` (and `go test -race ./...` if you have cgo) before
  opening a PR. Tests build a real git fixture with `testdata/make-fixture.sh`, so they need
  `git` and `bash`.
- Keep the rules in [CLAUDE.md](CLAUDE.md): never write to the user's branches or worktrees,
  shell out to `git`, standard library only.
- A bug report is most useful with: your check command, `mergecanary --version`, your OS, and
  the output of `mergecanary watch --once --json`.
