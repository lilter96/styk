package orders

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func nativePointReadFixture(t *testing.T) NativeOrderPointRead {
	t.Helper()
	target, started, finished := nativePointFixture(t)
	target.Fields.Description = Present("buyer-private@example.test")
	target.Fields.ShipmentAddress = Present("private-street")
	point, err := NewNativeOrderPoint(target, 7, started, finished)
	if err != nil {
		t.Fatal(err)
	}
	return NativeOrderPointRead{Correlation: point.Correlation, CredentialRevision: 7, StartedAt: started, FinishedAt: finished, Point: &point}
}

func nativePointPageFixture(t *testing.T) SourcePage {
	t.Helper()
	read := nativePointReadFixture(t)
	return SourcePage{Scope: read.Correlation.Scope, NativeReads: []NativeOrderPointRead{read}, ConfigRevision: 1, Credentials: CredentialRevisions{Moysklad: 7, Ozon: 3}, Stream: NativePointStream, FreshDetail: true, RefreshKey: "point-epoch", WindowFrom: read.StartedAt.Add(-time.Hour), WindowTo: read.FinishedAt, Complete: true, StartedAt: read.StartedAt, FinishedAt: read.FinishedAt, IdempotencyKey: "point-page", FenceToken: "point-lease"}
}

func TestNativePointReadRoundTripPreservesOnlySanitizedExactFacts(t *testing.T) {
	t.Parallel()
	read := nativePointReadFixture(t)
	raw, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "buyer-private") || strings.Contains(string(raw), "private-street") {
		t.Fatal("buyer facts escaped the native point allowlist")
	}
	var decoded NativeOrderPointRead
	if err = json.Unmarshal(raw, &decoded); err != nil || decoded.Validate() != nil {
		t.Fatal("valid exact-target read was rejected", err)
	}
	if decoded.Correlation != read.Correlation || decoded.CredentialRevision != 7 || !decoded.StartedAt.Equal(read.StartedAt) || !decoded.FinishedAt.Equal(read.FinishedAt) || !decoded.Point.ObservedAt.Equal(read.Point.ObservedAt) {
		t.Fatal("point read changed actual identity or nanosecond read clocks")
	}
	if err = nativePointPageFixture(t).Validate(); err != nil {
		t.Fatal("valid native point page rejected", err)
	}
}

func TestNativePointReadRejectsRawFailureOrSubstitutedSuccess(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(*NativeOrderPointRead)
	}{
		{"unknown code", func(r *NativeOrderPointRead) { r.Point, r.Code = nil, "NEW_PROVIDER_ENUM" }},
		{"raw PII error", func(r *NativeOrderPointRead) { r.Point, r.Code = nil, "HTTP 403 buyer-private@example.test" }},
		{"absent failure code", func(r *NativeOrderPointRead) { r.Point, r.Code = nil, "" }},
		{"success with failure", func(r *NativeOrderPointRead) { r.Code = "NATIVE_READ_FAILED" }},
		{"tenant substitution", func(r *NativeOrderPointRead) { r.Correlation.Scope.Tenant = "other" }},
		{"native account substitution", func(r *NativeOrderPointRead) { r.Correlation.Scope.MoyskladAccount = "other" }},
		{"Ozon account substitution", func(r *NativeOrderPointRead) { r.Correlation.Scope.OzonAccount = "other" }},
		{"revision substitution", func(r *NativeOrderPointRead) { r.CredentialRevision++ }},
		{"start clock substitution", func(r *NativeOrderPointRead) { r.StartedAt = r.StartedAt.Add(-time.Second) }},
		{"finish clock substitution", func(r *NativeOrderPointRead) { r.FinishedAt = r.FinishedAt.Add(time.Second) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			read := nativePointReadFixture(t)
			tc.edit(&read)
			if read.Validate() == nil {
				t.Fatal("substituted native point read accepted")
			}
		})
	}
}

