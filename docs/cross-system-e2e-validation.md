# Cross-system read-only diagnostic validation

Two previously owner-authorized accounts were registered to one local test tenant in a new disposable PostgreSQL database with migrations through version 15. The MoySklad account identifier matched the private fixture manifest; the Ozon configuration was unchanged since the owner's confirmation and prior live verification. Registrations are trusted administrative attestations with `include_import_history=false`, not external proof that the accounts share inventory. Imports began after registration. No live identifiers, credentials or customer data are included here.

## Observations and persistence

The existing read-only MoySklad catalog importer completed with 9 products, 2 variants, 3 warehouses and 33 warehouse-stock rows. A separate bounded 24-hour customer-order import recorded 5 orders and 6 positions. The existing Ozon bounded importer returned `COMPLETE_TRAVERSAL`, with 4 requests and 4 network attempts, and persisted 33 products, 13 warehouses and 5 warehouse-stock rows. No Ozon posting import was performed. Source data may change during traversal; neither completed import is an exhaustive point-in-time snapshot.

The MoySklad fixture manifest was checked privately against the imported identities: 9 products, 2 variants, 2 newly created fixture warehouses and 5 customer orders matched, with source updated timestamps present. Both variants retained their parent identity. Six fixture warehouse-stock pairs matched the previously verified manifest quantities, reserves and incoming values, and their warehouse-level available quantities remained NULL. The variant customer-order position retained its variant and warehouse identities, quantity 2, reserve 1 and source updated timestamp. The third imported warehouse predates the fixture. This comparison does not prove a consistent source snapshot or reserve attribution.

The Ozon import had 24 archived products and 5 missing SKUs; its warehouse rows included 7 FBS and 6 rFBS identities. All five stock rows explicitly reported zero present, reserved and free stock; absent rows remain unknown. The canonical fingerprint matched the prior separately verified Ozon import, an equality of observed imported data rather than a guarantee that the source never changed.

## Matching and diagnostics

The existing matching engine evaluated 33 Ozon sources against 11 MoySklad products and variants: 33 `no_candidate`, zero unique candidates, zero ambiguous candidates, zero invalid inputs and zero mapping conflicts. These are suggestions only; no mapping was confirmed. The two datasets are independently owned test data, and matching identifiers would not alone prove shared physical inventory.

There were zero confirmed product mappings and zero confirmed warehouse mappings. All imported warehouse identities remain unmapped; no name-based warehouse inference was made. No aggregation, splitting or warehouse allocation relationship is supported by this validation.

Offline Integration Doctor reported account registration, both catalog imports and customer-order import `PASS`; posting import, product mappings and warehouse mappings `WARN`; order correlations `NOT_CHECKED`; stock calculation `NOT_CHECKED`; and publication `DISABLED`. Connection probes were `NOT_CHECKED` in this offline report even though the preceding imports contacted both sources. Stock coverage remains `WARN`: missing rows, source ordering, permissions, reservation attribution and baseline inclusion are not proven. No physical pool or confirmed relationship was fabricated, and no decision or outbox task was created.

Doctor now evaluates each registered source independently. An unregistered source is not queried; cross-system mappings, correlations and stock evaluation require both registrations. Registration is trusted administrative scoping, not authenticated end-user authorization or PostgreSQL row-level security.

An additional offline diagnostic against this database deliberately selected an unregistered MoySklad scope: combined registration was `FAIL`, MoySklad import was `NOT_CHECKED`, and the registered Ozon import remained `PASS` with 33 products, 13 warehouses and 5 stock rows. Product mappings and calculation remained `NOT_CHECKED`; publication remained `DISABLED`. Live probe eligibility is also checked independently for each source and requires both its registration and a matching configured account ID.

Focused mocked and PostgreSQL tests cover both accounts, either account alone, neither, cross-tenant pairing, independently visible evidence and disabled cross-system checks. Existing tests cover ambiguous candidates, missing identifiers, archived products, zero stock, staleness, report redaction and absence of publication tasks. Standard lint, unit, race, build, PostgreSQL integration and race-enabled integration checks were run without live credentials. No external writes occurred.

Remaining blockers include explicit product and warehouse identity provenance, stock-baseline and reserve attribution, competing exposure and allocation evidence, source ordering and completeness, and posting coverage. No Ozon stock publication or order synchronization was attempted.
