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
		return r
	}
	if status != http.StatusTooManyRequests {
		return r
	}
	r.Cooldown = time.Second
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
	value = strings.TrimSpace(headers.Get("X-Lognex-Retry-After"))
	if millis, err := strconv.ParseUint(value, 10, 64); err == nil {
		if millis > uint64(maxCooldown/time.Millisecond) {
			r.IndefiniteCooldown = true
		} else {
			r.Cooldown = max(r.Cooldown, time.Duration(millis)*time.Millisecond)
		}
	} else if numeric(value) {
		r.IndefiniteCooldown = true
	}
	return r
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
