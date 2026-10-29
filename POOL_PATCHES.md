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
