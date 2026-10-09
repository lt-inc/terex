# AGENTS.md

Guidance for coding agents (Codex etc.). See also `CLAUDE.md` for the project overview.

## twitch-collector: `consumers` writes must take the per-flow lock

Every writer of `twitch_collector.consumers` for a flow (`SyncFlows`, `ObserveFlow`,
`SyncExclusions` in `twitch-collector/internal/repository/postgres/flows.go`) MUST run
inside a transaction and call `lockFlowConsumers(ctx, tx, flowID)` first
(`pg_advisory_xact_lock` per flow).

**Why:** these writers update the same rows in different orders (bulk `UPDATE` vs
row-by-row upsert). Without the shared lock PostgreSQL reports `40P01 deadlock detected`.
In production this showed up as `collector task failed ... stage=verification
reason=PostgreSQL 40P01` (retried 5-30 minutes later) and was caused by `SyncExclusions`
doing a bare `pool.Exec` UPDATE that bypassed the lock.

**Do not:**
- "simplify" `SyncExclusions` / `syncFlowExclusions` back to a plain `pool.Exec`;
- add a new `UPDATE`/`INSERT` on `consumers` outside a transaction that holds this lock;
- hold locks of several flows in one transaction unless they are always acquired in a
  fixed order (`syncFlowExclusions` deliberately holds one flow's lock per transaction).

Regression test: `twitch-collector/internal/repository/postgres/flows_lock_test.go`
(needs `COLLECTOR_TEST_DATABASE_URL` pointing at a disposable DB whose name ends in `_test`).
