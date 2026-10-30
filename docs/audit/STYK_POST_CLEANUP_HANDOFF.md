# STYK post-cleanup handoff

Дата: 30 октября 2026. Ветка: `chore/styk-cleanup-and-demo-seed`. Цель этого этапа — безопасное локальное тестирование, не production rollout.

## Executive Summary

До изменений существовали Go/PostgreSQL/React-приложение, незакоммиченные auth/UI изменения и глубокий Read-Only аудит. Тесты истории остатков ранее сообщали replay mismatch; make lint захватывал ignored/local Go files и CRLF checkout; runtime console ещё содержала старое in-memory operator-session implementation. Рабочий dev-стенд смешивался с прежним preview.

Исходное состояние сохранено коммитом `d919088`; untracked-артефакты дополнительно сохранены в ignored локальном архиве. Ничего не сбрасывалось. Выполнены точечная очистка auth/test scaffolding, build-context hygiene, безопасный reset/backup инструмент, development-only seed, настоящий verified demo owner и проверки полного локального сценария.

Локальное приложение готово к тестированию. Production readiness не заявляется: общая публикация остатков/каталога закрыта, WB/Yandex adapters, полноценный order management, analytics и billing не добавлены.

## Актуальная архитектура

- `cmd/api/main.go`: Go net/http API, loopback listener behind trusted proxy; merchant email/password и DB-backed cookie sessions.
- `cmd/worker/main.go`: imports, safe mock tasks, отдельно gated stock/catalog runners.
- `internal/domain`, `application`, `catalog`, `catalogsync`, `commitment`, `stocksync`: существующие domain/workflow модули, их бизнес-правила не переписаны.
- `internal/postgres`: pgx/PostgreSQL SQL, explicit transactions, outbox, fencing, audit и actor guards.
- `internal/identity`, `credentials`: verification/reset/bcrypt/sessions и encrypted provider secrets; не заменены dev-авторизацией.
- `internal/infrastructure/{moysklad,ozon}`: реальные provider adapters; никакие реальные provider requests в этом этапе не требовались.
- `migrations/`: те же 44 Goose migrations, исходники не изменялись.
- `web/src`: React 19/TypeScript/Vite/TanStack Query console.
- Новые `cmd/demo-seed`, `internal/devseed`, `scripts/demo-db.py`, `scripts/dev-demo.py`: изолированные dev tooling, не production startup.
- `internal/sessionfixture`: единая реализация synthetic test sessions; обычные API/worker dependency graphs её не включают. Normal runtime больше не содержит operator session map/token decoder/logout fallback.

.NET, Redis, RabbitMQ и новые библиотеки не вводились.

## Что удалено и что сохранено

Удалены runtime operator-session structures/authorization implementation и дублированные fixture implementations; неиспользуемые default-test Handler/helper/logout. Test-only adapter оставлен для существующих business integration tests. Его cookie называется `styk_fixture`, он не является методом входа пользователей.

Tracked-файлы и зависимости не удалялись ради количества: проверка imports/references не выявила обоснования удалять рабочие React pages, lazy imports, provider packages, migration history или demo assets. Все frontend dependencies используются. `go mod tidy` только перевёл используемый bcrypt dependency x/crypto в direct require без изменения версии.

Удалены два устаревших ignored local-only артефакта: прежний plaintext local login file и одноразовый старый launcher. Existing experiment evidence, encryption keys, старые базы и pre-existing design screenshots сохранены. Несвязанные screenshots остаются локальными untracked-файлами и не публикуются в PR.

Полная таблица файлов и commit provenance: [CHANGELOG](STYK_POST_CLEANUP_CHANGELOG.md).

## Database Report

Используется новая `styk_demo_dev` в подтверждённом локальном контейнере PostgreSQL 17.6 `styk-redesign-db`, host 127.0.0.1, port 55433. Старый preview database и реальные provider credentials не очищались.

Перед reset проверялись exact database comment, container image/loopback mapping, только demo owner, отсутствие credential rows и non-demo external account IDs. Custom dump проверялся pg_restore list, восстанавливался в отдельную только что созданную БД, сравнивались tables/row counts. Затем только business tables demo database были очищены транзакционным TRUNCATE с locks и повторными guards. Goose history/schema сохранены, версия 44.

В локальной demo DB валидированы 13 FK, объявленных прежними миграциями как NOT VALID. Новые/изменённые production migrations не создавались. Все public foreign keys теперь validated; чужих tenant rows и stock/price consistency violations — 0.

| Данные | Фактическое количество |
|---|---:|
| Verified owner / организация | 1 / 1 |
| Физические товары | 500 |
| Ozon listings / synthetic price snapshots | 2 500 / 2 500 |
| Склады MS / Ozon | 3 / 15 |
| Warehouse stock rows MS / Ozon | 1 500 / 7 500 |
| Product mappings / warehouse mappings | 1 960 / 15 |
| Ozon postings / MS order observations | 1 000 / 1 000 |
| Import operations / all outbox tasks | 10 / 22 |
| Mock tasks consumed to succeeded | 12 |
| Provider credentials | 0 |

Это 1 000 демонстрационных заказных сценариев с соответствующими source/marketplace observations, не 2 000 независимых покупок. Source order windows разбиты на три периода по 30 дней. Никаких настоящих данных покупателей нет.

WB/Yandex не добавлены фиктивными API-подключениями: существующий presentation demo показывает 3 WB + 5 Ozon + 1 Yandex + 1 MS. В реальной БД зарегистрированы только 6 явно синтетических MS/Ozon accounts без токенов.

Подробности: [DATABASE](STYK_POST_CLEANUP_DATABASE.md), [DEMO_DATA](../development/DEMO_DATA.md).

## Demo Account

