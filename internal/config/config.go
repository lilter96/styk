package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL, HTTPAddr                                                  string
	QueryTimeout, ShutdownTimeout, PollInterval, LeaseDuration, RetryDelay time.Duration
	MaxAttempts                                                            int
}

func Load() (Config, error) {
	if mode := value("STYK_DATABASE_POOL_MODE", "direct"); mode != "direct" && mode != "session" {
		return Config{}, fmt.Errorf("STYK_DATABASE_POOL_MODE must be direct or session: session advisory locks require a pinned backend")
	}
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), HTTPAddr: value("HTTP_ADDR", "127.0.0.1:8080")}
	var err error
	for _, p := range []struct {
		name, def string
		dst       *time.Duration
	}{{"QUERY_TIMEOUT", "5s", &c.QueryTimeout}, {"SHUTDOWN_TIMEOUT", "10s", &c.ShutdownTimeout}, {"WORKER_POLL_INTERVAL", "1s", &c.PollInterval}, {"WORKER_LEASE_DURATION", "30s", &c.LeaseDuration}, {"WORKER_RETRY_DELAY", "2s", &c.RetryDelay}} {
		*p.dst, err = time.ParseDuration(value(p.name, p.def))
		if err != nil || *p.dst <= 0 {
			return c, fmt.Errorf("%s must be a positive duration", p.name)
		}
	}
	c.MaxAttempts, err = strconv.Atoi(value("WORKER_MAX_ATTEMPTS", "3"))
	if err != nil || c.MaxAttempts < 1 || c.MaxAttempts > 100 {
		return c, fmt.Errorf("WORKER_MAX_ATTEMPTS must be between 1 and 100")
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" || u.Path == "" || u.Path == "/" {
		return c, fmt.Errorf("DATABASE_URL must be a PostgreSQL URL with host and database")
	}
	if _, _, err = net.SplitHostPort(c.HTTPAddr); err != nil {
		return c, fmt.Errorf("HTTP_ADDR must be host:port: %w", err)
	}
	if c.LeaseDuration <= c.QueryTimeout {
		return c, fmt.Errorf("WORKER_LEASE_DURATION must exceed QUERY_TIMEOUT")
	}
	return c, nil
}
func value(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
