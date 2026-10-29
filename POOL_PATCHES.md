# Pool changes on upstream

The pool daemon is based on `blocknetprivacy/core` master at `bc8e414`
(version 0.23.0). Pool-specific changes are replayed on that base in dependency
order, rather than retaining duplicate implementations of fixes accepted upstream.
The production branch remains `pool`.

| Commit | Retained behavior |
| --- | --- |
| `4dedf42` | Shared Seine PoW kernel and reusable validation memory |
| `0399caf` | Atomic canonical transaction reads, batched hashes and fast output audits; reads legacy JSON and binary blocks |
| `8d67849` | Atomic finalized checkpoint publication |
| `81c6635` | Block-path diagnostics and SSE timing on upstream's lock-free tip cache; preverification is included in elapsed time |
| `91fdb56` | Authenticated external-wallet feeds, wallet export, send handling and serialized wallet scanning/unload |
| `07fbe40` | Hugepage-backed PoW memory, startup warm-up and updated shared kernel |

Each commit builds on the preceding commits. For the next upstream update,
create a temporary worktree from the new upstream master and cherry-pick only
these retained patches, dropping patches upstream has since accepted. Compare
behavior as well as hashes: older pool commits and upstream fixes often have
different hashes. Keep new changes in focused commits and update this list.

Before replacing the canonical tip, account for any newer `pool` commits,
verify the complete resulting tree and preserve the old published branch as a
merge parent. Fast-forward `pool` to that integration commit and push normally;
never force-push or rewrite the published branch. An ancestry-only merge is
appropriate only after the retained changes have all been reconciled and tested.

## Upstream submissions

The following focused PRs are based on upstream master at `14ccd4b`:

- [#58: wallet scan/unload synchronization](https://github.com/blocknetprivacy/core/pull/58)
- [#59: atomic wallet saves](https://github.com/blocknetprivacy/core/pull/59)
- [#60: canonical transaction lookups and faster wallet audits](https://github.com/blocknetprivacy/core/pull/60), in two commits
- [#61: atomic finalized checkpoints](https://github.com/blocknetprivacy/core/pull/61), integrated with upstream's background refresh

Each PR is independent of the others. All five commits also cherry-pick together
without conflicts. Each branch passed `go test -race ./...` and `go vet ./...`;
the combined tree passed the focused regression tests under the race detector.
The PRs contain no external-wallet feed/export APIs or pool send-policy changes.
Retain the corresponding pool patches until these changes are accepted upstream,
then reconcile by content on the next upstream update.

The unrelated explorer layout patch was reverted in `c8fbba9`.

[#62: reusable PoW memory and Linux huge pages](https://github.com/blocknetprivacy/core/pull/62)
was merged as `b819f23`. It is an independent memory-management patch using upstream's existing
RustCrypto Argon2 implementation. It retains at most one idle 2 GiB buffer.
It passed 27 Rust tests, explicit production-size reference comparisons, the
full Go race suite and `go vet`. The benchmark harness and raw results are
included in that PR; local evidence is in `experiments/pow-upstream-20261029`.
The [shared package approach was approved](https://github.com/blocknetprivacy/core/pull/62#issuecomment-6488715224).
[#63: shared optimized PoW kernel](https://github.com/blocknetprivacy/core/pull/63)
is based on `b819f23` and pins the standalone
[blocknet-pow-kernel](https://github.com/zpalmtree/blocknet-pow-kernel) package at
`9f5d0b79d0cc4c169cd112443c524bb4bb946627`. The daemon change is three files
(+36/-18), retaining upstream's one-buffer cache. Full 2 GiB reference vectors
passed on Linux x86-64/ARM64, Windows x86-64 and macOS ARM64; local tests also
covered all AVX-512 variants. The daemon passed 28 Rust tests, the explicit
production reference test, the full Go race suite and `go vet`.
Median hash time on a Ryzen 9950X3D/WSL2 fell from 924 ms to 482 ms against
current upstream (same Rust 1.93.0 build settings, 30 matching hashes).
Evidence is in `experiments/shared-pow-kernel-20261029`. No production services
were deployed or restarted, and no package registry release or release tag was
created. Reconcile the existing pool kernel patch by content when #63 lands.

## Verification

This integration passed the Rust crypto suite (25 tests), full Go tests,
full Go race tests and `go vet`. Every replayed backend commit also compiled
with the Go test suite. The published pool and Seine branches passed their
client API tests (18 and 21 tests respectively). The audit covered template
leases, compact/full block submission, cookie authentication, SSE and wallet
send responses. Existing daemon startup flags are accepted by upstream's
settings migration; settings are persisted in `settings.json`.

## Deployment

This source integration does not deploy or restart any service. Deploy through
the pool repository's rolling daemon deploy script when explicitly requested.
Upstream writes new blocks in a binary storage format: the previous 0.16.1
pool binary cannot read those new records, so binary rollback requires a
compatible database snapshot. Retain the pre-upgrade backup when deploying.
