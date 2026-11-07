import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError, post } from "./api";
import { BrandLogo } from "./BrandLogo";
import { IntegrationDialog } from "./IntegrationDialog";
import { newCatalogCreation } from "./CatalogSyncCreation";
import { ProductCreationEditor } from "./ProductCreationEditor";
import { CatalogSyncPreview, canApprove } from "./CatalogSyncPreview";
import {
  catalogURL,
  scopeBody,
  productName,
  requestError,
  normalizePreview,
  issueLabel,
  type CatalogProduct,
  type CatalogPreview,
  type CatalogOperation,
  type ProductPage,
  type Provider,
  type Scope,
} from "./CatalogSyncTypes";

import {
  useProductImportDraft,
  importChunks,
  combinedImportPreview,
  type ImportSelection as Selection,
  type ImportIntent,
  type ImportDraftState,
} from "./ProductImportDraft";
const identityKey = (p: CatalogProduct) =>
  `${p.identity.entity_type}:${p.identity.id}`;
const linked = (p: CatalogProduct, data?: ProductPage) =>
  data?.enrollments?.some((e) =>
    [e.source, e.target].some(
      (i) =>
        i.account.provider === p.identity.account.provider &&
        i.id === p.identity.id &&
        (!i.entity_type || i.entity_type === p.identity.entity_type),
    ),
  );

