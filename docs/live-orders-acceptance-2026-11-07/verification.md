# Проверка итогового кода — 2026-11-07

**`make check-full`: PASS, 259.329 секунды (4 минуты 19.329 секунды), exit 0.**
Начало: 00:48:03.114 UTC. Время включает планирование, создание/migration
изолированного PostgreSQL, все выбранные группы, build и cleanup. Общий hard
budget — пять минут, один для всего запуска. Внешний watchdog не сработал.
Исходники до/после идентичны; source manifest SHA256:
`ffe891fbadda461d5db06a7165ae90b85cf0926930aff69963868abf549e9bcb`.

## Полный набор

- Go, `-race`, integration: **1628 top-level PASS / 27 top-level SKIP / 0 FAIL**;
  вместе с subtests **4430 PASS events / 29 SKIP events / 0 FAIL**.
- Business HTTP/PostgreSQL/worker: **582/582 compiled Test/Example/Fuzz names
  учтены ровно по одному разу**, 566 PASS / 16 SKIP / 0 FAIL, восемь
  непересекающихся workers. Новые native coverage happy/missing/null и late
  foreign draft прошли именно в полном наборе.
- Frontend: **403 теста / 47 файлов PASS**; TypeScript, ESLint и production
  build PASS. Существующий Vite large-chunk advisory остался advisory.
- Presentation browser: **60/60 PASS**. Это изолированные браузерные сценарии;
  они не названы реальной доставкой/отгрузкой покупателю.
- Go/workflow lint — 0 issues, build PASS. Изолированные миграции 1–73,
  upgrade/guarded rollback fixtures включены в набор.
- Все принадлежащие этому полному запуску PostgreSQL/process groups удалены.
  Исходный `styk-postgres-1` и другие пользовательские контейнеры сохранены.

Пропуски не скрыты: пять непосредственных subprocess helpers (выполняются
родительскими сценариями), девять контрактов с отдельно именованными dedicated
credential/connection БД, две live-provider opt-in проверки и 13 optional browser
cases (11 top-level и 2 nested import subcases). Полный список есть в приватном
`final-verification-counts.json`. Эти SKIP не объявлены PASS. LIVE-доказательства
провайдера собраны отдельными разрешёнными executors, а не через opt-in тесты.

Для новых двух HTTP/PG native coverage тестов веса business-планировщика
обновлены по наблюдённым 14.67/4.70 с. Изменены только распределение и баланс;
никакие имена, assertions или общий deadline не исключены.

## Дополнительные проверки по изменениям

Перед полным запуском прошли focused race domain/adapter/planner/consumer tests,
`go vet`, lint и API/worker build. Финансовый HTTP/PG набор из четырёх journeys
и schema gate — PASS **9.600 с**. Native coverage HTTP/PG/worker regression —
PASS **20.401 с**, включая задержанное HTTP-чтение, fresh planning clock,
missing/NULL, один applied demand и повтор/restart без дубля; поздний foreign
unposted draft дал **0 POST / 0 dispatch**. Все эти эффекты — loopback fixtures.

Дополнительный lint с `--build-tags integration` ранее завершился с шестью
существовавшими staticcheck style diagnostics в соседних integration fixtures.
Он не объявлен PASS; стандартный lint полного обязательного lane прошёл.
Один запуск pinned linter через неподходящий Go 1.26 toolchain был ошибкой setup,
а повтор того же pinned linter на Go 1.27.3 прошёл. Неудачный guessed package
path `internal/orderadapters` заменён проверкой реального `internal/orderprovider`.

Артефакты: `.local/live-orders-2026-11-07/check-full-final.log`,
`check-full-final-result.json`, `check-full-source-manifest.json`,
`final-verification-counts.json`, `.local/checks/full-3623118079/coverage.json`
и `worker-1..8.jsonl`. Данные реальных покупателей и production credentials
в автоматический набор не передавались; поставщики — изолированные fixtures,
PostgreSQL — собственный disposable сервер.

## Итоговый локальный runtime

После PASS локальный dev-API пересобран из проверенного кода и перезапущен.
PID **1350063**, SHA256
`2f78895d24a05ce9f633ecec4ab7a5f025b194a4b62fe3e258630d103317eb09`.
Бинарник содержит financial extension и `ReadNativeDemandCoverage`.
Сравнение actual process environment подтвердило тот же direct dev DATABASE_URL
и исходный vault key, без вывода их значений. `/health/live` и `/health/ready`
вернули **200**; схема — 73. Постоянный dispatch worker не оставлен.
Это локальный dev runtime, не production deployment.

Новые live UI-сессии с авторизацией в браузере в этом запуске не проверялись.
Реальный LIVE-путь подтверждён API/Factory/Service/ingestor/readback. Ранний
локальный ручной API-start без `STYK_CONSOLE_ORIGIN` завершился fail-closed;
обычный loopback origin был восстановлен перед успешным health-check.
