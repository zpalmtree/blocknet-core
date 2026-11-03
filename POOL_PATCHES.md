# Pool changes on upstream

`pool` is refreshed on `blocknetprivacy/core` master at `282987d` (November 2,
2026). The previous published pool tip is `5019120`. The first-parent stack
contains 11 code commits; the final history-preserving merge retains the old
pool tip so the canonical branch can advance without a force push. Raw ahead
counts include old retained history and do not describe the remaining patches.

## Dropped from the replayed stack

These changes are already upstream. Dropping their local copies does not remove
any of their behavior:

| Previous pool commit | Upstream replacement |
| --- | --- |
| `1ab3618` — full-size block announcements | `bcf74d2`, PR #68 |
| `c5e2e91` — cancel finished sync requests and retry incomplete runs | `d4f06d6`, PR #69 |
| `f211c74` — ignore unverified minority work claims | `d4f06d6`, PR #69 |
| `e2543e0` — keep minority runs alive while block data arrives | `d4f06d6`, PR #69 |
| `5019120` — relay transactions only after mempool acceptance | `282987d`, PR #70 |

PR #69 uses `IsSyncingFarBehind` directly. Our status extension still needs
`BlocksBehind`, so its unverified-claim guard and the pool assertions are retained
in the replay of the status patch. The sync and relay code matches the previous
pool tree after reconciliation.

