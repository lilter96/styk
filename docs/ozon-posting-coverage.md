# Ozon FBS posting coverage

The tests use synthetic data and mocked HTTP; no production credentials or external writes are used. `TestPostingV4Pages` uses the top-level v4 response. The implementation asks for an explicit bounded window, and the list and detail reads occur at different times, so they are not an atomic snapshot.

| Requirement | Evidence |
| --- | --- |
| Empty list; explicit bounded window | `TestPostingEmptyAndWindowValidation` |
| v4 top-level wrapper and multiple cursor pages | `TestPostingV4Pages`, `TestPostingRejectLegacyWrapper` |
| Missing fields, duplicate posting, repeated/missing cursor, incomplete traversal | `TestPostingRejectIncompleteTraversalAndDuplicates`, `TestPostingImportAtomicityAndRetry` |
| Fully cancelled posting, one posting of a split order, repeated cancellation, unknown or missing reason | `TestPostingHistoryCancellationCompositionAndOrdering`, `TestPostingDetailCancellationAndUnknownFields` |
| Partial cancellation | `TestPostingHistoryCancellationCompositionAndOrdering` verifies changed observed lines only. The selected API fields do not prove the cause or restore quantities; partial cancellation is not inferred. |
| Quantity or composition changes without status change; identical repeated observations | `TestPostingHistoryCancellationCompositionAndOrdering` |
| Out-of-order observations, local and source times | `TestPostingHistoryCancellationCompositionAndOrdering`, `TestPostingDetailCancellationAndUnknownFields`, `TestPostingLegacyTimeAndCancellationUncertainty` |
| Confirmed, unconfirmed, missing, conflicting and unknown product correlation; confirmed/missing warehouse correlation | `TestPostingCorrelationScopeAndCurrentMappings` |
| Current mapping changes after an observation | `TestPostingCorrelationScopeAndCurrentMappings`; mapping history is not applied to older observations |
| Tenant/account query scoping, identical posting IDs and cross-scope mappings | `TestPostingCorrelationScopeAndCurrentMappings` also checks database foreign keys reject cross-scope observation inserts; scoped read isolation is not database authorization |
| Failed, cancelled and duplicate traversals; retry and previously committed data | `TestPostingImportAtomicityAndRetry` |
| Database insertion failure, duplicate posting within one batch, cancellation during insertion, safe retry | `TestPostingDatabaseRollbackAndCancellation`, `TestConcurrentPostingIdentityConflict` |
| 401, 403, 429/Retry-After, 5xx, timeout and retry cancellation | `TestPostingHTTPFailuresAndCancellation` |
| Unmatched line and DB rollback | `TestPostingsPersistence` |
| Retired list and external write endpoints rejected | `TestPostingReadOnlyAllowlist` |
| Private response fields ignored | `TestPostingV4Pages` |

The v3 detail schema reviewed does not provide a general source creation or update timestamp. `in_process_at` is the start of processing, and is kept separately; source creation/update times remain unknown. The earlier prototype has `observed_time_basis=batch_finished`; new HTTP detail reads record `detail_received`. A status or composition change between observations is not proof of the order or cause of external events. Repeat imports intentionally produce new append-only observations; the read order is by database IDs, not inferred source chronology. Direct DB users and trusted administrators can select another tenant/account; authenticated tenant ownership and row-level isolation are not implemented.
