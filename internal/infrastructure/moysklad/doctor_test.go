package moysklad

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDoctorProbeBoundedReadAndAccount(t *testing.T) {
	for _, account := range []string{"selected", "other"} {
		t.Run(account, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/entity/product" || r.URL.Query().Get("limit") != "1" {
					t.Error("unbounded or unsafe probe")
				}
				fmt.Fprintf(w, `{"meta":{"size":1},"rows":[{"id":"item","accountId":%q}]}`, account)
			}))
			defer s.Close()
			c, err := New(Options{BaseURL: s.URL + "/", Token: "synthetic", Timeout: time.Second, Interval: time.Millisecond, Backoff: time.Millisecond, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 1})
			if err != nil {
				t.Fatal(err)
			}
			verified, err := c.Probe(context.Background(), "selected")
			if calls != 1 || (account == "selected" && (!verified || err != nil)) || (account == "other" && err == nil) {
				t.Fatal("invalid probe result")
			}
		})
	}
}
