# Интерфейс финансов и FBS-партий

Область: `/orders/settings?section=lifecycle`, текущая выбранная пара Ozon / МойСклад. Основной рабочий интерфейс продавца остаётся МойСклад. STYK показывает границы разрешённой автоматизации, финансовую сверку и отдельные операции над всей партией. Это расширение существующей страницы настроек; оформление, шрифт Inter, цвета и общие компоненты сохранены.

Применены прочитанные `styk-ui-workflow`, официальный `frontend-design`, `design-system`, `design-critique`, `accessibility-review`, `ux-copy`. Отдельные исследования пользователей, synthesis и design handoff не нужны для этой реализации существующего product flow. Пользовательские исследования или screen-reader acceptance не выдумывались.

## Что доступно продавцу

Финансовый источник читается без разрешения создавать документы. `analysis` показывает классифицированные, включённые, частично включённые и не включённые строки; точные значения и знаки компонентов; missing, NULL и явный ноль; контрольные расхождения. Суммы показывает backend как decimal text. JavaScript не суммирует деньги, не объединяет цены, количество и контрольные итоги и не превращает количество в денежную сумму. Списки строк, остатка и проверок доступны постранично.

Финансовая политика задаётся для выбранной пары: реальный договор, организация, контрагент, валюта, курс, основание цены, НДС, отдельный выбор финансовых возвратов и обработки неучтённых компонентов. По умолчанию действует строгая политика. Расширение на `COMMISSION_RETURN` или `PARTIAL_REVIEW` сбрасывает прежнее согласие и требует нового. Возврат в непроведённом отчёте не создаёт физический Возврат покупателя. Частичный черновик явно остаётся на бухгалтерской сверке; подтверждённый draft не выдаётся за финансовую полноту или выплату. Source correction сохраняет прежний документ и показывает versioned row/component diff: before/after суммы и presence, включая NULL → 0. Доказанное внешнее проведение показывается отдельным read-only состоянием и ссылкой на проведённый документ; из generic UNKNOWN оно не выводится.

Начисления (`ACCRUAL`) читаются отдельно по дню или как справочник типов. Явно указано, что они не прибавляются к реализации и не подтверждают выплату. Empty source, unknown fields, точные большие integer/decimal values, тип и полнота чтения видимы; документы не создаются. Optional postings diagnostic остаётся API-возможностью, UI её не имитирует.

Для FBS-партии отдельно выбирается `CREATE` или `APPROVE`. Preview фиксирует полный состав, товары/количества, method/warehouse, текущие статус/actions при их наличии, ревизию, fingerprint и срок. Все стадии требуют нового согласия на всю проверенную партию и отдельного заявления о фактической готовности. Выбор traceability не предзаполнен. Для cargo places нужна отдельная явная trust-acceptance декларация. Предупреждение объясняет возможный захват всех готовых отправлений способа доставки. Отдельное подтверждение партии не доказывает handoff, доставку, оплату или Отгрузку МойСклад. rFBS не получает FBS Carriage по умолчанию.

Pending confirmation хранит idempotency key в sessionStorage до подтверждённого ответа/найденной операции. Потерянный ответ блокирует повтор записи после перезагрузки. История и `carriage/reread` только перечитывают сохранённое состояние; ACK не называется успешным формированием. Stale config, expired preview, changed/foreign membership и серверные blockers не дают кнопки записи. «Закрыть проверку без записи» убирает локальный preview, не отменяя и не изменяя партию Ozon; pending ACK эта кнопка не снимает.

Ввод method/carriage IDs остаётся точным строковым вводом: проверенного публичного API выбора всех доступных delivery methods в существующей console нет. Новая UI-функция не имитирует такой список и не угадывает идентификаторы по названию склада.

## Браузерные доказательства и границы

Изолированный Vite/Playwright harness не имеет API proxy и блокирует обращения к внешним hosts. Данные синтетические и обезличенные. Auth/HTTP fixtures показывают презентацию и поведение UI; они не создают сессию в shared dev, не доказывают provider write и не заменяют штатный LIVE acceptance.

