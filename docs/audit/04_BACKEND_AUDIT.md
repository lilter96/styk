# Backend-аудит: фактически Go

## Stack и bootstrap

go.mod декларирует Go 1.27.3; pgx/v5 v5.7.6; x/crypto bcrypt; x/sync и x/text в module graph. Makefile и Dockerfile закрепляют Go toolchain, Goose v3.25.0 и golangci-lint v2.15.0. Это заявленные проектом версии; аудит не проверял доступность их загрузки или свежесть advisory databases.

Нет .NET, ASP.NET, DbContext, EF, Controllers, MediatR или .NET DI. Эквиваленты: net/http.ServeMux, explicit constructors, Go interfaces, pgxpool, SQL transactions.

cmd/api/main.go run: load config → postgres.Open → configuredHandler → http.Server → goroutine ListenAndServe → graceful Shutdown. API требует loopback behind trusted proxy; merchant mode требует encryption key и SMTP. disabled mode устанавливает только health. local mode отклоняется.

cmd/worker/main.go run: config/vault/DB → optional environment clients → credential resolver → import runner → stock publishers/scheduler → optional catalogWork → application.Worker.Run. Ошибки provider config могут оставлять соответствующие account jobs unclaimed: это нужно мониторить.

## Компоненты и вызовы

| Компонент | Назначение и вызов | Данные/зависимости | Ограничения |
|---|---|---|---|
| console.MerchantHandler / handlerWithAuth | API bootstrap, register handlers | postgres.Store, identity.Sender, credentials | business routing и orchestration смешаны местами |
| merchantAuth.authorized / authorizeScope | каждый protected route | session digest, merchant binding, Origin/CSRF, actor audit | OWNER-only account model; proxy assumptions |
| identity.Store | register/auth/session/reset/verify | bcrypt cost 12, random tokens, pgx transactions | нет публичного team/admin management |
| credentials.Vault / Resolver | credential save/client construction | AES-GCM, scope/revision AAD, HMAC digest | нет явной многоключевой rotation/key-id схемы |
| application imports / ozonjob.Runner | HTTP outbox → worker → read/persist | bounded provider reader + fenced DB write | in-memory traversal; complete ≠ snapshot |
| matching | deterministic candidates | offer_id vs article/code + observed imports | name/barcode не универсальное подтверждение |
| mapping review/confirmation | explicit identity approval | expiring session review, revisions, audit/idempotency | confirmation не доказывает unit/warehouse identity |
| commitment | proof-driven stock quantity | physical/reserve/obligations/safety/coverage | UNKNOWN/BLOCKED при неполных доказательствах |
| stocksync.Runner | frozen stock dispatch/readback | scoped Ozon client + target state + presence/fence | общая production publication закрыта |
| catalog.BuildPlan | чистый plan на уровне полей | confirmed mapping, authority, baseline fingerprints | набор Field не равен adapter capabilities |
| catalogsync.Service | config/products/preview/approve/reconcile | Store + scoped Resolver + AllowScope | writes только разрешённый test scope |
| catalogsync.Runner | claim → guard → dispatch → receipt/poll/readback | command hash, credential/mapping revision, claim token | UNKNOWN сохраняется; нет слепых retries |
| postgres.Store | SQL и explicit transactions | pgxpool, FK/checks/locks | большая зона ответственности, нет generic repo |
| orderEvidenceAPI | on-demand bounded read coordination | MS/Ozon credentials, progress tables, advisory lock/slots | synchronous request; не order fulfillment |
| doctor | scoped persistence/connectivity diagnostics | observed state, optional provider probe | не monitoring/alerting platform |

## Внешние операции каталога

Service.Preview собирает frozen Decisions, проверяет expected config revision, target fence и confirmed mapping. Preview хранит credential revisions и expires_at (5 минут). Approval выбирает целые reviewed products; не подменяет значения команды.

Runner.RunNext забирает Work с минутной lease. dispatch проверяет DB guard, resolves credential revisions, перечитывает source/target (freshDecision), binds writer intent, журналирует финальный dispatch guard. write имеет provider timeout; complete сохраняет outcome с claim token и expected state.

ReceiptAccepted → PROCESSING; applied → ACKNOWLEDGED; rejected → REJECTED; ambiguous → UNKNOWN. ACKNOWLEDGED не равно readback-verified. После readback/reconciliation возможен RECONCILED; структура состояния документирована в catalogsync/contracts.go.

Ozon update writer в validateCatalogCommand допускает только known name/description; наличие price/images/category в generic model не означает их update support. Создание — отдельная schema-validated ветка. МойСклад payload поддерживает ограниченный набор text/price/barcodes/references/attributes, archive, create product/variant; не документы и не stock movements.

## Ошибки, валидация, конфигурация

Ошибки представлены sentinel types + wrapping/context и HTTP machine codes; generic UI не показывает сырые provider payloads. Public auth JSON decoder запрещает unknown fields и лишний JSON; merchant body parser также запрещает duplicate keys/нестандартное написание tenant. Account IDs/scopes явно валидируются.

