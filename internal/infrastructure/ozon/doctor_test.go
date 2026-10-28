package ozon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDoctorProbeIsOneReadOnlyPOST(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var b struct {
			Limit int `json:"limit"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Limit != 1 || r.Method != "POST" || r.URL.Path != "/v3/product/list" || r.Header.Get("Client-Id") != "selected" {
			t.Error("unsafe probe")
		}
		fmt.Fprint(w, `{"result":{"items":[]}}`)
	}))
	defer s.Close()
	c, err := New(Options{BaseURL: s.URL, ClientID: "selected", APIKey: "synthetic", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Probe(context.Background()); err != nil || calls != 1 {
		t.Fatal("probe failed", err)
	}
}
