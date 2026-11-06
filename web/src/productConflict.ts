import type { Product } from "./api";

export function conflictTitle(p: Product) {
  if (p.conflict_reasons?.includes("OTHER_ACCOUNT"))
    return "Связь относится к другому аккаунту";
  if (p.conflict_reasons?.includes("OFFER_CHANGED"))
    return "У товара изменился артикул";
  if (
    p.target_id &&
    p.candidates.some((c) => c.id !== p.target_id || c.kind !== p.target_kind)
  )
    return "По артикулу найден другой товар";
  if (p.conflict_reasons?.includes("OFFER_CONFLICT"))
    return "Артикул используется в другой связи";
  return "Текущая связь требует проверки";
}

export function conflictExplanation(p: Product) {
  const reasons = p.conflict_reasons || [];
  if (reasons.includes("OTHER_ACCOUNT"))
    return "Сохранённая связь относится к другому аккаунту МойСклад. Выберите тот аккаунт, в котором она была создана.";
  if (reasons.includes("OFFER_CHANGED"))
    return "Артикул товара в Ozon изменился после создания связи. Сохранённое соответствие нужно проверить заново.";
  if (
    p.target_id &&
    (reasons.includes("DIFFERENT_CANDIDATE") ||
      p.candidates.some(
        (c) => c.id !== p.target_id || c.kind !== p.target_kind,
      ))
  )
    return "По артикулу или коду найден другой товар МойСклад. Он отличается от товара в сохранённой связи.";
  if (reasons.includes("OFFER_CONFLICT"))
    return "Этот артикул уже используется в связи с другим товаром Ozon. Сначала проверьте артикулы в Ozon и обновите каталог.";
  return "STYK обнаружил противоречие в сохранённом соответствии. Сравните текущую пару и найденные товары перед изменением связи.";
}
