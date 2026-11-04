package ozon

import (
	"encoding/json"
	"strings"

	"github.com/lilter96/styk/internal/orders"
)

// Only the detail endpoint documents this option. Keep it out of list requests
// even when the same provider client has related detail reads enabled.
type orderRelatedReadOptions struct {
	OrderReadOptions
	RelatedPostings bool `json:"related_postings"`
}

func (c *OrdersClient) orderDetailOptions() orderRelatedReadOptions {
	return orderRelatedReadOptions{OrderReadOptions: c.options, RelatedPostings: c.options.RelatedPostings}
}

// Related numbers are provider-observed siblings, not child identities or proof
// that the original posting has disappeared. Preserve every presence state.
func decodeOrderRelated(f orderFields) *orders.RelatedPostingGroup {
	presence := f.objectPresence("related_postings")
	if presence == orders.PresenceMissing {
		return nil
	}
	group := &orders.RelatedPostingGroup{Presence: presence}
	if presence == orders.PresenceNull {
		return group
	}
	child := f.object("related_postings")
	group.PostingNumbers = orderField(child, "related_posting_numbers", decodeRelatedPostingNumbers)
	for key := range child.raw {
		if key != "related_posting_numbers" {
			*f.drift = append(*f.drift, orders.Drift{Path: f.path + ".related_postings", Code: "unknown_related_posting_field"})
			break
		}
	}
	if group.Validate() != nil {
		f.fail()
	}
	return group
}

func decodeRelatedPostingNumbers(data []byte) ([]string, error) {
	var numbers []string
	if json.Unmarshal(data, &numbers) != nil || numbers == nil || len(numbers) > 1000 {
		return nil, errOrderPayload
	}
	seen := make(map[string]bool, len(numbers))
	for _, number := range numbers {
		if number == "" || len(number) > 200 || strings.TrimSpace(number) != number || strings.ContainsAny(number, "\r\n\x00") || seen[number] {
			return nil, errOrderPayload
		}
		seen[number] = true
	}
	return numbers, nil
}
