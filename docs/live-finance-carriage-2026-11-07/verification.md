# Verification — 2026-11-07

Проверки используют disposable loopback PostgreSQL и synthetic provider fixtures. Команды тестов не получают production credentials; LIVE выполнялся отдельно и отражён в operations.jsonl.

| Проверка | Результат |
|---|---|
| make check-plan | PASS; high-risk/full required из-за schema/shared dispatch |
| go test -race ./internal/providercall ./cmd/worker -count=1 | PASS:1.029s /1.010s; read observer и независимая ротация трёх outbox runners |
| Affected domain/service/adapters race tests | PASS; точные commands/timings в private agent-checks ledgers |
| Carriage PostgreSQL safety,4 cases | PASS3.186s: generic worker exclusion, scoped/global fence, revoke, unknown, immutable payload |
| Synthetic carriage/accrual merchant HTTP+PG | PASS5.683s; final full дополнительно проверяет test-only provider-auth assertion |
| Finance ledger/archive/policy/rollback PG | PASS3.578s; staged rollback assertion finalPASS3.281s |
| Crypto evidence tests | PASS1.016s; credentials1.009s |
| Frontend scoped6 files | 36PASS2.67s |
| Frontend full | 418PASS/50files6.60s |
| Frontend ESLint,TypeScript,Vitebuild | PASS; Vite1.22s, прежнее предупреждение main chunk658kB осталось |
| Browser selected4cases | PASS18.0s;1440/1366/390px; screenshots/computed-style/keyboard/contrast evidence в ui-verification.md |
| Shared DEV backup restore+74–75 Up/Down/Up | PASS2.85s; PG18, full encrypted backup restored without network |
| Fresh backup restore+76 Up/Down/Up | PASS2.76s |
| Actual encrypted archive DB readback | PASS7/7,115120raw bytes |
| All LIVE response evidence | PASS620/620 GCM/SHA/metadata,5381966bytes |
| git diff --check | PASS |

Первый make check-full обнаружил невалидное новое тестовое утверждение о буквальном совпадении SQL predicate после historical73 wrapper roundtrip. Проверка исправлена: сохранность точного pre75 predicate проверяется при Down75→Down73, затем существование guard после Up. Production Up migrations не изменялись. Первый запуск остановлен после установленного FAIL на124.948s, он **не PASS**; исходники теста менялись при его выполнении. Последующий полный прогон выполнен на замороженном исходном manifest с общим внешним deadline299s; итог ниже.

Ранние локальные технические неуспехи: scoped lint без project GOTOOLCHAIN выбрал Go1.26 для lint и был отклонён целевым Go1.27.3; повтор с GOTOOLCHAIN=go1.27.3 PASS0issues. Custom audit transport в private LIVE harness был отвергнут adapter guard до dispatch; вместо обхода guard добавлен opt-in read observer в существующий coordinated transport. Это не external mutation и не потерянный HTTP вызов. Реальные400 negative-contract reads не замаскированы под200.

Пределы: нет реального финансового POST/posted-draft, monetary-return draft, новой carriage CREATE/APPROVE или физического handoff. Нет LIVE browser seller-session acceptance; browser fixtures не доказывают production outcome. Нет реального amended source или UNKNOWN write в этом запуске; lost-ACK/correction/concurrency покрываются fixtures. Полный перечень первого-seller blockers и отсутствующих accounting choices приведён в failures.md и scenario-matrix.md.

## Итог полного прогона

**make check-full — PASS, 259.096s (4m19s)**, UTC11:57:42.719–12:02:01.815. Exit0, deadline не достигнут, source manifest не изменился: `cfe9de41b37ef2c72f4b83ac6f13a4a05998f1715b6fdf4e999478264c5eb099`.

- Go lint и workflow lint: PASS2.445s,0issues.
- Go race/integration/build: PASS4m18.668s; всего top-level **1666PASS /27SKIP /0FAIL**, с subtests **4543PASS /29SKIP**.
- Business integration coverage: все **584/584** скомпилированных top-level имён получили terminal outcome в8 непересекающихся workers: **568PASS /16SKIP**. Тесты не исключались для достижения времени.
- Frontend: **418PASS /50files**, lint/typecheck/build PASS;63 browser scenarios PASS2m12.334s. Вся frontend/presentation lane2m37.449s.
- Disposable PostgreSQL и принадлежащие проверкам дочерние процессы удалены. Пользовательские контейнеры не останавливались.

Полный JSON event ledger и coverage manifest сохранены в закрытых `.local/checks/full-2205995269/` и `.local/live-finance-carriage-2026-11-07/`; они содержат точные terminal events. Ни один SKIP не назван PASS. Пропуски top-level перечислены ниже (process helpers/отдельно включаемые старые browser/registration harnesses); два дополнительных SKIP относятся к import browser subtests.

- `TestOwnedListenerHelper`
- `TestConcurrentRegistrationDoesNotTransferAccount`
- `TestConnectionBrowser`
- `TestConnectionRegistrationIntegration`
- `TestConnectionSettingsArchivePreservesEvidence`
- `TestConnectionSettingsConcurrentEditsAndRollback`
- `TestConnectionSettingsPersistenceIsolationAndCredentialIndependence`
- `TestCredentialConcurrentRevisionAndRegistrationRollback`
- `TestCredentialLegacyScopedProbeBeforeExplicitRegistration`
- `TestCredentialStorageIdentityReplayScopeAndStoredWorker`
- `TestRegistrationPersistenceFailureDoesNotLeavePartialState`
- `TestCatalogMerchantBrowserStack`
- `TestControlledStockAutomaticBrowser`
- `TestControlledStockAutomaticMultiBrowser`
- `TestControlledStockPublicationBrowser`
- `TestControlledStockRecoveryBrowser`
- `TestMerchantBrowserWorkspaces`
- `TestMoySkladSettingsBrowser`
- `TestOrderFlowFullE2E`
- `TestOrdersNativeProcessChild`
- `TestOrdersProcessChild`
- `TestOzonFulfillmentFbsE2E`
- `TestProviderCoordinatorProcessChild`
- `TestRedesignMatchingBrowser`
- `TestStockPublicationCrashHelper`
- `TestStockShadowBrowser`
- `TestWarehouseOverviewBrowser`
