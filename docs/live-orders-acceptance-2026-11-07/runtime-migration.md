# Development runtime, схема и резервная копия

Это проверка общей **development** PostgreSQL и локального API. Метка Neon branch `production` в `.env.local` не меняет назначение окружения из задания. STYK production hosting/deployment не выполнялись. Provider lifecycle и физические операции этим документом не подтверждаются.

## Исходное состояние

Read-only snapshot от **2026-11-07 00:07:12 UTC**: PostgreSQL **18.6**, схема **71**, размер БД **33 923 072 байта**. Миграции 72–73 отсутствовали. Запросы выполнены в явной транзакции `READ ONLY`; зашифрованные credential bytes, секреты, email/имена пользователей и buyer payload не выбирались.

В БД найдены четыре включённые credential records: два Ozon и два МойСклад, два tenant. Три подключения относятся к основному tenant; четвёртое МойСклад — к другому tenant. Это число сохранённых записей, а не доказательство текущего provider access или полномочий на запись. Настроены две пары. Ledger заказов: семь `RECONCILED`, один `BLOCKED`, ноль `UNKNOWN`; pending/running outbox в этом snapshot не было.

Старые API: PID `159777` (`8535493b…`, build revision `44666fe…`) и review API PID `529883` (`b4deefba…`, revision `306e579…`). Оба использовали правильную direct dev-БД и ключ, но не содержали новых merchant/finance tables в бинарнике. Они остановлены основным агентом перед миграцией; этот read-only audit процессы не останавливал. Vite PID `159797`, origin `http://127.0.0.1:5173`; worker при инвентаризации не найден.

Обезличенные fingerprints:

| Объект | SHA-256 prefix |
| --- | --- |
| Direct DSN | `7ff5c5c1870dd164` |
| Direct host | `048d7934fa35235a` |
| Декодированный 32-byte vault key | `f7c58ae56354f982` |

`credentials.FromEnvironment()` и обычный `credentials.Resolver{Store, Vault}` успешно создаются с текущей конфигурацией. Это проверка конструктора и формата ключа; реальное разрешение credential и provider identity подтверждаются отдельным API audit.

## Резервная копия до изменений

**00:12:45–00:13:02 UTC**, PostgreSQL client **18.6** (`postgres:18`): `pg_dump --format=custom` завершился с кодом 0 и без stderr. Direct подключение передавалось через `PG*` environment, не через argv. Поток сразу зашифрован OpenPGP AES256; plaintext dump/config копии на диск не записывались. Пароль OpenPGP передавался через FD и получен domain-separated HMAC от существующего vault key; новый plaintext backup secret не сохранялся.

Каталог: `.local/live-orders-acceptance-2026-11-07/backups/20261107T001245Z/`, режим каталога 0700, файлов 0600.

- `database-schema71.dump.gpg`: **1 967 857 bytes**, ciphertext SHA-256 `8a182e0a06d02c55eb4bf7e1ffd4f260ce2b24b7a4d5a016427e5dd60e7d4599`.
- Полная authenticated decryption прошла; plaintext только в памяти. Заголовок `PGDMP`, format `1.16.0`; `pg_restore --list` завершился с кодом 0, **1203 TOC entries**. Plain dump SHA-256 `df4374b750697ca98452e29bd3ff4bb19f4726e394d3ed6f81f8b6cf52c07390`.
- `app-env.gpg`: исходная `.env.local` зашифрована; обратное чтение и hash comparison совпали.
- Полное восстановление в отдельную БД не выполнялось: проверены целостность шифрования, формат и каталог архива.

Артефакты: `.local/live-orders-acceptance-2026-11-07/backup-latest.json`, `backup-dev.py`, `inventory-safety-audit.json`, `scope-refs-private.json`. Последний содержит только минимальные точные scope/owner/reference IDs, без credential bytes или PII, и имеет режим 0600.

Для чтения архива необходимо отдельно сохранить исходный `STYK_CREDENTIAL_ENCRYPTION_KEY`; его копия внутри зашифрованной конфигурации не заменяет независимое хранение ключа. Восстановление dump не отменяет внешние API-эффекты. После provider writes требуется сверка operation ledger и forward-fix, а не слепое восстановление старой БД. Миграции с созданной audit history могут намеренно запрещать `Down`.

