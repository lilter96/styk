import { useEffect, useRef, useState } from "react";
import { useConfirmation, type Confirmation, type Scope } from "./Confirmation";
import { IntegrationDialog } from "./IntegrationDialog";
import { Icon, StatePanel } from "./UI";
import {
  WarehouseFailure,
  WarehouseInfo,
  WarehousePair,
} from "./WarehouseParts";
import { useWarehouseReview } from "./useWarehouseReview";
import "./warehouses.css";

const reasons: Record<string, [string, string]> = {
  ALREADY_MAPPED: [
    "Склады уже сопоставлены",
    "Повторное сопоставление не требуется. Эта связь уже используется в STYK.",
  ],
  MAPPING_CONFLICT: [
    "Обнаружен конфликт сопоставлений",
    "Один из выбранных складов уже связан с другим складом. Повторное подтверждение недоступно.",
  ],
  UNSUPPORTED_WAREHOUSE: [
    "Возможность сопоставления не подтверждена",
    "Для типа или текущего состояния этого склада не подтверждены необходимые возможности. Проверьте подключение в «Интеграциях».",
  ],
  MISSING_SOURCE_EVIDENCE: [
    "Не хватает данных о складах",
    "В последней допустимой загрузке нет выбранного склада или его типа. Проверьте загрузку данных в «Интеграциях».",
  ],
};
export function WarehouseReview({
  scope,
  moy,
  ozon,
  confirmation,
  close,
  back,
  embedded = false,
  prepare = false,
}: {
  scope: Scope;
  moy: string;
  ozon: string;
  confirmation?: Confirmation;
  close?: () => void;
  back?: () => void;
  embedded?: boolean;
  prepare?: boolean;
}) {
  const local = useConfirmation();
  const active = confirmation ?? local;
  const review = useWarehouseReview(scope, moy, ozon, active);
  const { q, durable, saved, samePending } = review;
  const [dismissed, setDismissed] = useState(false);
  const prepared = useRef(false);
  const region = useRef<HTMLElement>(null);
  useEffect(() => {
    if (embedded) region.current?.focus();
  }, [embedded]);
  useEffect(() => {
    if (!prepare || prepared.current || !review.eligible) return;
    prepared.current = true;
    void review.createReview();
  }, [prepare, review]);
  const onClose = close ?? (() => setDismissed(true));
  const stale =
    !!review.reviewID &&
    (!review.reviewMatchesSelection ||
      durable.data?.status !== "CURRENT" ||
      active.failure?.reviewId === review.reviewID ||
      (durable.data &&
        (!Number.isFinite(Date.parse(durable.data.review.expires_at)) ||
          Date.now() >= Date.parse(durable.data.review.expires_at))));
  const reason =
    q.data &&
    (reasons[q.data.status] ??
      (q.data.status === "ELIGIBLE_FOR_REVIEW"
        ? undefined
        : [
            "Результат проверки неизвестен",
            "Обновите данные и повторите проверку выбранной пары.",
          ]));
  const body = (
    <section
      ref={region}
      tabIndex={-1}
      className="wh-review"
      aria-label="Проверка соответствия складов"
    >
      <span className="wh-step">Шаг 2 из 2 · Проверка</span>
      <h2>
        {saved
          ? "Соответствие подтверждено"
          : reason && q.data?.status !== "ALREADY_MAPPED"
            ? "Сопоставление недоступно"
            : "Проверьте сопоставление"}
      </h2>
      <p className="wh-subtitle">
        {saved ? "Выбранная связь сохранена в STYK" : "Связь пока не сохранена"}
      </p>
      {q.isLoading ? (
        <StatePanel kind="loading">Проверяем склады…</StatePanel>
      ) : q.error ? (
        <WarehouseFailure error={q.error} retry={() => void q.refetch()} />
      ) : (
        q.data && (
          <>
            <WarehousePair
              ozonName={q.data.ozon?.name || "Название склада Ozon недоступно"}
              storeName={
                q.data.moysklad?.name || "Название склада МойСклад недоступно"
              }
            />
            {saved ? (
              <div className="wh-verdict" data-state="mapped" role="status">
                <Icon name="check" size="metric" />
                <div>
                  <h3>Соответствие складов сохранено.</h3>
                  <p>Список складов и настройки заказов обновлены.</p>
                </div>
              </div>
            ) : reason ? (
              <div
                className="wh-verdict"
                data-state={
                  q.data.status === "ALREADY_MAPPED" ? "mapped" : "warning"
                }
              >
                <Icon
                  name={
                    q.data.status === "ALREADY_MAPPED" ? "check" : "warning"
                  }
                  size="metric"
                />
                <div>
                  <h3>{reason[0]}</h3>
                  <p>{reason[1]}</p>
                </div>
              </div>
            ) : (
              <>
                {review.creating || durable.isLoading ? (
                  <StatePanel kind="loading">
                    Получаем результат проверки…
                  </StatePanel>
                ) : durable.error ? (
                  <WarehouseFailure
                    error={durable.error}
                    retry={() => void durable.refetch()}
                  />
                ) : (
                  review.reviewID &&
                  !samePending && (
                    <div
                      className="wh-verdict"
                      data-state={stale ? "warning" : "mapped"}
                    >
                      <Icon name={stale ? "warning" : "check"} size="metric" />
                      <div>
                        <h3>
                          {stale ? "Проверка устарела" : "Можно сопоставить"}
                        </h3>
                        <p>
                          {stale
                            ? "Данные, срок проверки или соответствие изменились. Повторите проверку перед подтверждением."
                            : "Проверка актуальна, конфликтов не обнаружено"}
                        </p>
                      </div>
                    </div>
                  )
                )}
                {review.reviewID && !stale && !samePending && (
                  <div className="wh-checks">
                    <h3>Что мы проверили</h3>
                    {[
                      "Склады найдены в выбранных аккаунтах",
                      "Тип и состояние склада Ozon поддерживаются",
                      "Нет конфликтующего сопоставления",
                    ].map((label) => (
                      <div key={label}>
                        <Icon name="check" />
                        {label}
                      </div>
                    ))}
                  </div>
                )}
              </>
            )}
            <WarehouseInfo>
              Сопоставление помогает направлять заказы на нужный склад. Само по
              себе оно не включает синхронизацию остатков.
            </WarehouseInfo>
            {reason && q.data.status !== "ALREADY_MAPPED" && (
              <div className="wh-next">
                <h3>Что можно сделать</h3>
                <ol>
                  <li>Вернитесь к выбору склада</li>
                  <li>Проверьте существующие соответствия и загрузку данных</li>
                  <li>Выберите доступный склад и повторите проверку</li>
                </ol>
                <a href="/integrations">Перейти в «Интеграции»</a>
                {q.data.status === "MAPPING_CONFLICT" && q.data.mappings[0] && (
                  <p>
                    <a
                      href={`/warehouses/${encodeURIComponent(q.data.mappings[0].ozon_warehouse_id)}`}
                    >
                      Посмотреть текущее сопоставление
                    </a>
                  </p>
                )}
                <p>
                  <button
                    className="text-action"
                    disabled={q.isFetching || !!active.pending}
                    onClick={() => void q.refetch()}
                  >
                    Повторить проверку
                  </button>
                </p>
              </div>
            )}
            {!saved && review.reviewID && !samePending && !reason && (
              <>
                <label className="wh-consent">
                  <input
                    type="checkbox"
                    checked={review.ack}
                    disabled={!!active.pending}
                    onChange={(e) => review.setAck(e.target.checked)}
                  />
                  Подтверждаю, что эти склады соответствуют друг другу
                </label>
                <label className="wh-comment">
                  Комментарий (необязательно)
                  <input
                    value={review.explanation}
                    disabled={!!active.pending}
                    maxLength={1000}
                    onChange={(e) => review.setExplanation(e.target.value)}
                    placeholder="Для себя или команды"
                  />
                </label>
              </>
            )}
            <details className="wh-technical">
              <summary>Подробности проверки</summary>
              <p>
                Состояние: <code>{q.data.status}</code>
              </p>
              <p>
                Тип склада:{" "}
                {q.data.ozon?.is_rfbs === null
                  ? "неизвестен"
                  : q.data.ozon?.is_rfbs
                    ? "rFBS"
                    : "FBS"}
              </p>
              {durable.data && (
                <>
                  <p>
                    Проверка: <code>{durable.data.review.id}</code> ·{" "}
                    {durable.data.status}
                  </p>
                  <p>
                    Создана:{" "}
                    <time dateTime={durable.data.review.created_at}>
                      {new Date(durable.data.review.created_at).toLocaleString(
                        "ru-RU",
                      )}
                    </time>
                  </p>
                  <p>
                    Действует до:{" "}
                    <time dateTime={durable.data.review.expires_at}>
                      {new Date(durable.data.review.expires_at).toLocaleString(
                        "ru-RU",
                      )}
                    </time>
                  </p>
                </>
              )}
              {q.data.mappings.map((m) => (
                <p key={m.mapping_id}>
                  Соответствие {m.mapping_id}, версия {m.version}:{" "}
                  {m.moysklad_warehouse_id} → {m.ozon_warehouse_id}
                </p>
              ))}
              {q.data.status === "MAPPING_CONFLICT" &&
                !q.data.mappings.length && (
                  <p>
                    Конфликтующее соответствие существует, но его история за
                    пределами доступных аккаунтов.
                  </p>
                )}
              <details>
                <summary>История соответствий складов</summary>
                {q.data.history.length ? (
                  q.data.history.map((m) => (
                    <p key={`${m.mapping_id}:${m.version}`}>
                      Соответствие {m.mapping_id}, версия {m.version}.
                      Происхождение: {m.provenance}
                    </p>
                  ))
                ) : (
                  <p>Для этих складов доступной истории нет.</p>
                )}
                <div className="wh-history-pages">
                  <button
                    className="secondary"
                    disabled={!review.offset || q.isFetching}
                    onClick={() =>
                      review.setOffset(Math.max(0, review.offset - 50))
                    }
                  >
                    Предыдущие подтверждения
                  </button>
                  <button
                    className="secondary"
                    disabled={!q.data.has_next || q.isFetching}
                    onClick={() => review.setOffset(review.offset + 50)}
                  >
                    Следующие подтверждения
                  </button>
                </div>
              </details>
            </details>
          </>
        )
      )}
      {review.error && (
        <p role="alert" className="wh-error">
          {review.error}
        </p>
      )}
      {samePending && (
        <div className="wh-pending" role="status">
          <h3>
            {active.busy
              ? "Сохраняем сопоставление…"
              : "Результат сохранения уточняется"}
          </h3>
          <p>
            {active.sessionLost
              ? "Сессия завершилась. Войдите снова и проверьте историю исходных аккаунтов."
              : "Закрытие панели и смена аккаунтов не отменяют отправленный запрос."}
          </p>
          {!active.busy && !active.sessionLost && (
            <button className="secondary" onClick={active.retry}>
              Повторить тот же запрос
            </button>
          )}
        </div>
      )}
      <footer className="wh-drawer-footer">
        <div>
          <button
            className="secondary"
            disabled={!!active.pending}
            onClick={
              saved || q.data?.status === "ALREADY_MAPPED"
                ? onClose
                : (back ?? onClose)
            }
          >
            {saved || q.data?.status === "ALREADY_MAPPED"
              ? "Готово"
              : back
                ? "Назад"
                : "Закрыть"}
          </button>
          {!saved &&
            !samePending &&
            q.data &&
            (reason ? (
              back &&
              q.data?.status !== "ALREADY_MAPPED" && (
                <button className="mapping-primary" onClick={back}>
                  Выбрать другой склад
                </button>
              )
            ) : !review.reviewID || stale ? (
              <button
                className="mapping-primary"
                disabled={
                  !review.eligible || review.creating || !!active.pending
                }
                onClick={() => void review.createReview()}
              >
                {review.creating ? "Проверяем…" : "Повторить проверку"}
              </button>
            ) : (
              <button
                className="mapping-primary"
                disabled={!review.canConfirm}
                onClick={review.confirm}
              >
                Подтвердить сопоставление
              </button>
            ))}
          {stale && !saved && !samePending && !reason && (
            <button className="secondary" disabled>
              Подтвердить сопоставление
            </button>
          )}
        </div>
        <small>
          {saved
            ? "Синхронизация остатков настраивается отдельно."
            : review.reviewID
              ? "После подтверждения STYK сохранит выбранную связь."
              : "Изменения пока не сохраняются."}
        </small>
      </footer>
    </section>
  );
  if (dismissed) return null;
  if (embedded) return body;
  return (
    <IntegrationDialog
      title="Сопоставить склады"
      className="wh-drawer"
      onClose={onClose}
      closeLabel="Закрыть сопоставление складов"
    >
      {body}
    </IntegrationDialog>
  );
}
