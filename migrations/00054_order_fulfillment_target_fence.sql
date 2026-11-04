-- +goose Up
-- Provider actions target an Ozon posting, independent of which MoySklad
-- projection/account initiated them. Existing unresolved history is retained:
-- conflicting active rows intentionally make this migration fail closed.
CREATE UNIQUE INDEX orders_work_active_fulfillment_target ON orders_work
 (tenant_id,ozon_account,posting_number)
 WHERE action LIKE 'posting_%'
 AND state IN ('QUEUED','CLAIMED','DISPATCHING','ACKNOWLEDGED','POLLING','UNKNOWN');

-- +goose Down
DROP INDEX orders_work_active_fulfillment_target;
