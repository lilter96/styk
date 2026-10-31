# Brand and icon system audit

The scope is the existing `web/` React frontend. No Go code, provider capability, credential handling or synchronization business rule was changed for this cleanup. The provider-group/carousel work already present in the working tree was preserved and retained as the integration view; related components and styles are included with the shared visual infrastructure. Unrelated overview work and runtime documentation are excluded from the commit. App and MoySklad settings entry points are staged only for their icon changes.

## Inventory and decisions

| Surface / resource before cleanup | Result |
| --- | --- |
| `src/assets/brands/` | Retained as the single canonical directory; there is no separate `web/public` directory. |
| `moysklad.svg` and `moysklad.png` | Retained the existing vector, normalized its missing viewBox/intrinsic dimensions without editing paths or colors. Removed the unused raster after replacing all references. |
| `ozon.png`, `wildberries.svg` | Reused existing artwork without recoloring or tracing. |
| `yandex-official.png` | Renamed to `yandex-market.png`, byte-for-byte. |
| STYK `Brand` | Kept the existing code-native monogram and wordmark; metadata and compact mark now go through the registry and BrandLogo. No original STYK SVG exists here. |
| Integration group header / sidebar | ProviderLogo now delegates to BrandLogo. Sidebar marketplace selectors also use the canonical marks and accessible brand names. |
| Account settings / product matching | Replaced direct PNG imports and the private duplicate ProviderLogo. Added the Ozon mark to its existing settings dialog header. |
| Overview source badges | ProviderBadge delegates to BrandLogo instead of rendering `O` / `МС` placeholders. |
| Individual integration account cards | Kept brand-free. Removed the unused IntegrationBrand implementation. |
| Inline UI SVGs / Unicode controls | Search, copy, close and pagination/carousel arrows use UI.Icon, as do the add buttons and history disclosure. |
| Landing / demo | No separate landing or demo application sources exist in the checked-in tree. The local demo runs this same frontend. No external hosted design was modified. Historical screenshots and ignored local snapshots are evidence, not application asset duplicates. |

Search included source, styles, TypeScript/HTML references, inline SVG, image imports, CSS backgrounds/data URIs and all repository graphics outside dependencies and ignored local archives. No external image source or duplicate provider data URI remains in the app.

## Public API

```tsx
<BrandLogo provider="ozon" size="lg" />
<BrandLogo provider="yandex-market" size="sm" decorative />
```

`brands.ts` owns the provider union, display names, artwork paths and optional optical scale. `BrandLogo` owns accessible naming, decorative presentation, className, image-error fallback and five size tokens (20/24/32/36/40 px). The legacy UI ID `yandex` is mapped explicitly to the brand ID `yandex-market`; this does not enable a backend provider.

`brand-logo.css` owns square geometry, padding, subtle border and 9 px rounding. Four integration group headers use the exact same 36×36 container. No repeated provider logos were added to account cards. `UI.Icon` uses the existing local path registry, one 24-unit viewBox and 1.7 stroke. `design-system.css` provides metric/button/navigation/service sizes (16/16/20/24 px); `icons.css` only targets `.styk-icon` and cannot recolor brand images.

## Verification

- TypeScript typecheck, ESLint, Vite production build: passed.
- Vitest: 22 files, 220 tests passed, including accessible brand rendering, decorative marks, unknown/error fallback and recovery when changing provider.
- Authenticated Chromium smoke: all four sizes passed, including loaded artwork, exact square dimensions, border/radius, no card logos, no document overflow, carousel movement, Ozon dialog, MoySklad settings navigation, marketplace/account switching, keyboard focus and no console errors or failed asset responses.
- Existing carousel browser test passed (1/3/5/10/20 accounts, resize, search, boundaries and keyboard).
- Existing demo browser smoke passed (protected pages, tenant isolation, anonymous access and retired auth path).
- Existing workspace/new-account browser flows passed at 1440 and 390 px after updating accessible selector names.
- The broader existing `redesign.spec.ts` is not fully green: its overview assertions expect `Обзор`, but the pre-existing OverviewDashboard uses `Добро пожаловать в STYK`; its product pagination test expects a `Далее` button absent from the current UI. The unchanged overview heading was confirmed against the saved pre-task snapshot. These broader checks were not repaired as part of the brand cleanup.
- Go checks requested by AGENTS.md passed during the preceding startup work: `make lint` (golangci-lint: 0 findings), `make test`, `go test -race ./...`, `make build`, `go build ./...`. No Go source changed afterward.

## Visual evidence

`before/` contains authenticated Integrations and MoySklad settings screenshots at 1920×1080, 1536×1024, 1366×768 and 390×844. `after/` contains those surfaces plus the existing Ozon settings dialog at the same sizes. Screenshots were inspected for alignment and mobile layout; this is not a claim of pixel-perfect matching to an unavailable external reference.

The local database used for authenticated smoke was recovered from the existing 2026-10-31 12:12 UTC backup into a separate persistent volume. It contains synthetic accounts and no provider credential rows; it predates the later real-provider connection. The startup issue and backup recovery were runtime work preceding this frontend cleanup, not backend source changes.
