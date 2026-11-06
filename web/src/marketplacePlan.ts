export const marketplacePlanKey = "marketplace-plan";

export type MarketplacePlanLimits = {
  code: string;
  total_limit: number;
  provider_limit: number;
};

export type MarketplacePlan = MarketplacePlanLimits & {
  revision: number;
  used: number;
  providers: Record<string, number>;
  exceeded: boolean;
  blocked: Record<string, string>;
};

export const marketplaceQuotaMessages: Record<string, string> = {
  PLAN_TOTAL_LIMIT:
    "Все места по тарифу заняты. Отключите один кабинет или смените тариф.",
  PLAN_PROVIDER_LIMIT: "Можно подключить ещё один аккаунт другой площадки.",
  PLAN_OVER_LIMIT:
    "Лимит тарифа превышен. Отключите лишние кабинеты или смените тариф.",
};

export function marketplacePlanName(code: string) {
  return "Тариф " + code.slice("plan_".length);
}

export function marketplaceQuotaReason(
  plan: MarketplacePlan | undefined,
  provider: string,
) {
  const code = plan?.blocked?.[provider] ?? "";
  if (
    code === "PLAN_OVER_LIMIT" &&
    plan?.provider_limit === 1 &&
    Object.values(plan.providers).some((used) => used > 1)
  ) {
    return "По этому тарифу можно подключить по одному аккаунту каждой площадки. Отключите лишний аккаунт или смените тариф.";
  }
  return marketplaceQuotaMessages[code];
}
