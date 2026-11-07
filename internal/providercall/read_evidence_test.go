package providercall

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadEvidencePreservesBodyAndNeverObservesWrites(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path string
		observed   bool
	}{{"finance read", "/v2/finance/realization", true}, {"carriage read", "/v2/posting/fbs/act/get-postings", true}, {"carriage write", "/v1/carriage/approve", false}, {"native write", "/api/remap/1.2/entity/commissionreportin", false}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			req := httptest.NewRequest(http.MethodPost, "https://provider.example"+tc.path, strings.NewReader(`{"date":"2026-10-01"}`))
			req = req.WithContext(WithReadObserver(req.Context(), func(e ReadEvidence) error {
				calls++
				if e.Path != tc.path || e.Status != 200 || string(e.Body) != `{"result":[]}` {
					t.Fatal("incorrect observation", e.Path, e.Status)
				}
				return nil
			}))
			response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"result":[]}`))}
			if err := observeProviderRead(req, Scope{}, response, nil); err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			if err != nil || string(body) != `{"result":[]}` {
				t.Fatal("observer altered adapter input", err)
			}
			if (calls == 1) != tc.observed {
				t.Fatal("unexpected observer calls", calls)
			}
		})
	}
}

func TestReadEvidenceRecordsTransportFailureAndPropagatesPersistenceFailure(t *testing.T) {
	t.Parallel()
	network := errors.New("network unavailable")
	persistence := errors.New("evidence unavailable")
	calls := 0
	req := httptest.NewRequest(http.MethodPost, "https://provider.example/v1/carriage/get", nil)
	req = req.WithContext(WithReadObserver(req.Context(), func(e ReadEvidence) error {
		calls++
		if !errors.Is(e.Failure, network) || e.Status != 0 {
			t.Fatal("lost network outcome")
		}
		return persistence
	}))
	err := observeProviderRead(req, Scope{}, nil, network)
	if !errors.Is(err, network) || !errors.Is(err, persistence) || calls != 1 {
		t.Fatal("evidence failure hidden", err, calls)
	}
}

func TestReadEvidenceJournalsResponseWhenRequestDigestFails(t *testing.T) {
	t.Parallel()
	failure := errors.New("request replay body unavailable")
	req := httptest.NewRequest(http.MethodPost, "https://provider.example/v1/carriage/get", nil)
	req.GetBody = func() (io.ReadCloser, error) { return nil, failure }
	called := false
	req = req.WithContext(WithReadObserver(req.Context(), func(e ReadEvidence) error {
		called = true
		if e.Status != 200 || !errors.Is(e.Failure, failure) || string(e.Body) != "actual" {
			t.Fatal("actual read was lost")
		}
		return nil
	}))
	response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("actual"))}
	if err := observeProviderRead(req, Scope{}, response, nil); !errors.Is(err, failure) || !called {
		t.Fatal("digest failure hid observed response", err)
	}
}
