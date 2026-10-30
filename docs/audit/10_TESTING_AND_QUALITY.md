# Тесты, качество и технический долг

## Методика

Это Read-Only аудит. Даже go test/npm test могут создать кэши/артефакты, fixtures и DB state; они не запускались. make lint/test/race/build/migrate/integration требования AGENTS.md зафиксированы как необходимые для этапа изменений, но текущий прямой запрет на изменение состояния имеет приоритет.

## Существующие уровни тестов

- Go unit tests: domain validation, matching, catalog planning, adapters against httptest/synthetic responses, application workflows, crypto/SMTP и SQL-related orchestration.
- DB integration: transactions/rollback, actor expiry/ownership, mappings/history, outbox lease, customer orders/postings, stock commitment/publication/recovery, catalog config/approval/readback/security.
- Browser-backed integration fixtures: Go test HTTP server + Playwright scenarios.
- React Vitest/jsdom: auth flow, imports/integrations, mapping/reviews, catalog, pending/unknown states.
- Playwright e2e: smoke, merchant workspace isolation, imports, mappings, warehouse, catalog, stock-shadow, controlled publication/recovery, redesign.
- Demo/landing package scripts: build есть; отдельный полноценный test pipeline в package manifests не найден.

Исходная инвентаризация: 88 *_test.go под internal и 66 под tests/integration; это количество файлов, не coverage или доказательство качества. Cmd tests и frontend учитываются отдельно.

## Исторические результаты, не новый прогон

В предыдущем изменяющем этапе беседы:
- Frontend: 203 tests, typecheck, ESLint, production build прошли.
- Go: unit/race/build и golangci-lint сообщили успех.
- make lint не прошёл из-за массовых gofmt differences существующего checkout.
- Полный integration прогон на dedicated DB сообщил семь failures stock history/replay tests.
- TestStockImportedEvidenceIsUnknownAndImmutable воспроизведён на unchanged HEAD archive.
- Новый TestMerchantRejectsRemovedKeyLogin прошёл; registration→Mailpit→verification→password login→session read был показан.
- Mock task был consumed worker до succeeded.

Эти сведения имеют provenance беседы и docs/local-startup-auth-removal.md. Не утверждать, что все семь failures имеют доказанную одну причину. Test output и baseline compare не повторены в этом аудите. Root cause остаётся UNKNOWN.

Имена reported failing tests: TestStockImportedEvidenceIsUnknownAndImmutable, TestStockPolicyVersionsAreImmutable, TestStockMissingMappingVersionIsBlocked, TestStockRepeatedImportAndMissingObservation, TestStockConflictsAndMappingVersionHistory, TestStockDecisionRollbackAndConstraint, TestStockMappingChangeDuringCalculation.

## Качество архитектуры

Сильные стороны: явные scopes, revision/fingerprint checks, nullable unknown evidence, immutable history, durable intents/receipts, fenced completion, запрет blind write retries, typed errors, bounded reads, много проверок rollback/concurrency/isolation.

Зоны долга:
1. postgres.Store — широкая persistence поверхность; generic repository отсутствует, но важна discoverability и границы модулей.
2. console/http.go и order_evidence.go содержат прямой SQL/coordination, размывая transport/application boundary.
3. Большой App.tsx и minified-style demo/src/main.tsx объединяют множество UI функций; повышают локальную связанность и review cost.
4. CSS imports/mapping/design-system overrides дублируют selectors; CSS refactor не делался.
5. Legacy operator auth structures остались после удаления key login; test-only fixture с integration tag и executable suffix требует отдельной release discipline.
6. Worker priority может отложить imports при stock backlog; rate limits instance-local.
7. Документация разного поколения местами описывает local operator и отсутствие auth, тогда как current code имеет merchant identity. Код приоритетнее.
8. CI не охватывает React build/tests и реальную SPA/proxy deployment.

SOLID нельзя оценить одной отметкой. Go composition/interfaces хорошо выражают зависимости, но некоторые consumer/store interfaces широки и SQL layer крупный. Не предлагается wholesale rewrite или внедрение CQRS/MediatR ради стиля.

## Что считать дефектом

