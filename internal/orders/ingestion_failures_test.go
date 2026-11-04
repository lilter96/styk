package orders

import (
	"fmt"
	"testing"
)

func TestReadFailuresValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		page    SourcePage
		success map[string]bool
	}{
		{name: "provider body", page: SourcePage{Stream: "ozon-orders-active", FreshDetail: true, Failures: []ReadFailure{{PostingNumber: "p", Code: "buyer@example.test"}}}},
		{name: "duplicate", page: SourcePage{Stream: "ozon-orders-active", FreshDetail: true, Failures: []ReadFailure{{"p", "POSTING_NOT_FOUND"}, {"p", "POSTING_NOT_FOUND"}}}},
		{name: "success collision", page: SourcePage{Stream: "ozon-orders-active", FreshDetail: true, Failures: []ReadFailure{{"p", "POSTING_NOT_FOUND"}}}, success: map[string]bool{"p": true}},
		{name: "false complete", page: SourcePage{Stream: "ozon-orders-active", FreshDetail: true, Complete: true, Failures: []ReadFailure{{"p", "POSTING_NOT_FOUND"}}}},
		{name: "discovery exhaustion", page: SourcePage{Stream: "discovery", FreshDetail: true, Exhausted: true}},
		{name: "exhausted cursor", page: SourcePage{Stream: "ozon-orders-active", FreshDetail: true, Exhausted: true, NextCursor: "next"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.page.validateReadFailures(tc.success) == nil {
				t.Fatal("invalid failure evidence accepted")
			}
		})
	}
}

func TestReadFailuresRemainBoundedWithoutClaimingCompleteCoverage(t *testing.T) {
	t.Parallel()
	previous := make([]ReadFailure, MaxPagePostings)
	for index := range previous {
		previous[index] = ReadFailure{fmt.Sprintf("p-%d", index), "POSTING_NOT_FOUND"}
	}
	failures, truncated := MergeReadFailures(previous, []ReadFailure{{"extra", "POSTING_SCHEMA_DRIFT"}}, nil, false)
	if len(failures) != MaxPagePostings || !truncated {
		t.Fatal("epoch overflow silently lost")
	}
	failures, truncated = MergeReadFailures(failures, nil, []Posting{{PostingNumber: "p-0"}}, truncated)
	if len(failures) != MaxPagePostings-1 || !truncated {
		t.Fatal("partial recovery hid truncated diagnostics")
	}
}
