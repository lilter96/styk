import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, post, params, ApiError } from "./api";
import { useConfirmation, type Confirmation } from "./Confirmation";
import { IntegrationDialog } from "./IntegrationDialog";

type Scope = { tenant: string; moy: string; ozon: string };
type Evidence = {
  id: string;
  name: string;
  account: string;
  status: string;
  is_rfbs: boolean | null;
  observed_at: string;
  stock_rows: number;
};
type Mapping = {
  mapping_id: number;
  version: number;
  moysklad_warehouse_id: string;
  ozon_warehouse_id: string;
  provenance: string;
  confirmed_at: string;
};
type Review = {
  status: string;
  moysklad: Evidence | null;
  ozon: Evidence | null;
  mappings: Mapping[];
  history: Mapping[];
  has_next: boolean;
  offset: number;
};
type DurableReview = {
  review: {
    id: string;
    tenant: string;
    moysklad_account: string;
    ozon_account: string;
    moysklad_warehouse_id: string;
    ozon_warehouse_id: string;
    moysklad_import_id: number;
    ozon_import_id: number;
    created_at: string;
    expires_at: string;
    existing_mapping_id: number | null;
    existing_mapping_version: number | null;
  };
  status: string;
  principal_type: string;
};
export function WarehouseReview({
  scope,
  moy,
  ozon,
  confirmation,
  close,
}: {
  scope: Scope;
  moy: string;
  ozon: string;
  confirmation?: Confirmation;
  close?: () => void;
}) {
  const client = useQueryClient();
  const [dismissed, setDismissed] = useState(false);
  const local = useConfirmation();
  const active = confirmation ?? local;
  const [offset, setOffset] = useState(0);
  const [reviewID, setReviewID] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");
  const [ack, setAck] = useState(false);
  const [explanation, setExplanation] = useState("");
  const q = useQuery({
    queryKey: ["warehouse-review", scope, moy, ozon, offset],
    queryFn: ({ signal }) =>
      api<Review>(
        "/api/warehouse-mapping-review?" +
          params({
            tenant: scope.tenant,
            moysklad_account: scope.moy,
            ozon_account: scope.ozon,
            moysklad_warehouse: moy,
            ozon_warehouse: ozon,
            offset,
          }),
        signal,
      ),
    retry: false,
  });
  const durable = useQuery({
    queryKey: ["warehouse-durable-review", reviewID],
    queryFn: ({ signal }) =>
      api<DurableReview>(`/api/warehouse-mapping-reviews/${reviewID}`, signal),
    enabled: !!reviewID,
    staleTime: 0,
    refetchInterval: 5000,
    retry: false,
  });
  useEffect(() => {
    if (durable.error instanceof ApiError && durable.error.status === 401)
      void client.invalidateQueries({ queryKey: ["session"] });
  }, [durable.error, client]);
  const eligible =
    q.data?.status === "ELIGIBLE_FOR_REVIEW" &&
    q.data.moysklad?.id === moy &&
    q.data.moysklad.account === scope.moy &&
    q.data.ozon?.id === ozon &&
    q.data.ozon.account === scope.ozon &&
    q.data.ozon.is_rfbs === false;
  const reviewed = durable.data?.review;
  const reviewMatchesSelection =
    reviewed?.id === reviewID &&
    reviewed.tenant === scope.tenant &&
    reviewed.moysklad_account === scope.moy &&
    reviewed.ozon_account === scope.ozon &&
    reviewed.moysklad_warehouse_id === moy &&
    reviewed.ozon_warehouse_id === ozon &&
    reviewed.moysklad_import_id > 0 &&
    reviewed.ozon_import_id > 0 &&
    ["LOCAL_OPERATOR_SESSION", "MERCHANT_SESSION"].includes(
      durable.data?.principal_type || "",
    );
  async function createReview() {
    if (creating || active.pending || !eligible) return;
    setCreating(true);
    setError("");
    try {
      const result = await post<DurableReview>(
        "/api/warehouse-mapping-reviews",
        {
          tenant: scope.tenant,
          moysklad_account: scope.moy,
          ozon_account: scope.ozon,
          moysklad_warehouse_id: moy,
          ozon_warehouse_id: ozon,
        },
      );
      setReviewID(result.review.id);
      setAck(false);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        void client.invalidateQueries({ queryKey: ["session"] });
        setError("Сессия завершилась. Войдите снова.");
      } else if (
        e instanceof ApiError &&
        e.code === "WAREHOUSE_ALREADY_ASSIGNED"
      ) {
        setError(
          "Склад уже сопоставлен. Обновите выбор. История за пределами доступных аккаунтов не показывается.",
        );
        void client.invalidateQueries({ queryKey: ["warehouse-review"] });
      } else
        setError(
          "Не удалось проверить пару складов. Обновите данные и проверьте выбранные аккаунты.",
        );
    } finally {
      setCreating(false);
    }
  }
  const stateLabels: Record<string, string> = {
    ELIGIBLE_FOR_REVIEW: "Можно проверить соответствие",
    ALREADY_MAPPED: "Склады уже сопоставлены",
    MAPPING_CONFLICT: "Есть конфликт соответствий",
    UNSUPPORTED_WAREHOUSE: "Поддерживаются только склады Ozon FBS",
    MISSING_SOURCE_EVIDENCE: "Не хватает данных о складах",
  };
  const describe = (e: Evidence | null, source: string) =>
    e ? (
      <article className="warehouse-pair-evidence">
        <h3>{source}</h3>
        <strong>{e.name || "Склад без названия"}</strong>
        {source === "Ozon" && (
          <p>
            Тип склада:{" "}
            {e.is_rfbs === null ? "неизвестен" : e.is_rfbs ? "rFBS" : "FBS"}
          </p>
        )}
        <p>
          Данные: {new Date(e.observed_at).toLocaleString("ru-RU")}
          {Date.parse(e.observed_at) > Date.now() &&
            " · Время указано в будущем; давность неизвестна"}
          {Date.now() - Date.parse(e.observed_at) > 3600000 &&
            " · Данные могут быть устаревшими"}
        </p>
        <p>
          {e.stock_rows === 0
            ? "Данных об остатках нет. Количество неизвестно."
            : "Данные об остатках получены."}
        </p>
        <details className="mapping-technical">
          <summary>Данные склада</summary>
          <dl>
            <dt>ID склада</dt>
            <dd>
              <code>{e.id}</code>
            </dd>
            <dt>Аккаунт</dt>
            <dd>
              <code>{e.account}</code>
            </dd>
            <dt>Статус в сервисе</dt>
            <dd>{e.status || "Неизвестен"}</dd>
            <dt>Записей об остатках</dt>
            <dd>{e.stock_rows}</dd>
          </dl>
        </details>
      </article>
    ) : (
      <p>{source}: в последней загрузке нет доступных данных об этом складе.</p>
    );
  const samePending =
    !!active.pending &&
    active.pending.intent.kind === "warehouse" &&
    active.pending.scope.tenant === scope.tenant &&
    active.pending.scope.moy === scope.moy &&
    active.pending.scope.ozon === scope.ozon &&
    active.pending.product === `${moy} → ${ozon}`;
  const saved = !!reviewID && active.result?.review_id === reviewID;
  const alreadyMapped = q.data?.status === "ALREADY_MAPPED";
  if (dismissed) return null;
  return (
    <IntegrationDialog
      title="Сопоставить склады"
      onClose={close ?? (() => setDismissed(true))}
      closeLabel="Закрыть сопоставление складов"
    >
      <section
        className="warehouse-mapping-review"
        aria-label="Проверка соответствия складов"
      >
        {q.isLoading ? (
          <p role="status">Проверяем склады…</p>
        ) : q.error ? (
          <p role="alert">
            Не удалось проверить склады. Проверьте аккаунты и повторите попытку.
          </p>
        ) : (
          q.data && (
            <>
              <p
                className={
                  eligible || q.data.status === "ALREADY_MAPPED"
                    ? "warehouse-pair-status"
                    : "mapping-review-warning"
                }
              >
                {stateLabels[q.data.status] || "Проверьте состояние складов"}
              </p>
              <div className="warehouse-pair-grid">
                {describe(q.data.moysklad, "МойСклад")}
                {describe(q.data.ozon, "Ozon")}
              </div>
              {alreadyMapped ? (
                <div className="warehouse-mapped-summary" role="status">
                  <p>
                    Соответствие этой пары складов уже подтверждено. Повторное
                    сопоставление не требуется.
                  </p>
                  <p>
                    Данные подтверждения доступны в состоянии выбранной пары и
                    истории ниже. Сопоставление само по себе не включает
                    синхронизацию остатков.
                  </p>
                  <button
                    className="secondary"
                    onClick={close ?? (() => setDismissed(true))}
                  >
                    Готово
                  </button>
                </div>
              ) : (
                <>
                  <p>
                    Подтвердите, что склады соответствуют друг другу в выбранной
                    интеграции. Совпадение названий не доказывает физическую
                    идентичность склада или принадлежность остатков.
                  </p>
                  <p>
                    Поддерживается одна пара: склад МойСклад и склад Ozon FBS.
                    Склады rFBS не поддерживаются. Подтверждение не включает
                    синхронизацию остатков.
                  </p>
                  <button
                    className="mapping-primary"
                    disabled={creating || !!active.pending || !eligible}
                    onClick={() => void createReview()}
                  >
                    {creating ? "Проверяем…" : "Проверить пару складов"}
                  </button>
                </>
              )}
              {error && <p role="alert">{error}</p>}
              {reviewID &&
                (durable.isLoading ? (
                  <p role="status">Получаем результат проверки…</p>
                ) : durable.error ? (
                  <p role="alert">
                    Проверка недоступна. Если сессия завершилась, войдите снова.
                  </p>
                ) : (
                  durable.data && (
                    <div className="review-confirmation">
                      <h3>
                        {saved
                          ? "Соответствие складов подтверждено"
                          : "Проверка складов готова к подтверждению"}
                      </h3>
                      {!saved && (
                        <>
                          {durable.data.review.existing_mapping_id !== null && (
                            <p className="mapping-review-warning">
                              Уже существует соответствие. Новое подтверждение
                              недоступно.
                            </p>
                          )}
                          {durable.data.status !== "CURRENT" && (
                            <p className="mapping-review-warning">
                              Проверка устарела либо данные или соответствие
                              изменились. Создайте новую проверку; старая не
                              обновляется автоматически.
                            </p>
                          )}
                          {!reviewMatchesSelection && (
                            <p role="alert">
                              Проверка не соответствует выбранным аккаунтам,
                              складам или загрузкам. Обновите данные и создайте
                              новую проверку.
                            </p>
                          )}
                          <label className="review-field">
                            Комментарий, необязательно
                            <input
                              value={explanation}
                              onChange={(e) => setExplanation(e.target.value)}
                              maxLength={1000}
                              placeholder="Без ключей доступа и персональных данных"
                            />
                          </label>
                          <label className="review-acknowledge">
                            <input
                              type="checkbox"
                              checked={ack}
                              onChange={(e) => setAck(e.target.checked)}
                            />
                            Подтверждаю соответствие выбранных складов
                          </label>
                          <button
                            className="mapping-primary"
                            disabled={
                              !ack ||
                              !!active.pending ||
                              durable.data.status !== "CURRENT" ||
                              durable.data.review.existing_mapping_id !==
                                null ||
                              active.result?.review_id === reviewID ||
                              active.failure?.reviewId === reviewID ||
                              !Number.isFinite(
                                Date.parse(durable.data.review.expires_at),
                              ) ||
                              Date.now() >=
                                Date.parse(durable.data.review.expires_at) ||
                              !eligible ||
                              !reviewMatchesSelection
                            }
                            onClick={() => {
                              try {
                                active.start(
                                  scope,
                                  `${moy} → ${ozon}`,
                                  reviewID,
                                  explanation,
                                  "warehouse",
                                );
                              } catch {
                                setError(
                                  "Комментарий содержит недопустимые символы или превышает 1000 байт UTF-8.",
                                );
                              }
                            }}
                          >
                            Подтвердить соответствие складов
                          </button>
                        </>
                      )}
                      {saved && (
                        <p className="mapping-save-success" role="status">
                          Соответствие складов сохранено.
                        </p>
                      )}
                      <details className="mapping-technical">
                        <summary>Технические детали проверки</summary>
                        <dl>
                          <dt>Проверка</dt>
                          <dd>
                            <code>{durable.data.review.id}</code>
                          </dd>
                          <dt>Состояние</dt>
                          <dd>{durable.data.status}</dd>
                          <dt>Склад МойСклад</dt>
                          <dd>
                            <code>
                              {durable.data.review.moysklad_warehouse_id}
                            </code>
                          </dd>
                          <dt>Склад Ozon</dt>
                          <dd>
                            <code>{durable.data.review.ozon_warehouse_id}</code>
                          </dd>
                          <dt>Создана</dt>
                          <dd>
                            <time dateTime={durable.data.review.created_at}>
                              {new Date(
                                durable.data.review.created_at,
                              ).toLocaleString("ru-RU")}
                            </time>
                          </dd>
                          <dt>Действует до</dt>
                          <dd>
                            <time dateTime={durable.data.review.expires_at}>
                              {new Date(
                                durable.data.review.expires_at,
                              ).toLocaleString("ru-RU")}
                            </time>
                          </dd>
                          <dt>Загрузки</dt>
                          <dd>
                            МойСклад {durable.data.review.moysklad_import_id};
                            Ozon {durable.data.review.ozon_import_id}
                          </dd>
                        </dl>
                      </details>
                    </div>
                  )
                ))}
              {samePending && (
                <div className="mapping-request-state" role="alert">
                  <p>
                    {active.busy
                      ? "Сохраняем соответствие складов… Смена аккаунта не отменяет запрос."
                      : active.sessionLost
                        ? "Сессия завершилась. Войдите снова и проверьте историю исходной пары аккаунтов."
                        : "Результат сохранения уточняется. Повторите тот же запрос, чтобы проверить результат."}
                  </p>
                  {!active.busy && !active.sessionLost && (
                    <button className="mapping-primary" onClick={active.retry}>
                      Повторить тот же запрос
                    </button>
                  )}
                </div>
              )}
              <details className="mapping-technical">
                <summary>Состояние выбранной пары</summary>
                <p>
                  <code>{q.data.status}</code>
                </p>
                <h3>Существующие соответствия</h3>
                {q.data.mappings.length ? (
                  <ul>
                    {q.data.mappings.map((m) => (
                      <li key={m.mapping_id}>
                        Соответствие {m.mapping_id}, версия {m.version}:{" "}
                        <code>
                          {m.moysklad_warehouse_id} → {m.ozon_warehouse_id}
                        </code>
                      </li>
                    ))}
                  </ul>
                ) : q.data.status === "MAPPING_CONFLICT" ? (
                  <p>
                    Конфликтующее соответствие существует, но его история за
                    пределами доступных аккаунтов.
                  </p>
                ) : (
                  <p>Для выбранной пары подтверждённых соответствий нет.</p>
                )}
              </details>
              <details className="mapping-history">
                <summary>История соответствий складов</summary>
                {q.data.history.length ? (
                  <ul>
                    {q.data.history.map((m) => (
                      <li key={`${m.mapping_id}:${m.version}`}>
                        <strong>
                          Соответствие {m.mapping_id}, версия {m.version}
                        </strong>
                        <p>
                          <code>
                            {m.moysklad_warehouse_id} → {m.ozon_warehouse_id}
                          </code>
                        </p>
                        <time dateTime={m.confirmed_at}>
                          {new Date(m.confirmed_at).toLocaleString("ru-RU")}
                        </time>
                        <p>Происхождение: {m.provenance}</p>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p>Для этих складов доступной истории нет.</p>
                )}
                <div className="mapping-review-pagination">
                  <button
                    disabled={!offset}
                    onClick={() => setOffset(Math.max(0, offset - 50))}
                  >
                    Предыдущие подтверждения
                  </button>
                  <button
                    disabled={!q.data.has_next}
                    onClick={() => setOffset(offset + 50)}
                  >
                    Следующие подтверждения
                  </button>
                </div>
              </details>
            </>
          )
        )}
      </section>
    </IntegrationDialog>
  );
}
