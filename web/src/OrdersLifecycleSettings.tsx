import type { OrdersConfigData, OrdersMetadata } from "./OrdersTypes";
import {
  emptyLifecycle,
  lifecycleActions,
  type MerchantCommandMapping,
  type OrdersLifecyclePolicy,
} from "./OrdersLifecycleTypes";

type Props = {
  draft: OrdersConfigData;
  metadata?: OrdersMetadata;
  disabled: boolean;
  consent: boolean;
  consentRequired: boolean;
  onConsent: (value: boolean) => void;
  onChange: (value: OrdersConfigData) => void;
};

export function OrdersLifecycleSettings({
  draft,
  metadata,
  disabled,
  consent,
  consentRequired,
  onConsent,
  onChange,
}: Props) {
  const policy = draft.lifecycle ?? emptyLifecycle();
  const change = (next: Partial<OrdersLifecyclePolicy>) =>
    onChange({ ...draft, lifecycle: { ...policy, ...next } });
  const update = (index: number, next: Partial<MerchantCommandMapping>) =>
    change({
      mappings: policy.mappings.map((item, i) =>
        i === index ? { ...item, ...next } : item,
      ),
    });
  return (
    <>
      <h2 id="settings-lifecycle">Обработка в МойСклад</h2>
      <p>
        Сотрудник собирает заказ и меняет его состояние в МойСклад. STYK
        проверяет реальный переход и выполняет только разрешённое действие.
        Повторно открывать STYK для каждой сборки не требуется.
      </p>
      <p className="settings-info">
        В этом режиме после создания заказа его рабочим состоянием управляет
        продавец в МойСклад. Статус доставки Ozon показывается отдельно и не
        перезаписывает это состояние.
      </p>
      <label className="orders-check">
        <input
          type="checkbox"
          checked={policy.enabled}
          disabled={
            disabled ||
            (!policy.enabled && draft.publication_available !== true)
          }
          onChange={(event) => change({ enabled: event.target.checked })}
        />
        <span>
          <strong>Выполнять команды из МойСклад автоматически</strong>
          <small>Отдельное разрешение для выбранной пары аккаунтов</small>
        </span>
      </label>
      {draft.publication_available !== true && (
        <p className="settings-info">
          Проверьте подключения и права доступа. Действующее разрешение можно
          отключить даже при недоступном API.
        </p>
      )}
      {!policy.enabled && (
        <p className="settings-info">
          Команды из МойСклад выключены. Постоянное разрешение на передачу
          заказов само по себе их не включает.
        </p>
      )}
      {policy.enabled && (
        <fieldset className="settings-lifecycle-fields" disabled={disabled}>
          <legend>Разрешённые переходы продавца</legend>
          <p>
            Первое наблюдение состояния не запускает команду. Нужны изменение из
            выбранного предыдущего состояния и свежие данные. Для rFBS STYK
            дополнительно проверяет, кто управляет доставкой: продавец или
            перевозчик.
          </p>
          {policy.mappings.map((mapping, index) => (
            <fieldset className="settings-lifecycle-rule" key={index}>
              <legend>Переход {index + 1}</legend>
              <div className="settings-lifecycle-grid">
                <label>
                  Схема отправления {index + 1}
                  <select
                    value={mapping.scheme}
                    onChange={(event) => {
                      const scheme = event.target
                        .value as MerchantCommandMapping["scheme"];
                      const valid = lifecycleActions.find(
                        (item) => item.action === mapping.action,
                      )?.schemes as readonly string[] | undefined;
                      update(
                        index,
                        valid?.includes(scheme)
                          ? { scheme }
                          : {
                              scheme,
                              action: "posting_pack",
                              event_kind: "assembly_ready",
                            },
                      );
                    }}
                  >
                    <option value="FBS">FBS</option>
                    <option value="rFBS">rFBS</option>
                  </select>
                </label>
                <label>
                  Действие перехода {index + 1}
                  <select
                    value={mapping.action}
                    onChange={(event) => {
                      const selected = lifecycleActions.find(
                        (item) => item.action === event.target.value,
                      );
                      if (selected)
                        update(index, {
                          action: selected.action,
                          event_kind: selected.event,
                        });
                    }}
                  >
                    {lifecycleActions
                      .filter((item) =>
                        (item.schemes as readonly string[]).includes(
                          mapping.scheme,
                        ),
                      )
                      .map((item) => (
                        <option value={item.action} key={item.action}>
                          {item.label}
                        </option>
                      ))}
                  </select>
                </label>
                <label>
                  Новое состояние МойСклад {index + 1}
                  <select
                    value={mapping.state_id}
                    disabled={!metadata}
                    onChange={(event) =>
                      update(index, {
                        state_id: event.target.value,
                        from_state_ids: mapping.from_state_ids.filter(
                          (id) => id !== event.target.value,
                        ),
                      })
                    }
                  >
                    <option value="">Выберите состояние</option>
                    {mapping.state_id &&
                      !metadata?.states.some(
                        (item) => item.id === mapping.state_id,
                      ) && (
                        <option value={mapping.state_id}>
                          Сохранённое состояние недоступно
                        </option>
                      )}
                    {metadata?.states.map((item) => (
                      <option value={item.id} key={item.id}>
                        {item.name}
                      </option>
                    ))}
                  </select>
                </label>
                <details
                  className="settings-lifecycle-predecessors"
                  open={!mapping.from_state_ids.length || undefined}
                >
                  <summary>
                    Предыдущие состояния: {mapping.from_state_ids.length}
                  </summary>
                  <div
                    role="group"
                    aria-label={`Допустимые предыдущие состояния ${index + 1}`}
                  >
                    {!metadata && (
                      <p>
                        Для выбора состояний загрузите справочники МойСклад.
                      </p>
                    )}
                    {metadata?.states
                      .filter((item) => item.id !== mapping.state_id)
                      .map((item) => (
                        <label className="orders-check" key={item.id}>
                          <input
                            type="checkbox"
                            aria-label={`Из состояния «${item.name}», переход ${index + 1}`}
                            checked={mapping.from_state_ids.includes(item.id)}
                            onChange={(event) =>
                              update(index, {
                                from_state_ids: event.target.checked
                                  ? [...mapping.from_state_ids, item.id]
                                  : mapping.from_state_ids.filter(
                                      (id) => id !== item.id,
                                    ),
                              })
                            }
                          />
                          {item.name}
                        </label>
                      ))}
                  </div>
                </details>
              </div>
              {mapping.state_id && (
                <p className="settings-lifecycle-selected">
                  Состояние команды продавца:{" "}
                  <strong>
                    {metadata?.states.find(
                      (item) => item.id === mapping.state_id,
                    )?.name ?? "Сохранённое состояние недоступно"}
                  </strong>
                </p>
              )}
              <p className="settings-info">
                {mapping.action === "demand_create" ||
                mapping.action === "posting_ship"
                  ? "Этот переход подтверждает фактическую передачу груза перевозчику или в пункт передачи. Готовность к отгрузке не является передачей."
                  : mapping.action === "salesreturn_create"
                    ? "Этот переход подтверждает фактическое получение возвращённого товара на складе. Отмена или отказ покупателя не доказывают возврат."
                    : mapping.action === "posting_pack"
                      ? "Сборка требует полного состава упаковок. Если Ozon требует страну, экземпляры или маркировку, отсутствующие данные нужно предоставить перед выполнением."
                      : "Подтверждайте только фактически наступивший этап. Интегрированному перевозчику принадлежат его статусы доставки."}
              </p>
              <label className="orders-check">
                <input
                  type="checkbox"
                  checked={mapping.whole_posting}
                  onChange={(event) =>
                    update(index, { whole_posting: event.target.checked })
                  }
                />
                Событие относится ко всем позициям отправления
              </label>
              <button
                type="button"
                className="text-action"
                aria-label={`Удалить переход ${index + 1}`}
                onClick={() =>
                  change({
                    mappings: policy.mappings.filter((_, i) => i !== index),
                  })
                }
              >
                Удалить переход
              </button>
            </fieldset>
          ))}
          <button
            type="button"
            className="secondary"
            disabled={policy.mappings.length >= 30}
            onClick={() =>
              change({
                mappings: [
                  ...policy.mappings,
                  {
                    state_id: "",
                    from_state_ids: [],
                    action: "posting_pack",
                    scheme: "FBS",
                    event_kind: "assembly_ready",
                    whole_posting: false,
                  },
                ],
              })
            }
          >
            Добавить переход МойСклад
          </button>
          <fieldset>
            <legend>Складские документы</legend>
            <label className="orders-check">
              <input
                type="checkbox"
                checked={policy.auto_demand}
                onChange={(event) =>
                  change({ auto_demand: event.target.checked })
                }
              />
              Разрешить автоматические проведённые Отгрузки
            </label>
            <label>
              Момент создания Отгрузки
              <select
                value={policy.demand_moment}
                onChange={(event) =>
                  change({
                    demand_moment: event.target
                      .value as OrdersLifecyclePolicy["demand_moment"],
                  })
                }
              >
                <option value="ON_HANDOFF_CONFIRMED">
                  После подтверждённой физической передачи
                </option>
                <option value="ON_OZON_ACCEPTANCE_CONFIRMED" disabled>
                  После приёмки Ozon — пока недоступно
                </option>
              </select>
            </label>
            <p>
              Проверенное событие приёмки Ozon сейчас не получено; для rFBS
              такая приёмка может быть неприменима. Используйте подтверждённую
              физическую передачу или ручную проверку.
            </p>
            <label className="orders-check">
              <input
                type="checkbox"
                checked={policy.auto_return}
                onChange={(event) =>
                  change({ auto_return: event.target.checked })
                }
              />
              Разрешить автоматические проведённые Возвраты покупателя
            </label>
            <p>
              Для каждого документа задайте собственный переход выше. Нужны
              проведённый заказ, точные связанные позиции и лимиты из раздела
              «Дополнительно». Возврат создаётся по подтверждённой Отгрузке и не
              разрешает повторное списание.
            </p>
          </fieldset>
          {consentRequired && (
            <label className="orders-check orders-warning">
              <input
                type="checkbox"
                checked={consent}
                onChange={(event) => onConsent(event.target.checked)}
              />
              Разрешаю выбранные команды Ozon и складские документы по указанным
              переходам МойСклад для этой пары аккаунтов, пока не отключу
              разрешение. Физические события сотрудники подтверждают только
              после их наступления.
            </label>
          )}
        </fieldset>
      )}
    </>
  );
}
