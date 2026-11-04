package orders

import (
	"context"
	"strconv"
	"time"
)

// These are sanitized response facts, not an independently observed inventory
// frontier. Ozon's product_id in this endpoint is a SKU, never a catalog ID.
type SplitResponseProduct struct {
	SKU      string  `json:"sku"`
	Quantity Decimal `json:"quantity"`
}

type SplitResponsePosting struct {
	PostingNumber Field[string]                 `json:"posting_number"`
	Products      Field[[]SplitResponseProduct] `json:"products"`
}

// Parent is described only as information about the original posting. Neither
// its presence nor its products prove retirement or a retained remainder.
type UnassembledSplitAck struct {
	Parent   Field[SplitResponsePosting]   `json:"parent"`
	Postings Field[[]SplitResponsePosting] `json:"postings"`
}

// SplitReadback is one actual point read. Unavailability never establishes
// deletion or retirement. Posting observations use the existing source store.
type SplitReadback struct {
	PostingNumber string
	Posting       *Posting
	Code          string
	CheckedAt     time.Time
}

type SplitReadStore interface {
	OrderSplitReadbacks(context.Context, Scope, string) ([]SplitReadback, error)
}

// UnassembledSplitCandidates returns only response identities plus the exact
// original request target. The original is a candidate to READ, not a member
// of the resulting allocation until independent details establish that fact.
func UnassembledSplitCandidates(command Command, receipt Receipt) ([]string, error) {
	ack := receipt.UnassembledSplit
	if command.Action != ActionPostingSplitUnassembled || receipt.CommandID != command.ID || receipt.HTTPStatus != 200 || receipt.ObservedAt.IsZero() || ack == nil || ack.Parent.Validate() != nil || ack.Postings.Validate() != nil {
		return nil, ErrBlocked
	}
	if !ack.Parent.IsMissing() && (!ack.Parent.IsKnown() || !validUnassembledParent(command.PostingNumber, *ack.Parent.Value)) {
		return nil, ErrBlocked
	}
	if !ack.Postings.IsKnown() || len(*ack.Postings.Value) == 0 || len(*ack.Postings.Value) > 1000 {
		return nil, ErrBlocked
	}
	seen := make(map[string]bool, len(*ack.Postings.Value)+1)
	result := make([]string, 0, len(*ack.Postings.Value)+1)
	for _, posting := range *ack.Postings.Value {
		if posting.PostingNumber.Validate() != nil || !posting.PostingNumber.IsKnown() || !validID(*posting.PostingNumber.Value) || posting.Products.Validate() != nil || !posting.Products.IsKnown() || !validSplitProducts(*posting.Products.Value) || seen[*posting.PostingNumber.Value] {
			return nil, ErrBlocked
		}
		seen[*posting.PostingNumber.Value] = true
		result = append(result, *posting.PostingNumber.Value)
	}
	if !validID(command.PostingNumber) {
		return nil, ErrInvalid
	}
	if !seen[command.PostingNumber] {
		result = append(result, command.PostingNumber)
	}
	return result, nil
}

func validUnassembledParent(expected string, parent SplitResponsePosting) bool {
	if parent.PostingNumber.Validate() != nil || parent.Products.Validate() != nil {
		return false
	}
	if !parent.PostingNumber.IsMissing() && (!parent.PostingNumber.IsKnown() || *parent.PostingNumber.Value != expected) {
		return false
	}
	return parent.Products.IsMissing() || parent.Products.Presence == PresenceEmpty || parent.Products.IsKnown() && validSplitProducts(*parent.Products.Value)
}

func validSplitProducts(products []SplitResponseProduct) bool {
	if len(products) == 0 || len(products) > 1000 {
		return false
	}
	seen := make(map[string]bool, len(products))
	for _, product := range products {
		sku, err := strconv.ParseInt(product.SKU, 10, 64)
		if err != nil || sku <= 0 || strconv.FormatInt(sku, 10) != product.SKU || seen[product.SKU] {
			return false
		}
		if _, valid := fulfillmentQuantity(product.Quantity); !valid {
			return false
		}
		seen[product.SKU] = true
	}
	return true
}
