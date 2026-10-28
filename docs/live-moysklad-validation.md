# MoySklad live read-only contract validation

On 2026-10-29, an operator confirmed that the locally configured account is the previously authorized test account and that the local token is the rotated replacement. The token and account identity are intentionally omitted here. The existing allowlisted Go client issued six GET requests, each limited to one row at offset zero, with a five-second request timeout, one attempt, one-second spacing and a 40-second overall deadline. No source writes, database imports, raw payload logs or customer-data output occurred.

| Method | Observed result |
| --- | --- |
| `GET /entity/product` | Successful; empty first page. |
| `GET /entity/variant` | Successful; empty first page. |
| `GET /entity/store` | Successful; one warehouse. Its `id`, `accountId`, `meta` and `updated` fields were present; the ID had UUID syntax and `accountId` matched the configured account. |
| `GET /report/stock/all` with `stockMode=all;quantityMode=all` | Successful; empty first page. |
| `GET /report/stock/bystore` with `stockMode=all` and `groupBy=variant` | Successful; empty first page. |
| `GET /entity/customerorder` | Successful; empty first page. |
| `GET /entity/customerorder/{id}/positions` | Not checked: no customer order was present in the sampled page. |

All six successful responses contained `meta.size`, `meta.limit`, `meta.offset` and `rows`; offset was zero, limit was positive and at most one row was returned. No response indicated a second page. The client's existing bearer authorization and JSON Accept header were accepted for these requests. This confirms access to the sampled methods, not complete permissions or exhaustive account visibility. No 401, 403, 429 or 5xx response was encountered or intentionally induced.

The empty-page envelopes and populated warehouse identity agree with the existing sanitized mock expectations. No incompatibility was observed, and no production code fix or regression fixture was warranted. This does **not** verify populated product, variant, stock, order or position schema, nullable or missing fields, pagination across multiple pages, warehouse stock references, stock and reserve meanings, order timestamps, or error response behavior. Successful pagination does not establish a point-in-time snapshot, and missing stock rows do not imply zero.

For the next bounded read-only validation, the account owner can create these synthetic records manually in the test UI:

1. Two warehouses and enough products to require at least two pages at the selected test page size; include products with distinct articles and two with the same article.
2. A product with at least one variant, and warehouse-level stock in both warehouses, including a nonzero quantity and an explicitly recorded zero where the source reports one.
3. A customer order with a variant position, a nonzero reserve and a partial shipment; retain another order or position without an assigned warehouse if the UI permits it.
4. A separate customer order or position with an explicit zero reserve. Use synthetic data and no personal customer information.

After the data is available, separately authorize another small bounded GET-only run. Compare populated field presence and identity references with the fixtures, sample two pages, and investigate any differences without recording raw customer data. A matching order and reserve alone still cannot prove inclusion in a particular stock-report baseline.
