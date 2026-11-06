import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError, params, post, type Product } from "./api";
import { useConfirmation, type Confirmation, type Scope } from "./Confirmation";
import { ProductReview } from "./ProductReview";
import { BrandLogo } from "./BrandLogo";
import { scopeBody } from "./CatalogSyncTypes";

type Policy = {
  revision: number;
  exact_sku: boolean;
  names: boolean;
  auto_confirm: boolean;
  after_import: boolean;
};
type Candidate = {
  id: string;
  kind: string;
  name: string;
  article: string;
  evidence: string;
  score: number;
};
type MatchItem = {
  product_id: string;
  offer_id: string;
  name: string;
  status: string;
  reason: string;
  candidates: Candidate[];
  shared_inventory: boolean;
};
type Preview = {
  id: string;
  scope: ReturnType<typeof scopeBody>;
  policy: Policy;
  expires_at: string;
  items: MatchItem[];
};
type Run = {
  id: string;
  preview_id: string;
  items: MatchItem[];
  confirmed: number;
};
const reasons: Record<string, string> = {
  exact_sku: "Артикул или код совпал",
  similar_name: "Похожее название",
  duplicate_sku: "Дубли артикула",
  duplicate_offer: "Повторяющийся артикул Ozon",
  no_match: "Нет пары",
  already_confirmed: "Связь уже подтверждена",
  confirmed_by_rule: "Подтверждено по точному совпадению",
  lifecycle_unknown: "Нужно обновить данные об архивности",
  archived: "Товар в архиве",
  variant_requires_review: "Проверьте вариант товара",
  mapping_conflict: "Конфликт существующей связи",
};
function failure(error: unknown) {
  return error instanceof ApiError && error.status === 409
    ? "Данные или настройки изменились. Обновите каталоги обоих сервисов и выполните новую проверку. Требуются полные данные не старше часа."
    : error instanceof ApiError && error.status === 404
      ? "Эта проверка недоступна для выбранных аккаунтов."
      : "Не удалось выполнить сопоставление. Обновите данные и повторите проверку.";
}

