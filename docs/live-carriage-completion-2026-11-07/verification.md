# Проверки доработки Carriage

LIVE счётчики: `audit-summary.json`, построчный журнал: `operations.jsonl`. В инструментированном журнале 56 READ (46 Ozon, 10 МойСклад), 54 HTTP200 и 2 ожидаемых 404 для чужого carriage. Provider WRITE=0. Дополнительно через обычный открытый STYK выполнены 4 read-only preview: историческая дата чужого аккаунта — пусто; правильный аккаунт/дата — critical outdated; текущая дата — нет готовой партии; после тестов текущий preview обновлён ещё раз. Эти UI-запросы не смешиваются с инструментированным журналом и не выдаются за новые внешние записи.

Исследованы 2 Ozon аккаунта, identity 2 МойСклад аккаунтов, 1 историческая партия и 2 различных отправления. Исторический участник сейчас cancelled, текущее отправление второго аккаунта — delivering. Документы закрытой исторической партии ready; это дополнительно показывает, почему document readiness не является доказательством физической передачи или готовности APPROVE.

## Проверки кода

- `make check-plan` выполнен до выбора проверок и обновлён по итоговому diff: high-risk dispatch/readback требует full.
- `make check` прошёл как предварительный scoped запуск: Go lint/race/build и frontend typecheck/lint/420 unit/build. Финальные изменения Go затем проверяются полной регрессией.
- `make check-integration` отказал до запуска по политике high-risk: потребовал full. Не объявляется integration PASS.
- Focused `go test -race ... -run 'Carriage|MonthlyRealization'` прошёл для orders/ozon/providercall. Покрыты непустой discovery, counters, critical/warning, profile, отсутствие rFBS method для FBS, pagination/count mismatch, document ready/partial/duplicate composition и approval outcome.
- Focused race HTTP/PostgreSQL прогон в owned disposable PostgreSQL прошёл за 18.807 s. CREATE/readback/APPROVE/readback, lost APPROVE ACK с чтением без повтора, UNKNOWN CREATE без ID без повтора, stale composition без отправки. Последующий regression на cancelled member проверяется в full.
- `make check-ui`: 26/26 browser PASS за 67.517 s.
- Первый `make check-full`: **FAIL/INCOMPLETE**, 281.370 s, global deadline не продлевался. 60/63 browser PASS; 3 устаревших matching сценария ожидали preview до сохранения изменённых правил. Один business worker не завершился до work deadline; его недостающие результаты не объявлены PASS.
- Matching browser сценарий исправлен: проверяет disabled preview при unsaved policy, сохраняет правила и только затем выполняет preview. Production matching UI не изменён.
- Scheduling costs обновлены по 562 фактически завершённым top-level PASS: 499 прежних значений и 63 новых имени. Оценки меняют только распределение; весь compiled список по-прежнему выполняется ровно один раз в 8 процессах, лимиты и coverage checks сохранены. Незавершённые имена сохранили прежнюю оценку.
- Второй запуск: все три этапа записали PASS, 588/588 business terminal outcomes без дублей/пропусков, 63 browser PASS, но внешняя обёртка завершилась сигналом 143 без общего exit code. Это не объявлено успешным агрегатным запуском.
- Финальный `make check-full` под отдельной bounded обёрткой: **PASS, exit code0, 261.303 s (4m21.303s)**. Source manifest неизменён, внешний deadline не достигнут. Все три этапа прошли; 588 business top-level outcomes: 572 PASS/16 SKIP, missing=0, duplicates=0; frontend420 PASS; browser63 PASS. Полные Go counts и имена штатных SKIP — `test-summary.json`, root exit/time/source digest — `check-full-result.json`. Все попытки сохраняют один прежний work deadline280s и внешний299s; неуспешные попытки не переобъявлены PASS.

Миграции не добавлялись; shared dev остаётся на schema76. Все application integration checks используют отдельный disposable PostgreSQL, не dev или production DB. В full отсутствуют реальные provider credentials.

## Проверка интерфейса

Применены `styk-ui-workflow`, `frontend-design`, `design-system`, `design-critique`, `accessibility-review`, `ux-copy`: локальная доработка ошибок без изменения визуальной системы. Исследование пользователей, synthesis и handoff не требовались.

Fixture route `/orders/settings?section=lifecycle`: 1440, 1366 и 390 CSS px; полная партия с длинными ID, отдельные consent стадии, disabled buttons, изменённый состав, critical outdated/empty, UNKNOWN read-only recovery. Новые причины проверены на 1440 и 390, без page overflow. Просмотрены PNG при нормальном масштабе и computed styles: input44px, Inter, line-height19.5px, focus outline3px solid. Контраст create button: 5.69 normal/6.97 hover. Клавиатура: Space для согласия, select через End/Enter; состав раскрывается и показывает последнюю строку. На mobile технические коды переносятся, пользовательский текст остаётся читаемым; clipping/overlap новых сообщений не обнаружено. Screen reader и полное соответствие WCAG не заявляются.

В настоящем браузере после обновления local API (health/live и ready HTTP200) подтверждены обе причины на реальных аккаунтах. Фактический viewport2036px, card856px, line-height22.5px, без page overflow. Текущая дата оставлена без ручной подстановки. Ни consent на физическую готовность, ни CREATE/APPROVE кнопки не нажимались. Примерные screenshots и метрики находятся в `.local/carriage-completion/ui/`; реальные screenshots — `browser-outdated.png` и `browser-current.png` в соседнем private каталоге. Локальный API перезапущен; production deployment и worker startup не выполнялись.
