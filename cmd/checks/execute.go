package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/checkpolicy"
)

func executePlan(ctx context.Context, root string, p checkpolicy.Plan, lane string) error {
	if err := command(ctx, root, nil, "git", "diff", "--check"); err != nil {
		return err
	}
	if err := command(ctx, root, nil, "git", "diff", "--cached", "--check"); err != nil {
		return err
	}
	switch lane {
	case "fast":
		if err := goChecks(ctx, root, p); err != nil {
			return err
		}
		if err := webChecks(ctx, root, p.Web); err != nil {
			return err
		}
		fmt.Println("Fast checks complete. Run selected browser/integration lanes when listed by check-plan; full regression is a separate explicit/CI lane.")
		if p.Full {
			fmt.Println("This impact requires the full CI gate. Fast checks are not a full regression result.")
		}
	case "go":
		return goChecks(ctx, root, p)
	case "web":
		return webChecks(ctx, root, p.Web)
	case "browser":
		return browserChecks(ctx, root, p.Browser)
	case "integration":
		return integrationChecks(ctx, root, p)
	case "full":
		return fullChecks(ctx, root)
	default:
		return errors.New("unknown lane")
	}
	return nil
}

func goChecks(ctx context.Context, root string, p checkpolicy.Plan) error {
	if len(p.Go) == 0 {
		return nil
	}
	fmt.Printf("Go checks: %s\n", strings.Join(p.Go, " "))
	if err := formattedGo(ctx, root, p.GoFiles); err != nil {
		return err
	}
	commands := [][]string{
		append([]string{"go", "run", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0", "run"}, p.Go...),
		append([]string{"go", "test", "-race", "-timeout=2m"}, p.Go...),
	}
	if err := commandsInOrder(ctx, root, nil, commands); err != nil {
		return err
	}
	if err := buildPackages(ctx, root, p.Go); err != nil {
		return err
	}
	if p.Policy {
		return command(ctx, root, nil, "go", "run", "github.com/rhysd/actionlint/cmd/actionlint@v1.7.7", "-shellcheck=", "-pyflakes=")
	}
	return nil
}

func buildPackages(ctx context.Context, root string, packages []string) (err error) {
	dir, err := os.MkdirTemp("", "styk-check-build-")
	if err != nil {
		return fmt.Errorf("create build output directory: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove build output directory: %w", cleanupErr))
		}
	}()
	return command(ctx, root, nil, "go", append([]string{"build", "-o", dir}, packages...)...)
}

func formattedGo(ctx context.Context, root string, files []string) error {
	args := []string{"-l"}
	for _, file := range files {
		if _, err := os.Stat(filepath.Join(root, file)); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("inspect Go file: %w", err)
		}
		args = append(args, file)
	}
	if len(args) == 1 {
		return nil
	}
	data, err := output(ctx, root, "gofmt", args...)
	if err != nil {
		return err
	}
	if len(data) > 0 {
		return fmt.Errorf("gofmt required for: %s", strings.TrimSpace(string(data)))
	}
	return nil
}

func webChecks(ctx context.Context, root string, enabled bool) error {
	if !enabled {
		return nil
	}
	for _, script := range []string{"typecheck", "lint", "test", "build"} {
		fmt.Printf("Frontend: %s\n", script)
		if err := command(ctx, filepath.Join(root, "web"), nil, "npm", "run", script); err != nil {
			return err
		}
	}
	return nil
}

func browserChecks(ctx context.Context, root string, specs []string) error {
	if len(specs) == 0 {
		return nil
	}
	return command(ctx, filepath.Join(root, "web"), nil, "node", append([]string{"e2e/run-ui.mjs"}, specs...)...)
}

func integrationChecks(ctx context.Context, root string, p checkpolicy.Plan) error {
	if p.Full {
		return errors.New("impact requires full regression; use check-full or the CI full job, not a partial integration result")
	}
	if len(p.Integration) == 0 {
		fmt.Println("No integration tests selected; PostgreSQL not needed.")
		return nil
	}
	env, cleanup, err := testDatabase(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	if err = migrateTestDatabase(ctx, root, env); err != nil {
		return err
	}
	packages := make([]string, 0, len(p.Integration))
	for pkg := range p.Integration {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)
	for _, pkg := range packages {
		fmt.Printf("Integration %s: %s\n", pkg, strings.Join(p.Integration[pkg], ", "))
		if err = command(ctx, root, env, "go", "test", "-race", "-tags=integration", "-count=1", "-timeout=8m", pkg, "-run", checkpolicy.TestPattern(p.Integration[pkg])); err != nil {
			return err
		}
	}
	return nil
}

func commandsInOrder(ctx context.Context, root string, env []string, commands [][]string) error {
	for _, args := range commands {
		if err := command(ctx, root, env, args[0], args[1:]...); err != nil {
			return err
		}
	}
	return nil
}

func command(ctx context.Context, root string, extra []string, name string, args ...string) error {
	started := time.Now()
	// Arguments may contain the dedicated test DSN; never print argument values.
	fmt.Printf("Running %s (%s)\n", name, filepath.Base(root))
	cmd := exec.CommandContext(ctx, name, args...)
	configureProcess(cmd)
	cmd.Dir, cmd.Env = root, append(cleanEnvironment(), extra...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed after %s: %w", name, time.Since(started).Round(time.Millisecond), err)
	}
	fmt.Printf("Passed %s in %s\n", name, time.Since(started).Round(time.Millisecond))
	return nil
}

func cleanEnvironment() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "MOYSKLAD_") || strings.HasPrefix(key, "OZON_") || strings.HasPrefix(key, "STYK_") || strings.HasPrefix(key, "PG") || key == "DATABASE_URL" || key == "TEST_DATABASE_URL" || strings.HasSuffix(key, "_TEST_DATABASE_URL") {
			continue
		}
		result = append(result, entry)
	}
	return result
}
