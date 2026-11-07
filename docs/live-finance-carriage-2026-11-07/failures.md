# Реальные ограничения реализации и оставшиеся gates

Этот файл описывает проверенные границы кода. LIVE failures/counts и окончательный CI дополняет основной агент по журналу; прежние 7/254 или прошлый PASS здесь не переобъявлены текущими.

## Carriage: текущий контракт не даёт завершить обычную запись

Есть реальные CREATE/APPROVE transports, durable previews/work, глобальный account+method fence, outbox/worker, readback и UI. Но текущий adapter **не допускает** production CREATE на непроверенном непустом `/v2/carriage/delivery/list` и APPROVE без доказанного canonical успешного readback state. Live пустые envelopes и limit/cursor validation не доказывают shape непустой партии, completeness всех профилей или success enum. Это неполнота проверенного контракта/поддерживаемого workflow; не утверждение, что Ozon запрещает writer.

Доступный zh Swagger не перечисляет approval states; main Swagger превышает лимит reader, публичные download attempts были недоступны. Неизвестное состояние не заменяется выдуманным `formed`. Известный carriage ID можно читать, но ACK/HTTP200 не считается завершённым подтверждением. При lost create ACK без ID нет безопасного автоматического усыновления кандидата по method/date.

`all_blr_traceable=true` остаётся blocked до подтверждения ETTN prerequisites. `containers_count` требует реального trust-acceptance режима. Composition replacement (`set-postings`) не реализован: STYK не подрезает и не отменяет чужие отправления после неожиданного batch expansion. rFBS и неподтверждённые profile/Express/carrier combinations не получают FBS Carriage authority.

Первый настоящий seller gate — проверенный текущий непустой discovery и approval state contract, затем реальная полностью собранная FBS партия с exact membership, ownership и двумя согласиями. При отсутствии фактической партии CREATE/APPROVE/PHYSICAL_HANDOFF остаются NOT_OBSERVED_LIVE; пустой ответ не превращается в искусственный PASS.

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
