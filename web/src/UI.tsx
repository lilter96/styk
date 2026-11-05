import type { ReactNode } from "react";
import "./icons.css";
import { BrandLogo } from "./BrandLogo";

export type IconName =
  | "overview"
  | "products"
  | "orders"
  | "catalog"
  | "warehouse"
  | "integrations"
  | "diagnostics"
  | "menu"
  | "settings"
  | "refresh"
  | "arrow"
  | "copy"
  | "plus"
  | "chevron-left"
  | "chevron-right"
  | "chevron-down"
  | "close"
  | "search"
  | "history"
  | "more"
  | "error"
  | "variants"
  | "check";
const paths: Record<IconName, ReactNode> = {
  copy: (
    <>
      <rect x="8" y="8" width="12" height="12" rx="2" />
      <path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  "chevron-left": <path d="m15 6-6 6 6 6" />,
  "chevron-right": <path d="m9 6 6 6-6 6" />,
  "chevron-down": <path d="m6 9 6 6 6-6" />,
  check: <path d="m5 12 4 4L19 6" />,
  close: <path d="m6 6 12 12M6 18 18 6" />,
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="6.5" />
      <path d="m16 16 5 5" />
    </>
  ),
  history: (
    <>
      <path d="M3 4v5h5M3 9a9 9 0 1 1 1 8M12 7v5l3 2" />
    </>
  ),
  more: (
    <>
      <circle cx="5" cy="12" r="1" />
      <circle cx="12" cy="12" r="1" />
      <circle cx="19" cy="12" r="1" />
    </>
  ),
  error: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v6M12 17h.01" />
    </>
  ),
  variants: (
    <>
      <rect x="3" y="3" width="12" height="12" rx="2" />
      <path d="M8 18v3h13V8h-3" />
    </>
  ),
  refresh: (
    <path d="M20 7v5h-5M4 17v-5h5M6 6a8 8 0 0 1 13 3M5 15a8 8 0 0 0 13 3" />
  ),
  settings: (
    <>
      <path d="m9.5 3-.7 2.2-2 .9-2.1-.5-2.2 3.8L4 11v2l-1.5 1.6 2.2 3.8 2.1-.5 2 .9.7 2.2h5l.7-2.2 2-.9 2.1.5 2.2-3.8L20 13v-2l1.5-1.6-2.2-3.8-2.1.5-2-.9L14.5 3h-5Z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
  overview: (
    <>
      <rect x="3" y="3" width="7" height="7" rx="1.5" />
      <rect x="14" y="3" width="7" height="7" rx="1.5" />
      <rect x="3" y="14" width="7" height="7" rx="1.5" />
      <rect x="14" y="14" width="7" height="7" rx="1.5" />
    </>
  ),
  products: (
    <>
      <path d="m12 3 9 5-9 5-9-5 9-5Zm-9 5v9l9 5 9-5V8M12 13v9M7.5 5.5l9 5" />
    </>
  ),
  orders: <path d="M5 3h14v18l-3-2-4 2-4-2-3 2V3ZM8 7h8M8 11h8M8 15h4" />,
  catalog: (
    <>
      <path d="M5 3h14v18H5zM8 7h8M8 11h8M8 15h4" />
    </>
  ),
  warehouse: (
    <>
      <path d="m3 9 9-6 9 6v12H3V9Zm4 12V11h10v10M7 15h10M7 18h10" />
    </>
  ),
  integrations: (
    <>
      <rect x="3" y="3" width="6" height="6" rx="2" />
      <rect x="15" y="15" width="6" height="6" rx="2" />
      <path d="M9 6h6a3 3 0 0 1 3 3v6M6 9v6a3 3 0 0 0 3 3h6" />
    </>
  ),
  diagnostics: (
    <>
      <path d="M9 3h6v3H9zM9 4H6a2 2 0 0 0-2 2v14a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1V6a2 2 0 0 0-2-2h-3M8 13l3 3 5-6" />
    </>
  ),
  menu: <path d="M4 6h16M4 12h16M4 18h16" />,
  arrow: <path d="M5 12h14m-5-5 5 5-5 5" />,
};
export type IconSize = "metric" | "button" | "navigation" | "service";
export function Icon({
  name,
  size = "navigation",
  className = "",
}: {
  name: IconName;
  size?: IconSize;
  className?: string;
}) {
  return (
    <svg
      className={`styk-icon ${className}`.trim()}
      data-size={size}
      focusable="false"
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths[name]}
    </svg>
  );
}
export function Brand() {
  return (
    <div className="app-brand" aria-label="STYK">
      <BrandLogo provider="styk" decorative />
      <b aria-hidden="true">TYK</b>
    </div>
  );
}
export function PageHeader({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children?: ReactNode;
}) {
  return (
    <header className="styk-page-header">
      <div>
        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </div>
      {children}
    </header>
  );
}
export function StatePanel({
  children,
  kind = "empty",
}: {
  children: ReactNode;
  kind?: "empty" | "loading" | "error";
}) {
  return (
    <div
      className={`styk-state-panel ${kind}`}
      role={
        kind === "error" ? "alert" : kind === "loading" ? "status" : undefined
      }
    >
      {kind === "loading" && (
        <span className="styk-spinner" aria-hidden="true" />
      )}
      {children}
    </div>
  );
}
export function ProviderBadge({ provider }: { provider: "moysklad" | "ozon" }) {
  return <BrandLogo provider={provider} decorative />;
}
