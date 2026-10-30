# Риски, нагрузочные сценарии и roadmap

## Приоритеты

P0 — blocker безопасного коммерческого запуска. P1 — согласованный MVP для первых клиентов. P2 — устойчивость/эксплуатация. P3 — рост. S/M/L/XL — относительная сложность, не обещание срока и не человеко-дни.

### Обнаруженные проблемы и обязательные работы

| ID | Приоритет | Задача/причина | Зависимости | Компоненты | Критерий готовности | Сложность |
|---|---|---|---|---|---|---|
| R01 | P0 | Определить реально продаваемый MVP; marketing шире реализации | owner/product decision | landing/demo/console/docs | согласованная capability matrix и ограничения pilot | M |
| R02 | P0 | Разобрать семь reported stock history/replay failures и make lint | dedicated test DB; provenance | tests/integration, commitment, postgres | root cause, regression assertions, reproducible full CI | M/L |
| R03 | P0 | Production stock/catalog design, не снять test gates | provider contract, proof model | stocksync/catalogsync/adapters | idempotency/unknown/reconcile/partial success/limits, approved rollout | XL |
| R04 | P0 | Tenant/security release review и fixture isolation | current identity model | console/identity/postgres/build | every route cross-tenant tests, no fixture in release artifacts | L |
| R05 | P0 | Build-context hygiene и secret lifecycle | inventory локальных артефактов без чтения значений | Dockerfile/.dockerignore/credentials | allowlisted context, no sensitive build layers, rotation/restore test | M |
| R06 | P0 | Production hosting/backups/restore/observability | infrastructure owner | deployment/DB/SMTP/worker | TLS/proxy spec, restore rehearsal, RPO/RTO, alerts/runbooks | L |
| R07 | P0 | Действующие legal docs и acceptance model | entity/jurisdiction/data map, lawyer | landing/legal, identity, DB | signed-off documents, versions, acceptance evidence | L |
| R08 | P1 | Billing/entitlements/renewal/cancel/arrears | provider choice/legal/tariff | новые billing boundaries | signed webhook idempotency, ledger, quotas, cancellation E2E | XL |
| R09 | P1 | Assisted onboarding до production-safe первой синхронизации | R01,R03,R04 | integrations/imports/mapping | synthetic E2E + bounded approved provider acceptance | L |
| R10 | P1 | Multi-account lifecycle и явная OWNER-only модель | product decision | merchant binding/selector/credentials | несколько accounts same provider, revocation/ownership E2E | L |
| R11 | P1 | Согласовать команды/приглашения/RBAC либо убрать обещания | ownership model | identity/migrations/frontend | роли реально enforced; no cross-tenant access | L |
| R12 | P2 | Fair worker scheduling и shared provider quota | measured queue profiles | Worker/clients/queue | imports не starve, quota bursts bounded, backlog alert | L |
| R13 | P2 | Retention/partitioning/DB performance | storage metrics | observations/catalog/audit | query plans, retention policy, load/restore tests | L |
| R14 | P2 | Full-stack CI, release artifact policy и SCA | current build manifests | GitHub Actions/web/Docker | Go+TS+E2E+SBOM/advisory checks, migration compatibility | M/L |
| R15 | P2 | Свести docs/legacy auth/CSS duplication | tests first | auth fixtures/docs/App/CSS | удалён неиспользуемый runtime fallback, UX без regressions | M |
| R16 | P2 | Privacy export/deletion и support runbooks | R07 retention decision | identity/audit/support | verified export/deletion/incident workflows | L |

### Новые продуктовые направления — не найденные дефекты

| ID | Приоритет | Идея | Зависимости | Компоненты | Критерий готовности | Сложность |
|---|---|---|---|---|---|---|
| G01 | P1 или P3 по MVP | WB adapter | vendor contract, credentials, quotas | infrastructure/application/DB/UI | read-first vertical slice и safe write design | XL |
| G02 | P3 | Yandex Market adapter | тот же lifecycle framework | integration modules | scoped end-to-end with unknown recovery | XL |
| G03 | P3 | Полноценные заказы/labels/returns/status writes | external commitments design | orders/domain/adapters/UI | no duplicate orders, transitions/reconciliation | XL |
| G04 | P3 | Analytics/profit/fees | financial source model | ingestion/ledger/aggregates/UI | reconciled financial definitions и tests | XL |
| G05 | P3 | Repricing/markup bulk engine | approvals/guardrails | pricing/policy/writers | bounded preview/delta caps/currency/history | XL |
| G06 | P3 | Дополнительные queues/cache/sharding | нагрузочные измерения | infra/worker/DB | доказанная необходимость и migration plan | L/XL |

Не превращать идеи G01–G06 в обязательный rewrite текущего ядра.

## Модель нагрузки: только оценочные сценарии

Допущения для сравнения, не реальные клиенты: на одного tenant 9 marketplace accounts (3 WB+5 Ozon+1 Yandex) + 1 MS; по 5 000 SKU/account; 100 заказов/tenant/day; polling каждого account раз в 15 минут. Это бизнес-ориентированный сценарий, текущий код обслуживает лишь MS/Ozon subset, не все 10 источников.

