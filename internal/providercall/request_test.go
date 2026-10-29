package providercall

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestSyntheticProfileRequiresLoopbackEndpoint(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, endpoint string
		interval       time.Duration
	}{
		{"real", "https://api-seller.ozon.ru", time.Second},
		{"lookalike", "https://localhost.example.com", time.Second},
		{"userinfo", "http://localhost@api-seller.ozon.ru", time.Second},
		{"loopback", "http://127.0.0.1:12345", 20 * time.Millisecond},
		{"ipv6", "http://[::1]:12345", 20 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := Request{Scope: Scope{Tenant: "tenant", Provider: "ozon", Account: "account"}, Method: "POST /v3/posting/fbs/list", Operation: "orders_read", Endpoint: tc.endpoint}
			a, err := normalize(r, Options{SyntheticInterval: 20 * time.Millisecond, MaxInflight: 1})
			if err != nil || a.Interval != tc.interval {
				t.Fatalf("interval %s: %v", a.Interval, err)
			}
			if a.Method == r.Method || a.Operation == r.Operation || len(a.Method) != 64 {
				t.Fatal("raw method metadata persisted")
			}
		})
	}
}

func TestPendingScopeCannotClaimAccountNamespace(t *testing.T) {
	t.Parallel()
	r := Request{Scope: Scope{Tenant: "tenant", Provider: "moysklad", PendingID: "probe"}, Method: "GET /context/companysettings", Operation: "connect"}
	a, err := normalize(r, Options{MaxInflight: 1})
	if err != nil || a.Scope.Account[:8] != "pending:" || a.Scope.PendingID != "" {
		t.Fatalf("pending account: %+v %v", a.Scope, err)
	}
	r.Scope.Account = "actual-account"
	if _, err = normalize(r, Options{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("probe impersonated account")
	}
	r.Scope.PendingID = ""
	r.Scope.Account = a.Scope.Account
	if _, err = normalize(r, Options{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("account impersonated probe")
	}
}

func TestRetryAfterDoesNotPermitEarlyRetry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, value, millis string
		delay               time.Duration
		blocked             bool
	}{
		{"seconds", "12", "", 12 * time.Second, false},
		{"millis", "1", "1500", 1500 * time.Millisecond, false},
		{"excessive", "86401", "", time.Second, true},
		{"overflow", "999999999999999999999999999999", "", time.Second, true},
		{"overflow_millis", "", "99999999999999999999999999999", time.Second, true},
		{"invalid", "invalid", "", time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := responseResult(http.StatusTooManyRequests, http.Header{"Retry-After": {tc.value}, "X-Lognex-Retry-After": {tc.millis}}, time.Minute)
			if r.Cooldown != tc.delay || r.IndefiniteCooldown != tc.blocked {
				t.Fatalf("result %+v", r)
			}
		})
	}
	until := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	r := responseResult(429, http.Header{"Retry-After": {until.Format(http.TimeFormat)}}, time.Minute)
	if !r.CooldownUntil.Equal(until) {
		t.Fatal("HTTP date was reduced using application clock")
	}
	denied := responseResult(403, http.Header{"Retry-After": {"123"}}, time.Minute)
	if denied.Cooldown != 0 || denied.MethodCooldown != time.Minute {
		t.Fatal("method denial blocks whole account")
	}
	reset := responseResult(200, http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Lognex-Reset": {"1500"}}, time.Minute)
	if reset.Cooldown != 1500*time.Millisecond {
		t.Fatal("successful response discarded exhausted quota")
	}
	unavailable := responseResult(503, http.Header{"Retry-After": {"3"}}, time.Minute)
	if unavailable.Cooldown != 3*time.Second {
		t.Fatal("unavailable response discarded Retry-After")
	}
}

func TestForbiddenMethodScopeIncludesOperation(t *testing.T) {
	request := Request{Scope: Scope{Tenant: "merchant", Provider: "ozon", Account: "account", CredentialRevision: 1}, Method: "POST", Operation: "v3/posting/fbs/get", Timeout: time.Second}
	first, err := normalize(request, Options{MaxInflight: 1})
	if err != nil {
		t.Fatal(err)
	}
	request.Operation = "v2/products/stocks"
	second, err := normalize(request, Options{MaxInflight: 1})
	if err != nil {
		t.Fatal(err)
	}
	if first.Method == second.Method {
		t.Fatal("403 on one POST endpoint fenced an unrelated POST operation")
	}
	if first.Scope.Account != second.Scope.Account {
		t.Fatal("operations must still share account budget")
	}
}
