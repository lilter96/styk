package catalogsync

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

type providerPair struct {
	source        Provider
	target        Provider
	sourceAccount catalog.Account
	targetAccount catalog.Account
}

func (pair providerPair) forAccount(account catalog.Account) Provider {
	if account == pair.sourceAccount {
		return pair.source
	}
	return pair.target
}

func (runner Runner) resolvePair(ctx context.Context, work Work) (providerPair, error) {
	sourceAccount, targetAccount := work.Decision.Source.Identity.Account, work.Decision.Target.Identity.Account
	source, err := resolveProvider(ctx, runner.Resolver, work.Scope, sourceAccount, work.Credentials)
	if err != nil {
		return providerPair{}, err
	}
	target, err := resolveProvider(ctx, runner.Resolver, work.Scope, targetAccount, work.Credentials)
	if err != nil {
		return providerPair{}, err
	}
	return providerPair{source: source, target: target, sourceAccount: sourceAccount, targetAccount: targetAccount}, nil
}

func freshDecision(ctx context.Context, clients providerPair, work Work) bool {
	source, err := readProduct(ctx, clients.source, work.Scope, work.Decision.Source.Identity)
	if err != nil || !sameFacts(source, work.Decision.Source, true) {
		return false
	}
	if work.Command.Action == catalog.ActionCreate {
		if work.CreatedTarget != nil || !work.Decision.TargetAbsent {
			return false
		}
		snapshot, err := readCreationTarget(ctx, clients.target, work.Scope, creationEnrollment(work.Command))
		return err == nil && len(snapshot.Products) == 0
	}
	target, err := readProduct(ctx, clients.target, work.Scope, work.Decision.Target.Identity)
	return err == nil && sameFacts(target, work.Decision.Target, true)
}

func creationEnrollment(command catalog.Command) Enrollment {
	return Enrollment{Source: command.Source, Target: command.Target,
		Create: &Creation{ID: command.CreateID, ExternalCode: command.CreateExternalCode, Values: command.Values,
			Extensions: command.Extensions, Parent: command.Parent}}
}

func (runner Runner) recordReceipt(ctx context.Context, work Work, receipt catalog.Receipt) error {
	if !validReceiptFor(work.Command, receipt) {
		return runner.complete(ctx, work, Completion{State: StateUnknown, Code: "PROVIDER_RESULT_INVALID",
			Receipt: unknownReceipt(work.Command, receipt), NextRunAt: runner.now().Add(30 * time.Second)})
	}
	completion := Completion{Receipt: safeReceipt(receipt), CreatedTarget: createdIdentity(work.Command, receipt), NextRunAt: runner.now().Add(5 * time.Second)}
	switch receipt.State {
	case catalog.ReceiptAccepted:
		completion.State, completion.Code = StateProcessing, "TASK_ACCEPTED"
	case catalog.ReceiptApplied:
		completion.State, completion.Code = StateAcknowledged, "WRITE_ACKNOWLEDGED"
	case catalog.ReceiptRejected:
		completion.State, completion.Code = StateRejected, "PROVIDER_REJECTED"
	default:
		completion.State, completion.Code = StateUnknown, "PROVIDER_OUTCOME_UNKNOWN"
		if receipt.State == catalog.ReceiptUnknown && receipt.TaskID != "" {
			completion.State, completion.Code = StateProcessing, "TASK_OUTCOME_UNKNOWN"
		}
	}
	return runner.complete(ctx, work, completion)
}

func validReceiptFor(command catalog.Command, receipt catalog.Receipt) bool {
	if receipt.CommandID != command.ID || len(receipt.Products) > 1 || len(receipt.Errors) > 100 || !optionalID(receipt.TaskID) || !optionalID(receipt.ExternalID) {
		return false
	}
	if receipt.HTTPStatus != 0 && (receipt.HTTPStatus < 100 || receipt.HTTPStatus > 599) {
		return false
	}
	if command.Action != catalog.ActionCreate && receipt.ExternalID != "" && receipt.ExternalID != command.Target.ID {
		return false
	}
	if receipt.State == catalog.ReceiptAccepted && receipt.TaskID == "" {
		return false
	}
	if len(receipt.Products) == 0 {
		return true
	}
	product := receipt.Products[0]
	if product.ID != "" && receipt.ExternalID != "" && product.ID != receipt.ExternalID {
		return false
	}
	if !optionalID(product.ID) || !optionalID(product.OfferID) || len(product.Errors) > 100 {
		return false
	}
	if command.Action != catalog.ActionCreate && product.ID != "" && product.ID != command.Target.ID {
		return false
	}
	return command.Target.OfferID == "" || product.OfferID == "" || product.OfferID == command.Target.OfferID
}

func optionalID(value string) bool { return value == "" || validID(value) }

func unknownReceipt(command catalog.Command, receipt catalog.Receipt) catalog.Receipt {
	unknown := catalog.Receipt{CommandID: command.ID, State: catalog.ReceiptUnknown}
	if receipt.HTTPStatus >= 100 && receipt.HTTPStatus <= 599 {
		unknown.HTTPStatus = receipt.HTTPStatus
	}
	if receipt.CommandID == command.ID && len(receipt.Errors) <= 100 {
		unknown.Errors = safeFieldErrors(receipt.Errors)
	}
	return unknown
}

func createdIdentity(command catalog.Command, receipt catalog.Receipt) *catalog.Identity {
	if command.Action != catalog.ActionCreate {
		return nil
	}
	identity := command.Target
	identity.ID = receipt.ExternalID
	if identity.ID == "" && len(receipt.Products) == 1 {
		identity.ID = receipt.Products[0].ID
	}
	if identity.Validate() != nil {
		return nil
	}
	return &identity
}

func safeReceipt(receipt catalog.Receipt) catalog.Receipt {
	switch receipt.State {
	case catalog.ReceiptAccepted, catalog.ReceiptApplied, catalog.ReceiptRejected, catalog.ReceiptPartial, catalog.ReceiptUnknown:
	default:
		receipt.State = catalog.ReceiptUnknown
	}
	receipt.Errors = safeFieldErrors(receipt.Errors)
	products := make([]catalog.ProductReceipt, 0, len(receipt.Products))
	for _, product := range receipt.Products {
		product.Errors = safeFieldErrors(product.Errors)
		product.Status = safeProductStatus(product.Status)
		products = append(products, product)
	}
	receipt.Products = products
	return receipt
}

func safeProductStatus(status string) string {
	switch status {
	case "pending", "imported", "failed", "skipped", "applied", "accepted", "rejected":
		return status
	default:
		return ""
	}
}

func safeFieldErrors(fields []catalog.FieldError) []catalog.FieldError {
	result := make([]catalog.FieldError, 0, len(fields))
	for _, field := range fields {
		if !field.Field.Valid() {
			field.Field = ""
		}
		code := field.Code
		if !validCode(code) {
			code = "PROVIDER_FIELD_ERROR"
		}
		result = append(result, catalog.FieldError{Field: field.Field, Code: code})
	}
	return result
}

func validCode(code string) bool {
	if len(code) == 0 || len(code) > 64 {
		return false
	}
	for _, character := range code {
		if character != '_' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}
