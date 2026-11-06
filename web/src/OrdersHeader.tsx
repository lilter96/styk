import type { ReactNode } from "react";
import { Icon } from "./UI";
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
      <header className="styk-page-header">
        <div className="orders-heading-copy">
          <h1>Заказы</h1>
          <p>Отправления Ozon и передача в МойСклад</p>
          {moyLabel && ozonLabel && (
            <div
              className="orders-account-context"
              aria-label="Аккаунты заказов"
            >
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
        {children && (
          <div
            className="orders-header-actions"
            role="group"
            aria-label="Действия с заказами"
          >
            {children}
          </div>
        )}
      </header>
    </div>
  );
}
