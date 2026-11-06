import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, post, params, ApiError, type Product, type Page } from "./api";
import { IntegrationDialog } from "./IntegrationDialog";
import { BrandLogo } from "./BrandLogo";
import type { Confirmation, Scope } from "./Confirmation";
import type { MappingTarget } from "./ProductReview";
import { ProductConflictSearch } from "./ProductConflictSearch";
import { conflictTitle, conflictExplanation } from "./productConflict";

type Review = {
  Review: { ID: string; ExpiresAt: string };
  Status: string;
  MappingConflict: boolean;
  ReplacementAllowed?: boolean;
};
type History = {
  mapping_id: number;
  version: number;
  confirmed_at: string;
  target_id: string;
};

function Pair({
  product,
  target,
  current = false,
}: {
  product: Product;
  target?: MappingTarget | null;
  current?: boolean;
}) {
  return (
    <div className="conflict-pair">
      <div>
        <span className="conflict-provider">
          <BrandLogo provider="ozon" decorative />
          Товар в Ozon
        </span>
        <b>{product.name || product.offer_id}</b>
        <small>Артикул: {product.offer_id || "не указан"}</small>
      </div>
      <div>
        <span className="conflict-provider">
          <BrandLogo provider="moysklad" decorative />
          {current ? "Сейчас связан с" : "Выбран в МойСклад"}
        </span>
        <b>
          {current
            ? product.target_name || "Связь в другом аккаунте"
            : target?.name || target?.id}
        </b>
        <small>
          Артикул:{" "}
          {(current ? product.target_article : target?.article) || "не указан"}
        </small>
        {!current && target?.code && <small>Код: {target.code}</small>}
      </div>
    </div>
  );
}

function ConflictDetails({
  product,
  scope,
}: {
  product: Product;
  scope: Scope;
}) {
  const [open, setOpen] = useState(false);
  const [offset, setOffset] = useState(0);
  const history = useQuery({
    queryKey: ["review-history", scope, product.product_id, offset],
    enabled: open,
    retry: false,
    queryFn: ({ signal }) =>
      api<Page<History>>(
        "/api/product-mapping-history?" +
          params({
            tenant: scope.tenant,
            moysklad_account: scope.moy,
            ozon_account: scope.ozon,
            product: product.product_id,
            offset,
          }),
        signal,
      ),
  });
  return (
    <details
      className="conflict-details"
      open={open}
      onToggle={(e) => setOpen(e.currentTarget.open)}
    >
      <summary>История связи и идентификаторы</summary>
      <dl>
        <div>
          <dt>Артикул Ozon</dt>
          <dd>{product.offer_id}</dd>
        </div>
        <div>
          <dt>ID товара Ozon</dt>
          <dd>{product.product_id}</dd>
        </div>
        {product.target_id && (
          <div>
            <dt>ID текущего товара МойСклад</dt>
            <dd>{product.target_id}</dd>
          </div>
        )}
      </dl>
      {product.shared_inventory && (
        <p>
          Товар МойСклад связан с несколькими предложениями Ozon. Общий остаток
          сам по себе не является ошибкой.
        </p>
      )}
      {history.isFetching && <p role="status">Загружаем историю…</p>}
      {history.isError && (
        <p role="alert">
          История недоступна.{" "}
          <button
            className="text-action"
            onClick={() => void history.refetch()}
          >
            Повторить
          </button>
        </p>
      )}
      {history.data?.items.length === 0 && (
        <p>История подтверждений для выбранных аккаунтов пуста.</p>
      )}
      <ol>
        {history.data?.items.map((h) => (
          <li key={h.mapping_id + ":" + h.version}>
            <div>
              <b>Версия {h.version}</b>
              <small>
                Товар МойСклад: <code>{h.target_id}</code>
              </small>
            </div>
            <time dateTime={h.confirmed_at}>
              {new Date(h.confirmed_at).toLocaleString("ru-RU")}
            </time>
          </li>
        ))}
      </ol>
      {(offset > 0 || history.data?.has_next) && (
        <div className="conflict-search-pager">
          <button
            className="secondary"
            disabled={!offset || history.isFetching}
            onClick={() => setOffset(Math.max(0, offset - 50))}
          >
            Предыдущие подтверждения
          </button>
          <button
            className="secondary"
            disabled={!history.data?.has_next || history.isFetching}
            onClick={() => setOffset(offset + 50)}
          >
            Следующие подтверждения
          </button>
        </div>
      )}
    </details>
  );
}

