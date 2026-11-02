# Pool changes on upstream

`pool` is based on `blocknetprivacy/core` master at `e17baca` (0.23.0).
The wallet scan, atomic wallet save, canonical transaction audit, checkpoint,
baseline CPU compatibility and reclaimable PoW memory changes are now upstream
([#58](https://github.com/blocknetprivacy/core/pull/58),
[#59](https://github.com/blocknetprivacy/core/pull/59),
[#60](https://github.com/blocknetprivacy/core/pull/60),
[#61](https://github.com/blocknetprivacy/core/pull/61),
[#64](https://github.com/blocknetprivacy/core/pull/64) and
[#65](https://github.com/blocknetprivacy/core/pull/65)). Their local copies are
no longer part of the replayed stack. The kernel dependency and CPU dispatch
match upstream.

## Remaining patch stack

These code commits are replayed directly on that upstream base:

| Commit | Purpose | Upstream status |
| --- | --- | --- |
| `463a7dd` | Block processing, submission, template and SSE diagnostics | Pool-specific |
| `19a2a1f` | Include chain-lock wait in block latency diagnostics | Pool-specific |
| `c8fc312` | External-wallet feeds, signed-transaction submission, encrypted export and pool send handling | Pool-specific |
| `5f75137` | Bounded canonical block-hash batch API and endpoint test | Pool-specific |
| `98b8cec` | Background PoW memory warm-up and verification benchmark helper | Pool-specific |
| `ede0268` | Keep block templates within the peer block-announcement size | Candidate for upstream with a larger announcement limit |
| `71be1be` | Report syncing only when behind the sync target, plus `sync_in_progress` and `blocks_behind` | Candidate for upstream |
| `1ab3618` | Accept new-block announcements up to 6 MiB | Upstream PR [#68](https://github.com/blocknetprivacy/core/pull/68) |
| (this refresh) | Template cap pinned to the 2 MiB limit older nodes enforce; sync-run peers protected from connection trimming | Pool-specific / candidate for upstream |

## Reconciliation

The refresh preserves the previous pool APIs, cookie authentication, mining
template leases, compact/full block submission, SSE events and block-path
metrics. The accepted wallet, canonical lookup and checkpoint changes match the
implementations already carried by pool.

PoW warm-up uses upstream's bounded memory cache and now skips allocation when
`--pow-cache=false` is set. With caching enabled, upstream retains at most one
idle 2 GiB arena and allows Linux to reclaim its pages under memory pressure.
The warm-up guard keeps its memory separate from active hashes. Upstream's
cache option defaults to enabled and applies at process startup; see
`docs/blocknet-mantle.md` for its behavior. The earlier local addition and revert
of the cache option are superseded by upstream #65.

## Verification

The refreshed tree passed 35 Rust tests, explicit full-size reference hashing
with warm-up, the full Go race suite, `go vet ./...` and a daemon build. The
warm-up regression tests cover cache reuse, separate active arenas and disabled
caching without allocation. The pool and Seine client contracts were audited;
the daemon API, authentication, SSE and submission implementations are unchanged
from the previous pool tip.
Local validation logs and diffs are archived under
`experiments/pool-upstream-refresh-20261029-merged` in the parent workspace.

## Updating and deploying

Sync recovery cancels outstanding block-range streams when a run ends, cancels
losing hedge requests, and skips mempool downloads until the block target is
reached. Incomplete runs log their local height, target and next requested block
before retrying. This changes no consensus, API, authentication, SSE or mining
template/submission contract. It requires a rolling daemon deployment to apply.

A peer's status alone no longer marks the node as behind. When only a minority of
responding peers advertise the most work, `syncing`, `blocks_behind` and template
serving ignore the run until a block above the local tip arrives; `sync_in_progress`
still reports it. Such a run gives up after 30 seconds without any block data, or
after the usual 2 minutes while data is still arriving but no block completes. Its peers'
status is then ignored for 10 minutes and they lose peer score, and no other minority
claim can start a run for 2 minutes. A run most peers corroborate behaves as before.
No field or endpoint changes, but `syncing` is false in more cases. It requires a
rolling daemon deployment to apply.

For the next refresh, start from current upstream master and replay only the
remaining commits above, dropping accepted patches by content. Preserve any new
`pool` work, test the complete tree, then retain the previous published pool tip
as a merge parent before fast-forwarding `pool`. Never force-push the canonical
branch. That merge preserves history; the tree diff and the replayable first-parent
stack describe the remaining changes more accurately than raw ahead counts.

This source refresh does not deploy or restart services. Use the pool's rolling
daemon deployment script when explicitly requested; never restart both daemon
instances together. The pre-0.23 daemon cannot read upstream's new binary block
records, so any binary rollback still requires a compatible database snapshot.
