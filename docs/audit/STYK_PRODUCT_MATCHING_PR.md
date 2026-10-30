The product matching dialog now uses a wide two-column workspace with an Ozon identity panel, server-side MoySklad search/count/pagination, selectable result rows and fixed actions. Existing reviewed, audited, idempotent confirmation remains authoritative; successful save-and-next opens the next unconfirmed product and restores search focus.

The second UI pass reuses the actual Ozon and MoySklad brand assets from the demo, aligns metadata and copy controls, refines the result grid and spacing, and restores consistent footer button sizes. Additional is closed by default and contains only Possible matches and Mapping history. Confirmation is presented separately above the footer, preserving acknowledgement, conflicts, expiry and uncertain-outcome retry. No backend or synchronization logic changed in this pass.

The existing `/api/moysklad-products` route supports query/field selection, observed in-stock filtering, exact total and bounded server pages, with stable ordering and tenant/account isolation. No synchronization algorithms or database schema changed.

This PR remains draft: the requested screenshot was not attached, so pixel equivalence cannot be verified. Separate MoySklad SKU and activity are not persisted by the current importer; the corresponding controls are unavailable and API requests explicitly return 422. Prices/images are omitted or represented by placeholders. Explicit review acknowledgement is preserved rather than silently bypassed.

Validation: frontend build and lint, 206 Vitest tests, Go lint/test/race/build, and 3 real local Playwright smoke tests passed. Browser coverage includes SQL search/count/pagination, stock filtering, isolation, anonymous denial, selection, real confirmation and save-and-next on isolated seeded local data. No production/provider writes.

[Technical handoff](https://github.com/lilter96/styk/blob/feature/styk-product-matching-redesign/docs/audit/STYK_PRODUCT_MATCHING_HANDOFF.md) documents the contract, files, coverage gaps and remaining acceptance work.

Before:
![Before](https://github.com/lilter96/styk/blob/feature/styk-product-matching-redesign/docs/audit/product-matching/before.png?raw=true)

After:
![After](https://github.com/lilter96/styk/blob/feature/styk-product-matching-redesign/docs/audit/product-matching/normal.png?raw=true)

Selected:
![Selected](https://github.com/lilter96/styk/blob/feature/styk-product-matching-redesign/docs/audit/product-matching/selected.png?raw=true)

Additional (only two sections):
![Additional](https://github.com/lilter96/styk/blob/feature/styk-product-matching-redesign/docs/audit/product-matching/additional.png?raw=true)
