package ozon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestRelatedPostingDecoderPreservesMissingNullAndEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, body   string
		outer, inner orders.Presence
	}{
		{"missing", `{}`, orders.PresenceMissing, orders.PresenceMissing},
		{"null", `{"related_postings":null}`, orders.PresenceNull, orders.PresenceMissing},
		{"empty object", `{"related_postings":{}}`, orders.PresenceEmpty, orders.PresenceMissing},
		{"null list", `{"related_postings":{"related_posting_numbers":null}}`, orders.PresenceValue, orders.PresenceNull},
		{"empty list", `{"related_postings":{"related_posting_numbers":[]}}`, orders.PresenceValue, orders.PresenceEmpty},
		{"known siblings", `{"related_postings":{"related_posting_numbers":["9007199254740993-1","sibling"]}}`, orders.PresenceValue, orders.PresenceValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newOrderFields([]byte(tc.body))
			group := decodeOrderRelated(f)
			if *f.err != nil {
				t.Fatal(*f.err)
			}
			if tc.outer == orders.PresenceMissing {
				if group != nil {
					t.Fatal("absent group fabricated")
				}
				return
			}
			if group == nil || group.Presence != tc.outer || !sameRelatedPresence(group.PostingNumbers, tc.inner) {
				t.Fatalf("presence collapsed: %+v", group)
			}
			if tc.name == "known siblings" && (*group.PostingNumbers.Value)[0] != "9007199254740993-1" {
				t.Fatal("posting identity lost precision")
			}
		})
	}
}

func sameRelatedPresence(field orders.Field[[]string], wanted orders.Presence) bool {
	return field.Presence == wanted || wanted == orders.PresenceMissing && field.IsMissing()
}

func TestRelatedPostingDecoderRejectsInvalidShapeAndUnboundedNumbers(t *testing.T) {
	t.Parallel()
	cases := []string{`[]`, `"text"`, `1`, `true`, `{"related_posting_numbers":"a"}`, `{"related_posting_numbers":[1]}`, `{"related_posting_numbers":[null]}`, `{"related_posting_numbers":[""]}`, `{"related_posting_numbers":[" a"]}`, `{"related_posting_numbers":["a","a"]}`, `{"related_posting_numbers":["a\nb"]}`, `{"related_posting_numbers":["` + strings.Repeat("a", 201) + `"]}`, `{"related_posting_numbers":[` + strings.Repeat(`"a",`, 1000) + `"b"]}`}
	for _, body := range cases {
		t.Run(body[:min(len(body), 45)], func(t *testing.T) {
			t.Parallel()
			f := newOrderFields([]byte(`{"related_postings":` + body + `}`))
			decodeOrderRelated(f)
			if *f.err == nil {
				t.Fatal("invalid related posting facts accepted")
			}
		})
	}
}

func TestRelatedPostingUnknownFieldsAreDiagnosedWithoutCopyingValues(t *testing.T) {
	t.Parallel()
	f := newOrderFields([]byte(`{"related_postings":{"related_posting_numbers":["self"],"private@example.test":"PRIVATE_ADDRESS"}}`))
	group := decodeOrderRelated(f)
	if *f.err != nil || group == nil || len(*f.drift) != 1 || (*f.drift)[0].Path != "$.related_postings" {
		t.Fatal("new related field escaped diagnostics")
	}
	raw, err := json.Marshal(struct {
		Group *orders.RelatedPostingGroup
		Drift []orders.Drift
	}{group, *f.drift})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "private@example.test") {
		t.Fatal("unknown related data leaked")
	}
}

func TestRelatedPostingReadFlagIsDetailOnly(t *testing.T) {
	t.Parallel()
	var detailRequested atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var with map[string]json.RawMessage
		if err := json.Unmarshal(request["with"], &with); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path != orderGetPath || string(with["related_postings"]) != "true" {
			t.Error("detail related option missing")
		}
		detailRequested.Store(true)
		writeTestResponse(t, w, `{"result":{"posting_number":"self","order_id":"1","status":"awaiting_deliver","scheme":"FBS","parent_posting_number":"old-parent","child_posting_numbers":["actual-child"],"related_postings":{"related_posting_numbers":["self","sibling"]},"products":[{"sku":"1","offer_id":"offer","quantity":1}]}}`)
	}))
	defer server.Close()
	reader := newOrderTestClient(t, server, OrderReadOptions{RelatedPostings: true})
	posting, err := reader.ReadOrderPosting(context.Background(), orderTestScope(), "self")
	if err != nil || !detailRequested.Load() || posting.RelatedPostings == nil {
		t.Fatal("detail related group not observed", err)
	}
	if *posting.ParentPostingNumber.Value != "old-parent" || len(posting.ChildPostingNumbers) != 1 || posting.ChildPostingNumbers[0] != "actual-child" || (*posting.RelatedPostings.PostingNumbers.Value)[1] != "sibling" {
		t.Fatal("siblings replaced parent or child identities")
	}
	request := reader.orderPageRequest(orderListPath, orders.OrderWindow{From: time.Now().Add(-time.Hour), To: time.Now(), Limit: 10}, "")
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "related_postings") {
		t.Fatal("detail-only option leaked into unverified list contract")
	}
}
