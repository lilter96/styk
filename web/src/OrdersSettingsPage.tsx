import { OrdersAccrualReport } from "./OrdersAccrualReport";
import { OrdersCarriage } from "./OrdersCarriage";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "./api";
import { Icon } from "./UI";
import { ProviderLogo } from "./ProviderLogo";
import { OrdersConfig } from "./OrdersConfig";
import { OrdersLifecycleJournal } from "./OrdersLifecycleJournal";
import { OrdersRealizationReport } from "./OrdersRealizationReport";
import { OrdersCommissionImport } from "./OrdersCommissionImport";
import { settingsSections, type SettingsSection } from "./OrdersSettingsParts";
import {
  ordersURL,
  orderRequestError,
  sameOrderScope,
  type OrdersConfigData,
} from "./OrdersTypes";
import type { OrdersProps } from "./Orders";
import "./Orders.css";

export function OrdersSettingsPage({
  scope,
  moyLabel = "Подключённый аккаунт",
  ozonLabel = "Подключённый аккаунт",
}: OrdersProps) {
  const client = useQueryClient();
  const [search, setSearch] = useSearchParams();
  const section =
    settingsSections.find((item) => item.id === search.get("section"))?.id ??
    "general";
  const config = useQuery({
    queryKey: ["orders-config", scope],
    enabled: !!scope?.tenant && !!scope.moy && !!scope.ozon,
    queryFn: async ({ signal }) => {
      const value = await api<OrdersConfigData>(
        ordersURL("config", scope!),
        signal,
      );
      if (!sameOrderScope(value.scope, scope!))
        throw new Error("config scope mismatch");
      return {
        ...value,
        interval_seconds: value.interval_seconds || 300,
        source_freshness_seconds: value.source_freshness_seconds || 300,
        preview_ttl_seconds: value.preview_ttl_seconds || 300,
        field_mappings: value.field_mappings ?? [],
        status_mappings: value.status_mappings ?? [],
      };
    },
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    staleTime: 0,
  });
  function openSection(value: SettingsSection) {
    setSearch({ section: value });
  }
  return (
    <div className="orders-settings-page">
      <header className="settings-page-heading">
        <div>
          <nav aria-label="Хлебные крошки">
            <Link to="/orders">Заказы</Link>
            <span>/</span>
            <span>Настройки передачи</span>
          </nav>
          <h1>Настройки заказов</h1>
          <p>Выберите, как заказы Ozon попадают в МойСклад</p>
          <div className="settings-account-pair">
            <ProviderLogo provider="ozon" />{" "}
            <span>
              Ozon: <strong>{ozonLabel}</strong>
            </span>
            <Icon name="arrow" />
            <ProviderLogo provider="moysklad" />
            <span>
              МойСклад: <strong>{moyLabel}</strong>
            </span>
          </div>
        </div>
        <Link
          className="settings-outline-link"
          aria-label="К заказам"
          to="/orders"
        >
          <Icon name="chevron-left" />К заказам
        </Link>
      </header>
      {!scope?.tenant || !scope.moy || !scope.ozon ? (
        <p>
          Выберите подключённые аккаунты Ozon и МойСклад.{" "}
          <Link to="/integrations">Перейти к подключениям</Link>
        </p>
      ) : config.isLoading || config.isFetching ? (
        <p role="status">Загружаем настройки заказов…</p>
      ) : config.isError ? (
        <p role="alert" className="orders-error">
          {orderRequestError(config.error)}{" "}
          <button className="text-action" onClick={() => void config.refetch()}>
            Повторить загрузку
          </button>
        </p>
      ) : (
        config.data && (
          <>
            <OrdersConfig
              key={`${JSON.stringify(scope)}:${config.data.revision}`}
              scope={scope}
              config={config.data}
              section={section}
              onSection={openSection}
              moyLabel={moyLabel}
              ozonLabel={ozonLabel}
              onDirty={() => {}}
              onSaved={(value) => {
                client.setQueryData(["orders-config", scope], value);
                void client.invalidateQueries({
                  queryKey: ["orders-config", scope],
                });
                void client.invalidateQueries({
                  queryKey: ["orders-postings", scope],
                });
              }}
            />
            {section === "lifecycle" && (
              <div
                className="settings-content settings-lifecycle-diagnostics"
                key={JSON.stringify(scope)}
              >
                <OrdersLifecycleJournal scope={scope} />
                <OrdersRealizationReport scope={scope} />
                <OrdersAccrualReport scope={scope} />
                <OrdersCommissionImport scope={scope} config={config.data} />
                <OrdersCarriage scope={scope} config={config.data} />
              </div>
            )}
          </>
        )
      )}
    </div>
  );
}
