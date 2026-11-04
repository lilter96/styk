package orders

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
