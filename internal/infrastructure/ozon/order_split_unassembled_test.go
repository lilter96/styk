package ozon

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lilter96/styk/internal/orders"
)

func unassembledTestCommand() orders.Command {
	line := orders.ActionQuantity{Identity: orders.LineIdentity{SKU: "9007199254740993"}, Quantity: "2147483648"}
	return orders.Command{ID: "split-intent", Action: orders.ActionPostingSplitUnassembled, PostingNumber: "TEST-parent", PostingAction: &orders.PostingAction{Parameters: orders.ActionParameters{Packages: []orders.ActionPackage{{Lines: []orders.ActionQuantity{line}}, {Lines: []orders.ActionQuantity{line}}}}}}
}

func TestUnassembledSplitPayloadPreservesInt64AndDistinctEndpointShape(t *testing.T) {
	t.Parallel()
	command := unassembledTestCommand()
	payload, err := unassembledSplitPayload(command)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"product_id":9007199254740993`)) || !bytes.Contains(raw, []byte(`"quantity":2147483648`)) || !bytes.Contains(raw, []byte(`"postings":`)) || bytes.Contains(raw, []byte(`"packages":`)) {
		t.Fatal("SKU/quantity precision or split-without-assembly shape lost")
	}
}

func TestUnassembledSplitPayloadRejectsInvalidLocalIntent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		alter func(*orders.Command)
	}{
		{"wrong action", func(c *orders.Command) { c.Action = orders.ActionPostingSplit }},
		{"nil action", func(c *orders.Command) { c.PostingAction = nil }},
		{"no packages", func(c *orders.Command) { c.PostingAction.Parameters.Packages = nil }},
		{"one package", func(c *orders.Command) { c.PostingAction.Parameters.Packages = c.PostingAction.Parameters.Packages[:1] }},
		{"empty lines", func(c *orders.Command) { c.PostingAction.Parameters.Packages[0].Lines = nil }},
		{"SKU overflow", func(c *orders.Command) {
			c.PostingAction.Parameters.Packages[0].Lines[0].Identity.SKU = "9223372036854775808"
		}},
		{"quantity overflow", func(c *orders.Command) {
			c.PostingAction.Parameters.Packages[0].Lines[0].Quantity = "9223372036854775808"
		}},
		{"fraction", func(c *orders.Command) { c.PostingAction.Parameters.Packages[0].Lines[0].Quantity = "1.5" }},
		{"zero", func(c *orders.Command) { c.PostingAction.Parameters.Packages[0].Lines[0].Quantity = "0" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := unassembledTestCommand()
			tc.alter(&c)
			if _, err := unassembledSplitPayload(c); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

const unassembledChildren = `"postings":[{"posting_number":"TEST-child-a","products":[{"product_id":9007199254740993,"quantity":2147483648}]},{"posting_number":"TEST-child-b","products":[{"product_id":9007199254740993,"quantity":2147483648}]}]`

func TestUnassembledSplitReceiptPreservesOptionalParentWithoutInventingAllocation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, parent string
		presence     orders.Presence
	}{
		{"absent", "", orders.PresenceMissing},
		{"empty object", `"parent_posting":{},`, orders.PresenceValue},
		{"identity only", `"parent_posting":{"posting_number":"TEST-parent"},`, orders.PresenceValue},
		{"full original information", `"parent_posting":{"posting_number":"TEST-parent","products":[{"product_id":9007199254740993,"quantity":4294967296}]},`, orders.PresenceValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &OrdersClient{}
			r, err := c.unassembledSplitReceipt(unassembledTestCommand(), []byte("{"+tc.parent+unassembledChildren+"}"), 200)
			if err != nil || r.State != orders.StateAcknowledged || r.UnassembledSplit == nil {
				t.Fatal("valid acknowledgment blocked", err)
			}
			if len(r.PostingNumbers) != 0 || r.UnassembledSplit.Parent.Presence != tc.presence {
				t.Fatal("allocation invented or parent presence lost")
			}
			p := (*r.UnassembledSplit.Postings.Value)[0]
			if (*p.Products.Value)[0].SKU != "9007199254740993" || (*p.Products.Value)[0].Quantity != "2147483648" {
				t.Fatal("exact response number lost")
			}
		})
	}
}

func TestUnassembledSplitMalformedOrInsufficientReceiptRemainsUnknown(t *testing.T) {
	t.Parallel()
	valid := "{" + unassembledChildren + "}"
	cases := []struct{ name, payload string }{
		{"lost", ``}, {"malformed", `{`}, {"missing", `{}`}, {"null", `{"postings":null}`}, {"empty", `{"postings":[]}`},
		{"ship wrapper", `{"result":["TEST-child-a","TEST-child-b"]}`},
		{"wrong parent", `{"parent_posting":{"posting_number":"OTHER"},` + unassembledChildren + `}`},
		{"null parent", `{"parent_posting":null,` + unassembledChildren + `}`},
		{"duplicate posting", strings.Replace(valid, "TEST-child-b", "TEST-child-a", 1)},
		{"bad quantity", strings.Replace(valid, `"quantity":2147483648`, `"quantity":0`, 1)},
		{"string SKU", strings.Replace(valid, `"product_id":9007199254740993`, `"product_id":"9007199254740993"`, 1)},
		{"unknown field", strings.Replace(valid, `"postings":`, `"PRIVATE_KEY":"PRIVATE_VALUE","postings":`, 1)},
		{"duplicate key", strings.Replace(valid, `"postings":`, `"postings":[],"postings":`, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := (&OrdersClient{}).unassembledSplitReceipt(unassembledTestCommand(), []byte(tc.payload), 200)
			if err == nil || r.State != orders.StateUnknown || len(r.PostingNumbers) != 0 {
				t.Fatal("uncertain mutation became confirmed")
			}
		})
	}
}

func TestUnassembledSplitUnknownRetainsParsableFacts(t *testing.T) {
	t.Parallel()
	raw := `{"parent_posting":null,` + unassembledChildren + `}`
	r, err := (&OrdersClient{}).unassembledSplitReceipt(unassembledTestCommand(), []byte(raw), 200)
	if err == nil || r.UnassembledSplit == nil || r.UnassembledSplit.Parent.Presence != orders.PresenceNull || !r.UnassembledSplit.Postings.IsKnown() {
		t.Fatal("known response facts erased on uncertainty")
	}
	r, err = (&OrdersClient{}).unassembledSplitReceipt(unassembledTestCommand(), []byte("{"+unassembledChildren+"}"), 500)
	if err == nil || r.State != orders.StateUnknown || r.HTTPStatus != 500 {
		t.Fatal("non200 became successful")
	}
}
