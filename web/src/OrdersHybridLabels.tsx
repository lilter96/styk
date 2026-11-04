import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { useLabelExpiryClock } from "./useLabelExpiryClock";
import {
  ordersURL,
  sameOrderScope,
  orderDate,
  orderRequestError,
  type OrderScope,
  type OrdersOperationPage,
} from "./OrdersTypes";

export function OrdersHybridLabels({
  scope,
  postingNumber,
  value,
  printed,
  disabled,
  onSelect,
  onPrinted,
}: {
  scope: OrderScope;
  postingNumber: string;
  value: string;
  printed: boolean;
  disabled: boolean;
  onSelect: (id: string) => void;
  onPrinted: (printed: boolean) => void;
}) {
  const [cursors, setCursors] = useState([""]);
  const cursor = cursors[cursors.length - 1];
  const history = useQuery({
    queryKey: ["orders-label-selection", scope, postingNumber, cursor],
    queryFn: async ({ signal }) => {
      const page = await api<OrdersOperationPage>(
        ordersURL("operations", scope, {
          search: postingNumber,
          limit: "25",
          cursor,
        }),
        signal,
      );
      if (page.operations.some((op) => !sameOrderScope(op.scope, scope)))
        throw new Error("label account mismatch");
      return page;
    },
    retry: false,
    refetchOnWindowFocus: false,
  });
  const labelNow = useLabelExpiryClock(history.data);
  const labels = useMemo(
    () =>
      (history.data?.operations ?? [])
        .flatMap((op) => op.targets)
        .filter(
          (work) =>
            work.command.action === "posting_label_create" &&
            work.command.posting_number === postingNumber &&
            work.state === "RECONCILED" &&
            work.receipt.artifact?.available &&
            !!work.receipt.artifact.expires_at &&
            Date.parse(work.receipt.artifact.expires_at) > labelNow,
        ),
    [history.data, postingNumber, labelNow],
  );
  useEffect(() => {
    if (!value || history.isLoading) return;
    const selected = labels.find((work) => work.id === value);
    const expires = selected?.receipt.artifact?.expires_at;
    if (history.isError || !expires || Date.parse(expires) <= Date.now()) {
      onSelect("");
      return;
    }
  }, [
    value,
    history.data,
    history.isLoading,
    history.isError,
    labels,
    onSelect,
  ]);
  return (
    <fieldset disabled={disabled}>
      <legend>Этикетка для передачи в ПВЗ</legend>
      <p className="orders-note">
        Выберите полученную этикетку этого отправления и подтвердите её
        фактическую печать. Скачивание файла само по себе не подтверждает
        печать.
      </p>
      {history.isLoading && <p role="status">Ищем готовые этикетки…</p>}
      {history.isError && (
        <p role="alert">{orderRequestError(history.error)}</p>
      )}
      <label>
        Подтверждённая этикетка
        <select
          value={value}
          onChange={(event) => onSelect(event.target.value)}
          disabled={history.isFetching || history.isError}
        >
          <option value="">Выберите этикетку</option>
          {labels.map((work) => (
            <option key={work.id} value={work.id}>
              {work.receipt.artifact?.label_type === "small_label"
                ? "Малая"
                : "Большая"}{" "}
              · до {orderDate(work.receipt.artifact?.expires_at)} · {work.id}
            </option>
          ))}
        </select>
      </label>
      {!history.isLoading && !history.isError && !labels.length && (
        <p className="orders-warning">
          На этой странице нет действующей этикетки. Сначала получите её через
          действие «Создать этикетку» или проверьте другие страницы истории.
        </p>
      )}
      <div className="orders-actions">
        <button
          type="button"
          className="secondary"
          disabled={history.isFetching || cursors.length === 1}
          onClick={() => {
            setCursors(cursors.slice(0, -1));
            onSelect("");
          }}
        >
          Назад по истории
        </button>
        <button
          type="button"
          className="secondary"
          disabled={history.isFetching || !history.data?.next_cursor}
          onClick={() => {
            setCursors([...cursors, history.data!.next_cursor!]);
            onSelect("");
          }}
        >
          Следующие этикетки
        </button>
        <button
          type="button"
          className="secondary"
          disabled={history.isFetching}
          onClick={() => void history.refetch()}
        >
          Обновить этикетки
        </button>
      </div>
      <label className="orders-check">
        <input
          type="checkbox"
          checked={printed}
          disabled={!value || history.isFetching}
          onChange={(event) => onPrinted(event.target.checked)}
        />
        Этикетка распечатана и наклеена на это отправление.
      </label>
    </fieldset>
  );
}
