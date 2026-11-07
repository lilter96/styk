# Независимый readback

> Это evidence первоначального запуска. Последующая доработка Carriage, исправленные contract gates и новые проверки: [актуальный отчёт](../live-carriage-completion-2026-11-07/README.md). Исторические результаты ниже не переобъявляются новыми LIVE-операциями.


В этом запуске внешних WRITE 0. Поэтому число обязательных readback после WRITE равно 0; нельзя заявлять подтверждённое создание или проведение финансового документа/новой carriage.

Отдельно выполнены реальные независимые чтения: source monthly reports, metadata договоров, accrual types/by-day/postings, текущая carriage discovery и known-ID исторической carriage. Historical get вернул root envelope с status=closed, integration_type=ozon_optional, first_mile_type=dropoff, available_actions=[show_ui_assembly_list,get_waybill]. Чужой аккаунт не смог прочитать этот carriage ID: provider 404 CARRIAGE_NOT_FOUND. Это доказывает scope isolation для данного чтения, а не новое бизнес-событие.

Каждый фактический HTTP READ, включая negative validation 400 и source 404, записан в operations.jsonl. Полный response хранится зашифрованно в закрытой `.local` evidence папке; публичный ledger содержит маску аккаунта, UTC, метод, credential/config revisions где доступны, digest запроса, trace, HTTP status и ciphertext digest. Ранние прямые discovery probes ещё не имели config revision; это явно 0/не задано, не выдаётся за новую config authority. Последующие normal Factory/Service вызовы используют DB provider gate и сохраняют actual HTTP evidence без подмены trusted transport.

Read-only операция не требует «readback после записи»; у неё стоит NOT_APPLICABLE_READ. READ_COMPLETE обозначает транспортный ответ, не бизнес-успех: HTTP status и error classification рассматриваются отдельно. UNKNOWN-внешняя запись не обнаружена и не повторялась.

Итог журнала: **620 READ, 0 WRITE** (Ozon 582, МойСклад 38), четыре уникальных аккаунта. HTTP 200 — 558, 404 — 54, 400 — 8. Повторное чтение отсутствующего месячного отчёта и foreign-scope known-ID 404 входят в эти числа; это не 54 разных отчёта. Все 620 ciphertext/metadata прошли GCM authentication и SHA-256 verification, всего 5 381 966 response bytes. Gzip сохраняется как фактический HTTP body и отмечен в ledger. `failure_class` дополнен из неизменного HTTP status; исходный captured ledger сохранён отдельно в private evidence.

После миграции 76 normal Service снова прочитал семь финансовых источников и сохранил **7 encrypted raw records / 115 120 bytes**. Для каждого Store.FinancialEvidence независимо загрузил ciphertext из БД, FinancialEvidenceProtector.OpenFinancialEvidence расшифровал его, и SHA-256 совпал с исходным raw digest: **7/7 PASS**. Это отдельная проверка хранения, не внешняя финансовая запись.
