import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  prepareBulkImport,
  runBulkImport,
  type BulkResult,
} from "./CatalogBulkRequests";
import { type BulkPlan } from "./CatalogBulkPlan";
import {
  productName,
  type CatalogConfig,
  type Scope,
} from "./CatalogSyncTypes";

export function CatalogBulkImport({
  scope,
  config,
  disabled,
  onSaved,
  onBusy,
}: {
  scope: Scope;
  config: CatalogConfig;
  disabled: boolean;
  onSaved: (config: CatalogConfig) => void;
  onBusy: (busy: boolean) => void;
}) {
  const client = useQueryClient();
  const [plan, setPlan] = useState<BulkPlan | null>(null);
  const [phase, setPhase] = useState<
    "idle" | "preparing" | "review" | "running" | "done"
  >("idle");
  const [acknowledge, setAcknowledge] = useState(false);
  const [count, setCount] = useState(0);
  const [result, setResult] = useState<BulkResult | null>(null);
  const [error, setError] = useState("");
  const lock = useRef(false);
  const controller = useRef(new AbortController());
  useEffect(() => {
    const active = new AbortController();
    controller.current = active;
    return () => active.abort();
  }, []);
  async function prepare() {
    if (lock.current || disabled) return;
    lock.current = true;
    setPhase("preparing");
    setError("");
    setAcknowledge(false);
    setResult(null);
    try {
      setPlan(
        await prepareBulkImport(scope, config, controller.current.signal),
      );
      setPhase("review");
    } catch {
      setError(
        "Не удалось проверить весь каталог. Обновите список и повторите проверку.",
      );
      setPhase("idle");
    } finally {
      lock.current = false;
    }
  }
  async function start() {
    if (lock.current || !plan?.pending.length || !acknowledge || disabled)
      return;
    lock.current = true;
    setPhase("running");
    setCount(0);
    setError("");
    onBusy(true);
    try {
      setResult(
        await runBulkImport(
          scope,
          config,
          plan,
          controller.current.signal,
          onSaved,
          setCount,
        ),
      );
      setPhase("done");
    } catch {
      setError(
        "Импорт остановлен. Часть запросов могла сохраниться: проверьте результаты ниже перед новым запуском.",
      );
      setPhase("done");
    } finally {
      await client.invalidateQueries({
        queryKey: ["catalog-operations", scope],
      });
      lock.current = false;
      onBusy(false);
    }
  }
  return (
    <section
      className="catalog-section catalog-bulk"
      aria-labelledby="catalog-bulk-title"
    >
      <div className="catalog-bulk-heading">
        <div>
          <h2 id="catalog-bulk-title">Импорт товаров из Ozon</h2>
          <p>Перенесите весь каталог в МойСклад за один запуск.</p>
        </div>
        {phase !== "running" && (
          <button
            disabled={
              disabled ||
              phase === "preparing" ||
              config.publication_available === false
            }
            onClick={() => void prepare()}
          >
            {phase === "preparing"
              ? "Проверяем весь каталог…"
              : "Импортировать все товары"}
          </button>
        )}
      </div>
      <p>
        Будут созданы новые товары с названием, доступным описанием и артикулом
        продавца. Связанные и уже созданные карточки пропускаются. Архивные
        карточки будут созданы в архиве МойСклад.
      </p>
      {plan && phase !== "idle" && phase !== "preparing" && (
        <>
          <p>
            Новых: <strong>{plan.pending.length}</strong> · Уже перенесено или
            связано: <strong>{plan.existing}</strong> · В архиве:{" "}
            <strong>{plan.archived}</strong>
            {plan.unresolved > 0 && (
              <>
                {" "}
                · Ожидают сверки: <strong>{plan.unresolved}</strong>
              </>
            )}
          </p>
          {phase === "review" && (
            <>
              {plan.configRevision !== config.revision && (
                <p className="catalog-warning">
                  Настройки изменились. Проверьте весь каталог заново перед
                  импортом.
                </p>
              )}
              <details>
                <summary>Товары для импорта ({plan.products.length})</summary>
                <ul>
                  {plan.products.map((product) => (
                    <li key={product.identity.id}>
                      {productName(product)} —{" "}
                      {product.identity.offer_id || product.identity.id}
                    </li>
                  ))}
                </ul>
              </details>
              {plan.pending.length > 0 ? (
                <>
                  <label className="catalog-check">
                    <input
                      type="checkbox"
                      checked={acknowledge}
                      onChange={(event) => setAcknowledge(event.target.checked)}
                    />
                    Разрешаю создать все перечисленные товары в выбранном
                    аккаунте МойСклад
                  </label>
                  <div>
                    <button
                      disabled={
                        !acknowledge ||
                        disabled ||
                        plan.configRevision !== config.revision
                      }
                      onClick={() => void start()}
                    >
                      Начать импорт ({plan.pending.length})
                    </button>
                  </div>
                </>
              ) : (
                <p role="status">Новых товаров для импорта нет.</p>
              )}
            </>
          )}
          {phase === "running" && (
            <div role="status">
              <p>
                Импортируем товары: {count} из {plan.pending.length}. Отправку и
                сверку выполняет STYK.
              </p>
              <progress
                aria-label="Ход импорта товаров"
                max={plan.pending.length}
                value={count}
              />
            </div>
          )}
        </>
      )}
      {result && (
        <p role="status">
          Передано на отправку: {result.queued}. Результаты сверки показаны
          ниже.
          {result.failed.length > 0 && (
            <> Не прошли проверку: {result.failed.length}.</>
          )}
          {result.uncertain && (
            <>
              {" "}
              Результат последнего подтверждения неизвестен. Сначала проверьте
              историю; повторная отправка не выполнялась.
            </>
          )}
        </p>
      )}
      {error && (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      )}
    </section>
  );
}
