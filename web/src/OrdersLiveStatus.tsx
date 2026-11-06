export function OrdersLiveStatus({ status }: { status: string }) {
  if (status === "disabled") return null;
  return (
    <p className="orders-note" role="status">
      {status === "live"
        ? "История обновляется автоматически."
        : status === "error"
          ? "Живые обновления недоступны. Используйте кнопку обновления статусов."
          : "Подключаем живые обновления истории…"}
    </p>
  );
}
