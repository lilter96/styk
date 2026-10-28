# ADR 002: PostgreSQL durable tasks and outbox

Status: accepted.

An observation and its outbox task commit in one PostgreSQL transaction. Workers claim rows with SKIP LOCKED and expiring leases, and claim tokens fence stale database updates. This uses existing infrastructure and survives process restarts. Claims and an outbox do not provide exactly-once execution of arbitrary external API calls: lease expiry can overlap execution and a response can be lost after a write. Real operations require explicit unknown-outcome handling, safe retry rules, ordering and reconciliation before introduction. This initial worker processes only a deterministic mock.
