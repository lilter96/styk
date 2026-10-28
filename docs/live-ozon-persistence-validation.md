# First live Ozon PostgreSQL ingestion

The account owner previously confirmed that the locally configured Ozon credentials belong to their account and authorized bounded read-only diagnostics. The ignored local configuration had not changed since that confirmation. Account registration is a trusted administrative attestation, not independent proof of ownership or authenticated tenant authorization. No live identifiers, credentials, raw responses or customer data are recorded here. No Ozon or MoySklad write was made.

## Live execution

A dedicated disposable local PostgreSQL database was created and existing migrations applied through version 15. The confirmed Ozon account was registered to a local test tenant with the existing `RegisterStockConnection` service. No MoySklad token was loaded and no MoySklad account was registered. The first CLI launch failed configuration validation before making an API request because the selected database query timeout equaled the worker lease duration; the timeout was corrected before the import.

Twelve live Ozon requests were made, with one attempt per operation and bounded timeouts: four preflight requests, four requests in the existing bounded catalog CLI, and four for an independent read-only comparison. Preflight observed 9 `ALL` and 24 `ARCHIVED` products, 13 distinct warehouses in one page at page size 100, and no stock rows for the single sampled offer. These observations fit the existing default execution budgets; no limits were raised.

The catalog CLI returned `COMPLETE_TRAVERSAL`: 4 logical requests, 4 network attempts, 4 pages, 33 products, 13 warehouses and 5 stock rows. One completed batch was saved. A completed traversal is neither exhaustive account visibility nor a point-in-time snapshot. The five reported stock rows each had explicit zero `present`, `reserved` and `free_stock`; this does not imply missing rows are zero. A single-offer preflight was empty, while the full catalog request returned stock rows for other offers.

## PostgreSQL and independent comparison

An independent bounded traversal compared every persisted product ID, offer ID, nullable SKU and archived flag; every warehouse ID, name, status, type and rFBS flag; and every stock row's product, offer, nullable SKU, warehouse, quantities and observation time. Counts matched: 9 non-archived products, 24 archived products, 5 missing SKUs, 7 FBS warehouses, 6 rFBS warehouses and 5 stock rows. All five stock warehouse identities appeared in the imported warehouse observations. Database constraints enforce product references; the warehouse association check was an explicit query, not a claimed warehouse foreign key.

The completed batch was associated with the selected account and its trusted tenant registration, and its start and finish timestamps were ordered. Catalog observations themselves are account-scoped through their batch rather than protected by tenant row-level security. No additional completed batch, product mapping, warehouse mapping, posting batch or outbox task was created. No second live import was run; repeated and rollback behavior remain covered by synthetic PostgreSQL tests, not this live run.

## Integration Doctor and remaining gaps

Integration Doctor was invoked against the registered Ozon scope with an explicitly unregistered MoySklad placeholder. It reported account registration `FAIL`, stock calculation `NOT_CHECKED` and publication `DISABLED`. Because its scope check stops at the missing MoySklad registration, it did not evaluate the Ozon import; the counts above are from the separate scoped PostgreSQL comparison. No pairing or mapping was fabricated.

No posting request was made in this stage; previous bounded windows were empty and posting details remain unverified. The live sample verifies only observed account permissions and values, not source consistency, general reservation attribution or safe publication. No API contract discrepancy requiring a code fix was found. Standard checks use mocked credentials and a separate PostgreSQL integration database; they do not validate live imports.