BROKEN подтверждать воспроизводимым наблюдением либо однозначной кодовой ошибкой. Здесь historical test regressions — подтверждённые наблюдения прошлого запуска, не доказанная неисправность production stock system. Latent starvation, key rotation gaps, Docker context exposure — отдельно классифицированные risks. Отсутствующий billing — product gap, не bug.

Нет текущего coverage report, benchmark, dependency CVE scan, fuzzing corpus result или exhaustive audit по всем строкам. Не публиковать выдуманный coverage/RPS.

## Рекомендуемая verification matrix после разрешения изменений

| Проверка | Зачем | Изолированная среда |
|---|---|---|
| lint/vet/golangci + frontend ESLint/TS | build hygiene | ephemeral CI checkout |
| unit/race all | concurrent safety | isolated temp/cache paths |
| DB migrations Up/guarded Down/upgrade | schema compatibility | dedicated disposable DB |
| regression семи stock failures | root cause и invariant | original + changed snapshots |
| merchant IDOR/session expiry after lock | authorization | synthetic users/scopes |
| unknown write/partial success/restart | no duplicate commitments | httptest provider |
| E2E console/proxy/SameSite/CSRF | browser boundary | synthetic accounts and mail catcher |
| load/soak queue fairness/quota | saturation/capacity | simulated provider latency/429 |
| SCA/SBOM | dependency advisories | lockfile-only isolated scan |
| restore DB+key | recovery integrity | private disposable backup clone |

Production APIs и реальные provider mutations не требуются для этих основных проверок. Отдельный bounded contract experiment возможен только в явно разрешённом scope, вне этого аудита.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [Makefile:1](../../Makefile#L1) | configuration / declarations; see file |
| [web/package.json:1](../../web/package.json#L1) | configuration / declarations; see file |
| [tests/integration/stock_commitment_test.go:1](../../tests/integration/stock_commitment_test.go#L1) | 33: newStockFixture; 35: newStockFixtureWithHistory; 82: stockCalc; 91: stockHasReason; 96: TestStockImportedEvidenceIsUnknownAndImmutable; 146: TestStockReportedZeroStillNeedsProof; 161: TestStockUnconfirmedAndSharedMappings; 186: TestStockConcurrentOwnershipClaim; 211: TestStockRegistrationDoesNotAdoptOldImports; 231: TestStockWarehouseConflictDoesNotExposeOtherTenant; 251: TestStockDatabaseRejectsReadyWithoutDurableProof; 261: TestStockPolicyVersionsAreImmutable |
| [tests/integration/merchant_http_test.go:1](../../tests/integration/merchant_http_test.go#L1) | 33: Send; 48: token; 68: newMerchantBrowser; 77: request; 108: registerAndLogin; 144: TestMerchantHTTPWorkspaceAndObjectIsolation; 172: assertLegacyCredentialsHidden; 184: assertMerchantIsolation; 224: assertMerchantObjects; 295: assertMerchantMapping; 340: TestMerchantCannotReadOrRecoverForeignPublication; 406: TestMerchantRejectsRemovedKeyLogin |
| [tests/integration/merchant_actor_expiry_test.go:1](../../tests/integration/merchant_actor_expiry_test.go#L1) | 18: TestMerchantActorExpiryIsCheckedAfterLockWait; 37: expiringMerchantSession; 63: assertExpiredActorCannotCreateImport; 100: waitForMerchantSessionExpiry |
| [internal/console/auth_test.go:1](../../internal/console/auth_test.go#L1) | 9: TestAnonymousAndRetiredCookieRemainUnauthorized; 33: TestConsoleRequiresLoopback |
| [web/src/AppAuth.test.tsx:1](../../web/src/AppAuth.test.tsx#L1) | 23: mount |
| [internal/application/worker_test.go:1](../../internal/application/worker_test.go#L1) | 9: TestMockIdempotent |
| [docs/local-startup-auth-removal.md:1](../../docs/local-startup-auth-removal.md#L1) | configuration / declarations; see file |
| [.github/workflows/ci.yml:1](../../.github/workflows/ci.yml#L1) | configuration / declarations; see file |

## Benchmark source versus measured capacity

`internal/matching/engine_benchmark_test.go` contains benchmark code; this audit did not execute it or obtain a benchmark report. Matching microbenchmarks cannot establish end-to-end provider/DB/worker throughput or the tenant-capacity scenarios in 12.
