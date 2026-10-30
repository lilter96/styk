# STYK: итог технического аудита

Дата: 30 октября 2026. Объект: текущее рабочее дерево E:/work/styk, включая незакоммиченные изменения, и доступные соседние frontend-проекты. Это аудит кода, а не сертификация безопасности или успешности внешних интеграций.

## Главный вывод

STYK — Go-монолит с отдельными API и worker, PostgreSQL и React/TypeScript-консолью. Это инженерный прототип с развитой моделью доказательств, сопоставлений и контролируемых внешних операций. Он не готов продаваться как показанный на лендинге универсальный SaaS для трёх маркетплейсов.

Подтверждён код для МойСклад и Ozon. Адаптеров Wildberries и Яндекс Маркета нет. В рабочей консоли нет полноценного управления заказами, аналитики продаж, тарифов или оплаты. Демо показывает эти сценарии на локальных массивах. Реальные внешние записи ограничены отдельными тестовыми разрешениями, которые нельзя считать production-возможностью.

## Как читать комплект

- [Продукт и матрица функций](01_PRODUCT_OVERVIEW.md).
- [Карта репозиториев](02_REPOSITORY_STRUCTURE.md), [архитектура](03_SYSTEM_ARCHITECTURE.md), [backend](04_BACKEND_AUDIT.md), [frontend](05_FRONTEND_AUDIT.md).
- [Интеграции](06_INTEGRATIONS.md), [БД](07_DATABASE.md), [безопасность](08_SECURITY_AUDIT.md).
- [Эксплуатация](09_INFRASTRUCTURE.md), [тесты](10_TESTING_AND_QUALITY.md), [SaaS и юридические пробелы](11_SAAS_BUSINESS_READINESS.md).
- [Риски и roadmap](12_RISKS_AND_ROADMAP.md), [API](13_API_REFERENCE.md), [потоки](14_DATA_FLOWS.md).
- [Открытые вопросы](15_OPEN_QUESTIONS.md), [дизайн](16_DESIGN_SYSTEM.md).
- Для передачи ChatGPT первым загрузить [самодостаточный handoff](17_CHATGPT_HANDOFF.md).

## Что реально есть

Регистрация с проверкой почты, установка и восстановление пароля, persistent cookie sessions, серверное определение tenant; сохранение зашифрованных ключей МойСклад/Ozon; scoped чтение каталогов, складов и остатков; durable импорты через PostgreSQL outbox; reviewed product/warehouse mappings; история и аудит; чтение заказов как доказательств; fail-closed расчёт обязательств и остатка; preview/approval/dispatch/readback для ограниченного каталожного эксперимента. Доказательства: cmd/api/main.go, cmd/worker/main.go, internal/console/*, internal/catalogsync/*, migrations/00001–00044.

Наличие реализации не означает повторно проверенную работоспособность: в этом Read-Only аудите тесты, сборки, миграции и внешние API не запускались.

## Пять главных рисков

1. Production-синхронизация остатков и каталога для произвольных клиентов намеренно закрыта; снятие gate без отдельного дизайна создаст риск overselling и неверных внешних изменений.
2. Изоляция зависит от цепочки HTTP → identity → scoped SQL и actor guards. PostgreSQL RLS не найден; доверенные CLI и фоновые операции имеют другую границу доверия.
3. .dockerignore исключает .env, .git и bin, но не .local и node_modules. COPY . . в build-stage может передавать ненужные локальные чувствительные артефакты в build context.
4. Нет подтверждённого production-контура backups/restore, monitoring/alerting, key rotation и безопасного deployment/rollback.
5. Нет коммерческого цикла: billing, entitlements, юридическое принятие документов и заявленные WB/Yandex/аналитика не реализованы.

Это не пять доказанных эксплуатируемых уязвимостей. Каждая оценка и доказательство раскрыты в 08 и 12.

## Критерии готовности

| Уровень | Оценка |
|---|---|
| Исследовательский прототип | Подтверждён структурой и кодом |
| Локальный инженерный стенд | Ранее демонстрировался; в текущем аудите не перезапускался |
| Контролируемый pilot МойСклад/Ozon | Возможен после исправления/объяснения тестовых блокеров и утверждения границ pilot |
| Самостоятельный платящий клиент | Не готов: отсутствуют billing, production publication, эксплуатационные и правовые процедуры |
| Масштабируемый SaaS на 1 000+ клиентов | Не подтверждён: нет релевантных нагрузочных измерений |

Прежний этап этой беседы сообщил: frontend 203 теста, Go unit/race/build/golangci успешно; make lint с форматными отличиями, полный integration с семью stock-history/replay failures. Это исторические наблюдения, не результаты данного аудита. Один failure ранее воспроизведён на HEAD; остальные требуют независимой проверки.

## Пять первоочередных действий

Зафиксировать узкий продаваемый MVP; закрыть проверяемые дефекты и CI; спроектировать production-публикацию без ослабления тестовых gates; подготовить эксплуатацию и восстановление; завершить billing и юридический onboarding. Детальные зависимости, критерии приёмки и сложность приведены в 12.

## Ограничения

Исходный код не менялся. Значения .env, локальные login-файлы, DB records и пользовательские данные не читались. Никаких production API, регистраций, синхронизаций или новых тестовых данных. ZIP содержит только эти 18 Markdown-документов.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [go.mod:1](../../go.mod#L1) | configuration / declarations; see file |
| [cmd/api/main.go:1](../../cmd/api/main.go#L1) | 21: main; 27: run; 66: configuredHandler; 90: apiForMode |
| [cmd/worker/main.go:1](../../cmd/worker/main.go#L1) | 28: catalogClient; 85: moyskladCatalogClient; 142: main; 148: run |
| [internal/console/http.go:1](../../internal/console/http.go#L1) | 20: MerchantHandler; 31: handlerWithAuth; 152: writeJSON; 156: writeError |
| [internal/catalogsync/runner.go:1](../../internal/catalogsync/runner.go#L1) | 21: RunNext; 54: dispatch; 87: write; 101: complete; 112: stop; 121: now; 128: validate; 154: corresponds; 169: containsCommand |
| [internal/config/stock_publication.go:1](../../internal/config/stock_publication.go#L1) | 13: StockPublicationTestEndpoint |
| [web/package.json:1](../../web/package.json#L1) | configuration / declarations; see file |

## Completeness and read-only checks

The bundle contains 18 reports, a 57-route normal-runtime inventory, 81 CREATE TABLE declarations with migration provenance, initial columns/constraint counts, constraint/trigger evolution, package imports and environment variable names. Counts describe source declarations, not deployed objects or successful runtime checks. Internal report/source links were checked for file existence; code fences and sensitive connection-string/private-key patterns were checked. Only docs/audit was written during this audit.
