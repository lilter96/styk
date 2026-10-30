# Database report

## Target and safety evidence

The task created and used `styk_demo_dev` on existing local container `styk-redesign-db`, PostgreSQL image 17.6, bound only to 127.0.0.1:55433. The exact marker is `STYK isolated development demo v1`. Previous development databases were preserved; automated regression tests used the dedicated `styk_auth_test`, not the working demo.

The initial database was newly created. Guarded reset was then exercised only on the marked synthetic demo after API/worker stop and verified backup. No docker compose down -v, volume prune or unknown-volume removal was used.

## Backups and migrations

Custom pg_dump archives and SHA-256 files are under ignored `.local/demo/backups/`. Final reset backup: `20261030T114452986485Z.dump`. Archive listing, transactional restore into a newly created verification database, table inventory and canonicalized row-count comparison passed. Only the exact temporary verification databases created by this task were dropped.

Reset acquires exclusive business-table locks, repeats user/credential/account guards and TRUNCATEs the complete business-table set without CASCADE. `goose_db_version`, schema, constraints and functions remain. Existing migrations 1–44 were applied; no migration source was added, deleted or edited.

Fresh migration history includes 13 NOT VALID foreign keys for legacy upgrade compatibility. These were validated in this isolated demo database using `APP_ENV=development python scripts/demo-db.py verify --confirm styk_demo_dev`. All public FKs validated successfully; this does not change production migration files.

## Populated families

- identity: one verified active demo owner, workspace/membership/merchant binding; ordinary auth/security events.
- stock_connections: six synthetic account registrations, deliberately zero connection_credentials.
- source_imports/entities/aggregate and warehouse observations: 500 physical products, 3 source warehouses, 1,500 warehouse stock rows.
- Ozon batches/products/warehouses/stocks: 2,500 listings, 15 warehouse observations, 7,500 stock rows.
- catalog_sync_snapshots: 2,500 explicitly operator-attested synthetic price/name/currency facts.
- mapping/current/history: 1,960 product and 15 warehouse mappings, same scoped source batches.
- Ozon postings/identities/lines: 1,000 synthetic observations across five accounts.
- MS order batches/identities/observations/positions: 1,000 corresponding observations in three 30-day windows.
- imports/outbox: 10 import scenarios and 12 safe mock jobs, total 22 tasks.

## Integrity evidence

| Check | Result |
|---|---|
| Sole demo user / active verified owner | 1 / 1 |
| Organization STYK Demo Company | 1 |
| Non-demo provider credentials | 0 credential rows |
| Public unvalidated FKs after verification | 0 |
| Tenant-bearing rows belonging to another tenant | 0 |
| Ozon present != reserved + free_stock, or negative reserve/free | 0 |
| Matching listing price disagreements across accounts | 0 |
| Duplicate work on repeat seed | none; existing=true |
| Late fixture failure | transaction rolled back; no leaked imports |
| Concurrent first seed | exactly one creator; one idempotent observer |
| Different seed after completion | refused |
| Another user in seed DB | refused in isolated integration test |

Native sequences/auth IDs/audit timestamps are not claimed to be byte-identical across new account registrations. Product/stock/price values are deterministic for seed/reference time/scope. Mock inventory tables lack tenant columns in the existing schema; they remain confined to this dedicated single-owner database and do not create external commitments.

The current backend has no full order fulfillment, returns or analytics data model. No production table/API was invented for those. WB/Yandex remain presentation-demo entries, not fake operational connections.

Exact counts are in [REPORT.json](STYK_POST_CLEANUP_REPORT.json).
