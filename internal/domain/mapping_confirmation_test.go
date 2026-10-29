package domain

import (
	"strings"
	"testing"
)

func TestMappingConfirmationRequestValidation(t *testing.T) {
	base := MappingConfirmationRequest{ReviewID: strings.Repeat("a", 32), SessionID: "server-session", IdempotencyKey: "safe-key:1", Acknowledged: true, Explanation: "synthetic"}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*MappingConfirmationRequest)
	}{
		{"missing review", func(r *MappingConfirmationRequest) { r.ReviewID = "" }},
		{"nonhex review", func(r *MappingConfirmationRequest) { r.ReviewID = strings.Repeat("z", 32) }},
		{"missing session", func(r *MappingConfirmationRequest) { r.SessionID = "" }},
		{"missing key", func(r *MappingConfirmationRequest) { r.IdempotencyKey = "" }},
		{"long key", func(r *MappingConfirmationRequest) { r.IdempotencyKey = strings.Repeat("a", 129) }},
		{"key with space", func(r *MappingConfirmationRequest) { r.IdempotencyKey = "a b" }},
		{"key with unicode", func(r *MappingConfirmationRequest) { r.IdempotencyKey = "key-№" }},
		{"long explanation", func(r *MappingConfirmationRequest) { r.Explanation = strings.Repeat("a", 1001) }},
		{"nul explanation", func(r *MappingConfirmationRequest) { r.Explanation = "a\x00b" }},
		{"invalid utf8", func(r *MappingConfirmationRequest) { r.Explanation = string([]byte{0xff}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.mutate(&r)
			if r.Validate() == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	// A negative acknowledgement must reach idempotency comparison before it can
	// be rejected as a new confirmation, so changed retries are conflicts.
	base.Acknowledged = false
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
}
