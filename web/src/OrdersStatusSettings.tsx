import { Icon } from "./UI";
import {
  postingStatusLabels,
  type OrdersConfigData,
  type OrdersMetadata,
} from "./OrdersTypes";

type Props = {
  draft: OrdersConfigData;
  metadata?: OrdersMetadata;
  disabled: boolean;
  onChange: (next: OrdersConfigData) => void;
};
export function OrdersStatusSettings({
  draft,
  metadata,
  disabled,
  onChange,
}: Props) {
  return (
    <>
      <div className="settings-status-head">
        <span>Статус Ozon</span>
        <span>Состояние МойСклад</span>
      </div>
      {!draft.status_mappings.length && (
        <p>
          Соответствий пока нет. Для новых заказов используется выбранный статус
          из раздела «Общие».
        </p>
      )}
      {draft.status_mappings.map((mapping, index) => (
        <div className="settings-status-row" key={index}>
          <div>
            <label>
              <span className="sr-only">Статус Ozon {index + 1}</span>
              <select
                disabled={disabled}
                value={mapping.status}
                onChange={(event) =>
                  onChange({
                    ...draft,
                    status_mappings: draft.status_mappings.map((item, i) =>
                      i === index
                        ? { ...item, status: event.target.value }
                        : item,
                    ),
                  })
                }
              >
                <option value="">Выберите статус</option>
                {mapping.status && !postingStatusLabels[mapping.status] && (
                  <option value={mapping.status}>{mapping.status}</option>
                )}
                {Object.entries(postingStatusLabels).map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
            </label>
            <details
              className="settings-substatus"
              open={mapping.substatus.presence === "value" || undefined}
            >
              <summary>Подстатус (необязательно)</summary>
              <label>
                <span className="sr-only">Подстатус Ozon {index + 1}</span>
                <input
                  value={mapping.substatus.value ?? ""}
                  disabled={disabled}
                  onChange={(event) =>
                    onChange({
                      ...draft,
                      status_mappings: draft.status_mappings.map((item, i) =>
                        i === index
                          ? {
                              ...item,
                              substatus: event.target.value
                                ? {
                                    presence: "value",
                                    value: event.target.value,
                                  }
                                : { presence: "missing" },
                            }
                          : item,
                      ),
                    })
                  }
                />
              </label>
            </details>
          </div>
          <Icon name="arrow" />
          <label>
            <span className="sr-only">
              Состояние МойСклад для статуса {index + 1}
            </span>
            <select
              disabled={disabled || !metadata}
              value={mapping.state_id}
              onChange={(event) =>
                onChange({
                  ...draft,
                  status_mappings: draft.status_mappings.map((item, i) =>
                    i === index
                      ? { ...item, state_id: event.target.value }
                      : item,
                  ),
                })
              }
            >
              <option value="">Выберите состояние</option>
              {mapping.state_id &&
                !metadata?.states.some(
                  (state) => state.id === mapping.state_id,
                ) && (
                  <option value={mapping.state_id}>
                    Сохранённое состояние недоступно
                  </option>
                )}
              {metadata?.states.map((state) => (
                <option key={state.id} value={state.id}>
                  {state.name}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            className="text-action settings-remove"
            disabled={disabled}
            aria-label={`Удалить соответствие статуса ${mapping.status || index + 1}`}
            onClick={() =>
              onChange({
                ...draft,
                status_mappings: draft.status_mappings.filter(
                  (_, i) => i !== index,
                ),
              })
            }
          >
            <Icon name="close" />
          </button>
        </div>
      ))}
      <button
        type="button"
        className="secondary"
        disabled={disabled}
        onClick={() =>
          onChange({
            ...draft,
            status_mappings: [
              ...draft.status_mappings,
              { status: "", substatus: { presence: "missing" }, state_id: "" },
            ],
          })
        }
      >
        <Icon name="plus" />
        Добавить соответствие
      </button>
    </>
  );
}
