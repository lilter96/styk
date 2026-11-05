package main

import (
	"context"
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
	env, cleanup, err := testDatabase(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	if err = migrateTestDatabase(ctx, root, env); err != nil {
		return err
	}
	env = append(env, "STYK_PROCESS_E2E=true", "STYK_ORDERS_BROWSER_E2E=1", "STYK_CHECKS_DOCKER_SMOKE=1")
	if err = commandsInOrder(ctx, root, env, fullGoCommands()); err != nil {
		return err
	}
	if err = buildPackages(ctx, root, []string{"./..."}); err != nil {
		return err
	}
	if err = webChecks(ctx, root, true); err != nil {
		return err
	}
	return browserChecks(ctx, root, []string{"e2e/orders-console.spec.ts", "e2e/redesign.spec.ts"})
}

func fullGoCommands() [][]string {
	return [][]string{
		{"go", "run", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0", "run", "./..."},
		{"go", "run", "github.com/rhysd/actionlint/cmd/actionlint@v1.7.7", "-shellcheck=", "-pyflakes="},
		{"go", "test", "-race", "-tags=integration", "-count=1", "-timeout=40m", "-p=1", "./..."},
	}
}
