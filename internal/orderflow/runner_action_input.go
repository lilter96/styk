package orderflow

import (
	"context"
	"reflect"

	"github.com/lilter96/styk/internal/orders"
)

func (r *Runner) withActionInput(ctx context.Context, command orders.Command) (orders.Command, error) {
	action := command.PostingAction
	if action == nil || action.ProtectedInput == nil {
		return command, nil
	}
	if r.options.ActionReader == nil || action.ProtectedInput.Revision != command.ConfigRevision {
		return command, orders.ErrBlocked
	}
	input, err := r.options.ActionReader.ReadOrderActionInput(ctx, command.Scope, command.PostingNumber, command.Action, *action.ProtectedInput)
	if err != nil {
		return command, err
	}
	if !reflect.DeepEqual(input.Public(), action.Parameters) {
		return command, orders.ErrStale
	}
	command.ActionInput = &input
	return command, nil
}