export function ProductImport({
  scope,
  provider,
}: {
  scope: Scope;
  provider: Provider;
}) {
  const client = useQueryClient();
  const draft = useProductImportDraft(scope, provider);
  const [plans, setPlans] = useState<CatalogPreview[]>([]);
  const [intents, setIntents] = useState<ImportIntent[]>([]);
  const [progress, setProgress] = useState("");
  const [draftError, setDraftError] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [readySelection, setReadySelection] = useState<string[]>([]);
  const [reviewPage, setReviewPage] = useState(0);
  const [statusFilter, setStatusFilter] = useState("");
  const [search, setSearch] = useState("");
  const [cursor, setCursor] = useState("");
  const [pages, setPages] = useState<string[]>([]);
  const [selected, setSelected] = useState<Record<string, Selection>>({});
  const [editing, setEditing] = useState<string | null>(null);
  const checkedCount = plans.reduce((n, p) => n + p.decisions.length, 0);
  const preview =
    checkedCount === Object.keys(selected).length || intents.length
      ? combinedImportPreview(plans)
      : null;
  const setPreview = (value: CatalogPreview | null) => {
    setPlans(value ? [value] : []);
  };
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [operation, setOperation] = useState<CatalogOperation | null>(null);
  const [uncertain, setUncertain] = useState(false);
  const controller = useRef(new AbortController());
  const lock = useRef(false);

  useEffect(() => {
    const active = new AbortController();
    controller.current = active;
    return () => active.abort();
  }, []);
  const products = useQuery({
    queryKey: ["catalog-products", scope, provider, search, cursor],
    queryFn: ({ signal }) =>
      api<ProductPage>(
        catalogURL("products", scope, {
          provider,
          search,
          cursor,
          limit: "20",
        }),
        signal,
      ),
    retry: false,
  });
  function add(product: CatalogProduct): Selection {
    const enrollment = newCatalogCreation(product, scope);
    if (provider === "ozon" && product.identity.offer_id)
      enrollment.create!.values.article = {
        state: "known",
        data: product.identity.offer_id,
      };
    return { product, enrollment };
  }
  function toggle(product: CatalogProduct) {
    setPreview(null);
    const key = identityKey(product);
    setSelected((previous) => {
      const next = { ...previous };
      if (next[key]) delete next[key];
      else next[key] = add(product);
      return next;
    });
  }
  const snapshot = (
    nextPlans = plans,
    nextIntents = intents,
  ): ImportDraftState => ({ selected, plans: nextPlans, intents: nextIntents });
  async function saveDraft() {
    setDraftError("");
    try {
      await draft.save(snapshot());
    } catch (error) {
      setDraftError(
        error instanceof Error
          ? error.message
          : "Не удалось сохранить черновик.",
      );
    }
  }
  function restoreDraft() {
    const value = draft.query.data?.state;
    if (!value) return;
    setSelected(value.selected || {});
    setPlans(value.plans || []);
    setIntents(value.intents || []);
    const recovering = !!value.intents?.some(
      (i) => !i.operation && !i.rejected,
    );
    setUncertain(recovering);
    setConfirming(recovering);
    if (
      value.intents?.length &&
      value.intents.every((i) => i.operation || i.rejected)
    )
      setOperation(value.intents.find((i) => i.operation)?.operation || null);
    setError(
      recovering
        ? "Восстановлен незавершённый импорт. Продолжите теми же запросами: повторно поставленные операции не создадут дубликаты."
        : "",
    );
  }
  async function prepare() {
    if (
      lock.current ||
      !Object.keys(selected).length ||
      uncertain ||
      draft.saving
    )
      return;
    lock.current = true;
    setBusy(true);
    setError("");
    setPlans([]);
    const chunks = importChunks(
      Object.values(selected).sort((a, b) =>
        identityKey(a.product) < identityKey(b.product)
          ? -1
          : identityKey(a.product) > identityKey(b.product)
            ? 1
            : 0,
      ),
    );
    const resumable = plans.every(
      (p, index) =>
        Date.parse(p.expires_at) > Date.now() &&
        JSON.stringify(p.decisions.map((d) => d.source.identity.id).sort()) ===
          JSON.stringify(
            chunks[index]?.map((s) => s.product.identity.id).sort(),
          ),
    );
    const prepared: CatalogPreview[] = resumable ? [...plans] : [];
    try {
      await draft.save(snapshot(prepared, []));
      for (const [index, chunk] of chunks.entries()) {
        if (index < prepared.length) continue;
        setProgress(`Проверка пачки ${index + 1} из ${chunks.length}`);
        const result = normalizePreview(
          await post<CatalogPreview>(
            "/api/catalog-sync/import-preview",
            { ...scopeBody(scope), targets: chunk.map((s) => s.enrollment) },
            AbortSignal.any([
              controller.current.signal,
              AbortSignal.timeout(120000),
            ]),
          ),
        );
        if (controller.current.signal.aborted) return;
        if (
          result.scope.tenant !== scope.tenant ||
          result.scope.moysklad_account !== scope.moy ||
          result.scope.ozon_account !== scope.ozon
        )
          throw new Error("Ответ относится к другой паре аккаунтов.");
        if (
          prepared.length &&
          (result.config_revision !== prepared[0].config_revision ||
            JSON.stringify(result.credentials) !==
              JSON.stringify(prepared[0].credentials))
        )
          throw new Error(
            "Настройки изменились во время проверки. Выполните новую проверку.",
          );
        prepared.push(result);
        setPlans([...prepared]);
        await draft.save(snapshot(prepared, []));
      }
      setPlans(prepared);
      setIntents([]);
      setConfirming(false);
      setReadySelection([]);
      await client.invalidateQueries({ queryKey: ["catalog-config", scope] });
    } catch (error) {
      if (!controller.current.signal.aborted)
        setError(error instanceof Error ? error.message : requestError(error));
    } finally {
      lock.current = false;
      setBusy(false);
      setProgress("");
    }
  }
  async function submitBatches(manifest: ImportIntent[]) {
    // Persist the exact keys and selections before admitting any provider work.
    await draft.save(snapshot(plans, manifest));
    setIntents([...manifest]);
    for (const [index, intent] of manifest.entries()) {
      if (intent.operation || intent.rejected) continue;
      const recovering = !!intent.attempted;
      intent.attempted = true;
      await draft.save(snapshot(plans, manifest));
      setProgress(`Подтверждение пачки ${index + 1} из ${manifest.length}`);
      let result: CatalogOperation;
      try {
        result = await post<CatalogOperation>(
          "/api/catalog-sync/approve",
          {
            ...scopeBody(scope),
            preview_id: intent.preview_id,
            enrollment_ids: intent.enrollment_ids,
            acknowledge: true,
            idempotency_key: intent.idempotency_key,
          },
          AbortSignal.any([
            controller.current.signal,
            AbortSignal.timeout(45000),
          ]),
        );
      } catch (error) {
        if (
          !recovering &&
          error instanceof ApiError &&
          [400, 403, 404, 409, 410, 422].includes(error.status)
        ) {
          intent.rejected = requestError(error);
          setIntents([...manifest]);
          await draft.save(snapshot(plans, manifest));
          continue;
        }
        throw error;
      }
      if (controller.current.signal.aborted) return;
      if (
        !result.id ||
        result.scope.tenant !== scope.tenant ||
        result.scope.moysklad_account !== scope.moy ||
        result.scope.ozon_account !== scope.ozon
      )
        throw new Error("Ответ не соответствует исходной паре аккаунтов.");
      intent.operation = result;
      setIntents([...manifest]);
      await draft.save(snapshot(plans, manifest));
    }
    setUncertain(false);
    setOperation(manifest.find((i) => i.operation)?.operation || null);
    setPlans([]);
    if (manifest.some((i) => i.rejected))
      setError(
        "Часть пачек отклонена до постановки в очередь. Исправьте данные оставшихся товаров и проверьте их заново.",
      );
    await client.invalidateQueries({ queryKey: ["catalog-operations", scope] });
    await client.invalidateQueries({ queryKey: ["catalog-products", scope] });
  }
  async function approve(ids: string[]) {
    if (lock.current || !preview || uncertain) return;
    const chosen = new Set(ids);
    const manifest = plans
      .map((p) => ({
        preview_id: p.id,
        enrollment_ids: p.decisions
          .filter((d) => chosen.has(d.enrollment_id))
          .map((d) => d.enrollment_id),
        idempotency_key: crypto.randomUUID(),
      }))
      .filter((i) => i.enrollment_ids.length);
    await runBatches(manifest);
  }
  async function runBatches(manifest: ImportIntent[]) {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      await submitBatches(manifest);
    } catch (error) {
      if (!controller.current.signal.aborted) {
        const unknown = manifest.some(
          (i) => i.attempted && !i.operation && !i.rejected,
        );
        setUncertain(unknown);
        setIntents([...manifest]);
        setError(
          (unknown
            ? "Обработка пачек остановлена. Операция могла сохраниться. Уже подтверждённые операции сохраняются. Повторите те же запросы или проверьте историю. "
            : "Не удалось сохранить план импорта. Перед продолжением повторите сохранение черновика. ") +
            (error instanceof ApiError
              ? requestError(error)
              : error instanceof Error
                ? error.message
                : ""),
        );
      }
    } finally {
      lock.current = false;
      setBusy(false);
      setProgress("");
    }
  }
  const items = products.data?.products || [];
  const eligible = items.filter(
    (p) => !linked(p, products.data) && p.lifecycle === "active" && !p.variant,
  );
  const decisions = new Map(
    preview?.decisions.map((d) => [d.source.identity.id, d]),
  );
  const reviewProducts = Object.values(selected)
    .map((s) => s.product)
    .filter(
      (p) =>
        !statusFilter ||
        (statusFilter === "ready"
          ? decisions.get(p.identity.id)?.ready
          : !decisions.get(p.identity.id)?.ready),
    );
  const edit = editing ? selected[editing] : undefined;
  return (
    <div className="catalog-sync products-import-flow">
      <ol className="products-steps">
        <li aria-current={!preview && !operation ? "step" : undefined}>
          Выбор товаров
        </li>
        <li aria-current={preview ? "step" : undefined}>Проверка данных</li>
        <li aria-current={operation ? "step" : undefined}>
          Подтверждение и сверка
        </li>
      </ol>
      <p className="products-notice">
        Одноразовый импорт создаёт выбранные карточки. Правила и разрешения
        автоматической передачи сохраняются.
      </p>
      <section className="products-draft-bar" aria-label="Черновик импорта">
        <button
          className="secondary"
          disabled={busy || draft.saving || draft.blocked || !draft.query.data}
          onClick={() => void saveDraft()}
        >
          {draft.saving ? "Сохраняем черновик…" : "Сохранить черновик"}
        </button>
        {!!Object.keys(draft.query.data?.state.selected || {}).length &&
          !Object.keys(selected).length && (
            <button
              className="secondary"
              disabled={busy || draft.saving}
              onClick={restoreDraft}
            >
              Восстановить черновик (
              {Object.keys(draft.query.data!.state.selected!).length})
            </button>
          )}
        {draft.query.isLoading && (
          <span role="status">Загружаем черновик…</span>
        )}
        {draft.query.isError && (
          <button
            className="secondary"
            onClick={() => void draft.query.refetch()}
          >
            Повторить загрузку черновика
          </button>
        )}
        {draft.message && <p role="status">{draft.message}</p>}
        {draftError && (
          <p role="alert">
            {draftError}{" "}
            <button
              className="secondary"
              onClick={() =>
                void draft
                  .retry()
                  .catch((e) =>
                    setDraftError(
                      e instanceof Error ? e.message : "Сохранение недоступно",
                    ),
                  )
              }
            >
              Повторить сохранение
            </button>
          </p>
        )}
      </section>
      {checkedCount > 0 && !preview && (
        <p>
          Проверено и сохранено товаров: {checkedCount} из{" "}
          {Object.keys(selected).length}. При продолжении актуальные пачки будут
          использованы повторно.
        </p>
      )}
      {progress && <p role="status">{progress}</p>}
      {intents.length > 0 && (
        <p>
          Пачек поставлено в очередь:{" "}
          {intents.filter((i) => i.operation).length} из {intents.length}. Это
          ещё не подтверждение создания товаров в сервисе.
        </p>
      )}
      {operation ? (
        <section className="catalog-section">
          <h2>Запрос поставлен в обработку</h2>
          <p>
            Товаров в очереди:{" "}
            {intents.reduce(
              (sum, i) => sum + (i.operation?.targets.length || 0),
              0,
            )}
            . Создание будет подтверждено после сверки с сервисом.
          </p>
          {intents.some((i) => i.rejected) && (
            <ul>
              {intents
                .filter((i) => i.rejected)
                .map((i) => (
                  <li key={i.idempotency_key}>
                    Не поставлено в очередь: {i.enrollment_ids.length} товаров.{" "}
                    {i.rejected}
                  </li>
                ))}
            </ul>
          )}
          <ul>
            {intents
              .filter((i) => i.operation)
              .map((i) => (
                <li key={i.idempotency_key}>
                  <Link to={`/products/history?operation=${i.operation!.id}`}>
                    Операция {i.operation!.id}
                  </Link>{" "}
                  · {i.operation!.targets.length} товаров
                </li>
              ))}
          </ul>
          <button
            className="secondary"
            onClick={() => {
              setOperation(null);
              const accepted = new Set(
                intents
                  .filter((i) => i.operation)
                  .flatMap((i) => i.enrollment_ids),
              );
              const completed = new Set(
                (draft.query.data?.state.plans || [])
                  .flatMap((p) => p.decisions)
                  .filter((d) => accepted.has(d.enrollment_id))
                  .map((d) => identityKey(d.source)),
              );
              setSelected((value) =>
                Object.fromEntries(
                  Object.entries(value).filter(([key]) => !completed.has(key)),
                ),
              );
              setIntents([]);
              setPlans([]);
              setUncertain(false);
            }}
          >
            {intents.some((i) => i.rejected)
              ? "Исправить оставшиеся товары"
              : "Новый импорт"}
          </button>
          <Link className="primary-button" to="/products/history">
            Посмотреть результат
          </Link>
        </section>
      ) : (
        <>
          {!confirming && (
            <section className="catalog-section products-import-catalog">
              <div className="catalog-section-heading">
                <h2>
                  {preview
                    ? "Готовность карточек к созданию"
                    : "Выберите товары для создания"}
                </h2>
                <span>Большой выбор будет обработан пачками до 20 товаров</span>
              </div>
              {preview && (
                <div className="products-import-preflight-counts">
                  <div className="ready">
                    <b>{preview.decisions.filter((d) => d.ready).length}</b>
                    готовы к созданию
                  </div>
                  <div className="warning">
                    <b>{preview.decisions.filter((d) => !d.ready).length}</b>
                    требуют данных
                  </div>
                  <div>
                    <b>{products.data?.summary?.linked ?? "—"}</b>уже связаны
                  </div>
                  <div>
                    <b>{products.data?.summary?.excluded ?? "—"}</b>исключены
                  </div>
                </div>
              )}
              {products.data?.summary && !preview && (
                <div className="products-import-summary">
                  <p>
                    Найдено: <b>{products.data.summary.total}</b> · К проверке:{" "}
                    <b>{products.data.summary.to_review}</b> · Уже связаны:{" "}
                    <b>{products.data.summary.linked}</b> · Исключены:{" "}
                    <b>{products.data.summary.excluded}</b>
                  </p>
                  <small>
                    {products.data.summary.observed_at
                      ? `Полная загрузка: ${new Date(products.data.summary.observed_at).toLocaleString("ru-RU")}`
                      : "Загруженного каталога пока нет"}
                    . Готовность к созданию проверим перед отправкой.
                  </small>
                </div>
              )}
              <label className="products-import-search">
                Поиск по названию или артикулу
                <input
                  type="search"
                  value={search}
                  onChange={(e) => {
                    setSearch(e.target.value);
                    setCursor("");
                    setPages([]);
                  }}
                />
              </label>
              {products.isFetching && <p role="status">Загружаем товары…</p>}
              {products.isError && (
                <p role="alert" className="catalog-error">
                  {requestError(products.error)}{" "}
                  <button
                    className="secondary"
                    onClick={() => void products.refetch()}
                  >
                    Повторить
                  </button>
                </p>
              )}
              <div className="products-import-selection">
                <button
                  className="secondary"
                  disabled={
                    busy || draft.saving || !eligible.length || uncertain
                  }
                  onClick={() => {
                    setPreview(null);
                    setSelected({
                      ...selected,
                      ...Object.fromEntries(
                        eligible
                          .filter((p) => !selected[identityKey(p)])

                          .map((p) => [identityKey(p), add(p)]),
                      ),
                    });
                  }}
                >
                  Выбрать доступные на странице
                </button>
                <span>Выбрано: {Object.keys(selected).length}</span>
                {preview && (
                  <button
                    className="secondary"
                    disabled={busy || uncertain || draft.saving}
                    onClick={() =>
                      setReadySelection(
                        preview.decisions
                          .filter(canApprove)
                          .map((d) => d.enrollment_id),
                      )
                    }
                  >
                    Выбрать все готовые (
                    {preview.decisions.filter(canApprove).length})
                  </button>
                )}
              </div>
              {preview && (
                <div className="products-filter-pills">
                  {[
                    ["", "Все"],
                    ["ready", "Готовы"],
                    ["blocked", "Нужны данные"],
                  ].map(([value, label]) => (
                    <button
                      key={value}
                      className={
                        statusFilter === value ? "active" : "secondary"
                      }
                      onClick={() => {
                        setStatusFilter(value);
                        setReviewPage(0);
                      }}
                    >
                      {label}
                    </button>
                  ))}
                </div>
              )}
              <div className="products-import-table-heading">
                <span />
                <b>Товар в {provider === "ozon" ? "Ozon" : "МойСклад"}</b>
                <b>
                  Готовность карточки{" "}
                  {provider === "ozon" ? "МойСклад" : "Ozon"}
                </b>
                <b>Действие</b>
              </div>
              <div className="products-import-list">
                {(preview
                  ? reviewProducts.slice(reviewPage * 20, (reviewPage + 1) * 20)
                  : items
                )
                  .filter(
                    (p) =>
                      !preview ||
                      !statusFilter ||
                      (statusFilter === "ready"
                        ? decisions.get(p.identity.id)?.ready
                        : !decisions.get(p.identity.id)?.ready),
                  )
                  .map((product) => {
                    const key = identityKey(product),
                      paired = linked(product, products.data),
                      chosen = selected[key],
                      decision = decisions.get(product.identity.id);
                    return (
                      <article
                        key={key}
                        className={chosen ? "is-selected" : ""}
                      >
                        <label className="products-select-control">
                          <input
                            type="checkbox"
                            aria-label={`Выбрать ${productName(product)}`}
                            checked={
                              preview
                                ? !!decision &&
                                  readySelection.includes(
                                    decision.enrollment_id,
                                  )
                                : !!chosen
                            }
                            disabled={
                              busy ||
                              draft.saving ||
                              uncertain ||
                              (preview ? !decision?.ready : paired) ||
                              product.lifecycle !== "active" ||
                              product.variant
                            }
                            onChange={() => {
                              if (preview && decision) {
                                setReadySelection(
                                  readySelection.includes(
                                    decision.enrollment_id,
                                  )
                                    ? readySelection.filter(
                                        (id) => id !== decision.enrollment_id,
                                      )
                                    : [
                                        ...readySelection,
                                        decision.enrollment_id,
                                      ],
                                );
                                return;
                              }
                              toggle(product);
                            }}
                          />
                        </label>
                        <BrandLogo provider={provider} decorative />
                        <div>
                          <strong>{productName(product)}</strong>
                          <small>
                            {product.identity.offer_id ||
                              (product.fields.article?.state === "known"
                                ? String(product.fields.article.data)
                                : "Артикул не указан")}
                            {product.variant ? " · Вариант товара" : ""}
                          </small>
                        </div>
                        <span
                          className={`products-import-state ${paired ? "linked" : ""} ${decision?.ready ? "is-ready" : ""}`}
                        >
                          {decision
                            ? decision.ready
                              ? "Все поля проверены"
                              : decision.validation
                                  .map((i) => issueLabel(i.code))
                                  .join(" · ") || "Нужно уточнить данные"
                            : paired
                              ? "Уже связан"
                              : product.variant
                                ? "Модификация: требуется отдельная проверка"
                                : product.lifecycle === "unknown"
                                  ? "Активность неизвестна: обновите каталог"
                                  : product.lifecycle === "archived"
                                    ? "В архиве"
                                    : provider === "moysklad"
                                      ? "Нужно проверить поля Ozon"
                                      : "Нужна проверка перед созданием"}
                        </span>
                        {(chosen ||
                          (!paired &&
                            product.lifecycle === "active" &&
                            !product.variant)) && (
                          <button
                            className="secondary"
                            disabled={busy || uncertain || draft.saving}
                            onClick={() => {
                              if (!chosen)
                                setSelected({
                                  ...selected,
                                  [key]: add(product),
                                });
                              setEditing(key);
                            }}
                          >
                            Заполнить данные
                          </button>
                        )}
                      </article>
                    );
                  })}
              </div>
              {!products.isFetching && !products.isError && !items.length && (
                <p className="catalog-empty">
                  Товары не найдены. Измените поиск или обновите каталог в
                  интеграциях.
                </p>
              )}
              {preview ? (
                <nav
                  className="products-batch-pagination"
                  aria-label="Страницы проверки импорта"
                >
                  <button
                    className="secondary"
                    disabled={!reviewPage || busy}
                    onClick={() => setReviewPage(reviewPage - 1)}
                  >
                    Назад
                  </button>
                  <span>
                    Страница {reviewPage + 1} из{" "}
                    {Math.max(1, Math.ceil(reviewProducts.length / 20))}
                  </span>
                  <button
                    className="secondary"
                    disabled={
                      (reviewPage + 1) * 20 >= reviewProducts.length || busy
                    }
                    onClick={() => setReviewPage(reviewPage + 1)}
                  >
                    Далее
                  </button>
                </nav>
              ) : (
                <div className="catalog-actions">
                  <button
                    className="secondary"
                    disabled={!pages.length || products.isFetching}
                    onClick={() => {
                      setCursor(pages.at(-1) || "");
                      setPages(pages.slice(0, -1));
                    }}
                  >
                    Назад
                  </button>
                  <span>Страница {pages.length + 1}</span>
                  <button
                    className="secondary"
                    disabled={
                      !products.data?.next_cursor || products.isFetching
                    }
                    onClick={() => {
                      setPages([...pages, cursor]);
                      setCursor(products.data!.next_cursor!);
                    }}
                  >
                    Далее
                  </button>
                </div>
              )}
              <div className="products-import-footer">
                <p>
                  Выбор сохранится на сервере. Проверка прочитает актуальные
                  данные и требования сервиса. Отправки ещё не будет.
                </p>
                <button
                  disabled={
                    busy ||
                    uncertain ||
                    (preview
                      ? !readySelection.length
                      : !Object.keys(selected).length)
                  }
                  onClick={() =>
                    preview ? setConfirming(true) : void prepare()
                  }
                >
                  {busy
                    ? "Проверяем…"
                    : preview
                      ? `Продолжить с ${readySelection.length} товарами →`
                      : `Проверить выбранные (${Object.keys(selected).length})`}
                </button>
              </div>
            </section>
          )}
          {preview && confirming && (
            <CatalogSyncPreview
              key={preview.id}
              initialSelected={readySelection}
              preview={preview}
              busy={busy}
              blocked={uncertain}
              onApprove={(ids) => void approve(ids)}
            />
          )}
        </>
      )}
      {preview && (
        <button
          className="secondary products-import-back"
          disabled={busy || uncertain || draft.saving}
          onClick={() => {
            if (confirming) setConfirming(false);
            else setPreview(null);
          }}
        >
          ←{" "}
          {confirming ? "Вернуться к готовности товаров" : "Вернуться к выбору"}
        </button>
      )}
      {error && (
        <p role="alert" className="catalog-error">
          {error}{" "}
          {uncertain && (
            <>
              <button
                className="secondary"
                disabled={busy || draft.blocked}
                onClick={() => void runBatches(intents)}
              >
                Продолжить теми же запросами
              </button>
              <Link to="/products/history">История отправок</Link>
            </>
          )}
        </p>
      )}
      {edit && editing && (
        <IntegrationDialog
          title={productName(edit.product)}
          className="products-create-dialog"
          onClose={() => setEditing(null)}
        >
          <ProductCreationEditor
            scope={scope}
            facts={edit.facts}
            onClose={() => setEditing(null)}
            onSave={saveDraft}
            saving={draft.saving || draft.blocked}
            saveMessage={draftError || draft.message}
            enrollment={edit.enrollment}
            onChange={(enrollment) => {
              setSelected({ ...selected, [editing]: { ...edit, enrollment } });
              setPreview(null);
            }}
            onFacts={(facts) => {
              const create = edit.enrollment.create!;
              const values = { ...create.values };
              delete values.category;
              delete values.attributes;
              setSelected({
                ...selected,
                [editing]: {
                  ...edit,
                  facts,
                  enrollment: {
                    ...edit.enrollment,
                    create: {
                      ...create,
                      values: { ...values, ...facts.values },
                    },
                  },
                },
              });
              setPreview(null);
            }}
          />
        </IntegrationDialog>
      )}
    </div>
  );
}
