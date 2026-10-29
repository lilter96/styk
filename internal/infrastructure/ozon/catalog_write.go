package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/lilter96/styk/internal/providercall"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lilter96/styk/internal/catalog"
)

type CatalogWriteError struct{ HTTPStatus int }

func (e *CatalogWriteError) Error() string { return ErrCatalogWriteUnknown.Error() }
func (e *CatalogWriteError) Unwrap() error { return ErrCatalogWriteUnknown }

type catalogPatchItem struct {
	OfferID    string             `json:"offer_id"`
	Attributes []CatalogAttribute `json:"attributes"`
}

// WriteCatalog performs one HTTP mutation attempt. No transport, status, schema,
// or asynchronous import failure is an instruction to resend this command.
func (c *CatalogClient) WriteCatalog(ctx context.Context, command catalog.Command) (catalog.Receipt, error) {
	frozen, err := c.frozenCatalogCommand(command)
	if err != nil {
		return rejectedCatalogReceipt(command.ID, err), err
	}
	command = frozen
	if err := c.validateCatalogCommand(command); err != nil {
		return rejectedCatalogReceipt(command.ID, err), err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ctx = catalogBudgetContext(ctx)
	path, payload, err := c.catalogWritePayload(ctx, command)
	if err != nil {
		return rejectedCatalogReceipt(command.ID, err), err
	}
	data, status, err := c.catalogMutation(ctx, command, path, payload)
	receipt := catalog.Receipt{CommandID: command.ID, State: catalog.ReceiptUnknown, HTTPStatus: status}
	if err != nil {
		if !errors.Is(err, ErrCatalogWriteUnknown) {
			receipt = rejectedCatalogReceipt(command.ID, err)
		}
		return receipt, err
	}
	taskID, err := catalogTaskID(path, data)
	if err != nil {
		receipt.State = catalog.ReceiptUnknown
		return receipt, &CatalogWriteError{HTTPStatus: status}
	}
	receipt.State, receipt.TaskID = catalog.ReceiptAccepted, taskID
	receipt.Products = []catalog.ProductReceipt{{ID: command.Target.ID, OfferID: command.Target.OfferID, Status: "accepted"}}
	return receipt, nil
}

func (c *CatalogClient) validateCatalogCommand(command catalog.Command) error {
	if err := catalog.ValidateCommand(command); err != nil {
		return err
	}
	if command.Scope != c.scope || command.Target.Account != (catalog.Account{Provider: catalog.ProviderOzon, ID: c.read.id}) || command.Parent != nil {
		return catalog.ErrInvalidScope
	}
	if !catalogTextValid(command.Target.OfferID, 50) || (command.Target.EntityType != "" && command.Target.EntityType != "product") {
		return catalog.ErrInvalidIdentity
	}
	if command.Action == catalog.ActionCreate {
		if command.Target.ID != "" || command.Target.SKU != "" {
			return catalog.ErrInvalidIdentity
		}
		return nil
	}
	if command.Action != catalog.ActionUpdate || !catalogPositiveID(command.Target.ID) || command.ExpectedTargetRevision == "" || len(command.Extensions) != 0 {
		return ErrCatalogUnsupported
	}
	for field, value := range command.Values {
		if (field != catalog.FieldName && field != catalog.FieldDescription) || value.State != catalog.StateKnown {
			return ErrCatalogUnsupported
		}
	}
	return nil
}

func (c *CatalogClient) catalogWritePayload(ctx context.Context, command catalog.Command) (string, []byte, error) {
	if command.Action == catalog.ActionCreate {
		return c.catalogCreatePayload(ctx, command)
	}
	snapshot, err := c.ReadCatalog(ctx, catalog.ReadRequest{Scope: c.scope, Account: command.Target.Account, Identities: []catalog.Identity{command.Target}, Limit: 1})
	if err != nil {
		return "", nil, err
	}
	if len(snapshot.Products) != 1 || snapshot.Products[0].Revision != command.ExpectedTargetRevision {
		return "", nil, catalog.ErrIdentityMismatch
	}
	attributes, err := c.catalogPatchAttributes(ctx, command, snapshot.Products[0])
	if err != nil {
		return "", nil, err
	}
	payload, err := json.Marshal(struct {
		Items []catalogPatchItem `json:"items"`
	}{Items: []catalogPatchItem{{OfferID: command.Target.OfferID, Attributes: attributes}}})
	return catalogPatchPath, payload, err
}

func (c *CatalogClient) catalogPatchAttributes(ctx context.Context, command catalog.Command, product catalog.Product) ([]CatalogAttribute, error) {
	var selection CategorySelection
	if decodeCatalogJSON(product.Fields[catalog.FieldCategory].Data, &selection) != nil {
		return nil, ErrCatalogUnsupported
	}
	schema, err := c.CategoryAttributes(ctx, selection)
	if err != nil {
		return nil, err
	}
	attributes := make([]CatalogAttribute, 0, len(command.Values))
	for _, field := range []catalog.Field{catalog.FieldName, catalog.FieldDescription} {
		value, selected := command.Values[field]
		if !selected {
			continue
		}
		text, textErr := value.StringValue()
		if textErr != nil || !validPatchText(field, text) {
			return nil, catalog.ErrInvalidValue
		}
		attributeID := int64(4180)
		if field == catalog.FieldDescription {
			attributeID = 4191
		}
		if !supportsTextAttribute(schema, attributeID) {
			return nil, catalogFactError(field, "FIELD_NOT_WRITABLE", "The current Ozon category does not support this text attribute", ErrCatalogUnsupported)
		}
		attributes = append(attributes, CatalogAttribute{ID: attributeID, Values: []AttributeValue{{Value: text}}})
	}
	return attributes, nil
}

func supportsTextAttribute(schema []CategoryAttribute, id int64) bool {
	for _, attribute := range schema {
		if attribute.ID == id {
			kind := strings.ToLower(attribute.Type)
			return attribute.ComplexID == 0 && attribute.DictionaryID == 0 && !attribute.Collection && (kind == "string" || kind == "multiline")
		}
	}
	return false
}

func validPatchText(field catalog.Field, value string) bool {
	if field == catalog.FieldName {
		return catalogTextValid(value, 500)
	}
	return catalogTextValid(value, 32768)
}

func catalogTextValid(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && strings.TrimSpace(value) != "" && !strings.ContainsRune(value, 0)
}

func (c *CatalogClient) catalogMutation(ctx context.Context, command catalog.Command, path string, payload []byte) ([]byte, int, error) {
	if (path != catalogPatchPath && path != catalogImportPath) || len(payload) == 0 || len(payload) > 1<<20 {
		return nil, 0, ErrCatalogUnsupported
	}
	if err := catalogMutationBudget(ctx, "requests"); err != nil {
		return nil, 0, err
	}
	if err := c.read.wait(ctx); err != nil {
		return nil, 0, err
	}
	authorize, err := c.consumeCatalogIntent(command)
	if err != nil {
		return nil, 0, err
	}
	u := c.read.base.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, 0, ErrCatalogUnsupported
	}
	request.Close, request.GetBody = true, nil
	request.Header.Set("Client-Id", c.read.id)
	request.Header.Set("Api-Key", c.read.key)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	requestContext, err := c.read.authorizeRequest(ctx, func(check context.Context) error {
		allowed, authErr := authorize(check)
		if authErr != nil || !allowed || !c.writeEnabled() || check.Err() != nil {
			return ErrCatalogWritesDisabled
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	request = request.WithContext(requestContext)
	if err := catalogMutationBudget(ctx, "attempts"); err != nil {
		return nil, 0, err
	}
	response, err := c.read.http.Do(request)
	observeCatalogRequest(ctx, path, response, err)
	if err != nil {
		if providercall.IsNotDispatched(err) {
			return nil, 0, ErrCatalogWritesDisabled
		}
		return nil, 0, &CatalogWriteError{}
	}
	return readCatalogMutation(response)
}

func readCatalogMutation(response *http.Response) ([]byte, int, error) {
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	closeErr := response.Body.Close()
	// EOF already established a complete body. A subsequent close failure
	// cannot invalidate a task acknowledgement parsed from that complete body.
	if closeErr != nil && readErr != nil {
		readErr = errors.Join(readErr, closeErr)
	}
	if readErr != nil || len(data) > 1<<20 || response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.StatusCode, &CatalogWriteError{HTTPStatus: response.StatusCode}
	}
	return data, response.StatusCode, nil
}

func catalogMutationBudget(ctx context.Context, resource string) error {
	if active, _ := ctx.Value(budgetKey{}).(*budget); active != nil {
		return active.add(resource, 1)
	}
	return nil
}

func catalogTaskID(path string, data []byte) (string, error) {
	var response struct {
		TaskID *integer `json:"task_id"`
		Result *struct {
			TaskID *integer `json:"task_id"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &response) != nil {
		return "", ErrCatalogContract
	}
	if path == catalogPatchPath && id(response.TaskID) != "" && response.Result == nil {
		return id(response.TaskID), nil
	}
	if path == catalogImportPath && response.Result != nil && response.TaskID == nil && id(response.Result.TaskID) != "" {
		return id(response.Result.TaskID), nil
	}
	return "", ErrCatalogContract
}
