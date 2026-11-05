import moysklad from "./assets/brands/moysklad.svg";
import ozon from "./assets/brands/ozon.png";
import wildberries from "./assets/brands/wildberries.svg";
import yandexMarket from "./assets/brands/yandex-market.png";

export type BrandProvider =
  "styk" | "moysklad" | "ozon" | "wildberries" | "yandex-market";
type BrandAsset = {
  label: string;
  src?: string;
  mark?: string;
  opticalScale: number;
};

// Optical corrections belong here, never in individual screens.
export const brandRegistry: Record<BrandProvider, BrandAsset> = {
  styk: { label: "STYK", mark: "S", opticalScale: 1 },
  moysklad: { label: "МойСклад", src: moysklad, opticalScale: 1 },
  ozon: { label: "Ozon", src: ozon, opticalScale: 1 },
  wildberries: { label: "Wildberries", src: wildberries, opticalScale: 1 },
  "yandex-market": {
    label: "Яндекс Маркет",
    src: yandexMarket,
    opticalScale: 1,
  },
};

export type IntegrationBrandProvider = Exclude<BrandProvider, "styk">;
export function integrationBrand(
  provider: IntegrationBrandProvider | "yandex",
): IntegrationBrandProvider {
  return provider === "yandex" ? "yandex-market" : provider;
}
