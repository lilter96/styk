import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  emptyCommissionPolicy,
  type CommissionMetadata,
  type CommissionPolicy,
} from "./OrdersFinanceTypes";
import {
  ordersURL,
  sameOrderScope,
  orderRequestError,
  type OrderScope,
  type OrdersConfigData,
  type OrdersMetadata,
  type OrderField,
} from "./OrdersTypes";

type Props = {
  scope: OrderScope;
  draft: OrdersConfigData;
  metadata?: OrdersMetadata;
  disabled: boolean;
  consent: boolean;
  consentRequired: boolean;
  onConsent: (value: boolean) => void;
  onChange: (value: OrdersConfigData) => void;
};
export function OrdersCommissionSettings({
  scope,
  draft,
  metadata,
  disabled,
  consent,
  consentRequired,
  onConsent,
  onChange,
}: Props) {
  const policy = draft.finance ?? emptyCommissionPolicy();
  const change = (patch: Partial<CommissionPolicy>) =>
    onChange({ ...draft, finance: { ...policy, ...patch } });
  const contracts = useQuery({
    queryKey: ["orders-finance-metadata", scope],
    enabled: false,
    retry: false,
    queryFn: async ({ signal }) => {
      const value = await api<CommissionMetadata>(
        ordersURL("finance/metadata", scope),
        signal,
      );
      if (
        !sameOrderScope(value.scope, scope) ||
        !Array.isArray(value.contracts)
      )
        throw new Error("commission metadata scope mismatch");
      return value;
    },
    refetchOnWindowFocus: false,
  });
  const options = (contracts.data?.contracts ?? []).filter(
    (item) =>
      item.archived === false &&
      item.contract_type === "Commission" &&
      item.reward_type === "None" &&
      item.organization_id === policy.organization_id &&
      item.agent_id === policy.agent_id &&
      item.currency_id === policy.currency_id,
  );
  const boolValue = (field: OrderField<boolean>) =>
    ["value", "zero"].includes(field.presence) &&
    typeof field.value === "boolean"
      ? String(field.value)
      : "";
  const boolField = (value: string): OrderField<boolean> =>
    value === ""
      ? { presence: "missing" }
      : { presence: "value", value: value === "true" };
  return (
    <details
      className="settings-commission-policy"
      id="commission-policy"
      open={policy.enabled || undefined}
    >
      <summary>Черновики отчётов комиссионера</summary>
      <p>
        Отдельный финансовый процесс по месячному отчёту Ozon. Создаётся только
        непроведённый черновик МойСклад. Он не подтверждает выплату, физическую
        отгрузку или возврат. Если включена передача заказов, организация,
        контрагент и валюта отчёта должны совпадать с настройками заказов.
        Валюту заказов нужно выбрать явно.
      </p>
      <fieldset disabled={disabled}>
        <legend>Разрешение на финансовый черновик</legend>
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
          Разрешить черновики отчётов комиссионера
        </label>
        {policy.enabled && (
          <>
            <div className="settings-lifecycle-grid">
              <label>
                Организация финансового отчёта
                <select
                  aria-label="Организация финансового отчёта"
                  value={policy.organization_id}
                  disabled={!metadata}
                  onChange={(event) =>
                    change({
                      organization_id: event.target.value,
                      contract_id: "",
                      organization_account_id: undefined,
                      agent_account_id: undefined,
                    })
                  }
                >
                  <option value="">Выберите организацию</option>
                  {policy.organization_id &&
                    !metadata?.organizations.some(
                      (item) => item.id === policy.organization_id,
                    ) && (
                      <option value={policy.organization_id}>
                        Сохранённая организация недоступна
                      </option>
                    )}
                  {metadata?.organizations.map((item) => (
                    <option value={item.id} key={item.id}>
                      {item.name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Контрагент финансового отчёта
                <select
                  aria-label="Контрагент финансового отчёта"
                  value={policy.agent_id}
                  disabled={!metadata}
                  onChange={(event) =>
                    change({
                      agent_id: event.target.value,
                      contract_id: "",
                      organization_account_id: undefined,
                      agent_account_id: undefined,
                    })
                  }
                >
                  <option value="">Выберите контрагента</option>
                  {policy.agent_id &&
                    !metadata?.agents.some(
                      (item) => item.id === policy.agent_id,
                    ) && (
                      <option value={policy.agent_id}>
                        Сохранённый контрагент недоступен
                      </option>
                    )}
                  {metadata?.agents.map((item) => (
                    <option value={item.id} key={item.id}>
                      {item.name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Валюта финансового отчёта
                <select
                  aria-label="Валюта финансового отчёта"
                  value={policy.currency_id}
                  disabled={!metadata}
                  onChange={(event) =>
                    change({
                      currency_id: event.target.value,
                      currency_iso:
                        metadata?.currencies.find(
                          (item) => item.id === event.target.value,
                        )?.iso_code ?? "",
                      contract_id: "",
                    })
                  }
                >
                  <option value="">Выберите валюту</option>
                  {policy.currency_id &&
                    !metadata?.currencies.some(
                      (item) => item.id === policy.currency_id,
                    ) && (
                      <option value={policy.currency_id}>
                        Сохранённая валюта недоступна
                      </option>
                    )}
                  {metadata?.currencies.map((item) => (
                    <option value={item.id} key={item.id}>
                      {item.iso_code}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Курс валюты финансового отчёта
                <input
                  type="text"
                  inputMode="decimal"
                  autoComplete="off"
                  maxLength={128}
                  value={policy.rate_value}
                  onChange={(event) =>
                    change({ rate_value: event.target.value })
                  }
                />
              </label>
            </div>
            <button
              type="button"
              className="secondary"
              disabled={contracts.isFetching}
              onClick={() => void contracts.refetch()}
            >
              {contracts.isFetching
                ? "Читаем договоры…"
                : "Прочитать комиссионные договоры"}
            </button>
            {contracts.isError && (
              <p role="alert">{orderRequestError(contracts.error)}</p>
            )}
            {contracts.data?.truncated && (
              <p className="settings-warning">
                Список договоров ограничен. Показаны только полученные договоры;
                отсутствие в списке не доказывает отсутствие договора в
                МойСклад.
              </p>
            )}
            <label>
              Комиссионный договор
              <select
                aria-label="Комиссионный договор"
                value={policy.contract_id}
                disabled={!contracts.data}
                onChange={(event) => {
                  const item = options.find(
                    (option) => option.id === event.target.value,
                  );
                  change({
                    contract_id: event.target.value,
                    organization_account_id: item?.organization_account_id,
                    agent_account_id: item?.agent_account_id,
                  });
                }}
              >
                <option value="">Выберите проверенный договор</option>
                {policy.contract_id &&
                  !options.some((item) => item.id === policy.contract_id) && (
                    <option value={policy.contract_id}>
                      Сохранённый договор: требуется проверка
                    </option>
                  )}
                {options.map((item) => (
                  <option value={item.id} key={item.id}>
                    {item.name}
                  </option>
                ))}
              </select>
            </label>
            {policy.contract_id && (
              <p className="settings-lifecycle-selected">
                Выбранный договор:{" "}
                <strong>
                  {options.find((item) => item.id === policy.contract_id)
                    ?.name ?? "Сохранённый договор: требуется проверка"}
                </strong>
              </p>
            )}
            {contracts.data && options.length === 0 && (
              <p className="settings-info">
                Для выбранной организации, контрагента и валюты не найден
                подходящий комиссионный договор без автоматического перерасчёта
                вознаграждения. Проверьте договор в МойСклад.
              </p>
            )}
            <div className="settings-lifecycle-grid">
              <label>
                Основание цены отчёта
                <select
                  aria-label="Основание цены отчёта"
                  value={policy.price_basis}
                  onChange={(event) =>
                    change({ price_basis: event.target.value })
                  }
                >
                  <option value="">Выберите с бухгалтером</option>
                  <option value="SELLER_PRICE">
                    Цена продавца из отчёта Ozon
                  </option>
                  <option value="REALIZED_PRICE">
                    Цена реализации из отчёта Ozon
                  </option>
                </select>
              </label>
              <label>
                Учёт НДС в отчёте
                <select
                  aria-label="Учёт НДС в отчёте"
                  value={boolValue(policy.vat_enabled)}
                  onChange={(event) =>
                    change({ vat_enabled: boolField(event.target.value) })
                  }
                >
                  <option value="">Выберите явно</option>
                  <option value="true">НДС учитывается</option>
                  <option value="false">НДС не учитывается</option>
                </select>
              </label>
              <label>
                НДС в цене отчёта
                <select
                  aria-label="НДС в цене отчёта"
                  value={boolValue(policy.vat_included)}
                  onChange={(event) =>
                    change({ vat_included: boolField(event.target.value) })
                  }
                >
                  <option value="">Выберите явно</option>
                  <option value="true">НДС включён в цену</option>
                  <option
                    value="false"
                    disabled={boolValue(policy.vat_enabled) === "true"}
                  >
                    НДС не включён в цену
                  </option>
                </select>
              </label>
              <label>
                Ставка НДС финансового отчёта, %
                <input
                  type="text"
                  inputMode="numeric"
                  maxLength={3}
                  value={policy.vat}
                  onChange={(event) => change({ vat: event.target.value })}
                />
              </label>
            </div>
            <p>
              Поддерживается НДС, уже включённый в цену, с явно выбранной
              ставкой, либо отсутствие НДС со ставкой 0. Дополнительное
              начисление налога поверх суммы исходного отчёта не поддерживается.
            </p>
            <p className="settings-info">
              Курс, основание цены и НДС задаёт владелец с бухгалтером. STYK
              проверяет суммы и связи товаров, но не выбирает бухгалтерскую
              политику. Финансовый возврат в отчёте не подтверждает поступление
              товара на склад и не создаёт физический Возврат покупателя.
            </p>
            <div className="settings-lifecycle-grid">
              <label>
                Финансовые возвраты в отчёте
                <select
                  aria-label="Финансовые возвраты в отчёте"
                  value={policy.return_treatment || "REQUIRE_ZERO"}
                  onChange={(event) =>
                    change({
                      return_treatment: event.target
                        .value as CommissionPolicy["return_treatment"],
                    })
                  }
                >
                  <option value="REQUIRE_ZERO">Только при явном нуле</option>
                  <option value="COMMISSION_RETURN">Включать в черновик</option>
                </select>
              </label>
              <label>
                Неучтённые финансовые компоненты
                <select
                  aria-label="Неучтённые финансовые компоненты"
                  value={policy.other_components}
                  onChange={(event) =>
                    change({
                      other_components: event.target
                        .value as CommissionPolicy["other_components"],
                    })
                  }
                >
                  <option value="ZERO_ONLY">Только явный ноль</option>
                  <option value="PARTIAL_REVIEW">Часть + ручная сверка</option>
                </select>
              </label>
            </div>
            <p className="settings-warning">
              Частичный черновик не закрывает финансовую сверку. Услуги,
              удержания, неизвестные суммы и другие неучтённые компоненты
              остаются для бухгалтера; STYK не добавляет фиктивные товары,
              платежи или складские документы.
            </p>
            <label className="orders-check">
              <input
                type="checkbox"
                checked={policy.automatic}
                onChange={(event) =>
                  change({ automatic: event.target.checked })
                }
              />
              Автоматически готовить черновик предыдущего месяца начиная с 5-го
              числа
            </label>
            <p>
              Автоматизация работает только в пределах этих настроек.
              Корректировка исходного отчёта не перезаписывает существующий
              документ; неизвестный результат требует сверки.
            </p>
            {consentRequired && (
              <label className="orders-check orders-warning">
                <input
                  type="checkbox"
                  checked={consent}
                  onChange={(event) => onConsent(event.target.checked)}
                />
                Разрешаю создание непроведённых черновиков отчёта комиссионера
                для этой пары аккаунтов с выбранным договором, валютой,
                основанием цены, НДС, финансовыми возвратами и выбранной
                политикой неучтённых компонентов, пока не отключу это
                разрешение.
              </label>
            )}
          </>
        )}
      </fieldset>
    </details>
  );
}
