# Provider artwork

Artwork is served locally through `brands.ts` and `BrandLogo`. Brand colors and proportions are preserved. `ProviderLogo` and `ProviderBadge` are compatibility facades, with no separate artwork or CSS.

- `moysklad.svg`: https://www.moysklad.ru/favicon.svg — existing official vector symbol, downloaded 2026-10-31. Added a tight `viewBox="1 16 118 88.544"` and matching intrinsic dimensions; paths and colors are unchanged.
- `wildberries.svg`: https://www.wildberries.ru/icon.svg — official vector icon, downloaded 2026-10-31.
- `yandex-market.png`: https://yastatic.net/market-export/_/i/favicon/ymnew/180.png — existing official Yandex Market artwork, 180×180, downloaded 2026-10-31. Renamed without changing image bytes.
- `ozon.png`: existing project artwork, 128×128, reused without upscaling or recoloring. Official visual guidance: https://brandlab.ozon.ru/en/guide/logo_guide.

`moysklad.png` was removed after migrating all of its consumers to the existing vector symbol. There is one compact mark per provider, and no full wordmark variant in this repository. PNGs are retained where no existing vector artwork is available; they are not traced or redrawn.

STYK has no image asset in this repository. Its existing gradient `S` monogram remains code-native in `BrandLogo`, with its label and mark in the registry. `Brand` composes it with the existing `TYK` text. This preserves the product identity without inventing replacement artwork.

All sizes use one square surface: 20/24/32/36/40 px, 9 px corners, a 1 px `--styk-line` border, white background and proportional padding. Provider group headers use 36 px. Artwork uses `object-fit: contain`; optical scale, when needed, belongs only in the registry. Colored brand artwork is independent from monochrome UI icon rules.
