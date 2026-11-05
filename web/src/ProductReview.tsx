import { ProductMatchingConfirmation } from "./ProductMatchingConfirmation";
import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, post, params, ApiError, type Page, type Product } from "./api";
import type { Confirmation } from "./Confirmation";
import { OzonProductPanel, TargetSearchPanel } from "./ProductMatchingPanels";
import { IntegrationDialog } from "./IntegrationDialog";

type Scope = { tenant: string; moy: string; ozon: string };
export type MappingTarget = {
  kind: string;
  id: string;
  name?: string;
  article: string;
  code: string;
  parent_id?: string;
  quantity?: string | null;
  mapped?: boolean;
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
  next,
}: {
  product: Product;
  scope: Scope;
  close: () => void;
  confirmation: Confirmation;
  next?: () => void;
}) {
  const client = useQueryClient();
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [query, setQuery] = useState("");
  const [searchBy, setSearchBy] = useState("all");
  const [inStockOnly, setInStockOnly] = useState(false);
  const [pageSize, setPageSize] = useState(20);
  const advance = useRef(false);
  const transitioned = useRef(false);
  useEffect(() => {
    const timer = setTimeout(() => {
      setQuery(search);
      setOffset(0);
    }, 350);
    return () => clearTimeout(timer);
  }, [search]);
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
    queryKey: [
      "review-targets",
      scope,
      query,
      searchBy,
      inStockOnly,
      offset,
      pageSize,
    ],
    queryFn: ({ signal }) =>
      api<
        Page<MappingTarget> & { total: number; page: number; pageSize: number }
      >(
        "/api/moysklad-products?" +
          params({
            ...base,
            query,
            searchBy,
            inStockOnly: String(inStockOnly),
            page: offset / pageSize + 1,
            pageSize,
          }),
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
  useEffect(() => {
    if (!saved || transitioned.current || !advance.current) return;
    transitioned.current = true;
    next?.();
  }, [saved, next]);
  const rejected = !!reviewID && confirmation.failure?.reviewId === reviewID;
  return (
    <IntegrationDialog
      title="Сопоставить товар"
      onClose={close}
      closeLabel="Закрыть сопоставление"
      className="product-matching-dialog"
      initialFocus=".matching-search-line input"
    >
      <section className="mapping-review" aria-label="Проверка соответствия">
        <p className="matching-subtitle">
          Выберите товар МойСклад для товара Ozon
        </p>
        <div className="matching-workspace">
          <OzonProductPanel product={product} />
          <div className="matching-right">
            <TargetSearchPanel
              search={search}
              setSearch={setSearch}
              searchBy={searchBy}
              setSearchBy={(v) => {
                setSearchBy(v);
                setOffset(0);
              }}
              inStockOnly={inStockOnly}
              setInStockOnly={(v) => {
                setInStockOnly(v);
                setOffset(0);
              }}
              pageSize={pageSize}
              setPageSize={(v) => {
                setPageSize(v);
                setOffset(0);
              }}
              offset={offset}
              setOffset={setOffset}
              query={targets}
              selected={selected}
              disabled={creating || !!confirmation.pending}
              select={(t) => {
                setSelected(t);
                setReviewID("");
                setAcknowledge(false);
                setEvidence("OPERATOR_ATTESTED");
              }}
            />
            <details className="matching-additional">
              <summary>Дополнительно</summary>
              {
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
                      </li>
                    ))}
                  </ul>
                  {!product.candidates.length && (
                    <p>
                      Возможные соответствия пока не найдены. Используйте поиск.
                    </p>
                  )}
                  {product.candidates_truncated && (
                    <p className="mapping-review-warning">
                      Показана только часть предложений. Найденный вариант может
                      быть не единственным.
                    </p>
                  )}
                </details>
              }

              <details className="mapping-history">
                <summary>История сопоставлений</summary>
                {history.isLoading ? (
                  <p role="status">Загружаем историю…</p>
                ) : history.error ? (
                  <p role="alert">История соответствий недоступна.</p>
                ) : history.data?.items.length === 0 ? (
                  <p>
                    Для этого товара и пары аккаунтов пока нет подтверждений.
                  </p>
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
                      </li>
                    ))}
                  </ul>
                )}
                <div className="mapping-review-pagination">
                  <button
                    disabled={historyOffset === 0}
                    onClick={() =>
                      setHistoryOffset(Math.max(historyOffset - 50, 0))
                    }
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
            </details>
          </div>
        </div>
        {selected && (
          <ProductMatchingConfirmation
            selected={selected}
            offer={product.offer_id}
            candidate={!!candidate}
            evidence={evidence}
            setEvidence={(value) => {
              setEvidence(value);
              setReviewID("");
            }}
            create={() => void createReview()}
            creating={creating}
            pending={!!pending}
            reviewID={reviewID}
            loading={review.isLoading}
            error={!!review.error}
            message={reviewError}
            saved={saved}
            conflict={!!review.data?.MappingConflict}
            current={review.data?.Status === "CURRENT"}
            rejected={rejected}
            acknowledge={acknowledge}
            setAcknowledge={setAcknowledge}
            explanation={explanation}
            setExplanation={setExplanation}
          />
        )}
        <div className="matching-request-notice">
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
                <button
                  className="mapping-primary"
                  onClick={confirmation.retry}
                >
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
        </div>
        <footer className="matching-footer">
          <span>
            {selected
              ? `Выбран: ${selected.name || selected.id}`
              : "Выберите товар для сопоставления"}
          </span>
          {next && (
            <button disabled={creating || !!pending} onClick={next}>
              Пропустить товар
            </button>
          )}
          <button disabled={creating || !!pending} onClick={close}>
            Отмена
          </button>
          <button
            className="mapping-primary"
            disabled={
              !selected ||
              creating ||
              !!pending ||
              saved ||
              (!!reviewID &&
                (!acknowledge ||
                  review.data?.Status !== "CURRENT" ||
                  review.data.MappingConflict ||
                  Date.now() >= Date.parse(review.data.Review.ExpiresAt) ||
                  rejected))
            }
            onClick={() => {
              advance.current = false;
              if (!reviewID) {
                void createReview();
                return;
              }
              try {
                confirmation.start(
                  scope,
                  product.product_id,
                  reviewID,
                  explanation,
                );
              } catch {
                setReviewError("Проверьте комментарий.");
              }
            }}
          >
            {creating || confirmation.busy ? "Сохраняем…" : "Сопоставить"}
          </button>
          {next && (
            <button
              className="mapping-primary"
              disabled={
                !selected ||
                !reviewID ||
                !acknowledge ||
                creating ||
                !!pending ||
                saved ||
                review.data?.Status !== "CURRENT" ||
                review.data.MappingConflict ||
                Date.now() >= Date.parse(review.data.Review.ExpiresAt) ||
                rejected
              }
              onClick={() => {
                advance.current = true;
                try {
                  confirmation.start(
                    scope,
                    product.product_id,
                    reviewID,
                    explanation,
                  );
                } catch {
                  setReviewError("Проверьте комментарий.");
                }
              }}
            >
              Сопоставить и перейти к следующему
            </button>
          )}
        </footer>
      </section>
    </IntegrationDialog>
  );
}
