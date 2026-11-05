import type { ReactNode } from "react";
import { PageHeader, Icon } from "./UI";
import { ProviderLogo } from "./ProviderLogo";

export function OrdersHeader({
  moyLabel,
  ozonLabel,
  children,
}: {
  moyLabel?: string;
  ozonLabel?: string;
  children?: ReactNode;
}) {
  return (
    <div className="orders-heading">
      <PageHeader
        title="Заказы"
        description="Отправления Ozon и передача в МойСклад"
      >
        <div className="orders-header-actions">{children}</div>
      </PageHeader>
      {moyLabel && ozonLabel && (
        <div className="orders-account-context" aria-label="Аккаунты заказов">
          <span>
            <ProviderLogo provider="ozon" size="sm" />
            Ozon: <strong>{ozonLabel}</strong>
          </span>
          <Icon name="arrow" size="button" />
          <span>
            <ProviderLogo provider="moysklad" size="sm" />
            МойСклад: <strong>{moyLabel}</strong>
          </span>
        </div>
      )}
    </div>
  );
}
