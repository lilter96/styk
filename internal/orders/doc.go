/*
Package orders models one Ozon commercial order as independently identified
postings and reserves one MoySklad customer order correlation per posting.
Provider statuses are observations; only an explicit frozen Command can request
an action. Unknown outcomes are read-only until reconciliation.

Source chronology and collection chronology are independent. An observed-at
timestamp or local collection sequence is never a provider update watermark.
Serialized ingestion may choose an unversioned local head under its durable
fence while retaining source-order uncertainty and all superseded evidence.

PII is transient or separately encrypted and expires within the configured
retention limit. Ordinary DTOs preserve only protected references and safe schema
drift markers. Exact source amounts never pass through floating-point values.
Stock commitments and reserve accounting remain in the existing stock domain.
*/
package orders
