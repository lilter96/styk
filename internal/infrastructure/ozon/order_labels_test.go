package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func TestLabelSchemaDriftAndPrivateErrorCannotBecomeSuccess(t *testing.T) {
	t.Parallel()
	if _, err := decodeLabelTask("1", []byte(`{"status":{"code":"pending"},"future_sensitive_key":"PRIVATE"}`)); !errors.Is(err, orders.ErrSchemaDrift) {
		t.Fatal("unknown field silently discarded")
	}
	if _, err := decodeLabelTask("1", []byte(`{"status":{"code":"pending"},"status":{"code":"completed"}}`)); err == nil {
		t.Fatal("duplicate status accepted")
	}
	task, err := decodeLabelTask("1", []byte(`{"status":{"code":"completed","postings_count":1,"printed_postings_count":1},"error":{"message":"PRIVATE@example.test"},"file_url":"https://api-seller.ozon.ru/PRIVATE"}`))
	if err != nil || task.ErrorCode == "" {
		t.Fatal("error message without code ignored")
	}
	raw, err := json.Marshal(task)
	if err != nil || bytes.Contains(raw, []byte("PRIVATE")) {
		t.Fatal("private task text entered public receipt")
	}
}

func TestLabelTaskUsesNumericIdentityAndExactCounts(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TaskID json.Number `json:"task_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.TaskID.String() != "9007199254740993" {
			t.Error("task identity changed")
		}
		writeTestResponse(t, w, `{"status":{"code":"completed","postings_count":1,"printed_postings_count":1,"unprinted_postings":[]},"file_url":"https://api-seller.ozon.ru/label.pdf"}`)
	}))
	defer server.Close()
	client := newOrderTestClient(t, server, OrderReadOptions{})
	task, err := client.ReadOrderLabelTask(context.Background(), orderTestScope(), "9007199254740993")
	if err != nil || !task.CountsKnown || task.PrintedPostingsCount != 1 || task.ID != "9007199254740993" {
		t.Fatalf("task: %+v %v", task, err)
	}
}

func TestLabelParsingDoesNotInventSuccessfulCounts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		body         string
		valid, known bool
	}{
		{`{"status":{"code":"completed"}}`, true, false},
		{`{"status":{"code":"new_enum"}}`, false, false},
		{`{"status":{"code":"completed","postings_count":1,"printed_postings_count":2}}`, false, false},
		{`{"status":{"code":"completed","postings_count":1,"printed_postings_count":0,"unprinted_postings":[{"posting_number":"posting","message":"PRIVATE"}]}}`, true, true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			t.Parallel()
			task, err := decodeLabelTask("1", []byte(tc.body))
			if (err == nil) != tc.valid || (tc.valid && task.CountsKnown != tc.known) {
				t.Fatalf("parse %+v %v", task, err)
			}
		})
	}
}

func TestLabelCreationSelectsRequestedTaskWithoutAddingUnsupportedRequestField(t *testing.T) {
	t.Parallel()
	command := orders.Command{ID: "command", Action: orders.ActionPostingLabelCreate, PostingNumber: "posting", PostingAction: &orders.PostingAction{Parameters: orders.ActionParameters{LabelType: "small_label"}}}
	receipt, err := (&OrdersClient{}).labelCreateReceipt(command, []byte(`{"tasks":[{"task_id":1,"task_type":"big_label"},{"task_id":9007199254740993,"task_type":"small_label"}]}`), 200)
	if err != nil || receipt.TaskID != "9007199254740993" || len(receipt.TaskIDs) != 2 {
		t.Fatalf("selected wrong task: %+v %v", receipt, err)
	}
	body, err := labelCreatePayload(command)
	if err != nil || len(body) != 1 || body["label_type"] != nil {
		t.Fatal("unsupported field sent")
	}
}

func TestLabelURLPermitExpiresAndRequiresCompleteTask(t *testing.T) {
	t.Parallel()
	now := time.Now()
	c := &OrdersClient{now: func() time.Time { return now }}
	task := orders.LabelTask{ID: "99", Code: "completed", CountsKnown: true, PostingsCount: 1, PrintedPostingsCount: 1, FileURL: "https://cdn.example.test/file"}
	c.authorizeLabelURL(task)
	if c.consumeLabelURL("https://different.example.test/file") {
		t.Fatal("different task URL admitted")
	}
	now = now.Add(3 * time.Minute)
	if c.consumeLabelURL(task.FileURL) {
		t.Fatal("expired URL admitted")
	}
	c.authorizeLabelURL(task)
	task.ErrorCode = "failed"
	c.authorizeLabelURL(task)
	if c.consumeLabelURL(task.FileURL) {
		t.Fatal("failed task retained stale URL authorization")
	}
	task.ErrorCode = ""
	task.CountsKnown = false
	c.authorizeLabelURL(task)
	if c.consumeLabelURL(task.FileURL) {
		t.Fatal("unknown counts authorized download")
	}
}

func TestLabelErrorCodeCannotPersistPrivateFreeText(t *testing.T) {
	t.Parallel()
	task, err := decodeLabelTask("99", []byte(`{"status":{"code":"error"},"error":{"code":"private@example.test","message":"Private address"}}`))
	if err != nil || task.ErrorCode != "PROVIDER_ERROR_UNCLASSIFIED" {
		t.Fatal("unsafe code was not redacted")
	}
	if labelErrorCode("LABEL_NOT_FOUND") != "LABEL_NOT_FOUND" {
		t.Fatal("structured error code lost")
	}
}
