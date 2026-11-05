import { useState, type CSSProperties } from "react";
import { brandRegistry, type BrandProvider } from "./brands";
import "./brand-logo.css";

export type BrandLogoSize = "xs" | "sm" | "md" | "lg" | "xl";
export type BrandLogoProps = {
  provider: BrandProvider;
  size?: BrandLogoSize;
  className?: string;
  decorative?: boolean;
};

export function BrandLogo({
  provider,
  size = "md",
  className = "",
  decorative = false,
}: BrandLogoProps) {
  const asset = brandRegistry[provider];
  const [failedSource, setFailedSource] = useState<string>();
  const imageAvailable = asset?.src && asset.src !== failedSource;
  return (
    <span
      className={`brand-logo ${className}`.trim()}
      data-brand={provider}
      data-size={size}
      data-artwork={
        imageAvailable ? "image" : asset?.mark ? "mark" : "fallback"
      }
      role={decorative ? undefined : "img"}
      aria-label={decorative ? undefined : asset?.label || "Интеграция"}
      aria-hidden={decorative || undefined}
      style={
        { "--brand-optical-scale": asset?.opticalScale ?? 1 } as CSSProperties
      }
    >
      {imageAvailable ? (
        <img
          src={asset.src}
          alt=""
          draggable={false}
          onError={() => setFailedSource(asset.src)}
        />
      ) : (
        <span className="brand-logo-mark" aria-hidden="true">
          {asset?.mark || asset?.label.slice(0, 1) || "?"}
        </span>
      )}
    </span>
  );
}
