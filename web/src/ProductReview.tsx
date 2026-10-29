import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, post, params, ApiError, type Page, type Product } from "./api";
import type { Confirmation } from "./Confirmation";
import { IntegrationDialog } from "./IntegrationDialog";

type Scope = { tenant: string; moy: string; ozon: string };
export type MappingTarget = {
  kind: string;
  id: string;
  name?: string;
  article: string;
  code: string;
  parent_id?: string;
};
type History = {
  mapping_id: number;
  version: number;
  product_id: string;
  offer_id: string;
  target_kind: string;
  target_id: string;
  provenance: string;
  confirmed_at: string;
  ozon_import_id: number;
  moysklad_import_id: number;
  evidence_class?: string;
  principal_type?: string;
  explanation?: string;
};
type Review = {
  Review: {
    ID: string;
    EvidenceClass: string;
    CreatedAt: string;
    ExpiresAt: string;
    OzonImportID: number;
    MoyskladImportID: number;
    ExistingMappingVersion: number | null;
    Source: { ID: string };
    Target: { ID: string; Kind: string };
  };
  Status: string;
  MappingConflict: boolean;
  PrincipalType: string;
};
export function ProductReview({
  product,
  scope,
  close,
  confirmation,
}: {
  product: Product;
  scope: Scope;
  close: () => void;
  confirmation: Confirmation;
}) {
  const client = useQueryClient();
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [historyOffset, setHistoryOffset] = useState(0);
  const [selected, setSelected] = useState<MappingTarget | null>(null);
  const [evidence, setEvidence] = useState("OPERATOR_ATTESTED");
  const [reviewID, setReviewID] = useState("");
  const [creating, setCreating] = useState(false);
  const [reviewError, setReviewError] = useState("");
  const [acknowledge, setAcknowledge] = useState(false);
  const [explanation, setExplanation] = useState("");
  const base = {
    tenant: scope.tenant,
    moysklad_account: scope.moy,
    ozon_account: scope.ozon,
  };
  const targets = useQuery({
    queryKey: ["review-targets", scope, search, offset],
    queryFn: ({ signal }) =>
      api<Page<MappingTarget>>(
        "/api/moysklad-products?" + params({ ...base, search, offset }),
        signal,
      ),
  });
  const history = useQuery({
    queryKey: ["review-history", scope, product.product_id, historyOffset],
    queryFn: ({ signal }) =>
      api<Page<History>>(
        "/api/product-mapping-history?" +
          params({
            ...base,
            product: product.product_id,
            offset: historyOffset,
          }),
        signal,
      ),
  });
  const review = useQuery({
    queryKey: ["mapping-review", reviewID],
    queryFn: ({ signal }) =>
      api<Review>(`/api/product-mapping-reviews/${reviewID}`, signal),
    enabled: !!reviewID,
    staleTime: 0,
    refetchInterval: 5000,
    retry: false,
  });
  useEffect(() => {
    if (review.error instanceof ApiError && review.error.status === 401)
      void client.invalidateQueries({ queryKey: ["session"] });
  }, [review.error, client]);
  const candidate =
    selected &&
    product.candidates.find(
      (c) => c.kind === selected.kind && c.id === selected.id,
    );
  async function createReview() {
    if (!selected || creating || confirmation.pending) return;
    setCreating(true);
    setReviewError("");
    try {
      const result = await post<Review>("/api/product-mapping-reviews", {
        ...base,
        product_id: product.product_id,
        target_kind: selected.kind,
        target_id: selected.id,
        evidence_class: evidence,
      });
      setReviewID(result.Review.ID);
      setAcknowledge(false);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401)
        void client.invalidateQueries({ queryKey: ["session"] });
      setReviewError(
        err instanceof ApiError && err.status === 401
          ? "Сессия завершилась. Войдите снова."
          : "Не удалось проверить соответствие. Проверьте выбранные товары и аккаунты.",
      );
    } finally {
      setCreating(false);
    }
  }
  const pending = confirmation.pending;
  const samePending =
    !!pending &&
    pending.intent.kind === "product" &&
    pending.scope.tenant === scope.tenant &&
    pending.scope.moy === scope.moy &&
    pending.scope.ozon === scope.ozon &&
    pending.product === product.product_id;
  const saved = !!reviewID && confirmation.result?.review_id === reviewID;
  const rejected = !!reviewID && confirmation.failure?.reviewId === reviewID;
  const kindName = (kind: string) => (kind === "variant" ? "вариант" : "товар");
  const matchLabels: Record<string, string> = {
    CONFIRMED: "Соответствие уже подтверждено",
    ONE_CANDIDATE: "Найдено возможное соответствие",
    AMBIGUOUS: "Найдено несколько вариантов — выберите нужный",
    NO_CANDIDATE: "Предложений нет. Вы можете выбрать товар вручную.",
    CONFLICT:
      "Есть конфликт соответствий. Проверьте историю перед подтверждением.",
    INSUFFICIENT_DATA:
      "Недостаточно данных для предложения. Проверьте товары вручную.",
  };
  return (
    <IntegrationDialog
      title="Сопоставить товар"
      onClose={close}
      closeLabel="Закрыть сопоставление"
    >
      <section className="mapping-review" aria-label="Проверка соответствия">
        <div className="mapping-review-source">
          <span>Товар в Ozon</span>
          <strong>{product.offer_id || `Товар ${product.product_id}`}</strong>
          {product.sku && <span>SKU: {product.sku}</span>}
        </div>
        <p className="mapping-review-status">
          {matchLabels[product.match_status] ||
            "Проверьте товары перед подтверждением."}
        </p>
        {product.candidates.length > 0 && (
          <details className="review-suggestions">
            <summary>
              Возможные соответствия ({product.candidates.length})
            </summary>
            <p>
              Совпадение артикула или кода помогает найти товар, но не
              подтверждает его идентичность.
            </p>
            <ul>
              {product.candidates.map((c, index) => (
                <li key={c.kind + c.id}>
                  <button
                    disabled={creating || !!confirmation.pending}
                    onClick={() => {
                      setSearch(c.id);
                      setOffset(0);
                      setSelected(null);
                      setReviewID("");
                    }}
                  >
                    {c.name || `Посмотреть предложение ${index + 1}`}{" "}
                    {c.kind === "variant" ? "· вариант" : "· товар"}
                  </button>
                  <details className="mapping-technical">
                    <summary>Данные предложения</summary>
                    <p>
                      <code>
                        {c.kind} · {c.id}
                      </code>
                    </p>
                    <p>Совпавшие поля: {c.evidence.join(", ")}</p>
                  </details>
                </li>
              ))}
            </ul>
          </details>
        )}
        {product.candidates_truncated && (
          <p className="mapping-review-warning">
            Показана только часть предложений. Нельзя считать найденный вариант
            единственным.
          </p>
        )}
        <label className="review-search">
          Поиск товара МойСклад
          <input
            placeholder="Название, артикул, код или ID"
            disabled={creating}
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setOffset(0);
              setSelected(null);
              setReviewID("");
            }}
          />
        </label>
        {targets.isLoading ? (
          <p role="status">Загружаем товары…</p>
        ) : targets.error ? (
          <p role="alert">Поиск товаров недоступен. Попробуйте ещё раз.</p>
        ) : targets.data?.items.length === 0 ? (
          <p>Товары не найдены.</p>
        ) : (
          <ul className="review-targets">
            {targets.data?.items.map((t) => (
              <li
                key={t.kind + t.id}
                className={
                  selected?.kind === t.kind && selected?.id === t.id
                    ? "selected"
                    : ""
                }
              >
                <button
                  className="review-target-button"
                  aria-label={`${t.name || "Без названия"}, ${kindName(t.kind)}`}
                  aria-pressed={
                    selected?.kind === t.kind && selected?.id === t.id
                  }
                  disabled={creating}
                  onClick={() => {
                    setSelected(t);
                    setReviewID("");
                    setEvidence("OPERATOR_ATTESTED");
                  }}
                >
                  <span className="review-target-name">
                    {t.name ||
                      (t.kind === "variant"
                        ? "Вариант без названия"
                        : "Товар без названия")}
                  </span>
                  <span>
                    {t.kind === "variant" ? "Вариант товара" : "Товар"}
                  </span>
                </button>
                <p className="review-target-meta">
                  Артикул: {t.article || "не указан"} · Код:{" "}
                  {t.code || "не указан"}
                </p>
              </li>
            ))}
          </ul>
        )}
        <div className="mapping-review-pagination">
          <button
            disabled={offset === 0}
            onClick={() => setOffset(Math.max(offset - 50, 0))}
          >
            Предыдущие товары
          </button>
          <button
            disabled={!targets.data?.has_next}
            onClick={() => setOffset(offset + 50)}
          >
            Следующие товары
          </button>
        </div>
        {selected && (
          <div className="review-evidence">
            <h3>Проверьте выбранную пару</h3>
            <p>
              <strong>{product.offer_id}</strong> в Ozon →{" "}
              <strong>{selected.name || "Товар без названия"}</strong> в
              МойСклад
            </p>
            <p>
              {candidate
                ? "Артикул или код совпал. Убедитесь, что это один и тот же товар."
                : "Совпадения нет. Проверьте, что товары представляют одну и ту же позицию."}
            </p>
            <p>
              Артикул: {selected.article || "не указан"}; код:{" "}
              {selected.code || "не указан"}.
            </p>
            <label className="review-field">
              Основание для подтверждения
              <select
                disabled={creating}
                value={evidence}
                onChange={(e) => {
                  setEvidence(e.target.value);
                  setReviewID("");
                }}
              >
                <option value="OPERATOR_ATTESTED">
                  Проверено мной вручную
                </option>
                {candidate && (
                  <option value="DETERMINISTIC_CANDIDATE">
                    Совпадение артикула или кода, проверенное мной
                  </option>
                )}
              </select>
            </label>
            <button
              className="mapping-primary"
              disabled={creating || !!confirmation.pending}
              onClick={() => void createReview()}
            >
              {creating ? "Проверяем…" : "Проверить соответствие"}
            </button>
            {reviewError && <p role="alert">{reviewError}</p>}
            {reviewID &&
              (review.isLoading ? (
                <p role="status">Получаем результат проверки…</p>
              ) : review.error ? (
                <p role="alert">
                  Проверка недоступна. Если сессия завершилась, войдите снова.
                </p>
              ) : (
                review.data && (
                  <div className="review-confirmation">
                    <h3>
                      {saved
                        ? "Соответствие подтверждено"
                        : "Проверка готова к подтверждению"}
                    </h3>
                    {!saved && (
                      <>
                        <p>
                          Вы подтверждаете, что выбранные товары обозначают одну
                          и ту же позицию. Это ваше подтверждение, а не
                          независимая проверка.
                        </p>
                        {review.data.MappingConflict && (
                          <p className="mapping-review-warning" role="alert">
                            Уже существует конфликтующее соответствие.
                            Подтверждение заблокировано.
                          </p>
                        )}
                        {review.data.Status !== "CURRENT" && (
                          <p className="mapping-review-warning" role="alert">
                            Проверка устарела либо данные или соответствие
                            изменились. Создайте новую проверку; старая не
                            обновляется автоматически.
                          </p>
                        )}
                        {rejected && (
                          <p className="mapping-review-warning" role="alert">
                            Подтверждение отклонено. Проверьте выбранную пару и
                            создайте новую проверку.
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
                            checked={acknowledge}
                            onChange={(e) => setAcknowledge(e.target.checked)}
                          />
                          Подтверждаю, что это один и тот же товар
                        </label>
                        <button
                          className="mapping-primary"
                          disabled={
                            !acknowledge ||
                            !!confirmation.pending ||
                            rejected ||
                            review.data.Status !== "CURRENT" ||
                            confirmation.result?.review_id === reviewID ||
                            review.data.MappingConflict ||
                            Date.now() >=
                              Date.parse(review.data.Review.ExpiresAt)
                          }
                          onClick={() => {
                            try {
                              confirmation.start(
                                scope,
                                product.product_id,
                                reviewID,
                                explanation,
                              );
                            } catch {
                              setReviewError(
                                "Комментарий содержит недопустимые символы или превышает 1000 байт UTF-8.",
                              );
                            }
                          }}
                        >
                          Подтвердить соответствие
                        </button>
                      </>
                    )}
                    {saved && (
                      <p className="mapping-save-success" role="status">
                        Соответствие сохранено. Это подтверждение не включает
                        публикацию остатков.
                      </p>
                    )}
                    <details className="mapping-technical">
                      <summary>Технические детали проверки</summary>
                      <dl>
                        <dt>Проверка</dt>
                        <dd>
                          <code>{reviewID}</code>
                        </dd>
                        <dt>Класс подтверждения</dt>
                        <dd>{review.data.Review.EvidenceClass}</dd>
                        <dt>Состояние</dt>
                        <dd>{review.data.Status}</dd>
                        <dt>Создана</dt>
                        <dd>
                          <time dateTime={review.data.Review.CreatedAt}>
                            {new Date(
                              review.data.Review.CreatedAt,
                            ).toLocaleString("ru-RU")}
                          </time>
                        </dd>
                        <dt>Действует до</dt>
                        <dd>
                          <time dateTime={review.data.Review.ExpiresAt}>
                            {new Date(
                              review.data.Review.ExpiresAt,
                            ).toLocaleString("ru-RU")}
                          </time>
                        </dd>
                        <dt>Загрузки</dt>
                        <dd>
                          Ozon {review.data.Review.OzonImportID}; МойСклад{" "}
                          {review.data.Review.MoyskladImportID}
                        </dd>
                        <dt>Товар Ozon</dt>
                        <dd>
                          <code>{review.data.Review.Source.ID}</code>
                        </dd>
                        <dt>Товар МойСклад</dt>
                        <dd>
                          <code>
                            {review.data.Review.Target.Kind} ·{" "}
                            {review.data.Review.Target.ID}
                          </code>
                        </dd>
                        {review.data.Review.ExistingMappingVersion !== null && (
                          <>
                            <dt>Версия соответствия</dt>
                            <dd>{review.data.Review.ExistingMappingVersion}</dd>
                          </>
                        )}
                      </dl>
                    </details>
                  </div>
                )
              ))}
          </div>
        )}
        {samePending && (
          <div
            className="mapping-request-state"
            role={pending.uncertain ? "alert" : "status"}
          >
            <p>
              {confirmation.busy
                ? "Сохраняем соответствие… Смена аккаунта не отменяет запрос."
                : confirmation.sessionLost
                  ? "Сессия завершилась. Войдите снова и проверьте историю исходной пары аккаунтов."
                  : "Результат сохранения уточняется. Повторите тот же запрос, чтобы проверить результат без создания нового подтверждения."}
            </p>
            {!confirmation.busy && !confirmation.sessionLost && (
              <button className="mapping-primary" onClick={confirmation.retry}>
                Повторить тот же запрос
              </button>
            )}
          </div>
        )}
        {pending && !samePending && (
          <p className="mapping-review-warning" role="alert">
            Есть незавершённое подтверждение для другого товара или аккаунта.
            Закройте это окно и вернитесь к исходному запросу.
          </p>
        )}
        <details className="mapping-technical">
          <summary>Технические данные товаров</summary>
          <dl>
            <dt>Рабочее пространство</dt>
            <dd>
              <code>{scope.tenant}</code>
            </dd>
            <dt>Аккаунт МойСклад</dt>
            <dd>
              <code>{scope.moy}</code>
            </dd>
            <dt>Аккаунт Ozon</dt>
            <dd>
              <code>{scope.ozon}</code>
            </dd>
            <dt>ID товара Ozon</dt>
            <dd>
              <code>{product.product_id}</code>
            </dd>
            <dt>SKU</dt>
            <dd>{product.sku ?? "Неизвестен"}</dd>
            <dt>Состояние товара</dt>
            <dd>
              {product.archived === null
                ? "Статус архива неизвестен"
                : product.archived
                  ? "Архивный"
                  : "Активный"}
            </dd>
            <dt>Результат сопоставления</dt>
            <dd>{product.match_status}</dd>
            {product.target_id && (
              <>
                <dt>Текущее соответствие</dt>
                <dd>
                  <code>
                    {product.target_kind} · {product.target_id}
                  </code>
                </dd>
              </>
            )}
            {selected && (
              <>
                <dt>ID товара МойСклад</dt>
                <dd>
                  <code>
                    {selected.kind} · {selected.id}
                  </code>
                </dd>
                {selected.parent_id && (
                  <>
                    <dt>Родительский товар</dt>
                    <dd>
                      <code>{selected.parent_id}</code>
                    </dd>
                  </>
                )}
                {candidate && (
                  <>
                    <dt>Поля совпадения</dt>
                    <dd>{candidate.evidence.join(", ")}</dd>
                  </>
                )}
              </>
            )}
          </dl>
        </details>
        <details className="mapping-history">
          <summary>История соответствий</summary>
          {history.isLoading ? (
            <p role="status">Загружаем историю…</p>
          ) : history.error ? (
            <p role="alert">История соответствий недоступна.</p>
          ) : history.data?.items.length === 0 ? (
            <p>Для этого товара и пары аккаунтов пока нет подтверждений.</p>
          ) : (
            <ul>
              {history.data?.items.map((h) => (
                <li key={h.mapping_id + ":" + h.version}>
                  <strong>
                    Соответствие {h.mapping_id}, версия {h.version}
                  </strong>
                  <time dateTime={h.confirmed_at}>
                    {new Date(h.confirmed_at).toLocaleString("ru-RU")}
                  </time>
                  <details className="mapping-technical">
                    <summary>Подробнее</summary>
                    <p>
                      {h.offer_id} →{" "}
                      <code>
                        {h.target_kind} {h.target_id}
                      </code>
                    </p>
                    <p>
                      Происхождение: {h.provenance}; загрузки {h.ozon_import_id}{" "}
                      / {h.moysklad_import_id}
                    </p>
                    {h.evidence_class && (
                      <p>Класс подтверждения: {h.evidence_class}</p>
                    )}
                    {h.principal_type && <p>Подтвердил: {h.principal_type}</p>}
                    {h.explanation && <p>Комментарий: {h.explanation}</p>}
                  </details>
                </li>
              ))}
            </ul>
          )}
          <div className="mapping-review-pagination">
            <button
              disabled={historyOffset === 0}
              onClick={() => setHistoryOffset(Math.max(historyOffset - 50, 0))}
            >
              Предыдущие подтверждения
            </button>
            <button
              disabled={!history.data?.has_next}
              onClick={() => setHistoryOffset(historyOffset + 50)}
            >
              Следующие подтверждения
            </button>
          </div>
        </details>
      </section>
    </IntegrationDialog>
  );
}
