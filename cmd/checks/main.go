// checks is the shared local/CI verification entry point. It never reads .env.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/lilter96/styk/internal/checkpolicy"
)

type options struct{ base, head, format, lane string }

// Leave room inside the five-minute wall budget for terminating process groups
// and removing the owned PostgreSQL container after a deadline or failure.
const verificationBudget = 5 * time.Minute
const verificationCleanupReserve = 20 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "checks:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "plan" && args[0] != "run") {
		return errors.New("usage: checks plan|run [--base REF] [--head REF] [--format text|json|github] [--lane fast|go|web|browser|presentation|integration|full|unit-all|integration-all|browser-stack]")
	}
	if args[0] == "run" {
		var cancel context.CancelFunc
		ctx, cancel = verificationContext(ctx)
		defer cancel()
	}
	o, err := parseOptions(args[1:])
	if err != nil {
		return err
	}
	root, err := repositoryRoot(ctx)
	if err != nil {
		return err
	}
	p, err := buildPlan(ctx, root, o)
	if err != nil {
		return err
	}
	if args[0] == "plan" {
		return printPlan(p, o.format)
	}
	return executePlan(ctx, root, p, o.lane)
}

func parseOptions(args []string) (options, error) {
	var o options
	f := flag.NewFlagSet("checks", flag.ContinueOnError)
	f.StringVar(&o.base, "base", os.Getenv("STYK_CHECK_BASE"), "comparison ref; omitted: local staged/unstaged/untracked files")
	f.StringVar(&o.head, "head", os.Getenv("STYK_CHECK_HEAD"), "commit to check; omitted: working tree")
	f.StringVar(&o.format, "format", "text", "text, json or github outputs")
	f.StringVar(&o.lane, "lane", "fast", "fast, go, web, browser, presentation, integration, full, unit-all, integration-all or browser-stack")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, errors.New("unexpected positional arguments")
	}
	return o, nil
}

func verificationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, verificationBudget-verificationCleanupReserve)
}

func printPlan(p checkpolicy.Plan, format string) error {
	var output strings.Builder
	switch format {
	case "json":
		return json.NewEncoder(os.Stdout).Encode(p)
	case "github":
		fmt.Fprintf(&output, "go=%t\nweb=%t\nbrowser=%t\nintegration=%t\nfull=%t\npolicy=%t\n", len(p.Go) > 0 && !p.Full, p.Web && !p.Full, len(p.Browser) > 0, len(p.Integration) > 0 && !p.Full, p.Full, p.Policy)
		data, err := json.Marshal(p.Browser)
		if err != nil {
			return err
		}
		fmt.Fprintf(&output, "browser_specs=%s\n", data)
	case "text":
		writeTextPlan(&output, p)
	default:
		return errors.New("format must be text, json or github")
	}
	_, err := fmt.Fprint(os.Stdout, output.String())
	return err
}

func writeTextPlan(output *strings.Builder, p checkpolicy.Plan) {
	for _, reason := range p.Reasons {
		fmt.Fprintf(output, "%s: %s\n", reason.Path, reason.Rule)
	}
	fmt.Fprintf(output, "Go packages (%d): %s\nFrontend: %t\nBrowser: %s\n", len(p.Go), strings.Join(p.Go, " "), p.Web, strings.Join(p.Browser, " "))
	packages := make([]string, 0, len(p.Integration))
	for pkg := range p.Integration {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)
	for _, pkg := range packages {
		fmt.Fprintf(output, "Integration %s: %s\n", pkg, strings.Join(p.Integration[pkg], ", "))
	}
	fmt.Fprintf(output, "Full regression required: %t\n", p.Full)
}
