import { PageHeader, Icon } from "./UI";
import { ProviderLogo } from "./ProviderLogo";

export function OrdersHeader({
  moyLabel,
  ozonLabel,
}: {
  moyLabel?: string;
  ozonLabel?: string;
}) {
  return (
    <div className="orders-heading">
      <PageHeader
        title="Заказы"
        description="Проверьте отправления, передавайте заказы в МойСклад и подтверждайте действия в Ozon."
      />
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