## Независимая проверка после миграции

Основной агент применил 72–73. Проверка от **00:18:11 UTC** установила:

- эффективная версия схемы **73**; `Store.RequireCurrentSchema()` — **PASS**;
- существуют `orders_merchant_intents` и все шесть `orders_finance_*` tables;
- существует `finance_outbox_scope_fk`;
- подробности проверены явной `READ ONLY` транзакцией;
- `/health/live` и `/health/ready` нового API возвращают **200** (`live`, `ready`).

Единственный найденный API — PID `1252051`, `.local/live-orders-2026-11-07/api`, SHA-256 `9c68caa4ab4b56450471cf8b587e4381fa9942417c0e6e532f990f673a875b32`, bind `127.0.0.1:8080`, origin `http://127.0.0.1:5173`. Его direct DSN и vault key fingerprints совпадают с исходными. Старые два API отсутствуют; worker не найден. Бинарник содержит merchant-intents/finance code; новый live finance field `pick_up_point_coinvestment` в нём ещё отсутствует, поэтому применение последующего parser fix требует отдельной пересборки/перезапуска и проверки.

Сырые DSN, токены, credential ciphertext, имена/email и buyer data в публичный документ не включены. Фактические provider reads/writes и их независимые readbacks описываются отдельно.

## Независимая сверка write ledger и чтения native streams

Read-only проверка БД от **00:32:39 UTC**: с **00:20:00 UTC** сохранены ровно **пять** уникальных write permits. У всех пяти есть `started_at`, состояние `finished`, `finished_at` и HTTP **200**. Активных write permits нет. Их хеши HTTP method + нормализованного endpoint совпали один к одному, в порядке отправки, с пятью строками `operations.jsonl`: два создания нейтральных статусов, создание пустого непроведённого customerorder и два обновления его статуса. У каждой строки ledger — `RECONCILED` и сохранённый независимый readback. Дополнительных provider requests эта сверка не выполняла; она проверяет DB permits и уже записанные readback evidence.

Количество и состояния order work совпали с исходным snapshot: **7 `RECONCILED`, 1 `BLOCKED`, 0 `UNKNOWN`**. Finance work и merchant intents отсутствуют. Это сравнение количества и состояний; оно не утверждает побайтовую неизменность всех строк БД. Проверка выполнена в явной транзакции `READ ONLY`. Обезличенный результат: `.local/live-orders-acceptance-2026-11-07/canary-write-audit-summary.json`.

Первый последовательный native-ingestion canary завершил `moysklad-native-points` успешно, затем `moysklad-order-changes` вернул `MoySklad transport failure`; исходный запуск не считается полностью прошедшим. При последующей диагностике **00:30:41 UTC** canary-процесс уже отсутствовал, blocking PIDs и ожидающих advisory locks не было, ingestion leases были освобождены. В БД уже имелся более поздний changes checkpoint revision 2 от **00:27:42 UTC** и две сохранённые changes pages; это согласуется с отдельным последующим успешным повтором, о котором сообщает основной агент. Ретроспективно установить точную причину первоначальной транспортной ошибки по этому snapshot нельзя; доказательств deadlock нет. Артефакт: `.local/live-orders-acceptance-2026-11-07/native-stall-readonly-diagnostic.json`.

## Финальное обновление локального runtime

После полного `make check-full` PASS (259.329 с) API PID 1252051 штатно остановлен
и заменён локальной сборкой проверенного кода. Новый PID **1350063**, binary
SHA256 `2f78895d24a05ce9f633ecec4ab7a5f025b194a4b62fe3e258630d103317eb09`.
В бинарнике присутствуют financial extension и ReadNativeDemandCoverage.
Actual process DATABASE_URL и исходный vault key совпали с текущей `.env.local`
(сравнивались в памяти; значения не печатались). Оба health endpoint — 200.
Постоянный общий worker не запускался. Детали: [verification.md](verification.md).
