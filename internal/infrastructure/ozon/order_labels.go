package ozon

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

func (c *OrdersClient) DownloadOrderLabel(ctx context.Context, scope orders.Scope, rawURL string) ([]byte, error) {
	if scope != c.scope || c.read == nil || !c.read.coordinated {
		return nil, orders.ErrBlocked
	}
	if !c.consumeLabelURL(rawURL) {
		return nil, orders.ErrBlocked
	}
	downloader, err := newFBSLabelDownloaderForTask(c.read.http, rawURL)
	if err != nil {
		return nil, err
	}
	return downloader.Download(ctx, rawURL)
}

func (c *OrdersClient) ReadOrderLabelTask(ctx context.Context, scope orders.Scope, taskID string) (orders.LabelTask, error) {
	id, err := parseSKU(taskID)
	if err != nil || scope != c.scope {
		return orders.LabelTask{}, orders.ErrInvalid
	}
	data, err := c.orderPost(ctx, labelTaskPath, struct {
		TaskID int64 `json:"task_id"`
	}{id})
	if err != nil {
		return orders.LabelTask{}, err
	}
	task, err := decodeLabelTask(taskID, data)
	if err == nil {
		c.authorizeLabelURL(task)
	}
	return task, err
}

type labelTaskResponse struct {
	FileURL string `json:"file_url"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Status *struct {
		Code      string `json:"code"`
		Count     *int   `json:"postings_count"`
		Printed   *int   `json:"printed_postings_count"`
		Unprinted []struct {
			Number  string `json:"posting_number"`
			Message string `json:"message"`
		} `json:"unprinted_postings"`
	} `json:"status"`
}

func decodeLabelTask(id string, data []byte) (orders.LabelTask, error) {
	var response labelTaskResponse
	if err := decodeLabelJSON(data, &response); err != nil {
		return orders.LabelTask{}, err
	}
	if response.Status == nil || !slices.Contains([]string{"pending", "in_progress", "completed", "error"}, response.Status.Code) {
		return orders.LabelTask{}, orders.ErrSchemaDrift
	}
	task := orders.LabelTask{ID: id, Code: response.Status.Code, FileURL: response.FileURL}
	if response.Error != nil {
		task.ErrorCode = labelErrorCode(response.Error.Code)
		if task.ErrorCode == "" && response.Error.Message != "" {
			task.ErrorCode = "PROVIDER_ERROR_UNCLASSIFIED"
		}
	}
	if response.Status.Count != nil && response.Status.Printed != nil {
		task.CountsKnown = true
		task.PostingsCount = *response.Status.Count
		task.PrintedPostingsCount = *response.Status.Printed
		if task.PostingsCount < 0 || task.PrintedPostingsCount < 0 || task.PrintedPostingsCount > task.PostingsCount {
			return orders.LabelTask{}, errOrderPayload
		}
	}
	for _, row := range response.Status.Unprinted {
		task.UnprintedPostingNumbers = append(task.UnprintedPostingNumbers, row.Number)
	}
	return task, nil
}

func (c *OrdersClient) labelCreateReceipt(command orders.Command, data []byte, status int) (orders.Receipt, error) {
	var response struct {
		Tasks []struct {
			ID   int64  `json:"task_id"`
			Type string `json:"task_type"`
		} `json:"tasks"`
	}
	receipt := orderReceipt(command, orders.StateUnknown, status, "label_task_unverified")
	if err := decodeLabelJSON(data, &response); err != nil {
		receipt.Code = "LABEL_SCHEMA_DRIFT"
		return receipt, err
	}
	if len(response.Tasks) == 0 || len(response.Tasks) > 2 || command.PostingAction == nil {
		return receipt, orders.ErrStale
	}
	seen := make(map[string]bool, len(response.Tasks))
	ids := make(map[int64]bool, len(response.Tasks))
	for _, task := range response.Tasks {
		if task.ID <= 0 || ids[task.ID] || seen[task.Type] || !slices.Contains([]string{"big_label", "small_label"}, task.Type) {
			return receipt, orders.ErrStale
		}
		seen[task.Type] = true
		ids[task.ID] = true
		receipt.TaskIDs = append(receipt.TaskIDs, orders.ActionTask{ID: formatInt64(task.ID), Type: task.Type})
		if task.Type == command.PostingAction.Parameters.LabelType {
			receipt.TaskID = formatInt64(task.ID)
		}
	}
	if receipt.TaskID == "" {
		return receipt, orders.ErrStale
	}
	receipt.LabelTasksVerified = true
	receipt.State, receipt.Code = orders.StatePolling, "PROVIDER_LABEL_PENDING"
	return receipt, nil
}

func decodeLabelJSON(data []byte, target any) error {
	if ordercontract.CheckJSON(data) != nil {
		return errOrderPayload
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return orders.ErrSchemaDrift
	}
	return nil
}

// Authorization is transient: a new worker must read its task again, never
// persist or reuse a signed URL from another account or an expired response.
func (c *OrdersClient) authorizeLabelURL(task orders.LabelTask) {
	c.labelMu.Lock()
	defer c.labelMu.Unlock()
	c.labelURL, c.labelURLExpires = "", time.Time{}
	if task.Code != "completed" || task.ErrorCode != "" || !task.CountsKnown || task.PostingsCount != 1 || task.PrintedPostingsCount != 1 || len(task.UnprintedPostingNumbers) != 0 || task.FileURL == "" {
		return
	}
	c.labelURL, c.labelURLExpires = task.FileURL, c.now().Add(2*time.Minute)
}
func (c *OrdersClient) consumeLabelURL(raw string) bool {
	c.labelMu.Lock()
	defer c.labelMu.Unlock()
	if raw == "" || c.labelURL != raw || !c.now().Before(c.labelURLExpires) {
		return false
	}
	c.labelURL, c.labelURLExpires = "", time.Time{}
	return true
}

func labelErrorCode(raw string) string {
	if raw == "" {
		return ""
	}
	if len(raw) > 96 {
		return "PROVIDER_ERROR_UNCLASSIFIED"
	}
	for _, char := range raw {
		if char != '_' && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return "PROVIDER_ERROR_UNCLASSIFIED"
		}
	}
	return raw
}
