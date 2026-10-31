import moyLogo from "./assets/brands/moysklad.png";
import ozonLogo from "./assets/brands/ozon.png";
import { Icon, type IconName } from "./UI";
import type { Batch, ImportProvider } from "./api";

function updatedAt(value?: string) {
  if (
    !value ||
    !Number.isFinite(Date.parse(value)) ||
    new Date(value).getFullYear() < 1970
  )
    return "Нет данных";
  const date = new Date(value);
  return date.toDateString() === new Date().toDateString()
    ? `Сегодня в ${date.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" })}`
    : date.toLocaleString("ru-RU", {
        day: "numeric",
        month: "short",
        hour: "2-digit",
        minute: "2-digit",
      });
}
export function IntegrationBrand({ provider }: { provider: ImportProvider }) {
  return (
    <span className={`integration-brand ${provider}`}>
      <img src={provider === "moysklad" ? moyLogo : ozonLogo} alt="" />
    </span>
  );
}
export function IntegrationMetrics({
  provider,
  batch,
}: {
  provider: ImportProvider;
  batch?: Batch;
}) {
  const counts = batch?.present && batch.completed ? batch.counts : undefined;
  const metrics: { label: string; value: string; icon: IconName }[] = [
    {
      label: "Обновлено",
      value: updatedAt(
        batch?.present && batch.completed ? batch.observed_at : undefined,
      ),
      icon: "refresh",
    },
    {
      label: "Товаров",
      value: counts?.products?.toLocaleString("ru-RU") ?? "—",
      icon: "products",
    },
  ];
  if (provider === "moysklad" || counts?.variants !== undefined)
    metrics.push({
      label: "Вариантов",
      value: counts?.variants?.toLocaleString("ru-RU") ?? "—",
      icon: "catalog",
    });
  metrics.push({
    label: "Складов",
    value: counts?.warehouses?.toLocaleString("ru-RU") ?? "—",
    icon: "warehouse",
  });
  return (
    <dl className="integration-metrics">
      {metrics.map((metric) => (
        <div className="integration-metric" key={metric.label}>
          <Icon name={metric.icon} />
          <div>
            <dt>{metric.label}</dt>
            <dd>{metric.value}</dd>
          </div>
        </div>
      ))}
    </dl>
  );
}
