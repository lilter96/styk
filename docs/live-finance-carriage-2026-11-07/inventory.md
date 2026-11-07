# LIVE inventory — 2026-11-07

Этот запуск использует действительные production API Ozon и МойСклад с подключениями из shared DEV Neon. Production приложение/БД не развёртывались и не мигрировались. Отсчёт операций начинается с нового `operations.jsonl`; пять нейтральных записей прошлого запуска сюда не включены.

| Объект | Результат свежего чтения |
|---|---|
| Подключения | 4: 2 Ozon, 2 МойСклад; enabled credentials 4 |
| Настроенные пары | 2, обе используют один аккаунт МойСклад; второй МойСклад не имеет пары Ozon |
| Ozon b13240589abe | 9 уникальных отправлений, все cancelled |
| Ozon cc964f192932 | 371: delivered 293, cancelled 77, delivering 1 |
| Общее покрытие | 380 уникальных отправлений за год по creation windows ≤31 дня, дополнительно unfulfilled за 24 дня назад/7 вперёд; полный проход страниц и detail reads |
| Готовые к сборке/передаче | 0; физическое событие не имитировалось |
| Финансовые периоды | 12 закрытых месяцев на каждый Ozon, всего 24 account-periods |
| Реализация | 7 существующих отчётов/254 строки; остальные 17 запросов вернули provider 404, это не нулевые отчёты |
| Подходящие договоры | Normal Service/Factory вернул 0 подходящих действующих Commission/None договоров для обеих настроенных пар |
| Финансовая политика | Не включена; ни ставки НДС, ни курс, ни комиссионер/договор не выбирались автоматически |
| Начальное состояние | schema 73; UNKNOWN 0; ожидающих/running outbox jobs 0; order work: RECONCILED 7, BLOCKED 1 |

Маски аккаунтов стабильны внутри этого журнала; соответствия и credentials не публикуются. Повторные identity/contract reads входят в число HTTP-операций, но не увеличивают число аккаунтов/отправлений.

## Изменения только shared DEV

Перед schema 74–75 получен полный зашифрованный pg_dump PostgreSQL 18.6; проверены ciphertext SHA256, полная аутентифицированная расшифровка, PGDMP/TOC и восстановление всего архива в PostgreSQL 18 без сети. На восстановленной копии 74–75 прошли Up→Down→Up. Goose применил 74–75 к shared DEV в 11:39:45–11:39:47 UTC. Четыре credential records сохранились. Старый локальный API остановлен; общий worker не запускался.

Encrypted backup и ключ восстановления находятся раздельно: ciphertext в закрытой `.local` папке; ключ выводится из существующего vault key с отдельным HMAC purpose, не хранится в открытом отчёте. Точные хеши, команды и результаты находятся в private backup/restore metadata; raw dumps/configs в Git не добавлены.

Миграция **76** применена к shared DEV в 11:52:49–11:52:51 UTC после новой encrypted backup и полного restore схемы75 в изолированный PG18. На восстановленной копии 76 Up→Down→Up прошёл за 2.76 с. Ни одна development migration не применялась к production БД. Up75 не менялся; исправлен только его unreleased Down, восстанавливающий прежний outbox predicate. Staged rollback76→75→74→73 проверялся только на disposable test DB.

Финальный read-only inventory: schema76, 7 immutable financial ledgers, 7 encrypted raw records, 14 diagnostic finance previews, 3 blocked carriage previews; financial work=0, carriage work=0, carriage attempts=0. Четыре credentials, обе config revisions, исходные order work states и отсутствие pending/running jobs сохранены. Локальный API пересобран: /health/live и /health/ready вернули200; это loopback dev run, не deployment. Первое локальное поднятие было отвергнуто из-за HTTP merchant origin; повтор использовал точный HTTPS origin только в окружении процесса, не ослаблял проверку и не менял .env.

Git после финального fetch: HEAD=origin/master=340c6f884046e8119de6b328019ed4c72b1cb303; рабочая ветка codex/orders-fbs-rfbs-lifecycle. Изменения прежних срезов сохранены; новые commits, push, merge и production deployment не выполнялись.
