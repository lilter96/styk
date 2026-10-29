package providercall

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxCooldown = 24 * time.Hour

func responseResult(status int, headers http.Header, forbidden time.Duration) Result {
	r := Result{Status: status}
	if status == http.StatusForbidden {
		r.MethodCooldown = min(forbidden, maxCooldown)
	}
	if status == http.StatusTooManyRequests {
		r.Cooldown = time.Second
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		applyRetryAfter(&r, headers)
	}
	if status == http.StatusTooManyRequests || strings.TrimSpace(headers.Get("X-RateLimit-Remaining")) == "0" {
		applyMillis(&r, headers.Get("X-Lognex-Reset"))
	}
	return r
}

func applyRetryAfter(r *Result, headers http.Header) {
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		if seconds > uint64(maxCooldown/time.Second) {
			r.IndefiniteCooldown = true
		} else {
			r.Cooldown = max(r.Cooldown, time.Duration(seconds)*time.Second)
		}
	} else if until, parseErr := http.ParseTime(value); parseErr == nil {
		r.CooldownUntil = until
	} else if numeric(value) {
		r.IndefiniteCooldown = true
	}
	applyMillis(r, headers.Get("X-Lognex-Retry-After"))
}

func applyMillis(r *Result, value string) {
	value = strings.TrimSpace(value)
	if millis, err := strconv.ParseUint(value, 10, 64); err == nil {
		if millis > uint64(maxCooldown/time.Millisecond) {
			r.IndefiniteCooldown = true
		} else {
			r.Cooldown = max(r.Cooldown, time.Duration(millis)*time.Millisecond)
		}
	} else if numeric(value) {
		r.IndefiniteCooldown = true
	}
}

func numeric(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