- URL: http://127.0.0.1:5173
- Email: `demo@styk.local`
- Организация: `STYK Demo Company`
- Фактическая роль: `OWNER` — администратор текущей merchant-модели.
- Email подтверждён, user активен.
- Friendly label STYK Demo Admin не записан в вымышленное поле: существующая user schema не имеет display-name column.
- Проверка: login 204, session 200, HttpOnly/SameSite Strict cookie, CSRF present; чужой tenant 404, anonymous 401, missing CSRF 403, retired secret payload 400.

Пароль — криптографический, более 20 символов. Он не включён в документы/ZIP/Git. Получить один раз в доверенном локальном терминале:

```powershell
python E:/work/styk/scripts/dev-demo.py password
```

Доставка — encrypted current-user DPAPI file; после выдачи delivery file удаляется. Runtime state содержит только DPAPI ciphertext для дальнейших проверок. Не выводить пароль в PR/CI и не выполнять password-команду ради отчёта.

## Infrastructure Report

Работают PostgreSQL, Mailpit, один Go API, один безопасный Go worker и Vite console. Дополнительно доступны standalone demo на 5185 и landing preview на 5186. Go executables построены для Windows из pinned Go Docker tooling. PID/log/runtime files находятся под ignored `.local/demo/`.

API/worker получают отдельный local DB role и новый локальный encryption key. Inherited marketplace/test-publication configuration удаляется из окружения launcher. Ни provider keys, ни production write gates не включались. Worker readiness доказана не фиктивным endpoint, а alive process и 12 succeeded mock tasks.

Точные адреса и запуск/остановка: [INFRASTRUCTURE](STYK_POST_CLEANUP_INFRASTRUCTURE.md).

## Testing Report

PASS: make lint (gofmt/vet/golangci), make test, go test -race ./..., make build, go build ./..., make migrate, полный make test-integration, отдельный seed integration/race. Frontend TS/ESLint/build и 203 Vitest tests; два Playwright demo smoke tests. Standalone demo и landing/nested demo builds успешны. Legal HTML URLs возвращают 200.

Во время разработки были failures: seed SQL не совпадал с nullable/generated-column правилами, count query сначала не передавал параметр, fixture extraction сначала менял error body, linter нашёл лишние test helpers. Они исправлены; failure seed не оставлял partial imports. Итоговые успешные прогоны выполнены после исправлений.

Семь прежних stock-history failures устранены в тестовой проверке: wire JSON исходного и прочитанного объекта был одинаков, а reflect.DeepEqual сравнивал time.Location/внутреннее представление. Теперь сравнивается полный сериализуемый snapshot. Assertions о неизменности history/rollback сохранены, business calculator/persistence code не изменён.

Полная таблица PASS/FAIL/SKIPPED/NOT VERIFIED: [TESTING](STYK_POST_CLEANUP_TESTING.md).

## Security Review

Generated credentials отсутствуют в tracked/candidate source по exact-value scan; .env/.local/дumps/private keys не добавлены в Git. Docker context исключает .local, frontend outputs, caches и audit ZIP. Provider credential rows = 0. Seed требует exact local marked DB и verified sole demo user; production-mode reset отвергнут до mutation. Auth, tenant binding, actor guards и fail-closed external writes сохранены.

Security review — статическая и локальная functional проверка, не penetration test/production certification. Dependency CVE scan, live provider contracts и production deployment не заявляются проверенными. Подробнее: [SECURITY](STYK_POST_CLEANUP_SECURITY.md).

## Remaining Problems & Technical Debt

Подробный список с P0–P3: [REMAINING](STYK_POST_CLEANUP_REMAINING.md). Главное: production publication/field coverage, billing/entitlements/legal acceptance, полноценные orders/returns/analytics, shared quotas/fair scheduling, CI/frontend coverage, operational restore/monitoring и portability Windows tooling.

Нет утверждения, что repo стал production-ready или что вся функция из демонстрационного интерфейса реализована.

## NEXT STEPS FOR CHATGPT

1. Начать review с baseline `d919088`: он сохраняет pre-existing UI/auth/audit work; отделять его от cleanup commits.
2. Проверить `internal/console/auth.go` delegation и отсутствие `internal/sessionfixture` в normal API/worker deps. Не возвращать старый key login и не ослаблять merchant auth.
3. Проверить `scripts/demo-db.py`: exact target/marker, credential/user guards, backup restore, verified temporary DB deletion, Goose-preserving reset; не обобщать на произвольный DSN.
4. Проверить `internal/devseed/seed.go` transaction/advisory lock/idempotency/count assertions и development boundary; SQL fixture не считать provider observation.
5. Проверить stock test JSON comparison: сравниваются все wire fields, а не только status или quantity. Не ослаблять immutable evidence assertions.
6. При добавлении production writes сначала спроектировать idempotency, unknown outcome, partial success, reconciliation, account quota и rollout. Не снимать fixed test allowlists.
7. Следующие разумные задачи: portable disposable seed CI job, weighted worker fairness, key rotation/restore, узкий MS/Ozon commercial vertical slice; затем billing/legal и только согласованные WB/Yandex/order features.
8. Соседние demo/landing источники не переписывались; сохранять distinction presentation vs operational API.

## Pull Request и пакет

Фактический PR и статус: [PR](STYK_POST_CLEANUP_PR.md). Никакого merge не выполняется. Машиночитаемый результат — [REPORT.json](STYK_POST_CLEANUP_REPORT.json). Архив `STYK_POST_CLEANUP_AUDIT.zip` содержит только явно перечисленные отчёты и development guide, без исходников, credentials, dumps и logs.

Краткая актуальная карта: [REPOSITORY](STYK_POST_CLEANUP_REPOSITORY.md).