export function ProductAutoMatching({
  scope,
  confirmation,
}: {
  scope: Scope;
  confirmation?: Confirmation;
}) {
  const client = useQueryClient();
  const local = useConfirmation();
  const activeConfirmation = confirmation || local;
  const settings = useQuery({
    queryKey: ["product-matching-settings", scope],
    queryFn: ({ signal }) =>
      api<Policy>(
        `/api/product-matching/settings?${params(scopeBody(scope))}`,
        signal,
      ),
    retry: false,
  });
  const [policy, setPolicy] = useState<Policy | null>(null);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [acknowledge, setAcknowledge] = useState(false);
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [error, setError] = useState("");
  const [review, setReview] = useState<Product | null>(null);
  const [now, setNow] = useState(Date.now());
  const lock = useRef(false);
  const controller = useRef(new AbortController());
  const runKey = useRef(crypto.randomUUID());
  useEffect(() => {
    const active = new AbortController();
    controller.current = active;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => {
      active.abort();
      clearInterval(timer);
    };
  }, []);
  const draft = policy || settings.data;
  const signal = () =>
    AbortSignal.any([controller.current.signal, AbortSignal.timeout(45000)]);
  async function prepare() {
    if (!draft || lock.current) return;
    lock.current = true;
    setBusy(true);
    setError("");
    setRun(null);
    setSelected([]);
    setAcknowledge(false);
    try {
      let current = draft;
      if (JSON.stringify(current) !== JSON.stringify(settings.data)) {
        current = await post<Policy>(
          "/api/product-matching/settings",
          { ...scopeBody(scope), policy: current },
          signal(),
        );
        if (controller.current.signal.aborted) return;
        client.setQueryData(["product-matching-settings", scope], current);
        setPolicy(current);
      }
      const value = await post<Preview>(
        "/api/product-matching/preview",
        { ...scopeBody(scope), expected_revision: current.revision },
        signal(),
      );
      if (controller.current.signal.aborted) return;
      if (
        value.scope.tenant !== scope.tenant ||
        value.scope.moysklad_account !== scope.moy ||
        value.scope.ozon_account !== scope.ozon
      )
        throw new Error("scope mismatch");
      setPreview(value);
      runKey.current = crypto.randomUUID();
      setFilter("");
    } catch (error) {
      if (!controller.current.signal.aborted) setError(failure(error));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  async function execute() {
    if (!preview || !acknowledge || !selected.length || lock.current) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const result = await post<Run>(
        "/api/product-matching/runs",
        {
          ...scopeBody(scope),
          preview_id: preview.id,
          product_ids: selected,
          acknowledge: true,
          idempotency_key: runKey.current,
        },
        signal(),
      );
      if (controller.current.signal.aborted) return;
      if (result.preview_id !== preview.id || !result.id)
        throw new Error("result mismatch");
      setRun(result);
      setUncertain(false);
      setSelected([]);
      setAcknowledge(false);
      await Promise.all([
        client.invalidateQueries({ queryKey: ["products", scope] }),
        client.invalidateQueries({ queryKey: ["diagnostics", scope] }),
      ]);
    } catch (error) {
      if (!controller.current.signal.aborted) {
        const unknown = !(error instanceof ApiError) || error.status >= 500;
        setUncertain(unknown);
        setError(
          unknown
            ? "Ответ не получен. Пары могли сохраниться. Нажмите «Проверить результат»: STYK вернёт результат того же запроса без повторного сопоставления."
            : failure(error),
        );
      }
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  const outcomes = new Map(run?.items.map((i) => [i.product_id, i]));
  const items =
    preview?.items.map((i) => outcomes.get(i.product_id) || i) || [];
  const counts = (state: string) =>
    items.filter((i) => i.status === state).length;
  const expired = !!preview && Date.parse(preview.expires_at) <= now;
  function inspect(item: MatchItem) {
    setReview({
      provider: "ozon",
      kind: "product",
      observed: true,
      shared_inventory: item.shared_inventory,
      product_id: item.product_id,
      offer_id: item.offer_id,
      name: item.name,
      sku: null,
      archived: null,
      target_id: null,
      target_kind: null,
      match_status: item.status === "conflict" ? "CONFLICT" : "NO_CANDIDATE",
      candidate_count: item.candidates.length,
      candidates_truncated: false,
      candidates: item.candidates.map((c) => ({
        kind: c.kind,
        id: c.id,
        name: c.name,
        article: c.article,
        evidence: c.evidence === "exact_sku" ? ["article"] : [],
      })),
    });
  }
  return (
    <div className="products-automatch">
      {settings.isLoading && (
        <p role="status">Загружаем настройки сопоставления…</p>
      )}
      {settings.isError && (
        <p className="catalog-error" role="alert">
          {failure(settings.error)}{" "}
          <button className="secondary" onClick={() => void settings.refetch()}>
            Повторить
          </button>
        </p>
      )}
      {!preview && draft && (
        <>
          <section className="products-match-section">
            <h2>Как искать совпадения</h2>
            <label className="products-rule">
              <input
                type="checkbox"
                checked={draft.exact_sku}
                disabled={busy}
                onChange={(e) =>
                  setPolicy({
                    ...draft,
                    exact_sku: e.target.checked,
                    auto_confirm: e.target.checked && draft.auto_confirm,
                    after_import: e.target.checked && draft.after_import,
                  })
                }
              />
              <div>
                <b>По артикулу и коду</b>
                <p>
                  Артикул Ozon совпадает с артикулом или кодом МойСклад. Регистр
                  и значащие символы сохраняются.
                </p>
              </div>
              <span className="mapping-chip ok">Точное</span>
            </label>
            <div className="products-rule">
              <input
                type="checkbox"
                disabled
                aria-label="По штрихкоду: данные пока недоступны"
              />
              <div>
                <b>По штрихкоду</b>
                <p>
                  В загруженных каталогах пока нет проверенных штрихкодов обоих
                  сервисов.
                </p>
              </div>
              <span className="products-muted">Недоступно</span>
            </div>
            <label className="products-rule">
              <input
                type="checkbox"
                checked={draft.names}
                disabled={busy}
                onChange={(e) =>
                  setPolicy({ ...draft, names: e.target.checked })
                }
              />
              <div>
                <b>По названию</b>
                <p>
                  Похожие названия покажем как подсказки для ручной проверки.
                </p>
              </div>
              <span className="mapping-chip warning">Подсказки</span>
            </label>
          </section>
          <section className="products-match-section">
            <h2>Кто подтверждает соответствия</h2>
            <label
              className={`products-rule ${!draft.auto_confirm ? "is-selected" : ""}`}
            >
              <input
                type="radio"
                name="matching-mode"
                checked={!draft.auto_confirm}
                disabled={busy}
                onChange={() =>
                  setPolicy({
                    ...draft,
                    auto_confirm: false,
                    after_import: false,
                  })
                }
              />
              <div>
                <b>Только предлагать — решение за мной</b>
                <p>Покажем кандидатов. Каждую пару можно проверить отдельно.</p>
              </div>
            </label>
            <label
              className={`products-rule ${draft.auto_confirm ? "is-selected" : ""}`}
            >
              <input
                type="radio"
                name="matching-mode"
                checked={draft.auto_confirm}
                disabled={busy || !draft.exact_sku}
                onChange={() => setPolicy({ ...draft, auto_confirm: true })}
              />
              <div>
                <b>Разрешить подтверждение уникальных точных совпадений</b>
                <p>
                  После предпросмотра выберите пары. Перед записью STYK повторно
                  проверит данные и существующие связи.
                </p>
              </div>
            </label>
            <label className="products-rule">
              <input
                type="checkbox"
                checked={draft.after_import || false}
                disabled={busy || !draft.auto_confirm}
                onChange={(e) =>
                  setPolicy({ ...draft, after_import: e.target.checked })
                }
              />
              <div>
                <b>Подтверждать точные пары после новых полных загрузок</b>
                <p>
                  Для этой пары аккаунтов. Только уникальные активные товары;
                  дубли и похожие названия требуют проверки. Действует, пока вы
                  не выключите правило.
                </p>
              </div>
            </label>
            <p className="catalog-warning">
              Дубли, варианты товара и похожие названия останутся на ручной
              проверке.
            </p>
          </section>
          <div className="products-match-footer">
            <Link className="secondary" to="/products">
              Отмена
            </Link>
            <button
              disabled={busy || (!draft.exact_sku && !draft.names)}
              onClick={() => void prepare()}
            >
              {busy
                ? "Проверяем каталоги…"
                : "Сохранить настройки и посмотреть результат"}
            </button>
          </div>
        </>
      )}
      {preview && (
        <>
          <h2>
            {run ? "Результаты сопоставления" : "Предварительный результат"}
          </h2>
          <div className="products-match-counts">
            {[
              ["exact", "Точные совпадения"],
              ["review", "Требуют проверки"],
              ["conflict", "Конфликты"],
              ["unmatched", "Без пары"],
              ["confirmed", "Связаны"],
            ].map(([state, label]) => (
              <button
                className={filter === state ? "active" : "secondary"}
                key={state}
                onClick={() => setFilter(filter === state ? "" : state)}
              >
                <b>{counts(state)}</b>
                {label}
              </button>
            ))}
          </div>
          {run && (
            <p role="status" className="catalog-success">
              Подтверждено в этом запуске: {run.confirmed}. Существующие связи
              сохранены.
            </p>
          )}
          {expired && !run && (
            <p className="catalog-warning">
              Срок предпросмотра истёк. Выполните новую проверку.
            </p>
          )}
          <div className="products-match-list">
            {items
              .filter((i) => !filter || i.status === filter)
              .map((item) => (
                <article key={item.product_id}>
                  <label className="products-select-control">
                    <input
                      type="checkbox"
                      aria-label={`Выбрать пару ${item.offer_id}`}
                      checked={selected.includes(item.product_id)}
                      disabled={
                        busy ||
                        uncertain ||
                        !!run ||
                        expired ||
                        !preview.policy.auto_confirm ||
                        item.status !== "exact" ||
                        (!selected.includes(item.product_id) &&
                          selected.length >= 100)
                      }
                      onChange={(e) => {
                        setSelected(
                          e.target.checked
                            ? [...selected, item.product_id]
                            : selected.filter((id) => id !== item.product_id),
                        );
                        setAcknowledge(false);
                      }}
                    />
                  </label>
                  <div>
                    <b>{item.name || item.offer_id}</b>
                    <small>Ozon · {item.offer_id}</small>
                  </div>
                  <div>
                    <BrandLogo provider="moysklad" decorative />
                    {item.candidates.length === 1
                      ? item.candidates[0].name
                      : item.candidates.length
                        ? `${item.candidates.length} варианта`
                        : "Не найдено"}
                  </div>
                  <span className={`products-match-reason ${item.status}`}>
                    {reasons[item.reason] || "Требуется проверка"}
                    {item.shared_inventory && (
                      <small>Общий товар МойСклад</small>
                    )}
                  </span>
                  {item.status !== "confirmed" && (
                    <button
                      className="secondary"
                      disabled={busy}
                      onClick={() => inspect(item)}
                    >
                      Проверить
                    </button>
                  )}
                </article>
              ))}
          </div>
          {!items.length && (
            <p className="catalog-empty">
              В каталогах пока нет товаров для сопоставления.
            </p>
          )}
          <p className="products-notice">
            По названию STYK только предлагает. Для автоматического
            подтверждения нужна одна безопасная точная пара.
          </p>
          {preview.policy.auto_confirm && !run && (
            <div className="products-match-footer">
              <label className="catalog-check">
                <input
                  type="checkbox"
                  checked={acknowledge}
                  disabled={!selected.length || busy || expired}
                  onChange={(e) => setAcknowledge(e.target.checked)}
                />
                Разрешаю подтвердить выбранные точные пары ({selected.length})
              </label>
              <button
                disabled={
                  !acknowledge ||
                  !selected.length ||
                  busy ||
                  (expired && !uncertain)
                }
                onClick={() => void execute()}
              >
                {busy
                  ? "Проверяем и подтверждаем…"
                  : uncertain
                    ? "Проверить результат"
                    : "Подтвердить выбранные пары"}
              </button>
            </div>
          )}
          <button
            className="secondary"
            disabled={busy || uncertain}
            onClick={() => {
              setPreview(null);
              setRun(null);
              setError("");
            }}
          >
            Вернуться к настройкам
          </button>
        </>
      )}
      {error && (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      )}
      {review && (
        <ProductReview
          key={review.product_id}
          product={review}
          scope={scope}
          confirmation={activeConfirmation}
          close={() => setReview(null)}
        />
      )}
    </div>
  );
}
