import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError, api } from "./api";
import { OrdersExemplarEditor } from "./OrdersExemplarEditor";
import type { ExemplarDraft, ExemplarPublic } from "./OrdersExemplarTypes";
import {
  orderDate,
  ordersURL,
  sameOrderScope,
  orderRequestError,
  type OrderScope,
} from "./OrdersTypes";

export function validExemplarSummary(
  data: ExemplarPublic,
  scope: OrderScope,
  posting: string,
) {
  if (
    !sameOrderScope(data.scope, scope) ||
    data.posting_number !== posting ||
    !data.work_id ||
    !data.target_fingerprint ||
    !data.products?.length
  )
    return false;
  const ids = new Set<string>();
  return data.products.every(
    (product) =>
      /^[1-9]\d*$/.test(product.sku) &&
      Number.isSafeInteger(product.quantity) &&
      product.quantity > 0 &&
      product.quantity <= 1000 &&
      product.ids?.length === product.quantity &&
      product.ids.every((id) => {
        if (!/^[1-9]\d*$/.test(id) || ids.has(id)) return false;
        ids.add(id);
        return true;
      }),
  );
}

export function OrdersExemplars({
  scope,
  postingNumber,
  sourceRevision,
  mode,
  disabled,
  onChange,
}: {
  scope: OrderScope;
  postingNumber: string;
  sourceRevision: string;
  mode: "edit" | "proof";
  disabled: boolean;
  onChange: (draft: ExemplarDraft | undefined) => void;
}) {
  const [tick, setTick] = useState(Date.now);
  const previous = useRef<{ initialized: boolean; valid: boolean }>({
    initialized: false,
    valid: false,
  });
  const summary = useQuery({
    queryKey: ["orders-exemplars", scope, postingNumber, sourceRevision],
    queryFn: async ({ signal }) => {
      try {
        const data = await api<ExemplarPublic>(
          ordersURL("exemplars", scope, { posting_number: postingNumber }),
          signal,
        );
        if (!validExemplarSummary(data, scope, postingNumber))
          throw new Error("exemplar target mismatch");
        return data;
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) return null;
        throw error;
      }
    },
    retry: false,
    refetchOnWindowFocus: false,
  });
  const data = summary.data;
  const until = mode === "proof" ? data?.proof_valid_until : data?.expires_at;
  const deadline = until ? Date.parse(until) : NaN;
  const unsupported =
    mode === "edit" &&
    (data?.products.some((product) => !!product.unsupported_fields?.length) ??
      false);
  const valid =
    !unsupported &&
    !summary.isFetching &&
    !summary.isError &&
    Number.isFinite(deadline) &&
    deadline > Date.now() &&
    (mode === "edit" || !!data?.proof_work_id);
  useEffect(() => {
    if (!Number.isFinite(deadline) || deadline <= Date.now()) return;
    const timer = window.setTimeout(
      () => setTick(Date.now()),
      Math.min(deadline - Date.now() + 1, 2147483647),
    );
    return () => window.clearTimeout(timer);
  }, [deadline, tick]);
  useEffect(() => {
    if (!previous.current.initialized || (previous.current.valid && !valid))
      onChange(undefined);
    previous.current = { initialized: true, valid };
  }, [valid, onChange]);
  return (
    <section aria-label="Экземпляры товаров">
      {unsupported && (
        <p className="orders-warning">
          Ozon уже хранит сведения, которые этот редактор не может безопасно
          сохранить:{" "}
          {data?.products
            .flatMap((product) => product.unsupported_fields ?? [])
            .map(
              (field) =>
                ({
                  weight: "вес",
                  multi_box_qty: "число коробок",
                  multi_box_qty_unknown: "неизвестное число коробок",
                })[field] ?? field,
            )
            .join(", ")}
          . Передача из этого редактора заблокирована, чтобы не стереть данные.
        </p>
      )}
      {summary.isFetching && <p role="status">Проверяем экземпляры товаров…</p>}
      {summary.isError && (
        <p role="alert">{orderRequestError(summary.error)}</p>
      )}
      {!summary.isFetching && !summary.isError && !valid && (
        <p className="orders-warning">
          {mode === "edit"
            ? "Сначала выполните действие «Подготовить экземпляры в Ozon» и дождитесь сверки. Затем обновите сведения здесь."
            : "Нет действующей проверки экземпляров. Выполните подготовку или передачу реальных сведений и дождитесь сверки. Проверка действует пять минут; истёкшую проверку можно обновить отдельной подготовкой."}
        </p>
      )}
      {valid && data && (
        <>
          <p className="orders-note">
            {mode === "proof"
              ? "Проверка комплектации"
              : "Сведения об экземплярах"}{" "}
            действуют до {orderDate(until)}. Перед отправкой сервер повторно
            сверит данные с Ozon.
          </p>
          {mode === "proof" ? (
            <label className="orders-check">
              <input
                type="checkbox"
                disabled={disabled}
                onChange={(event) =>
                  onChange(
                    event.target.checked
                      ? { workID: data.proof_work_id!, validUntil: until! }
                      : undefined,
                  )
                }
              />
              Использовать эту проверку экземпляров для выбранной сборки.
            </label>
          ) : (
            <OrdersExemplarEditor
              key={`${data.work_id}:${data.target_fingerprint}:${summary.dataUpdatedAt}`}
              data={data}
              disabled={disabled}
              onChange={(input) =>
                onChange(
                  input
                    ? { workID: data.work_id, input, validUntil: until! }
                    : undefined,
                )
              }
            />
          )}
        </>
      )}
      <button
        className="secondary"
        type="button"
        disabled={disabled || summary.isFetching}
        onClick={() => {
          onChange(undefined);
          void summary.refetch();
        }}
      >
        Обновить экземпляры
      </button>
    </section>
  );
}
