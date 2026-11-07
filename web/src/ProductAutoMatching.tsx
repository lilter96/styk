import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ApiError, type Product } from "./api";
import { useConfirmation, type Confirmation, type Scope } from "./Confirmation";
import { ProductReview } from "./ProductReview";
import { BrandLogo } from "./BrandLogo";
import { useProductMatchingJob } from "./ProductMatchingJob";
import { ProductMatchingAutomation } from "./ProductMatchingAutomation";
import { matchingCriteria, type MatchItem } from "./ProductMatchingTypes";

function failure(error: unknown) {
  if (error instanceof ApiError) {
    if (error.status === 409)
      return "Каталоги изменились или результаты поиска устарели. Обновите поиск совпадений.";
    if (error.status === 403)
      return "Проверьте права доступа к этой паре аккаунтов.";
    if (error.status === 404)
      return "Этот поиск недоступен. Запустите новый поиск совпадений.";
  }
  return "Не удалось получить ответ сервера. Повторите действие.";
}
const reasons: Record<string, string> = {
  duplicate_sku: "Несколько товаров с таким артикулом или кодом",
  duplicate_offer: "Повторяющийся артикул Ozon",
  mapping_conflict: "Есть конфликтующая связь",
  archived: "Товар в архиве",
  lifecycle_unknown: "Нужно обновить данные товара",
  variant_requires_review: "Вариант товара требует проверки",
  no_match: "Совпадение не найдено",
};
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
  const [error, setError] = useState("");
  const [review, setReview] = useState<Product | null>(null);
  const [retrying, setRetrying] = useState(false);
  const lock = useRef(false);
  const controller = useRef(new AbortController());
  useEffect(() => {
    const c = new AbortController();
    controller.current = c;
    return () => c.abort();
  }, []);
  const data =
    job.query.data?.criterion === job.criterion ? job.query.data : undefined;
  const applying = !!data && ["QUEUED", "RUNNING"].includes(data.apply.state);
  const done = data?.apply.state === "COMPLETED";
  const locked = job.submitting || job.uncertain || applying || retrying;
  const scanning =
    job.starting || !data || ["QUEUED", "RUNNING"].includes(data.state);
  const exact = data?.summary?.exact || 0;
  const confirmed = data?.apply.confirmed || 0;
  const skipped = data?.apply.skipped || 0;
  const exceptions = data
    ? Math.max(
        0,
        data.total - (data.summary.exact || 0) - (data.summary.confirmed || 0),
      )
    : 0;
  const signal = () =>
    AbortSignal.any([controller.current.signal, AbortSignal.timeout(45000)]);
  async function search() {
    if (lock.current || locked) return;
    lock.current = true;
    setError("");
    try {
      await job.start(signal());
    } catch (e) {
      if (!controller.current.signal.aborted) setError(failure(e));
    } finally {
      lock.current = false;
    }
  }
  useEffect(() => {
    if (job.id && !job.query.data) return;
    if (data || job.query.isError) return;
    const timer = setTimeout(() => void search(), 200);
    return () => clearTimeout(timer);
    // Search is one explicit criterion choice; it never saves a permanent rule.
  }, [job.criterion, job.id, job.query.data?.criterion]);
  useEffect(() => {
    if (data && data.apply.state !== "NOT_STARTED") setError("");
    if (data && ["COMPLETED", "STALE", "FAILED", "CANCELLED"].includes(data.apply.state)) {
      void client.invalidateQueries({ queryKey: ["products", scope] });
      void client.invalidateQueries({ queryKey: ["diagnostics", scope] });
    }
  }, [data?.apply.state, client, scope]);
  async function apply() {
    if (lock.current) return;
    lock.current = true;
    setError("");
    try {
      await job.apply(signal());
    } catch (e) {
      if (!controller.current.signal.aborted)
        setError(
          e instanceof ApiError && e.status < 500
            ? failure(e)
            : "Ответ о запуске не получен. Проверьте запуск тем же запросом — повторного сопоставления не будет.",
        );
    } finally {
      lock.current = false;
    }
  }
  async function resume() {
    if (lock.current) return;
    lock.current = true;
    setRetrying(true);
    setError("");
    try {
      await job.resume();
    } catch (e) {
      setError(failure(e));
    } finally {
      lock.current = false;
      setRetrying(false);
    }
  }
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
    <div className="products-automatch automatic-matching">
      <section
        className="automatch-panel"
        aria-label="Массовое сопоставление товаров"
      >
        <fieldset className="automatch-criteria" disabled={locked}>
          <legend>С чем сравнивать артикул Ozon</legend>
          <p>
            Выберите критерий. STYK покажет примеры и найдёт все точные пары в
            каталогах.
          </p>
          <select
            className="automatch-criterion-mobile"
            aria-label="Критерий сопоставления"
            value={job.criterion}
            onChange={(e) =>
              job.setCriterion(e.target.value as typeof job.criterion)
            }
          >
            {matchingCriteria.map((c) => (
              <option key={c.id} value={c.id}>
                {c.title}
              </option>
            ))}
          </select>
          <div className="automatch-criteria-options">
            {matchingCriteria.map((c) => (
              <label
                key={c.id}
                className={job.criterion === c.id ? "is-selected" : ""}
              >
                <input
                  type="radio"
                  name="matching-criterion"
                  value={c.id}
                  checked={job.criterion === c.id}
                  onChange={() => job.setCriterion(c.id)}
                />
                <span>
                  <b>{c.title}</b>
                  <small>{c.description}</small>
                </span>
              </label>
            ))}
          </div>
        </fieldset>
        <div className="automatch-result" aria-busy={scanning || applying}>
          {job.query.isError ? (
            <div className="automatch-feedback" role="alert">
              <h2>Не удалось загрузить результат</h2>
              <p>Сохранённый запуск продолжает существовать на сервере.</p>
              <button
                className="secondary"
                onClick={() => void job.query.refetch()}
              >
                Повторить загрузку результата
              </button>
            </div>
          ) : scanning ? (
            <div className="automatch-feedback" role="status">
              <h2>Ищем точные совпадения</h2>
              <p>
                {data
                  ? `Проверено товаров: ${data.processed} из ${data.total}`
                  : "Проверяем выбранные каталоги…"}
              </p>
              <progress
                aria-label="Поиск совпадений"
                value={data?.processed}
                max={data?.total || 1}
              />
            </div>
          ) : data?.state === "FAILED" || data?.state === "STALE" ? (
            <div className="automatch-feedback" role="status">
              <h2>Поиск приостановлен</h2>
              <p>
                {data.state === "STALE"
                  ? "Каталоги или права доступа изменились. Выполните новый поиск."
                  : "Сервис временно недоступен. Повторите поиск."}
              </p>
              <button
                className="secondary"
                disabled={locked}
                onClick={() =>
                  void (search())
                }
              >
                Обновить поиск
              </button>
            </div>
          ) : (
            data && (
              <>
                <div className="automatch-result-heading">
                  <div>
                    <h2>
                      {done
                        ? `Сопоставлено товаров: ${confirmed}`
                        : applying
                          ? "Сопоставляем товары"
                          : exact
                            ? `Найдено точных пар: ${exact}`
                            : "Точных совпадений пока нет"}
                    </h2>
                    <p>
                      {done
                        ? "Готово. Связи сохранены в STYK."
                        : "Результат для всего каталога, а не одной страницы."}
                    </p>
                  </div>
                  {!applying && !job.submitting && (
                    <button
                      className="secondary"
                      disabled={locked}
                      onClick={() => void search()}
                    >
                      Обновить поиск
                    </button>
                  )}
                </div>
                {(applying ||
                  done ||
                  data.apply.state === "FAILED" ||
                  ["STALE", "CANCELLED"].includes(data.apply.state)) && (
                  <div className="automatch-application" role="status">
                    <p>
                      Сопоставлено: <b>{confirmed}</b>
                      {skipped > 0 && (
                        <>
                          {" "}
                          · Пропущено после повторной проверки: <b>{skipped}</b>
                        </>
                      )}
                    </p>
                    {applying && (
                      <>
                        <progress
                          aria-label="Сопоставление всех товаров"
                          value={confirmed + skipped}
                          max={data.apply.total || 1}
                        />
                        <p>
                          Обработано пар: {confirmed + skipped} из{" "}
                          {data.apply.total}. Можно закрыть страницу — запуск
                          продолжится.
                        </p>
                      </>
                    )}
                    {["FAILED", "STALE", "CANCELLED"].includes(data.apply.state) && (
                      <>
                        <p>
                          Сохранённые связи остаются.{" "}
                          {data.apply.state === "FAILED"
                            ? "Работу можно продолжить с места остановки."
                            : "Каталоги или права изменились. Обновите поиск для оставшихся товаров."}
                        </p>
                        <button
                          className="secondary"
                          disabled={locked}
                          onClick={() =>
                            void (data.apply.state === "FAILED"
                              ? resume()
                              : search())
                          }
                        >
                          {data.apply.state === "FAILED"
                            ? "Продолжить сопоставление"
                            : "Обновить поиск оставшихся товаров"}
                        </button>
                      </>
                    )}
                  </div>
                )}
                {data.examples.length > 0 && (
                  <section
                    className="automatch-examples"
                    aria-label="Примеры найденных совпадений"
                  >
                    <h3>Примеры совпадений</h3>
                    <div className="automatch-example-head">
                      <span>Товар Ozon</span>
                      <span />
                      <span>Товар МойСклад</span>
                    </div>
                    {data.examples.map((item) => (
                      <article key={item.product_id}>
                        <div>
                          <BrandLogo provider="ozon" decorative />
                          <span>
                            <strong title={item.name}>
                              {item.name || item.offer_id}
                            </strong>
                            <small>{item.offer_id}</small>
                          </span>
                        </div>
                        <span
                          className="automatch-pair-arrow"
                          aria-hidden="true"
                        >
                          ↔
                        </span>
                        <div>
                          <BrandLogo provider="moysklad" decorative />
                          <span>
                            <strong title={item.candidates[0]?.name}>
                              {item.candidates[0]?.name ||
                                item.candidates[0]?.id}
                            </strong>
                            <small>
                              {item.candidates[0]?.article ||
                                "Точное совпадение"}
                            </small>
                          </span>
                        </div>
                      </article>
                    ))}
                  </section>
                )}
                <div className="automatch-primary-action">
                  <p>
                    Сопоставятся только однозначные активные товары.
                    Существующие связи сохранятся.
                  </p>
                  {done ? (
                    <Link className="primary-button" to="/products">
                      Перейти к товарам
                    </Link>
                  ) : (
                    data.apply.state === "NOT_STARTED" && (
                      <button
                        disabled={
                          job.submitting ||
                          job.query.isFetching ||
                          (!exact && !job.uncertain)
                        }
                        onClick={() => void apply()}
                      >
                        {job.submitting
                          ? "Запускаем сопоставление…"
                          : job.uncertain
                            ? "Проверить запуск сопоставления"
                            : `Сопоставить все товары (${exact})`}
                      </button>
                    )
                  )}
                </div>
                <details className="automatch-exceptions">
                  <summary>
                    Остальные товары: {exceptions} · Уже связаны:{" "}
                    {data.summary.confirmed || 0}
                  </summary>
                  <div>
                    <p>
                      Неоднозначные совпадения, архивные товары и варианты не
                      сопоставляются автоматически. Попробуйте другой критерий
                      или проверьте оставшиеся товары вручную.
                    </p>
                    {data.attention.map((item) => (
                      <article key={item.product_id}>
                        <span>
                          <strong>{item.name || item.offer_id}</strong>
                          <small>
                            {reasons[item.reason] || "Нужна ручная проверка"}
                          </small>
                        </span>
                        <button
                          className="secondary"
                          disabled={locked}
                          onClick={() => inspect(item)}
                        >
                          Проверить
                        </button>
                      </article>
                    ))}
                    <Link to="/products">Открыть список товаров</Link>
                  </div>
                </details>
              </>
            )
          )}
          {error && (
            <div className="automatch-error" role="alert">
              <p>{error}</p>
              {job.uncertain ? (
                <button
                  className="secondary"
                  disabled={job.submitting}
                  onClick={() => void apply()}
                >
                  Проверить запуск сопоставления
                </button>
              ) : (
                <button
                  className="secondary"
                  disabled={job.starting}
                  onClick={() => void search()}
                >
                  Повторить поиск
                </button>
              )}
            </div>
          )}
        </div>
        <footer className="automatch-panel-footer">
          Сопоставление связывает товары в STYK. Само по себе оно не отправляет
          данные в Ozon или МойСклад.
        </footer>
      </section>
      <ProductMatchingAutomation scope={scope} />
      {review && (
        <ProductReview
          product={review}
          scope={scope}
          confirmation={confirmation || local}
          close={() => setReview(null)}
        />
      )}
    </div>
  );
}
