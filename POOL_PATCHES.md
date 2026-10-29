# Pool changes on upstream

`pool` is based on `blocknetprivacy/core` master at `2f2c4db` (0.23.0).
The memory and shared-kernel PRs [#62](https://github.com/blocknetprivacy/core/pull/62)
and [#63](https://github.com/blocknetprivacy/core/pull/63) are merged. Their old
pool implementations have been replaced by upstream's code, including its limit
of one idle 2 GiB buffer. The kernel dependency and CPU dispatch match upstream.

## Remaining patch stack

These ten code commits are replayed directly on that upstream base:

| Commit | Purpose | Upstream status |
| --- | --- | --- |
| `03fb43d` | Save wallets atomically | [#59](https://github.com/blocknetprivacy/core/pull/59), open |
| `0b96c9b` | Serialize wallet scans and stop them on unload | [#58](https://github.com/blocknetprivacy/core/pull/58), open |
| `4418bd5` | Read canonical transaction locations consistently | [#60](https://github.com/blocknetprivacy/core/pull/60), open |
| `6223b94` | Batch wallet output audits | #60, second commit |
| `f8304d4` | Publish checkpoints atomically and coordinate background refresh | [#61](https://github.com/blocknetprivacy/core/pull/61), open |
| `a3c4f5e` | Block processing, submission, template and SSE diagnostics | Pool-specific |
| `a57b871` | Include chain-lock wait in block latency diagnostics | Pool-specific |
| `d110124` | External-wallet feeds, signed-transaction submission, encrypted export and pool send handling | Pool-specific |
| `9a7576a` | Bounded canonical block-hash batch API and endpoint test | Pool-specific |
| `6e5846c` | Background PoW memory warm-up and verification benchmark helper | Pool-specific |

The first five commits are the focused upstream submissions, so they can be
dropped independently when those PRs merge. The external-wallet patch no longer
contains its own copy of atomic wallet saving or scan synchronization. The batch
API regression test is separate from the canonical transaction-index tests.

## Reconciliation

The refresh preserves the old pool APIs, cookie authentication, mining template
leases, compact/full block submission, SSE events and block-path metrics. It
also adopts upstream's newer peer discovery, seed selection, reachability checks,
peer software labels, release archive naming and automatic checkpoint refresh.
The checkpoint patch coordinates that refresh with local checkpoint publication.
The wallet scan patch uses the stronger unload checks prepared for upstream.

PoW warm-up uses upstream's existing memory cache. It skips an already cached
arena, owns separate memory from active hashes and releases surplus arenas. The
old Seine dependency, duplicate kernel dispatch and unbounded idle arena cache
are removed. The explorer uses upstream's implementation without a local patch.

## Verification

The refreshed tree passed 29 Rust tests, explicit full-size reference hashing
with warm-up, the full Go race suite, `go vet ./...` and a daemon build.
The pool and Seine client contracts were audited: template/submit/SSE/auth fields
remain compatible; upstream's status and peer additions are optional JSON fields.
Local validation logs and the final diff are archived under
`experiments/pool-upstream-refresh-20261029` in the parent workspace.

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
