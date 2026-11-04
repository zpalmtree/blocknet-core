# Pool changes on upstream

`pool` is refreshed on `blocknetprivacy/core` master at `df0dd2b` (November 3,
2026). The previous published pool tip is `c476374`. The first-parent stack
contains 11 code commits; the final history-preserving merge retains the old
pool tip so the canonical branch can advance without a force push. Raw ahead
counts include old retained history and do not describe the remaining patches.

The code tree is identical to `c476374`: every upstream change since the last
refresh (`282987d..df0dd2b`, PRs #71–#77) was already on `pool`. This refresh
only moves those changes from local commits to upstream history.

## Dropped from the replayed stack

These changes are already upstream. Dropping their local copies does not remove
any of their behavior:

| Previous pool commit | Upstream replacement |
| --- | --- |
| `3efcf43` — reject null block transactions; templates within consensus limits | `f89cf36`, PR #72 |
| `2691164` — wallet outputs sharing a one-time key count as one coin | `fc0f973`, PR #71 |
| `a0149bf` — keep transactions the chain would reject out of templates | `35f6c83`, PR #73 |
| `155954b` — verify announced blocks outside the locks; no bans for busy relays | `2ee075b`, PR #74 |
| `b8465b5`, `b15c174`, `d613f1c`, `12f08d0`, `3737282` — peer status is a claim; per-peer limits | `6337acc`, PR #75 |
| `86ccb22` — keep the first-seen branch unless another is longer or clearly heavier | `5b0b98f`, PR #76 |
| `739c2fd`, `cd7ffa0`, `57ca954`, `0c5c834`, `f99a952`, `e138a18` — hot-path concurrency | `df0dd2b`, PR #77 |

The upstream versions leave out pool-only pieces. Those now live in the stack
commit they depend on:

- Submit, gossip and sync timing on top of the preverification split
  (`ProcessBlockTimed`, `ProcessPreverifiedBlockTimed`, `SubmitBlockTimed`, the
  timed preverification and their tests): diagnostics commit `e94c5de`.
- Parallel block-range decoding for wallet-feed pages and the thread-safe wallet
  build timings: external-wallet commit `bd3018f`.
- `BlocksBehind` and the status fields that use it, including the dialed-peer
  claim test: status commit `190a768`.
- Sync-peer protection from connection trimming: `619b54e`.
- `walletOutputStatus` with the shared-key `unspendable` status, and its test:
  wallet output filter commit `1aefa8a`.

Earlier refreshes already dropped accepted sync and relay fixes (PRs #68–#70),
wallet scan lifecycle, atomic wallet save, canonical transaction audit,
checkpoint, baseline CPU compatibility and reclaimable PoW memory patches (PRs
#58–#61, #64 and #65). Do not replay their historical copies or the old
cache-option addition/revert again.

## Remaining code stack and upstream recommendations

Commits below are in replay order. Recommendations concern the remaining diff
against current upstream, rather than old commit subjects alone.

| Current commit (previous) | Purpose | Recommendation |
| --- | --- | --- |
| `e94c5de` (`16eb444`) | Block processing, template, submission and SSE diagnostics | Keep local for now; a focused observability proposal could be upstreamed separately. Pool clients consume the extra status fields. |
| `29536e1` (`77961f6`) | Include chain-lock wait in block latency measurements | Keep with the diagnostics patch; it is not an independent upstream fix. |
| `bd3018f` (`083af6c`) | External-wallet feeds, signed submission, encrypted export and pool send handling | Keep local. Split generic APIs from timeout/rate-limit policy before proposing any part upstream. |
| `89ced64` (`a486b5a`) | Bounded canonical block-hash batch endpoint | Useful upstream candidate for external scanners; preserve its request bound and canonical snapshot behavior. |
| `2ab55f9` (`5edee57`) | Background PoW arena warm-up and benchmark helper | Keep local pending evidence that startup preallocation benefits general nodes. This is optional policy, not a consensus requirement. It honors `pow_cache:false`. |
| `6f8378e` (`a0ed0cc`) | Trim templates to a JSON announcement budget | Keep while older peers remain relevant; see the conditional drop below. |
| `190a768` (`5dddec3`) | Distinguish active sync from being behind in daemon status | Upstream candidate. Upstream gates templates with `IsSyncingFarBehind` but does not add `sync_in_progress` or `blocks_behind`, or change daemon status to use them. |
| `c684bc1` (`4b73522`) | Retain a budget below older peers' 2 MiB announcement limit | Conditional drop after network rollout evidence; upstream's 6 MiB receive limit alone does not establish that older peers are gone. |
| `619b54e` (`177bbed`) | Protect active sync peers from connection trimming | Highest-priority small upstream candidate: general sync reliability fix with a focused protection/release test. Not included in PRs #69 or #75. |
| `d91d24c` (`7df5eb4`) | Optional compact templates and full-block lookup by template ID | Upstream candidate as a standalone API addition. Keep default full-template responses, authentication, lease expiry and compact/full submission compatibility. |
| `1aefa8a` (`ba856d9`) | Serialize wallet saves, preserve the mnemonic, use compact persistence and filter output queries | High-priority upstream candidate, split into wallet persistence and output-filter API changes. PR #66 changes coin selection; it does not include these fixes. |

Do not drop any remaining runtime patch unconditionally in this refresh. The
2 MiB template policy is the clearest future removal candidate: first establish
that required peers accept 6 MiB announcements and verify the largest valid
serialized block fits the chosen budget. Revisit the trimming helper at that
point rather than retaining an obsolete compatibility cap indefinitely.

For the external-wallet/send patch, the five-minute global HTTP write timeout
and removal of the per-IP send limiter are pool policy, not generic fixes to
submit unchanged. Upstream's fewer-input selection may reduce their necessity,
but explicit-input sends and sweeps still need evaluation before removing them.
Keep the existing concurrency bound and authenticated API behavior.

## Other branch work to review separately

The older wallet performance proposal at `71f427e` mixes compact wallet saving
with ring sampling. Compact saving overlaps the current pool patch, and upstream
has independently changed ring selection. Re-derive any still-useful remainder
against current upstream rather than cherry-picking the entire stale proposal.

## Compatibility

No code changes from `c476374`, so no client, API, setting or deployment changes.
The production daemons already run this code (`pool-e138a18`, live since
2026-11-03 21:05 EST); a rebuild from the refreshed `pool` gives the same tree.

## Verification

Each replayed commit passed `go vet ./...`. On Linux, the tip passed the Rust
release build and tests, `go test -race ./...` and a daemon build/version smoke
check. `git diff c476374` on the refreshed tip shows only this file. No native
Windows validation was run.

Validation logs are recorded in the workspace at
`experiments/pool-upstream-refresh-20261103`.

## Updating and deploying

For the next refresh, start from current upstream master and replay only the
remaining code stack above, dropping accepted patches by content. When upstream
has merged our own patches, replaying in reverse (revert the stack from the old
pool tip, newest first) yields each replayed tree directly and must end on
upstream's tree exactly; any remainder is a pool-only adaptation to assign to
the stack commit it depends on. Preserve new pool work, test the complete tree,
then retain the previous published pool tip as a merge parent before
fast-forwarding `pool`. Never force-push the canonical branch.

This refresh does not deploy or restart services. Applying a future code change
requires a rolling daemon deployment through the pool's deployment script when
requested; never restart both instances together. A daemon restart can affect
pool and direct miner connectivity. The pre-0.23 daemon cannot read the newer
binary block records, so rollback to an older binary requires a compatible
database snapshot.
