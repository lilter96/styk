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
      icon: "variants",
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
          <Icon name={metric.icon} size="metric" />
          <div>
            <dt>{metric.label}</dt>
            <dd>{metric.value}</dd>
          </div>
        </div>
      ))}
    </dl>
  );
}
