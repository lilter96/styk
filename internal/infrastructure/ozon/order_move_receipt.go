package ozon

import (
	"bytes"
	"encoding/json"

	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
)

type moveStatusResponse struct {
	Result []struct {
		PostingNumber string          `json:"posting_number"`
		Result        *bool           `json:"result"`
		Error         json.RawMessage `json:"error"`
	} `json:"result"`
}

// Official move examples disagree with their shared schema about error being
// string or string-array. Neither a missing result nor contradictory errors
// establish a safe rejection or acknowledgement after a dispatched mutation.
func moveStatusReceipt(command orders.Command, data []byte, status int, prefix, acknowledged string) (orders.Receipt, error) {
	var response moveStatusResponse
	unverified := prefix + "_unverified"
	if prefix == "tracking" {
		unverified = "tracking_confirmation_unverified"
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if ordercontract.CheckJSON(data) != nil || decoder.Decode(&response) != nil || len(response.Result) != 1 {
		return orderReceipt(command, orders.StateUnknown, status, unverified), orders.ErrStale
	}
	target := response.Result[0]
	failed, valid := moveStatusError(target.Error)
	if target.PostingNumber != command.PostingNumber || target.Result == nil || !valid || *target.Result && failed {
		return orderReceipt(command, orders.StateUnknown, status, unverified), orders.ErrStale
	}
	if !*target.Result {
		// This field is provider-controlled free text, not a safe stable error code.
		// Never copy a buyer's address, phone or email into an ordinary receipt.
		return orderReceipt(command, orders.StateRejected, status, prefix+"_rejected"), nil
	}
	return orderReceipt(command, orders.StateAcknowledged, status, acknowledged), nil
}

func moveStatusError(raw json.RawMessage) (failed, valid bool) {
	if len(raw) == 0 {
		return false, true
	}
	var message string
	if json.Unmarshal(raw, &message) == nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return message != "", true
	}
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, false
	}
	for _, cell := range messages {
		if bytes.Equal(bytes.TrimSpace(cell), []byte("null")) || json.Unmarshal(cell, &message) != nil {
			return false, false
		}
		if message != "" {
			failed = true
		}
	}
	return failed, true
}
