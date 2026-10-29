package ozon

import (
	"context"
	"strings"

	"github.com/lilter96/styk/internal/catalog"
)

type catalogImportError struct {
	Code        string `json:"code"`
	Field       string `json:"field"`
	Level       string `json:"level"`
	AttributeID int64  `json:"attribute_id"`
}

type catalogImportItem struct {
	OfferID   string                `json:"offer_id"`
	ProductID *integer              `json:"product_id"`
	Status    string                `json:"status"`
	Errors    *[]catalogImportError `json:"errors"`
}

type catalogImportResponse struct {
	Result *struct {
		Items *[]catalogImportItem `json:"items"`
		Total *integer             `json:"total"`
	} `json:"result"`
}

// PollCatalog reads one persisted task without issuing any mutation. A task
// receipt proves queue acceptance only; each product's result is checked here.
func (c *CatalogClient) PollCatalog(ctx context.Context, command catalog.Command, previous catalog.Receipt) (catalog.Receipt, error) {
	taskID := previous.TaskID
	receipt := catalog.Receipt{CommandID: command.ID, TaskID: taskID, State: catalog.ReceiptUnknown}
	if previous.CommandID != command.ID || command.Scope != c.scope || command.Target.Account != (catalog.Account{Provider: catalog.ProviderOzon, ID: c.read.id}) || command.Target.OfferID == "" || !catalogPositiveID(taskID) {
		return receipt, catalog.ErrInvalidScope
	}
	var response catalogImportResponse
	status, err := c.catalogPostStatus(ctx, catalogImportInfoPath, map[string]string{"task_id": taskID}, &response)
	receipt.HTTPStatus = status
	if err != nil {
		return receipt, err
	}
	item, err := response.item(command.Target)
	if err != nil {
		return receipt, err
	}
	product := catalogImportProduct(item)
	receipt.Products, receipt.ExternalID = []catalog.ProductReceipt{product}, product.ID
	receipt.Errors = product.Errors
	receipt.State = catalogImportState(item.Status, product.ID, len(product.Errors))
	return receipt, nil
}

func (response catalogImportResponse) item(target catalog.Identity) (catalogImportItem, error) {
	if response.Result == nil || response.Result.Items == nil || response.Result.Total == nil {
		return catalogImportItem{}, ErrCatalogContract
	}
	items := *response.Result.Items
	if int64(*response.Result.Total) != int64(len(items)) || len(items) != 1 {
		return catalogImportItem{}, ErrCatalogContract
	}
	item := items[0]
	if item.OfferID != target.OfferID || !validImportStatus(item.Status) || item.Errors == nil || len(*item.Errors) > 100 || (target.ID != "" && id(item.ProductID) != "" && target.ID != id(item.ProductID)) {
		return catalogImportItem{}, ErrCatalogContract
	}
	return item, nil
}

func catalogImportProduct(item catalogImportItem) catalog.ProductReceipt {
	product := catalog.ProductReceipt{ID: id(item.ProductID), OfferID: item.OfferID, Status: item.Status, Errors: make([]catalog.FieldError, 0, len(*item.Errors))}
	for _, problem := range *item.Errors {
		product.Errors = append(product.Errors, catalogImportFieldError(problem))
	}
	return product
}

// The read client emits actual dispatch observations, including failed polls.
// Forward the caller's observer while retaining the final task-read HTTP status.
func (c *CatalogClient) catalogPostStatus(ctx context.Context, path string, body, out any) (int, error) {
	status := 0
	read := catalog.WithRequestObserver(ctx, func(event catalog.RequestEvent) {
		if event.Provider == catalog.ProviderOzon && event.Operation == path {
			status = event.Status
		}
		catalog.ObserveRequest(ctx, event)
	})
	err := c.catalogPost(read, path, body, out)
	return status, err
}

func catalogImportState(status, productID string, problems int) catalog.ReceiptState {
	switch status {
	case "pending":
		return catalog.ReceiptAccepted
	case "imported", "skipped":
		// Warning severity does not independently prove the approved effect.
		if productID != "" && problems > 0 {
			return catalog.ReceiptPartial
		}
		if productID != "" && problems == 0 {
			return catalog.ReceiptApplied
		}
	case "failed":
		if productID != "" {
			return catalog.ReceiptPartial
		}
		return catalog.ReceiptRejected
	}
	return catalog.ReceiptUnknown
}

func catalogImportFieldError(problem catalogImportError) catalog.FieldError {
	field := catalog.Field(problem.Field)
	if !field.Valid() {
		field = catalog.FieldAttributes
	}
	if problem.AttributeID == 4180 {
		field = catalog.FieldName
	}
	if problem.AttributeID == 4191 {
		field = catalog.FieldDescription
	}
	attributeID := problem.AttributeID
	if attributeID < 0 {
		attributeID = 0
	}
	return catalog.FieldError{Field: field, Code: safeCatalogCode(problem.Code), Severity: safeCatalogSeverity(problem.Level), AttributeID: attributeID, Message: "Ozon reported an import diagnostic"}
}

func safeCatalogCode(code string) string {
	switch code {
	case "warning_attribute_values_empty", "error_attribute_values_empty", "EMPTY_REQUIRED", "INVALID_VALUE_DICTIONARY_TYPE_ID", "description_category_not_found", "description_category_not_final", "price_is_negative", "item_limit_exceeded", "INVALID_ARGUMENT", "PERMISSION_DENIED", "UNAUTHENTICATED", "VALIDATION_ERROR", "INTERNAL_ERROR":
		return code
	default:
		return "OZON_IMPORT_ERROR"
	}
}

func safeCatalogSeverity(level string) string {
	switch level {
	case "warning", "error":
		return level
	default:
		return "unknown"
	}
}

func validImportStatus(status string) bool {
	return len(status) > 0 && len(status) <= 64 && strings.Trim(status, "abcdefghijklmnopqrstuvwxyz_") == ""
}