Проверены **1440, 1366 и 390 CSS px**, включая компактный desktop высотой 768 px: раскрытые состав/строки, первый и последний элементы, пагинация, long IDs, точный decimal выше 2^53, NULL/missing/zero, partial/review, source correction, смена accounting policy, пустая история, staged consents, membership change, ACK/UNKNOWN и read-only recovery. Выполнена клавиатурная работа с checkbox и выбором стадии, проверены focus, доступные names/roles и отсутствие горизонтального переполнения. Измерены 44 px controls, Inter и focus outline 3 px; контраст primary carriage action проверен в обычном и hover состоянии. Нативные select сохраняют браузерную семантику; это не сертификация полного WCAG/screen-reader поведения.

При визуальной критике найдена и исправлена коллизия подписи со счётчиком финансовой сводки на 390 px. Добавлена проверка `scrollWidth` подписей. После исправления реальные screenshots просмотрены отдельно для desktop и mobile, вместе с computed styles; build сам по себе визуальным доказательством не считался.

Артефакты: `.local/finance-carriage-ui-complete/` — финальные screenshots/computed evidence; предыдущие `.local/finance-carriage-ui/`, `...-rerun/`, `...-final/` сохраняют историю исправлений. Единственный ранний browser failure — неточный selector нового select; он исправлен точным доступным именем. Ранние frontend failures касались устаревшей copy-ожидаемости и новых тестовых selectors/types; они не переименованы в PASS.

Итоговые выбранные проверки: TypeScript + Vite build — PASS (Vite 1.22 с), ESLint — PASS; шесть affected frontend test files — **36 PASS, 2.67 с**; четыре fixture browser cases — **4 PASS, 18.0 с**. Primary carriage action contrast: **5.69:1** normal, **6.97:1** hover. Browser-проверки отдельно подтвердили отказ без записи, semantic component diff и proven posted-externally presentation. Build сохранил существующее предупреждение о main chunk больше 500 kB (658.04 kB); оно не скрыто.

Логи: `.local/finance-carriage-focused-complete.log`, `finance-carriage-lint-complete.log`, `finance-carriage-build-complete.log`, `finance-carriage-browser-complete.log`. Полный frontend regression на финальном UI: `npm test` — **418 PASS / 50 files, 6.60 с**, лог `.local/finance-carriage-frontend-regression-complete.log`. Общий `make check-full` и реальный API ledger ведёт основной агент. LIVE browser с реальной merchant-сессией в этом UI-срезе не проверялся; credentials, raw buyer data и реальные финансовые тела в screenshots не использовались.

## Пояснение счётчика69 на реальном октябрьском отчёте

После пользовательской проверки разобран source fingerprint781dff339923…:45 UNREPRESENTED slots относятся к NULL-блокам RETURN (5строк×9денежныхполей),5 — CONTROL_TOTAL,19 — присутствующие денежные компоненты:5SALE_GROSS,5COMMISSION_FEE,5DISCOUNT_POINTS,4BANK_COINVESTMENT. Поэтому69 нельзя называть69 бухгалтерскими ошибками. Неизменяемый ledger не пересчитывался и NULL не превращался в0.

UI теперь объясняет45/5/19 и необходимость отдельного решения по bank component. Диагностический preview доступен с выключенной finance policy; создание по-прежнему требует active policy, zero validation и отдельное согласие. Через настоящий merchant UI выполнена проверка октября: source5строк, preview получен, коды finance_policy_or_references_unverified + finance_policy_not_enabled показаны понятным текстом. Внешнего финансового POST нет.

make check-web: PASS,419тестов/50файлов,TypeScript/ESLint/buildPASS; прежнее предупреждение bundle658kB осталось. Первый прямой vitest был вызван без jsdom и упал; первый штатный прогон выявил неподключённый matcher нового теста и устаревшее ожидание disabled диагностической кнопки. Оба тестовых дефекта исправлены, финальный штатный прогонPASS. ПолныйGo/DBregression не повторялся: backend/schema/dispatch не менялись.

Реальный desktop browser: визуально проверены новый текст и диагностический результат; Inter, цветrgb16,16,43, ширина826=scrollWidth826, без горизонтального переполнения нового блока. Mobile и screen reader для этого follow-up отдельно не проверялись. Сохранены существующие semantic section/list/details и стандартные control styles.
