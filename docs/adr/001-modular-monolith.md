# ADR 001: Modular monolith

Status: accepted.

STYK starts as one Go module with API and worker processes sharing domain and infrastructure. A small team can reason about transactions and deployments without distributed service coordination. Package boundaries preserve room for future integrations. We can extract services later when operational needs justify the added complexity.