function Frame({
  children,
  footer,
  close,
  step,
}: {
  children: ReactNode;
  footer: ReactNode;
  close: () => void;
  step: string;
}) {
  const body = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (step !== "search")
      body.current?.querySelector<HTMLElement>(".conflict-heading")?.focus();
  }, [step]);
  return (
    <IntegrationDialog
      title="Разобраться со связью"
      closeLabel="Закрыть разбор связи"
      className="products-conflict-dialog"
      onClose={close}
      initialFocus=".conflict-heading"
    >
      <div className="conflict-body" ref={body}>
        {children}
      </div>
      <footer className="conflict-footer">{footer}</footer>
    </IntegrationDialog>
  );
}

export function ProductMappingConflict({
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
  const [view, setView] = useState<"overview" | "search" | "compare">(
    "overview",
  );
  const [selected, setSelected] = useState<MappingTarget | null>(null);
  const [reviewID, setReviewID] = useState("");
  const [creating, setCreating] = useState(false);
  const [ack, setAck] = useState(false);
  const [error, setError] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const acknowledgement = useRef<HTMLInputElement>(null);
  const review = useQuery({
    queryKey: ["mapping-review", reviewID],
    queryFn: ({ signal }) =>
      api<Review>(`/api/product-mapping-reviews/${reviewID}`, signal),
    enabled: !!reviewID,
    retry: false,
    refetchInterval: 5000,
  });
  const busy = creating || !!confirmation.pending;
  const saved = !!reviewID && confirmation.result?.review_id === reviewID;
  const rejected = !!reviewID && confirmation.failure?.reviewId === reviewID;
  const ready =
    review.data?.Status === "CURRENT" &&
    review.data.ReplacementAllowed === true &&
    Date.now() < Date.parse(review.data.Review.ExpiresAt) &&
    !review.isError &&
    !rejected;
  useEffect(() => {
    if (ready) acknowledgement.current?.focus();
  }, [reviewID, ready]);
  function choose(target: MappingTarget) {
    setSelected(target);
    setView("compare");
    setReviewID("");
    setAck(false);
    setError("");
  }
  async function prepare() {
    if (!selected || busy) return;
    setCreating(true);
    setError("");
    try {
      const r = await post<Review>("/api/product-mapping-reviews", {
        tenant: scope.tenant,
        moysklad_account: scope.moy,
        ozon_account: scope.ozon,
        product_id: product.product_id,
        target_kind: selected.kind,
        target_id: selected.id,
        evidence_class: "OPERATOR_ATTESTED",
      });
      client.setQueryData(["mapping-review", r.Review.ID], r);
      setReviewID(r.Review.ID);
      setAck(false);
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 401
          ? "Сессия завершилась. Войдите снова."
          : "Не удалось проверить замену. Обновите сведения и повторите проверку.",
      );
    } finally {
      setCreating(false);
    }
  }
  async function refresh() {
    setRefreshing(true);
    setError("");
    try {
      await client.invalidateQueries(
        { queryKey: ["products"] },
        { throwOnError: true },
      );
      setReviewID("");
      setAck(false);
    } catch {
      setError("Не удалось обновить сведения. Повторите попытку.");
    } finally {
      setRefreshing(false);
    }
  }
  if (saved)
    return (
      <Frame
        step="saved"
        close={close}
        footer={
          <button className="primary-button" onClick={close}>
            Вернуться к товарам
          </button>
        }
      >
        <div className="conflict-success" role="status">
          <span>✓</span>
          <h3 className="conflict-heading" tabIndex={-1}>
            Связь заменена
          </h3>
          <p>
            Теперь товар Ozon связан с «{selected?.name || selected?.id}».
            Предыдущая связь сохранена в истории.
          </p>
          <p>Товары и остатки в сервисы не отправлялись.</p>
        </div>
      </Frame>
    );
  const candidates = product.candidates.filter(
    (c) => c.id !== product.target_id || c.kind !== product.target_kind,
  );
  const canChoose =
    !!product.target_id &&
    !product.conflict_reasons?.includes("OTHER_ACCOUNT") &&
    !product.conflict_reasons?.includes("OFFER_CONFLICT");
  return (
    <Frame
      step={view}
      close={close}
      footer={
        <>
          {view !== "overview" && (
            <button
              className="secondary"
              disabled={busy}
              onClick={() => {
                setView("overview");
                setReviewID("");
                setAck(false);
              }}
            >
              Назад к конфликту
            </button>
          )}
          <button className="secondary" onClick={close}>
            Закрыть
          </button>
          {view === "compare" && !reviewID && (
            <button
              className="primary-button"
              disabled={busy || !product.target_id || refreshing}
              onClick={() => void prepare()}
            >
              {creating ? "Проверяем замену…" : "Проверить замену"}
            </button>
          )}
          {view === "compare" && !!reviewID && (
            <button
              className="primary-button"
              disabled={busy || refreshing || !ready || !ack}
              onClick={() =>
                confirmation.start(
                  scope,
                  product.product_id,
                  reviewID,
                  "",
                  "product",
                  true,
                )
              }
            >
              {confirmation.busy ? "Заменяем связь…" : "Заменить связь"}
            </button>
          )}
        </>
      }
    >
      {view === "overview" ? (
        <>
          <div className="conflict-intro">
            <span className="conflict-status">Конфликт соответствия</span>
            <h3 className="conflict-heading" tabIndex={-1}>
              {conflictTitle(product)}
            </h3>
            <p>{conflictExplanation(product)}</p>
          </div>
          <section aria-label="Текущая связь">
            <Pair product={product} current />
          </section>
          {canChoose && (
            <section className="conflict-next">
              <h3>
                {candidates.length
                  ? "Сравните с найденным товаром"
                  : "Найдите правильный товар"}
              </h3>
              <p>
                Сначала сравните товары. Замена сохранится только после
                отдельного подтверждения.
              </p>
              <div className="conflict-options">
                {candidates.map((c) => (
                  <button
                    key={c.kind + ":" + c.id}
                    className="conflict-option"
                    disabled={busy}
                    onClick={() =>
                      choose({
                        ...c,
                        article: c.article || "",
                        code: c.code || "",
                      })
                    }
                  >
                    <span>
                      <b>{c.name || "Товар без названия"}</b>
                      <small>
                        {c.evidence.includes("article")
                          ? `Совпал артикул: ${c.article}`
                          : c.evidence.includes("code")
                            ? `Совпал код: ${c.code}`
                            : "Предложение для ручной проверки"}
                      </small>
                    </span>
                    <span>Сравнить</span>
                  </button>
                ))}
              </div>
              {product.conflict_reasons?.includes("OFFER_CHANGED") && (
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={() =>
                    choose({
                      id: product.target_id!,
                      kind: product.target_kind || "product",
                      name: product.target_name || "Текущий товар",
                      article: product.target_article || "",
                      code: "",
                    })
                  }
                >
                  Проверить текущую пару с новым артикулом
                </button>
              )}
              {product.candidates_truncated && (
                <p>
                  Показана часть совпадений. Используйте поиск, чтобы проверить
                  остальные товары.
                </p>
              )}
              <button
                className="secondary conflict-search-action"
                disabled={busy}
                onClick={() => setView("search")}
              >
                Найти другой товар в МойСклад
              </button>
            </section>
          )}
          <ConflictDetails product={product} scope={scope} />
        </>
      ) : view === "search" ? (
        <>
          <h3 className="conflict-heading" tabIndex={-1}>
            Выберите товар для сравнения
          </h3>
          <ProductConflictSearch
            scope={scope}
            offer={product.offer_id}
            choose={choose}
          />
        </>
      ) : (
        <>
          <div className="conflict-intro">
            <span className="conflict-step">Проверка замены</span>
            <h3 className="conflict-heading" tabIndex={-1}>
              Это один и тот же товар?
            </h3>
            <p>
              Проверьте модель, характеристики и комплектацию в обоих сервисах.
            </p>
          </div>
          <Pair product={product} target={selected} />
          <div className="conflict-change">
            <span>Текущая связь</span>
            <b>{product.target_name || "Не указана в выбранном аккаунте"}</b>
            <span>После замены</span>
            <b>{selected?.name || selected?.id}</b>
          </div>
          {product.offer_id !== selected?.article &&
            product.offer_id !== selected?.code && (
              <p className="conflict-warning">
                Артикулы и коды не совпадают. Одного похожего названия
                недостаточно для подтверждения.
              </p>
            )}
          <p className="conflict-impact">
            Замена изменит, какой товар используется при следующих передачах и
            расчёте остатков. Сама замена ничего не отправляет в Ozon или
            МойСклад.
          </p>
          {reviewID &&
            (review.isError ? (
              <p role="alert">
                Результат проверки недоступен.{" "}
                <button
                  className="text-action"
                  onClick={() => void review.refetch()}
                >
                  Повторить чтение
                </button>
              </p>
            ) : review.isFetching && !review.data ? (
              <p role="status">Проверяем актуальность связи…</p>
            ) : ready ? (
              <label className="conflict-ack">
                <input
                  ref={acknowledgement}
                  type="checkbox"
                  checked={ack}
                  disabled={busy}
                  onChange={(e) => setAck(e.target.checked)}
                />
                <span>
                  Я проверил товары и подтверждаю замену текущей связи
                </span>
              </label>
            ) : (
              <div role="alert" className="conflict-warning">
                <b>Замена пока недоступна</b>
                <p>
                  {rejected && confirmation.failure?.code === "MAPPING_BUSY"
                    ? "Есть незавершённая или неподтверждённая передача. Дождитесь её завершения или сверки результата и повторите проверку."
                    : review.data?.Status !== "CURRENT" || rejected
                      ? "Данные или связь изменились. Обновите сведения и выполните новую проверку."
                      : "Эту пару нельзя сохранить: связь относится к другому аккаунту, артикул занят другой связью или выбран текущий товар. Проверьте аккаунты, артикулы и выбранную пару."}
                </p>
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={() => void prepare()}
                >
                  Проверить заново
                </button>
              </div>
            ))}
        </>
      )}
      {error && (
        <p role="alert" className="conflict-warning">
          {error}
        </p>
      )}
      {confirmation.pending && (
        <div role="status" className="conflict-warning">
          <p>
            {confirmation.busy
              ? "Сохраняем замену…"
              : confirmation.sessionLost
                ? "Сессия завершилась. Войдите снова и проверьте историю связи."
                : "Ответ не получен. Замена могла сохраниться; уточните результат того же запроса."}
          </p>
          {!confirmation.busy && !confirmation.sessionLost && (
            <button className="secondary" onClick={confirmation.retry}>
              Уточнить результат
            </button>
          )}
        </div>
      )}
      <button
        className="text-action conflict-refresh"
        disabled={busy || refreshing}
        onClick={() => void refresh()}
      >
        {refreshing ? "Обновляем…" : "Обновить сведения"}
      </button>
    </Frame>
  );
}
