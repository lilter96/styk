package orderflow

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) merchantIntentCurrent(ctx context.Context, work orders.Work) bool {
	intent := work.Command.MerchantIntent
	if intent == nil {
		return true
	}
	client, err := resolveOrderClient(ctx, r.resolver, work.Scope, "moysklad", work.Credentials.Moysklad)
	if err != nil {
		return false
	}
	correlation, err := client.ReadOrderCorrelation(ctx, intent.Correlation)
	if err != nil || correlation != intent.Correlation {
		return false
	}
	reader, ok := client.(orders.TargetReader)
	if !ok {
		return false
	}
	target, err := reader.ReadCustomerOrder(ctx, intent.Correlation)
	return err == nil && merchantTargetMatches(*intent, target)
}
