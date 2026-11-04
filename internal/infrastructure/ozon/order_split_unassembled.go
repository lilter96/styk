package ozon

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

// This is a local response/request bound, not a claimed provider maximum.
const maxUnassembledSplitRows = 1000

func unassembledSplitPayload(command orders.Command) (map[string]any, error) {
	if command.Action != orders.ActionPostingSplitUnassembled || command.PostingAction == nil || !unassembledPostingID(command.PostingNumber) {
		return nil, orders.ErrInvalid
	}
	packages := command.PostingAction.Parameters.Packages
	if len(packages) < 2 || len(packages) > maxUnassembledSplitRows {
		return nil, orders.ErrInvalid
	}
	postings := make([]map[string]any, 0, len(packages))
	for _, box := range packages {
		if len(box.Lines) == 0 || len(box.Lines) > maxUnassembledSplitRows {
			return nil, orders.ErrInvalid
		}
		products := make([]map[string]any, 0, len(box.Lines))
		seen := make(map[string]bool, len(box.Lines))
		for _, line := range box.Lines {
			sku, err := parseSKU(line.Identity.SKU)
			quantity, qtyErr := unassembledPositiveInt(string(line.Quantity))
			if err != nil || qtyErr != nil || seen[line.Identity.SKU] {
				return nil, orders.ErrInvalid
			}
			seen[line.Identity.SKU] = true
			products = append(products, map[string]any{"product_id": sku, "quantity": quantity})
		}
		postings = append(postings, map[string]any{"products": products})
	}
	return map[string]any{"posting_number": command.PostingNumber, "postings": postings}, nil
}

func (c *OrdersClient) unassembledSplitReceipt(command orders.Command, data []byte, status int) (orders.Receipt, error) {
	receipt := orderReceipt(command, orders.StateUnknown, status, "SPLIT_UNASSEMBLED_RESPONSE_UNVERIFIED")
	var response struct {
		Parent   json.RawMessage `json:"parent_posting"`
		Postings json.RawMessage `json:"postings"`
	}
	if decodeLabelJSON(data, &response) != nil {
		return receipt, orders.ErrSchemaDrift
	}
	ack := &orders.UnassembledSplitAck{}
	parentErr := unassembledResponseField(response.Parent, &ack.Parent, decodeUnassembledPosting)
	postingsErr := unassembledResponseField(response.Postings, &ack.Postings, decodeUnassembledPostings)
	receipt.UnassembledSplit = ack
	if parentErr != nil || postingsErr != nil || status != 200 || command.Action != orders.ActionPostingSplitUnassembled || !unassembledAckValid(command, ack) {
		return receipt, orders.ErrSchemaDrift
	}
	receipt.State, receipt.Code = orders.StateAcknowledged, "SPLIT_UNASSEMBLED_ACKNOWLEDGED_REQUIRES_READBACK"
	return receipt, nil
}

func unassembledResponseField[T any](raw json.RawMessage, destination *orders.Field[T], decode func([]byte) (T, error)) error {
	if len(raw) == 0 {
		destination.Presence = orders.PresenceMissing
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		destination.Presence = orders.PresenceNull
		return nil
	}
	value, err := decode(raw)
	*destination = orders.Present(value)
	return err
}

func decodeUnassembledPostings(raw []byte) ([]orders.SplitResponsePosting, error) {
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) > maxUnassembledSplitRows {
		return nil, orders.ErrSchemaDrift
	}
	result := make([]orders.SplitResponsePosting, 0, len(rows))
	for _, row := range rows {
		posting, err := decodeUnassembledPosting(row)
		result = append(result, posting)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func decodeUnassembledPosting(raw []byte) (orders.SplitResponsePosting, error) {
	var wire struct {
		Number   json.RawMessage `json:"posting_number"`
		Products json.RawMessage `json:"products"`
	}
	var posting orders.SplitResponsePosting
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || decodeLabelJSON(raw, &wire) != nil {
		return posting, orders.ErrSchemaDrift
	}
	numberErr := unassembledResponseField(wire.Number, &posting.PostingNumber, orderString)
	productsErr := unassembledResponseField(wire.Products, &posting.Products, decodeUnassembledProducts)
	if numberErr != nil || productsErr != nil {
		return posting, orders.ErrSchemaDrift
	}
	return posting, nil
}

func decodeUnassembledProducts(raw []byte) ([]orders.SplitResponseProduct, error) {
	var rows []struct {
		SKU      int64 `json:"product_id"`
		Quantity int64 `json:"quantity"`
	}
	if decodeLabelJSON(raw, &rows) != nil || len(rows) > maxUnassembledSplitRows {
		return nil, orders.ErrSchemaDrift
	}
	products := make([]orders.SplitResponseProduct, 0, len(rows))
	seen := make(map[int64]bool, len(rows))
	for _, row := range rows {
		if row.SKU <= 0 || row.Quantity <= 0 || seen[row.SKU] {
			return products, orders.ErrSchemaDrift
		}
		seen[row.SKU] = true
		products = append(products, orders.SplitResponseProduct{SKU: strconv.FormatInt(row.SKU, 10), Quantity: orders.Decimal(strconv.FormatInt(row.Quantity, 10))})
	}
	return products, nil
}

func unassembledAckValid(command orders.Command, ack *orders.UnassembledSplitAck) bool {
	if !ack.Parent.IsMissing() {
		if !ack.Parent.IsKnown() || !unassembledParentValid(command.PostingNumber, *ack.Parent.Value) {
			return false
		}
	}
	if !ack.Postings.IsKnown() || len(*ack.Postings.Value) == 0 {
		return false
	}
	seen := make(map[string]bool, len(*ack.Postings.Value))
	for _, posting := range *ack.Postings.Value {
		if !unassembledResponsePostingValid(posting) || seen[*posting.PostingNumber.Value] {
			return false
		}
		seen[*posting.PostingNumber.Value] = true
	}
	return true
}

func unassembledResponsePostingValid(posting orders.SplitResponsePosting) bool {
	return posting.PostingNumber.IsKnown() && unassembledPostingID(*posting.PostingNumber.Value) && posting.Products.IsKnown() && len(*posting.Products.Value) > 0
}

func unassembledPostingID(value string) bool {
	return value != "" && len(value) <= 200 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\x00")
}

func unassembledPositiveInt(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != value {
		return 0, orders.ErrInvalid
	}
	return parsed, nil
}

func unassembledParentValid(expected string, parent orders.SplitResponsePosting) bool {
	if !parent.PostingNumber.IsMissing() && (!parent.PostingNumber.IsKnown() || *parent.PostingNumber.Value != expected) {
		return false
	}
	return parent.Products.IsMissing() || parent.Products.IsKnown() || parent.Products.Presence == orders.PresenceEmpty
}