func TestNativePointPageRejectsCrossStreamOrCrossScopeFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range nativePointPageInvalidCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			page := nativePointPageFixture(t)
			tc.edit(&page)
			if page.Validate() == nil {
				t.Fatal("invalid native page accepted")
			}
		})
	}
}

type nativePointPageMutation struct {
	name string
	edit func(*SourcePage)
}

func nativePointPageInvalidCases() []nativePointPageMutation {
	return []nativePointPageMutation{
		{"native reads in Ozon stream", func(p *SourcePage) { p.Stream = "ozon-orders-active" }},
		{"Ozon posting in native stream", func(p *SourcePage) { p.Postings = []Posting{{}} }},
		{"Ozon posting without native reads", func(p *SourcePage) { p.NativeReads, p.Postings = nil, []Posting{{}} }},
		{"scope substitution", func(p *SourcePage) { p.Scope.Tenant = "other" }},
		{"credential substitution", func(p *SourcePage) { p.Credentials.Moysklad++ }},
		{"duplicate exact read", func(p *SourcePage) { p.NativeReads = append(p.NativeReads, p.NativeReads[0]) }},
		{"read before page", func(p *SourcePage) { p.StartedAt = p.StartedAt.Add(time.Second) }},
		{"read after page", func(p *SourcePage) { p.FinishedAt = p.FinishedAt.Add(-time.Second) }},
		{"success and failure contradiction", func(p *SourcePage) {
			p.Complete = false
			p.NextCursor = "continuation"
			p.Failures = []ReadFailure{{p.NativeReads[0].Correlation.PostingNumber, "NATIVE_READ_FAILED"}}
		}},
	}
}

func nativePointFailurePage(t *testing.T) SourcePage {
	t.Helper()
	page := nativePointPageFixture(t)
	page.Complete, page.Exhausted = false, true
	page.NativeReads[0].Point, page.NativeReads[0].Code = nil, "NATIVE_READ_FAILED"
	page.Failures = []ReadFailure{{page.NativeReads[0].Correlation.PostingNumber, "NATIVE_READ_FAILED"}}
	return page
}

func TestNativePointFailureRequiresMatchingSanitizedSummaryAndPartialCoverage(t *testing.T) {
	t.Parallel()
	page := nativePointFailurePage(t)
	if err := page.Validate(); err != nil {
		t.Fatal("failed bounded read cannot be persisted truthfully", err)
	}
	page.Complete = true
	if page.Validate() == nil {
		t.Fatal("failed read claimed complete coverage")
	}
	page = nativePointFailurePage(t)
	page.Failures = nil
	if page.Validate() == nil {
		t.Fatal("failed point did not require its diagnostic summary")
	}
	page.Failures = []ReadFailure{{page.NativeReads[0].Correlation.PostingNumber, "NATIVE_ORDER_NOT_FOUND"}}
	if page.Validate() == nil {
		t.Fatal("failure summary changed the actual read outcome")
	}
}

func TestNativePointPageRetainsPriorFailureSummaryAcrossPartialPages(t *testing.T) {
	t.Parallel()
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "independent_success", true: "final_empty_page"}[empty], func(t *testing.T) {
			t.Parallel()
			page := nativePointPageFixture(t)
			page.Complete, page.Exhausted, page.NextCursor = false, true, ""
			page.Failures = []ReadFailure{{"prior-posting", "NATIVE_ORDER_NOT_FOUND"}}
			if empty {
				page.NativeReads = nil
			}
			if err := page.Validate(); err != nil {
				t.Fatal("prior page failure diagnostics discarded despite independent progress", err)
			}
		})
	}
}

func TestNativePointFailureNamespaceCannotEnterOzonPostingStream(t *testing.T) {
	t.Parallel()
	page := nativePointFailurePage(t)
	page.NativeReads, page.Stream = nil, "ozon-orders-active"
	if page.Validate() == nil {
		t.Fatal("native provider outcome was accepted as an Ozon posting read")
	}
}
