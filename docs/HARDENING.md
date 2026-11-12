# Hardening review and implementation

Scope: the existing Windows/browser clients, Go server, installer and release
workflow, based on commit `048b8b7`. No new product features or database migrations.

## Completed implementation plan

- [x] Isolate E2EE caches by session identity and server; abort requests and reject
  late native results after a session ends. Clear cached epochs and wake waiters.
- [x] Recheck current account, channel visibility, connect and publishing rights
  when adopting a LiveKit participant without a current voice state.
- [x] Recheck the key sender's session/device, container membership, epoch,
  recipient devices and pending rotation inside the SQLite write transaction.
- [x] Serialize last-admin and space-ownership checks with account deletion.
  Recheck transfer actors/recipients and reject space creation by deleted users.
- [x] Read storage usage while holding the reservation lock.
- [x] Route failed service restarts through installer rollback. Explicitly state
  that binary rollback does not undo database migrations.
- [x] Drain manual attachment purges in batches while keeping maintenance bounded.
- [x] Bound expensive work: two active password operations with sixteen waiting;
  one active image operation with eight waiting. Waiting requests honor cancellation.
  Running Argon2/image decoder calls are not forcibly interrupted.
- [x] Set body-read deadlines: thirty seconds for ordinary state-changing requests,
  ten minutes for attachment uploads; WebSocket lifetime is not an upload timeout.
- [x] Override minimatch to 11.0.1 and make dependency checks mandatory in complete
  release builds and CI. Pin staticcheck, govulncheck and cargo-audit versions.
- [x] Derive displayed safety numbers from SHA-256 of all 32 public-key bytes,
  with a shared Rust/browser test vector. Device keys and media formats are unchanged.

## Regression coverage

Tests cover late key results and same-ID account switches, attempted requests to
a different server, replaced cookie sessions, current voice recovery permissions,
concurrent admin deletion, device revocation while a key request waits for the
database writer, concurrent quota reservations, purges larger than 1,000 rows,
bounded/canceled work admission, full-key fingerprints and installer restart failure.
The installer test runs the real rollback function with mocked service/file commands.

Validation includes TypeScript/Svelte checks, ESLint/Prettier, 173 client tests,
both frontend builds, Go tests with the race detector, vet/staticcheck, Windows and
Linux-target vulnerability scans, a Linux cross-build, 17 Rust tests, Clippy,
dependency audits, Bash syntax and PowerShell parsing.

## Dependency policy and remaining upstream findings

- npm production audit must pass without findings. The full audit blocks moderate
  and higher severity findings. The low-severity `deepmerge` advisory comes through
  the Svelte build plugin, has no published fix at review time, and remains visible.
  Two npm findings represent this dependency and its affected parent, not two
  separate application vulnerabilities.
- Cargo audit remains unfiltered. Its `glib` unsoundness advisory is on the Linux
  GTK dependency path, absent from the shipped Windows dependency tree. Other
  warnings concern unmaintained dependencies and a yanked `async-executor` version.
  These warnings are not suppressed or presented as a clean dependency tree.
- Go's symbol analysis found no affected calls on the checked targets; module-level
  findings outside imported/called vulnerable code are not evidence of exploitation.
- Revisit exceptions when dependencies or supported platforms change. Audit tool
  versions are pinned, but advisory databases are refreshed at execution time.

## Release boundaries

The implementation does not publish a release, update a production server, or
change the version number. Before deploying a release, perform a staging smoke test
with real browser/Windows peers, permission changes during a call, server restart,
and a failed update on a disposable Linux installation. The local regression harness
does not replace testing systemd, Caddy and LiveKit together.

Safety numbers change after this update even though device keys do not. Compare
numbers using updated clients on both sides. Automatic binary rollback does not
restore a pre-migration database; use the existing backup and an operator-controlled
recovery procedure if a future migration is incompatible with the previous binary.
