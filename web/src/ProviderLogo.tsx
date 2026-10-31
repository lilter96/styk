import { BrandLogo, type BrandLogoSize } from "./BrandLogo";
import { integrationBrand, type IntegrationBrandProvider } from "./brands";
export type BrandProvider = IntegrationBrandProvider | "yandex";

/** Compatibility for existing provider IDs; all artwork and styling live in BrandLogo. */
export function ProviderLogo({
  provider,
  className = "",
  size = "lg",
}: {
  provider: BrandProvider;
  className?: string;
  size?: BrandLogoSize;
}) {
  return (
    <BrandLogo
      provider={integrationBrand(provider)}
      size={size}
      className={className}
      decorative
    />
  );
}
