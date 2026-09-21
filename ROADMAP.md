# Roadmap

Ideas that are deliberately **not** in v0.1. Nothing here exists in code yet.

- Sweep stale `mergecanary-*` temp worktrees left behind by a SIGKILL or power loss
  (today they are only removed by a clean exit, Ctrl-C/SIGTERM, or a panic).
- Per-worktree setup step (e.g. `npm ci`) before the check; today the check runs in a
  fresh checkout, so anything untracked (node_modules, .env) is absent.
- Retry / flaky-check handling; a flaky check can produce a wrong culprit.
- Bisect beyond pairs when several independent multi-branch failures exist
  (today: singles, then pairs, then a greedy shrink of the full set only if none were found).
- Faster bisect (parallel checks; reusing build caches).
- Watch worktrees' uncommitted changes (today only committed HEADs are merged).
