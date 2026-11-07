import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError, params, post, type Product } from "./api";
import { useConfirmation, type Confirmation, type Scope } from "./Confirmation";
import { ProductReview } from "./ProductReview";
import { Icon } from "./UI";
import { BrandLogo } from "./BrandLogo";
import { useProductMatchingJob } from "./ProductMatchingJob";
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
export type MatchingPreview = {
  after?: string;
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

function sharedNameWords(source: string, target: string): string[] {
  const tokens = (text: string) =>
    new Set(
      text
        .toLocaleLowerCase("ru")
        .replaceAll("ё", "е")
        .match(/[\p{L}\p{N}]+/gu) || [],
    );
  const sourceWords = tokens(source);
  return [...tokens(target)].filter((word) => sourceWords.has(word));
}

export function ProductAutoMatching(props: {
  scope: Scope;
  confirmation?: Confirmation;
}) {
  return (
    <ProductAutoMatchingAccount key={JSON.stringify(props.scope)} {...props} />
  );
}

function ProductAutoMatchingAccount({
  scope,
  confirmation,
}: {
  scope: Scope;
  confirmation?: Confirmation;
}) {
  const client = useQueryClient();
  const job = useProductMatchingJob(scope);
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
  const [preview, setPreview] = useState<MatchingPreview | null>(null);
  const [run, setRun] = useState<Run | null>(null);
  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [acknowledge, setAcknowledge] = useState(false);
  const [busy, setBusy] = useState(false);
  const intentKey = "styk.matching-confirmation." + JSON.stringify(scope);
  type RunIntent = {
    preview_id: string;
    product_ids: string[];
    acknowledge: true;
    idempotency_key: string;
  };
  const pendingRun = useRef<RunIntent | null>(
    (() => {
      try {
        const value = JSON.parse(localStorage.getItem(intentKey) || "null");
        return value &&
          typeof value.preview_id === "string" &&
          typeof value.idempotency_key === "string" &&
          value.acknowledge === true &&
          Array.isArray(value.product_ids) &&
          value.product_ids.every((id: unknown) => typeof id === "string")
          ? value
          : null;
      } catch {
        return null;
      }
    })(),
  );
  const [uncertain, setUncertain] = useState(!!pendingRun.current);
  const [error, setError] = useState("");
  const [review, setReview] = useState<Product | null>(null);
  const [now, setNow] = useState(Date.now());
  const lock = useRef(false);
  const controller = useRef(new AbortController());
  const runKey = useRef(
    pendingRun.current?.idempotency_key || crypto.randomUUID(),
  );
  const adoptedPreview = useRef("");
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
  const policyChanged =
    !!draft && JSON.stringify(draft) !== JSON.stringify(settings.data);
  async function savePolicy() {
    if (!draft || lock.current) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const value = await post<Policy>(
        "/api/product-matching/settings",
        { ...scopeBody(scope), policy: draft },
        signal(),
      );
      if (controller.current.signal.aborted) return;
      client.setQueryData(["product-matching-settings", scope], value);
      setPolicy(value);
      setPreview(null);
    } catch (error) {
      if (!controller.current.signal.aborted) setError(failure(error));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  async function prepare() {
    if (!draft || policyChanged || lock.current) return;
    lock.current = true;
    setBusy(true);
    setError("");
    setRun(null);
    setSelected([]);
    setAcknowledge(false);
    try {
      const current = settings.data;
      if (!current || policyChanged) return;
      await job.start(current.revision, signal());
      if (controller.current.signal.aborted) return;
      setPreview(null);
      runKey.current = crypto.randomUUID();
      setFilter("");
    } catch (error) {
      if (!controller.current.signal.aborted) setError(failure(error));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  useEffect(() => {
    const value = job.query.data?.preview;
    if (
      !value ||
      job.query.data?.state !== "COMPLETED" ||
      adoptedPreview.current === value.id
    )
      return;
    adoptedPreview.current = value.id;
    if (
      value.scope.tenant !== scope.tenant ||
      value.scope.moysklad_account !== scope.moy ||
      value.scope.ozon_account !== scope.ozon
    )
      return;
    setPreview(value);
    setRun(null);
    const pending = pendingRun.current;
    setSelected(pending?.preview_id === value.id ? pending.product_ids : []);
    setAcknowledge(pending?.preview_id === value.id);
    runKey.current = pending?.idempotency_key || crypto.randomUUID();
    setFilter("");
  }, [job.query.data?.preview, job.query.data?.state, scope]);
  async function refreshPage() {
    if (!preview || lock.current || uncertain) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const value = await post<MatchingPreview>(
        "/api/product-matching/preview",
        {
          ...scopeBody(scope),
          expected_revision: preview.policy.revision,
          after: preview.after || "",
        },
        signal(),
      );
      if (controller.current.signal.aborted) return;
      setPreview(value);
      setRun(null);
      setSelected([]);
      setAcknowledge(false);
      runKey.current = crypto.randomUUID();
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
      const intent = pendingRun.current || {
        preview_id: preview.id,
        product_ids: [...selected],
        acknowledge: true as const,
        idempotency_key: runKey.current,
      };
      pendingRun.current = intent;
      localStorage.setItem(intentKey, JSON.stringify(intent));
      const result = await post<Run>(
        "/api/product-matching/runs",
        { ...scopeBody(scope), ...intent },
        signal(),
      );
      if (controller.current.signal.aborted) return;
      if (result.preview_id !== preview.id || !result.id)
        throw new Error("result mismatch");
      setRun(result);
      pendingRun.current = null;
      localStorage.removeItem(intentKey);
      setUncertain(false);
      setSelected([]);
      setAcknowledge(false);
      await Promise.all([
        client.invalidateQueries({ queryKey: ["products", scope] }),
        client.invalidateQueries({ queryKey: ["diagnostics", scope] }),
      ]);
    } catch (error) {
      if (!controller.current.signal.aborted) {
        const unknown =
          uncertain || !(error instanceof ApiError) || error.status >= 500;
        if (!unknown) {
          pendingRun.current = null;
          localStorage.removeItem(intentKey);
        }
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
      {uncertain && !error && (
        <p role="alert">
          Сохранён запрос с неизвестным результатом. Откройте исходную страницу
          результатов и нажмите «Проверить результат», чтобы повторить тот же
          запрос.
        </p>
      )}
      {job.id && (
        <section className="products-match-job" aria-label="Обработка каталога">
          {job.query.isError ? (
            <p role="alert">
              Не удалось прочитать результат.{" "}
              <button
                className="secondary"
                onClick={() => void job.query.refetch()}
              >
                Повторить чтение
              </button>
            </p>
          ) : (
            <p role="status">
              Обработано товаров: {job.query.data?.processed ?? 0} из{" "}
              {job.query.data?.total ?? "…"}.{" "}
              {job.query.data?.state === "COMPLETED"
                ? "Проверка завершена."
                : job.query.data?.state === "STALE"
                  ? "Данные или права изменились. Запустите новую проверку."
                  : job.query.data?.state === "FAILED"
                    ? "Проверка остановлена из-за ошибки сервиса. Можно продолжить с сохранённого места."
                    : "Проверка выполняется в фоне. Страницу можно закрыть и вернуться позже."}
            </p>
          )}
          {job.query.data?.state === "FAILED" && (
            <button
              className="secondary"
              disabled={busy || uncertain}
              onClick={() => {
                setBusy(true);
                void job
                  .resume()
                  .catch((e) => setError(failure(e)))
                  .finally(() => setBusy(false));
              }}
            >
              Продолжить проверку
            </button>
          )}
          {job.query.data?.state === "COMPLETED" && (
            <p>
              Итог фоновой проверки: точных {job.query.data.summary?.exact ?? 0}
              , требуют проверки {job.query.data.summary?.review ?? 0},
              конфликтов {job.query.data.summary?.conflict ?? 0}, без пары{" "}
              {job.query.data.summary?.unmatched ?? 0}, связанных{" "}
              {job.query.data.summary?.confirmed ?? 0}.
            </p>
          )}
          <button
            className="secondary"
            disabled={busy || uncertain}
            onClick={() => {
              void job
                .clear()
                .then(() => {
                  setPreview(null);
                  setRun(null);
                  setSelected([]);
                })
                .catch((error) => setError(failure(error)));
            }}
          >
            Новая проверка
          </button>
          {job.query.data?.state === "COMPLETED" && (
            <nav aria-label="Страницы сопоставления">
              <button
                className="secondary"
                disabled={
                  !job.page || job.query.isFetching || busy || uncertain
                }
                onClick={() => job.setPage(job.page - 1)}
              >
                Предыдущие товары
              </button>
              <span>
                Страница {job.page + 1} из {job.query.data.pages}
              </span>
              <button
                className="secondary"
                disabled={
                  job.page + 1 >= job.query.data.pages ||
                  job.query.isFetching ||
                  busy ||
                  uncertain
                }
                onClick={() => job.setPage(job.page + 1)}
              >
                Следующие товары
              </button>
            </nav>
          )}
        </section>
      )}
      {!preview && !job.id && draft && (
        <>
          <section className="products-match-section">
            <h2>
              <span className="products-step-number">1</span>Как искать
              совпадения
            </h2>
            <div className="products-rule-group">
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
                <span className="products-signal-icon">
                  <Icon name="products" />
                </span>
                <div>
                  <b>По артикулу и коду</b>
                  <p>
                    Артикул Ozon совпадает с артикулом или кодом МойСклад.
                    Регистр и значащие символы сохраняются.
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
                <span className="products-signal-icon">▥</span>
                <div>
                  <b>По штрихкоду</b>
                  <p>
                    В загруженных каталогах пока нет проверенных штрихкодов
                    обоих сервисов.
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
                <span className="products-signal-icon">Aa</span>
                <div>
                  <b>По названию</b>
                  <p>
                    Похожие названия покажем как подсказки для ручной проверки.
                    Если нужного товара нет в подсказках, используйте ручной
                    поиск.
                  </p>
                </div>
                <span className="mapping-chip warning">Подсказки</span>
              </label>
            </div>
          </section>
          <section className="products-match-section">
            <h2>
              <span className="products-step-number">2</span>Кто подтверждает
              соответствия
            </h2>
            <div className="products-mode-options">
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
                  <p>
                    Покажем кандидатов. Каждую пару можно проверить отдельно.
                  </p>
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
                    После предпросмотра выберите пары. Перед записью STYK
                    повторно проверит данные и существующие связи.
                  </p>
                </div>
              </label>
            </div>
            <p className="catalog-warning">
              Дубли, варианты товара и похожие названия останутся на ручной
              проверке.
            </p>
          </section>
          <section className="products-match-section products-after-import">
            <h2>
              <span className="products-step-number">3</span>После импорта
            </h2>
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
          </section>
          {policyChanged && (
            <p role="status">
              Изменения ещё не сохранены. Сохранение применит выбранные правила,
              в том числе подтверждение после импорта.
            </p>
          )}
          <div className="products-match-footer">
            <Link className="secondary" to="/products">
              Отмена
            </Link>
            <button
              className="secondary"
              disabled={busy || !policyChanged}
              onClick={() => void savePolicy()}
            >
              Сохранить правила
            </button>
            <button
              disabled={
                busy || policyChanged || (!draft.exact_sku && !draft.names)
              }
              onClick={() => void prepare()}
            >
              {busy
                ? "Проверяем каталоги…"
                : "Посмотреть предварительный результат"}
            </button>
          </div>
        </>
      )}
      {preview && (
        <>
          <button
            className="secondary"
            disabled={busy || uncertain}
            onClick={() => void refreshPage()}
          >
            Перепроверить эту страницу
          </button>
          <h2>
            {run ? "Результаты сопоставления" : "Предварительный результат"}
          </h2>
          <p>Ниже — результаты для {items.length} товаров текущей страницы.</p>
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
          <div className="products-match-selection">
            <button
              className="secondary"
              disabled={
                busy ||
                uncertain ||
                !!run ||
                expired ||
                !preview.policy.auto_confirm ||
                !items.some((item) => item.status === "exact")
              }
              onClick={() => {
                setSelected(
                  items
                    .filter((item) => item.status === "exact")
                    .map((item) => item.product_id),
                );
                setAcknowledge(false);
              }}
            >
              Выбрать точные пары на странице ({counts("exact")})
            </button>
            <span>Выбрано пар: {selected.length}</span>
          </div>
          <div className="products-match-table-heading">
            <span />
            <b>Товар Ozon</b>
            <b>Предложено в МойСклад</b>
            <b>Основание</b>
            <b>Решение</b>
          </div>
          {filter && !items.some((item) => item.status === filter) && (
            <p role="status">
              В этой группе товаров нет. Выберите другую группу или нажмите
              выбранный фильтр ещё раз, чтобы показать все результаты.
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
                        ? `Показано вариантов: ${item.candidates.length}`
                        : "Не найдено"}
                  </div>
                  <span className={`products-match-reason ${item.status}`}>
                    {reasons[item.reason] || "Требуется проверка"}
                    {item.candidates.some(
                      (c) => c.evidence === "similar_name",
                    ) && (
                      <small>
                        {item.candidates
                          .filter((c) => c.evidence === "similar_name")
                          .map(
                            (c) =>
                              `${c.name}: ${Math.round(c.score * 100)}% совпадения слов; общие слова: ${sharedNameWords(item.name, c.name).join(", ") || "—"}`,
                          )
                          .join("; ")}
                        . Название — только подсказка, проверьте модель и
                        комплектацию.
                      </small>
                    )}
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