Earlier refreshes already dropped accepted wallet scan lifecycle, atomic wallet
save, canonical transaction audit, checkpoint, baseline CPU compatibility and
reclaimable PoW memory patches (PRs #58–#61, #64 and #65). Do not replay their
historical copies or the old cache-option addition/revert again. Old patch-stack
documentation commits are replaced by this inventory.

## Remaining code stack and upstream recommendations

Commits below are in replay order. Recommendations concern the remaining diff
against current upstream, rather than old commit subjects alone.

| Current commit (previous) | Purpose | Recommendation |
| --- | --- | --- |
| `16eb444` (`463a7dd`) | Block processing, template, submission and SSE diagnostics | Keep local for now; a focused observability proposal could be upstreamed separately. Pool clients consume the extra status fields. |
| `77961f6` (`19a2a1f`) | Include chain-lock wait in block latency measurements | Keep with the diagnostics patch; it is not an independent upstream fix. |
| `083af6c` (`c8fc312`) | External-wallet feeds, signed submission, encrypted export and pool send handling | Keep local. Split generic APIs from timeout/rate-limit policy before proposing any part upstream. |
| `a486b5a` (`5f75137`) | Bounded canonical block-hash batch endpoint | Useful upstream candidate for external scanners; preserve its request bound and canonical snapshot behavior. |
| `5edee57` (`98b8cec`) | Background PoW arena warm-up and benchmark helper | Keep local pending evidence that startup preallocation benefits general nodes. This is optional policy, not a consensus requirement. It honors `pow_cache:false`. |
| `a0ed0cc` (`ede0268`) | Trim templates to a JSON announcement budget | Keep while older peers remain relevant; see the conditional drop below. |
| `5dddec3` (`71be1be`) | Distinguish active sync from being behind in daemon status | Upstream candidate. PR #69 fixes mining-template gating but does not add `sync_in_progress` or `blocks_behind`, or change daemon status to use them. |
| `4b73522` (`03d29f7`) | Retain a budget below older peers' 2 MiB announcement limit | Conditional drop after network rollout evidence; upstream's 6 MiB receive limit alone does not establish that older peers are gone. |
| `177bbed` (`2583bcb`) | Protect active sync peers from connection trimming | Highest-priority small upstream candidate: general sync reliability fix with a focused protection/release test. Not included in PR #69. |
| `7df5eb4` (`22dc1ab`) | Optional compact templates and full-block lookup by template ID | Upstream candidate as a standalone API addition. Keep default full-template responses, authentication, lease expiry and compact/full submission compatibility. |
| `ba856d9` (`f1490ef`) | Serialize wallet saves, preserve the mnemonic, use compact persistence and filter output queries | High-priority upstream candidate, split into wallet persistence and output-filter API changes. PR #66 changes coin selection; it does not include these fixes. |

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

## Security patches sent upstream

Live on bntpool since 2026-11-02 20:05 EST. Each maps to an upstream PR; drop the
local commit by content at the next refresh once its PR is merged.

| Commit | Purpose | Upstream |
| --- | --- | --- |
| `3efcf43` | Reject null block transactions; size and stamp templates within consensus limits | PR #72, merged upstream as `f89cf36` |
| `2691164` | Treat wallet outputs sharing a one-time key as one coin | PR #71, merged upstream as `fc0f973` (same wallet code; local API and test adapt to pool status fields) |
| `a0149bf` | Drop and evict transactions the chain would reject when building a template; recheck after reorgs | PR #73, merged upstream as `35f6c83` |
| `155954b` | Verify announced blocks outside the locks; one slot for untrusted peers; no bans for busy, duplicate or orphan | PR #74, merged upstream as `2ee075b` |
| `b8465b5`, `b15c174`, `d613f1c`, `12f08d0`, `3737282` | Peer status is a claim until dialed peers or delivered blocks back it; per-peer stream and serve limits; protect dialed peers; reorg-limit rejection | PR #75, merged upstream as `6337acc` |
| `86ccb22` | Keep the first-seen branch unless another is longer or more than half a block heavier; sync uses the same rule | PR #76 |

The upstream versions leave out pool-only pieces: `BlocksBehind` and the status
fields that use it, and sync-peer protection from connection trimming. Each requires
a rolling daemon deployment.

## Other branch work to review separately

`harden-block-intake` at `fdf2387` is not part of the published pool stack used
for this refresh. It contains null-transaction prefiltering and template size/
timestamp fixes that deserve a separate correctness review and upstream work.
It is not silently included here.

The older wallet performance proposal at `71f427e` mixes compact wallet saving
with ring sampling. Compact saving overlaps the current pool patch, and upstream
has independently changed ring selection. Re-derive any still-useful remainder
against current upstream rather than cherry-picking the entire stale proposal.

## New upstream behavior and compatibility

This refresh also takes PR #66 (`4a88325`), which prefers fewer inputs for ordinary
wallet payments, and PR #67 (`3272972`), which moves PoW caching into settings and
adds Windows idle-memory eviction.

Operators using `--pow-cache` must remove that startup argument and set
`pow_cache` in `settings.json` or through `PATCH /api/settings`. Existing settings
files default to `true`; setting it to `false` disables idle caching and pool
warm-up. A change requires a restart. The settings API reports it in
`restart_required`. No production unit or setting was changed by this refresh.

Pool and Seine daemon clients were audited for templates, template leases,
compact/full submission, SSE and bearer-cookie authentication. Those daemon
implementations and the pool status fields are unchanged from `5019120`; clients
need no changes for this refresh. Generic external-wallet endpoints remain in
the daemon; private wallet implementation remains outside this repository.

## Verification

On Linux, the refreshed tree passed 35 regular Rust tests, the explicitly run
production-size PoW reference test (including warm-up), `go test -race ./...`,
`go vet ./...`, a release Rust library build and a daemon build/version smoke
check. The code delta from `5019120` is exactly PRs #66 and #67, with the existing
PoW warm-up extension preserved. No native Windows validation was run during
this refresh.

Validation logs and the final tree diff are recorded in the workspace at
`experiments/pool-upstream-refresh-20261102`. See `validation.txt` there for the
exact commands and platform coverage.

## Updating and deploying

For the next refresh, start from current upstream master and replay only the
remaining code stack above, dropping accepted patches by content. Preserve new
pool work, test the complete tree, then retain the previous published pool tip
as a merge parent before fast-forwarding `pool`. Never force-push the canonical
branch.

This refresh does not deploy or restart services. Applying it requires a rolling
daemon deployment through the pool's deployment script when requested; never
restart both instances together. A daemon restart can affect pool and direct
miner connectivity. The pre-0.23 daemon cannot read the newer binary block
records, so rollback to an older binary requires a compatible database snapshot.