Формулы: accounts=10N; SKU-account rows=50 000N; traversal starts/hour=40N; order observations/day=100N. Один traversal может потребовать много HTTP requests/деталей, поэтому это не RPS. Не все 5 000 SKU одинаковы между accounts; counts — объём source records, не unique products.

| Клиенты N | Accounts | SKU-account rows на один обход | Traversal starts/hour | Orders/day |
|---:|---:|---:|---:|---:|
| 10 | 100 | 500 000 | 400 | 1 000 |
| 100 | 1 000 | 5 000 000 | 4 000 | 10 000 |
| 500 | 5 000 | 25 000 000 | 20 000 | 50 000 |
| 1 000 | 10 000 | 50 000 000 | 40 000 | 100 000 |
| 5 000 | 50 000 | 250 000 000 | 200 000 | 500 000 |

Storage estimate нельзя переводить в GB без измерения row/JSON/index/WAL size и dedup ratio. Верхняя модель append-only: retained rows ≈ observations per traversal × traversals × retention, с поправкой на dedup. Память imports зависит от largest concurrent account traversal, не только общего N.

### По этапам

- 10 клиентов: доказать корректность и recoverability. PostgreSQL outbox может быть достаточен; один serial worker не гарантирует latency SLA. Нужны per-account quota и bounded job duration.
- 100: измерить pool contention, independent tenants fairness, импортные batches, source storage growth и период freshness. Несколько workers увеличивают throughput и quota pressure одновременно.
- 500: отдельные worker classes/weighted scheduling, retention и admission control; измерить repeated catalog/provider reads.
- 1 000: DB index/WAL/vacuum, source history partitioning, regional/provider throttling, bulkhead по accounts, capacity/restore time.
- 5 000: multi-host queue coordination, rate-limit distribution, partitioning/archival strategy, backpressure и deterministic isolation. Broker/cache/sharding — решения после профиля, не факт текущей необходимости.

## Риски потоков данных

Infinite loop: catalog field authority + baseline fingerprints снижают риск ping-pong; external changes и ambiguous readback остаются reason to block. Duplicate orders: identity/read evidence есть, order writer отсутствует. Lost updates: expected revisions и fresh reads помогают, но vendor-side atomic compare-and-write contract не доказан. Wrong prices: Ozon updates намеренно узкие, generic FieldPrice не является safe repricer. Stale stock: snapshot uncertainty/coverage proof не позволяют интерпретировать missing как zero. Queue replay: read imports допускают retries, external writes требуют journal/fence/reconcile.

Без нагрузочных тестов не утверждается, что текущая система выдерживает хоть одну указанную численность с конкретным SLA.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [internal/application/worker.go:1](../../internal/application/worker.go#L1) | 38: Process; 62: Run; 150: advanceCatalog |
| [internal/infrastructure/ozon/client.go:1](../../internal/infrastructure/ozon/client.go#L1) | 62: NewControlledTestStockClient; 100: Error; 106: New; 148: sleep; 161: wait; 172: retryAfter; 195: post; 326: observeCatalogRequest; 337: UnmarshalJSON; 346: id; 393: nextCursor; 406: Read |
| [internal/infrastructure/moysklad/client.go:1](../../internal/infrastructure/moysklad/client.go#L1) | 28: allowedReadPath; 56: Error; 57: New; 82: wait; 93: sleep; 106: retryAfter; 127: millisHeader; 138: rateDelay; 155: deferRequests; 163: get; 280: pages; 394: number |
| [internal/config/stock_publication.go:1](../../internal/config/stock_publication.go#L1) | 13: StockPublicationTestEndpoint |
| [internal/catalogsync/runner.go:1](../../internal/catalogsync/runner.go#L1) | 21: RunNext; 54: dispatch; 87: write; 101: complete; 112: stop; 121: now; 128: validate; 154: corresponds; 169: containsCommand |
| [.dockerignore:1](../../.dockerignore#L1) | configuration / declarations; see file |
| [migrations/00039_merchant_accounts.sql:1](../../migrations/00039_merchant_accounts.sql#L1) | 6: saas_merchant_accounts |
| [tests/integration/stock_commitment_test.go:1](../../tests/integration/stock_commitment_test.go#L1) | 33: newStockFixture; 35: newStockFixtureWithHistory; 82: stockCalc; 91: stockHasReason; 96: TestStockImportedEvidenceIsUnknownAndImmutable; 146: TestStockReportedZeroStillNeedsProof; 161: TestStockUnconfirmedAndSharedMappings; 186: TestStockConcurrentOwnershipClaim; 211: TestStockRegistrationDoesNotAdoptOldImports; 231: TestStockWarehouseConflictDoesNotExposeOtherTenant; 251: TestStockDatabaseRejectsReadyWithoutDurableProof; 261: TestStockPolicyVersionsAreImmutable |
