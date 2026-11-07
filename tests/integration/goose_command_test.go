//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const integrationGoosePackage = "github.com/pressly/goose/v3/cmd/goose@v3.25.0"
const integrationGooseTags = "no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql"

var integrationGoose struct {
	once      sync.Once
	directory string
	binary    string
	err       error
}

// Build the exact existing CLI once. Each migration still executes the real
// pinned Goose command, retaining its transaction, history and rollback rules.
func newGooseCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	integrationGoose.once.Do(func() { integrationGoose.err = installIntegrationGoose(ctx) })
	if integrationGoose.err != nil {
		return nil, integrationGoose.err
	}
	return exec.CommandContext(ctx, integrationGoose.binary, args...), nil
}

func migrationCommand(t testing.TB, ctx context.Context, args ...string) *exec.Cmd {
	t.Helper()
	command, err := newGooseCommand(ctx, args...)
	if err != nil {
		t.Fatalf("prepare pinned migration CLI: %v", err)
	}
	return command
}

func installIntegrationGoose(ctx context.Context) error {
	directory, err := os.MkdirTemp("", "styk-integration-goose-")
	if err != nil {
		return fmt.Errorf("create private migration CLI directory: %w", err)
	}
	integrationGoose.directory = directory
	build, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(build, "go", "install", "-tags="+integrationGooseTags, integrationGoosePackage)
	command.Env = make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOBIN=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GOBIN="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("build pinned migration CLI: %w: %s", err, output)
	}
	name := "goose"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	integrationGoose.binary = filepath.Join(directory, name)
	return nil
}

func cleanupIntegrationGoose() error {
	if integrationGoose.directory == "" {
		return nil
	}
	return os.RemoveAll(integrationGoose.directory)
}
