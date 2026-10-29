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
| `e942787` | Responsive explorer statistics that fit narrow screens |

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

## Verification

This integration passed the Rust crypto suite (25 tests), full Go tests,
full Go race tests and `go vet`. Every replayed backend commit also compiled
with the Go test suite. The published pool and Seine branches passed their
client API tests (18 and 21 tests respectively). The audit covered template
leases, compact/full block submission, cookie authentication, SSE and wallet
send responses. Existing daemon startup flags are accepted by upstream's
settings migration; settings are persisted in `settings.json`.

The explorer showcase is kept at the workspace path
`experiments/core-upstream-showcase/index.html`, outside the source repository.
It uses the real explorer handlers with synthetic chain data, captured through
the configured Playwright MCP at desktop and mobile widths. Run that folder's
`serve.sh` to view it at `http://localhost:18765`.

## Deployment

This source integration does not deploy or restart any service. Deploy through
the pool repository's rolling daemon deploy script when explicitly requested.
Upstream writes new blocks in a binary storage format: the previous 0.16.1
pool binary cannot read those new records, so binary rollback requires a
compatible database snapshot. Retain the pre-upgrade backup when deploying.
