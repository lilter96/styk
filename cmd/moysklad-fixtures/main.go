// moysklad-fixtures is an isolated administrative tool for synthetic test data.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lilter96/styk/internal/moyskladfixture"
)

const manifestDir = ".local/moysklad-fixtures"

func run() error {
	f := flag.NewFlagSet("moysklad-fixtures", flag.ContinueOnError)
	mode := f.String("mode", "plan", "plan, init, inspect, execute, resume, enable-shipment, enable-multiple, enable-variants or verify-stock")
	namespace := f.String("namespace", "", "fixture namespace; generated for plan/init")
	timeout := f.Duration("timeout", 3*time.Minute, "whole-run timeout")
	requestTimeout := f.Duration("request-timeout", 7*time.Second, "per-request timeout")
	interval := f.Duration("interval", 500*time.Millisecond, "minimum time between requests")
	maxPosts := f.Int("max-posts", 22, "maximum creation requests")
	maxRequests := f.Int("max-requests", 200, "maximum total HTTP requests")
	if err := f.Parse(os.Args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 || *timeout <= 0 || *timeout > 10*time.Minute {
		return errors.New("invalid fixture settings")
	}
	if *namespace == "" && (*mode == "plan" || *mode == "init") {
		var err error
		*namespace, err = moyskladfixture.NewNamespace(time.Now())
		if err != nil {
			return err
		}
	}
	ops, err := moyskladfixture.Plan(*namespace)
	if err != nil {
		return err
	}
	if *mode == "plan" {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Namespace  string
			Operations []moyskladfixture.Operation
		}{*namespace, ops})
	}
	if *mode != "init" && *mode != "inspect" && *mode != "execute" && *mode != "resume" && *mode != "verify-stock" && *mode != "enable-shipment" && *mode != "enable-multiple" && *mode != "enable-variants" {
		return errors.New("unsupported fixture mode")
	}
	l, err := moyskladfixture.OpenLock(manifestDir, *namespace)
	if err != nil {
		return err
	}
	defer l.Close()
	account := os.Getenv("MOYSKLAD_ACCOUNT_ID")
	if *mode == "init" {
		m, err := l.Create(account)
		if err != nil {
			return err
		}
		fmt.Printf("Created local fixture manifest for namespace %s (%d planned operations). No network requests were made.\n", m.Namespace, len(m.Operations))
		return nil
	}
	m, err := l.Load()
	if err != nil {
		return err
	}
	if *mode == "inspect" {
		type operation struct {
			Key, Kind   string
			State       moyskladfixture.State
			HasRemoteID bool
		}
		result := struct {
			Namespace       string
			InitialCounts   map[string]int
			Operations      []operation
			StockChecks     map[string]moyskladfixture.StockResult
			StockCheckedAt  *time.Time
			StockMayBeStale bool
		}{Namespace: m.Namespace, InitialCounts: m.InitialCounts}
		result.StockChecks = m.StockChecks
		result.StockCheckedAt = m.StockCheckedAt
		result.StockMayBeStale = m.StockMayBeStale
		for _, o := range m.Operations {
			result.Operations = append(result.Operations, operation{o.Key, o.Kind, o.State, o.RemoteID != ""})
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	// These independent gates deliberately require a fresh, explicit operator
	// assertion each time, including when resuming after a process interruption.
	if account == "" || os.Getenv("STYK_FIXTURE_CONFIRM_ACCOUNT_ID") != account || os.Getenv("STYK_FIXTURE_ISOLATED_TEST_ACCOUNT") != "yes" {
		return errors.New("execution requires exact account confirmation and isolated test-account attestation")
	}
	hash, err := moyskladfixture.AccountHash(account)
	if err != nil || hash != m.AccountHash {
		return errors.New("fixture account does not match its manifest")
	}
	client, err := moyskladfixture.NewClient(account, os.Getenv("MOYSKLAD_TOKEN"), *requestTimeout, *interval, *maxRequests, nil)
	if err != nil {
		return err
	}
	if *mode == "enable-shipment" {
		if err = moyskladfixture.EnableSkippedShipment(l, &m, account); err != nil {
			return err
		}
		fmt.Println("Skipped shipment was replanned locally; no network request was made.")
		return nil
	}
	if *mode == "enable-multiple" {
		if err = moyskladfixture.EnableSkippedMultipleOrder(l, &m, account); err != nil {
			return err
		}
		fmt.Println("Skipped order was replanned locally with two products; the variant scenario remains unverified.")
		return nil
	}
	if *mode == "enable-variants" {
		if err = moyskladfixture.EnableVariantContinuation(l, &m, account); err != nil {
			return err
		}
		fmt.Println("Never-sent variants and two new fixture documents were planned locally; no network request was made.")
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if *mode == "verify-stock" {
		if err = client.ReadOnlyPreflight(ctx, account); err != nil {
			return errors.New("test-account preflight failed")
		}
		result, err := client.PollStock(ctx, m, 5, 2*time.Second)
		if err != nil {
			return errors.New("bounded stock observation failed")
		}
		now := time.Now().UTC()
		m.StockCheckedAt = &now
		m.StockChecks = result
		m.StockMayBeStale = false
		if err = l.Save(m); err != nil {
			return err
		}
		if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
			return err
		}
		if !moyskladfixture.AllStockMatches(result) {
			return errors.New("stock comparison incomplete or mismatched; inspect the recorded observations")
		}
		return nil
	}
	err = moyskladfixture.Run(ctx, l, &m, account, client, *maxPosts)
	counts := map[moyskladfixture.State]int{}
	for _, o := range m.Operations {
		counts[o.State]++
	}
	if e := json.NewEncoder(os.Stdout).Encode(struct {
		Namespace     string
		Counts        map[moyskladfixture.State]int
		InitialCounts map[string]int
	}{m.Namespace, counts, m.InitialCounts}); e != nil {
		return e
	}
	return err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "moysklad-fixtures:", err)
		os.Exit(1)
	}
}
