# Реальные ограничения реализации и оставшиеся gates

Этот файл описывает проверенные границы кода. LIVE failures/counts и окончательный CI дополняет основной агент по журналу; прежние 7/254 или прошлый PASS здесь не переобъявлены текущими.

## Carriage: исправленные contract gates и оставшаяся LIVE-проверка

Актуальная доработка описана в [отчёте Carriage](../live-carriage-completion-2026-11-07/README.md). Безусловные `CARRIAGE_NONEMPTY_DISCOVERY_CONTRACT_UNVERIFIED` и `CARRIAGE_APPROVAL_CONTRACT_UNVERIFIED` удалены. Непустой `/v2/carriage/delivery/list` подтверждён реальным filtered read; текущие counters, errors и carriages читаются, полный ready-состав собирается и сверяется со счётчиками.

CREATE и APPROVE доступны для обычного FBS/dropoff при выполнении реальных prerequisites. Подтверждение проверяется по отдельному `/v2/posting/fbs/act/check-status`, ready документам, exact membership и изменившемуся header; enum успешного carriage status не выдуман. Сквозные HTTP/PostgreSQL/worker тесты проходят, но **новые production CREATE/APPROVE остаются NOT_OBSERVED_LIVE**: в обоих аккаунтах сейчас нет готовой партии. Историческая закрытая партия прочитана; это не новая выполненная запись.

`all_blr_traceable=true` остаётся blocked до реализации проверенных ETTN prerequisites. `containers_count` требует реального trust-acceptance режима. Composition replacement (`set-postings`) не реализован. rFBS, Express, pickup, сторонние integration/profile combinations не получают обычную FBS Carriage authority. Потерянный CREATE ACK без ID остаётся UNKNOWN и требует установления точной identity; повторный POST/усыновление кандидата по похожей дате запрещены.

## Финансы: классификация не равна полному бухгалтерскому импорту

Канонический ledger удерживает все известные компоненты и новые unknown numeric/digest evidence, но native documents создаются только для доказуемого supported subset. PARTIAL_REVIEW не превращает остаток в успех. Нужны реальные Commission/None contract и owner/accountant price/rate/VAT/return/component choices; их нельзя выбрать наугад для LIVE POST.

Услуги, логистика, продвижение, штрафы, неподтверждённые compensation/PUP/bank/loyalty components и standalone monetary adjustments могут остаться unrepresented. Отдельные native UPD/service/payment documents без подходящего первичного источника и политики не создаются. Accruals являются отдельным источником, а не универсальным заменителем realization или выплат. Отрицательное количество, дробное требующее округления значение, unknown/null обязательных полей и неподтверждённые mappings блокируют соответствующую позицию. Отрицательное reward сохраняется, а финансовый return не вызывает физический salesreturn.

Автоматические correction writes и редактирование проведённых native reports отсутствуют. Source fingerprints/component diff хранятся; бухгалтер вручную решает, как учитывать новую версию. Постороннее проведение exact native document переводит результат в read-only review. Внешний сотрудник может изменить reference или создать ручной документ после GET: provider CAS/cross-system transaction отсутствуют; локальные fences не обещают абсолютного exactly-once относительно внешних авторов.

## Сохранение evidence

Raw archive gap устранён migration76 и штатным `FinanceEvidenceSealer` console/worker Factory: успешно полученный/разобранный realization body сохраняется AES-GCM ciphertext blocks64KiB, привязанными к scope/period/credential/source/raw digest. `Store.FinancialEvidence` и protector.Open восстанавливают точные bytes с проверкой authentication/длины/SHA-256. Canonical sanitized source и immutable ledger сохраняются отдельно; ciphertext не попадает в публичный JSON. Отказ чтения/парсинга или oversized body не выдаётся за полный archived financial source.

**Автоматическое удаление/TTL raw archive не настроено:** append-only encrypted history не имеет автоматического срока удаления. Перед общим эксплуатационным использованием нужна явная owner/privacy retention policy и процедура ограниченного доступа/хранения ключа. Шифрование не является сроком хранения. Это оставшееся ограничение реализации, а не заявление о выполненном удалении. Независимый LIVE DB-decrypt проверяет основной агент; unit/PG fixtures не заменяют его.

Отсутствующие или поздние source reports, permission/rate-limit/provider errors и incompleteness остаются явными отказами. Нельзя выдавать 404 за подтверждённое отсутствие начислений/нулевой отчёт или сбой за успешный импорт. Current UI не предлагает несуществующего списка delivery methods: вводятся точные IDs; read-only accrual UI показывает day/types, а optional postings diagnostic остаётся API-операцией.

## Задержка совмещённого worker

Совмещённый/default orders worker последовательно выполняет scheduling/ingestion и dispatch; полный обход due scopes может задержать уже согласованную finance/carriage операцию до истечения preview/source freshness. Rotation не делает внешний I/O scheduler параллельным. Это ограничение liveness, а не разрешение отправлять устаревший intent: expiry/final guard сохраняют отказ. Для рабочего seller flow поддерживаются отдельные процессы `STYK_WORKER_MODE=schedule` и `STYK_WORKER_MODE=dispatch` с теми же scoped grants; queue latency нужно наблюдать. Эти процессы здесь не развёрнуты и неконтролируемый общий daemon не запускался.

## Проверки и внешний смысл

Fixture browser/HTTP/PostgreSQL tests проверяют детерминированные guards и отображение, но не доказывают производство, фактическую передачу товара, выплату, legal/accounting approval или текущее API-действие чужого сотрудника. Актуальные PASS/SKIP/FAIL, duration и реальные read/write/readback counts публикуются в соседних verification/scenario/readback документах. Остановленный или failed запуск не назван PASS.

## Свежесть статуса документа после завершённого readback

`FINANCE_DOCUMENT_POSTED_EXTERNALLY` подтверждается при readback pending/UNKNOWN документа с точным ID/marker/scope. Терминальный RECONCILED автоматически не опрашивается: строка истории описывает последнее наблюдение, а не гарантию, что бухгалтер впоследствии не провёл документ. Непрерывный мониторинг позднего внешнего проведения и автоматические корректировки не реализованы.