slog используется для process/job status; raw SQL errors встречаются в worker generic paths, поэтому нельзя утверждать отсутствие всех sensitive log risks. Controlled provider errors часто санитизированы. В нескольких местах имеются проигнорированные errors (например MaintainPresence heartbeat, constructors с уже проверенными options); это предмет точечной проверки, не автоматическое доказательство потери данных.

## Конкурентность и fairness

PostgreSQL lease + claim_token защищает завершение от stale worker. Account/target advisory locks и immutable history guards защищают пересечения. Catalog workflow даёт import-turn после каталога. Но stock publication и automatic branches в Worker.Run выполняют continue при успешной работе до импортов: INFERRED риск starvation при непрерывной очереди этих типов.

Rate limiter находится в client instance; пересоздание clients и несколько processes могут нарушить общий account budget. DB dispatch clock решает отдельные stock scopes, не универсальную распределённую rate-limit задачу.

## Долг и проверка

Большие функции/файлы и console SQL отличаются от строгих guidelines, но размер не измеряет корректность. Проверять transaction/actor guards для каждого нового write path; не расширять один generic MockHandler на реальные writes. Исторические integration failures и make lint ограничения см. 10. В текущем аудите ничего не компилировалось и не выполнялось.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [internal/identity/identity.go:1](../../internal/identity/identity.go#L1) | 59: WithSession; 63: SessionFromContext; 68: New; 79: canonicalEmail; 88: validPassword; 92: validName; 96: identifier; 104: randomToken; 112: tokenHash; 121: passwordHash; 131: passwordMatches; 141: rollback |
| [internal/identity/sessions.go:1](../../internal/identity/sessions.go#L1) | 13: CreateSession; 37: insertSession; 60: Session; 80: RevokeSession |
| [internal/credentials/vault.go:1](../../internal/credentials/vault.go#L1) | 31: Valid; 34: validID; 50: Scope; 63: New; 80: FromEnvironment; 95: aad; 105: Encrypt; 122: Decrypt; 143: Digest; 161: Resolve; 168: ResolveWithRevision; 195: ScopeKey |
| [internal/application/worker.go:1](../../internal/application/worker.go#L1) | 38: Process; 62: Run; 150: advanceCatalog |
| [internal/catalog/plan.go:1](../../internal/catalog/plan.go#L1) | 82: BuildPlan; 101: newPlan; 106: validatePlanRequest; 124: valid; 128: policyFields; 137: addField; 172: fieldPairFor; 188: valueAt; 196: manualValueAt; 204: baselineAt; 214: skipReason; 233: sourceSkipReason |
| [internal/catalogsync/service.go:1](../../internal/catalogsync/service.go#L1) | 20: Config; 34: SaveConfig; 54: Preview; 58: Approve; 63: Products; 77: History; 88: validateScope; 98: allowed; 102: now; 109: boundedPage; 116: newID |
| [internal/catalogsync/preview.go:1](../../internal/catalogsync/preview.go#L1) | 13: preview; 46: reviewContext; 69: selectTargets; 93: decide; 121: validateDecision; 152: mappingMatches; 159: observeDecision; 179: observeDestination; 203: updateDecision; 228: updateCommands; 253: commandForChange; 268: creationDecision |
| [internal/catalogsync/runner.go:1](../../internal/catalogsync/runner.go#L1) | 21: RunNext; 54: dispatch; 87: write; 101: complete; 112: stop; 121: now; 128: validate; 154: corresponds; 169: containsCommand |
| [internal/catalogsync/dispatch.go:1](../../internal/catalogsync/dispatch.go#L1) | 17: forAccount; 24: resolvePair; 37: freshDecision; 53: creationEnrollment; 59: recordReceipt; 81: validReceiptFor; 110: optionalID; 112: unknownReceipt; 123: createdIdentity; 138: safeReceipt; 155: safeProductStatus; 164: safeFieldErrors |
| [internal/postgres/catalog_claim.go:1](../../internal/postgres/catalog_claim.go#L1) | 14: ClaimCatalogWork; 50: fenceCatalogClaim; 72: expireCatalogDispatches; 105: expireCatalogWork; 120: lockCatalogWork; 143: writeCatalogWork; 162: catalogTerminal; 166: insertCatalogAttempt |
| [internal/console/order_evidence.go:1](../../internal/console/order_evidence.go#L1) | 56: registerOrderEvidenceRoutes; 62: get; 84: refresh; 111: runOrderRefresh; 141: orderRefreshError; 155: orderReaders; 183: orderCredential; 209: orderReaderError; 221: orderEvidenceLockKey; 227: lockOrderEvidence; 252: closeOrderEvidenceSession; 261: refreshSourceOrders |

## Numeric semantics

CONFIRMED: commitment.Evaluate (internal/commitment/calculator.go:44) uses math/big.Int internally for bounded whole-piece arithmetic after validation. When all required checks pass, it subtracts reserve, unreflected obligations, unseen-reduction bound and safety from physical stock and clamps a negative result to zero (lines 221-230). The result is stored as a decimal string. This differs from the general preference for int64 in AGENTS.md; exact arithmetic/size-limit rationale should be reviewed before changing types. A valid arithmetic result alone does not satisfy coverage, freshness, unit or publication proofs.
