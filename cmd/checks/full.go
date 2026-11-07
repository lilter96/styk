package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Full regression is deliberately explicit and runs the integration build only
// once with race detection; it already includes the ordinary Go unit tests.
func fullChecks(ctx context.Context, root string) error {
	files, err := output(ctx, root, "git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "*.go")
	if err != nil {
		return err
	}
	if err = formattedGo(ctx, root, strings.FieldsFunc(string(files), func(r rune) bool { return r == 0 })); err != nil {
		return err
	}
	commands := fullQualityCommands()
	return concurrentChecks(ctx, []namedCheck{
		{"Go lint and workflow lint", func(ctx context.Context) error { return commandsInOrder(ctx, root, nil, commands) }},
		{"Go race integration and build", func(ctx context.Context) error { return fullIntegration(ctx, root) }},
		{"Frontend and presentation", func(ctx context.Context) error { return fullFrontend(ctx, root) }},
	})
}

func fullIntegration(ctx context.Context, root string) error {
	env, cleanup, err := testDatabase(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	if err = migrateTestDatabase(ctx, root, env); err != nil {
		return err
	}
	env = append(env, "STYK_PROCESS_E2E=true", "STYK_ORDERS_BROWSER_E2E=1", "STYK_CHECKS_DOCKER_SMOKE=1")
	return fullGoIntegration(ctx, root, env)
}

func fullGoIntegration(ctx context.Context, root string, env []string) (err error) {
	other, business, err := fullPackagePartition(ctx, root)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "styk-full-business-")
	if err != nil {
		return fmt.Errorf("create business binary directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	binary := filepath.Join(dir, "business.test")
	if err = command(ctx, root, nil, "go", "test", "-race", "-tags=integration", "-c", "-o", binary, business); err != nil {
		return err
	}
	if err = fullBusinessChecks(ctx, root, env, other, business, binary); err != nil {
		return err
	}
	return buildPackages(ctx, root, []string{"./..."})
}

func fullFrontend(ctx context.Context, root string) error {
	if err := webChecks(ctx, root, true); err != nil {
		return err
	}
	return browserChecks(ctx, root, []string{"e2e/orders-console.spec.ts", "e2e/redesign.spec.ts"})
}

func fullQualityCommands() [][]string {
	return [][]string{
		{"go", "run", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0", "run", "./..."},
		{"go", "run", "github.com/rhysd/actionlint/cmd/actionlint@v1.7.7", "-shellcheck=", "-pyflakes="},
	}
}
