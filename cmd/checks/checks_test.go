package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDatabaseRejectsApplicationRemoteAndOverrideDSNs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, dsn string
		valid     bool
	}{
		{"dedicated", "postgres://test:secret@127.0.0.1:5432/styk_orders_store_test?sslmode=disable", true},
		{"application", "postgres://test:secret@127.0.0.1/styk", false},
		{"remote", "postgres://test:secret@example.com/styk_orders_store_test", false},
		{"host override", "postgres://test:secret@localhost/styk_orders_store_test?host=example.com", false},
		{"database override", "postgres://test:secret@localhost/styk_orders_store_test?dbname=styk", false},
		{"disguised database", "postgres://test:secret@localhost/styk?dbname=styk_orders_store_test", false},
		{"keyword DSN", "host=localhost dbname=styk_orders_store_test", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTestDatabase(tc.dsn)
			if (err == nil) != tc.valid {
				t.Fatalf("validation mismatch: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("DSN credential appeared in diagnostic")
			}
		})
	}
}

func TestEnvironmentExcludesLiveCredentialsFlagsAndPGDefaults(t *testing.T) {
	for _, key := range []string{"MOYSKLAD_TOKEN", "OZON_API_KEY", "STYK_LIVE_ORDER_FLOW_TEST", "STYK_TEST_ACCOUNT_ORDERS", "PGSERVICE", "DATABASE_URL", "TEST_DATABASE_URL"} {
		t.Setenv(key, "must-not-reach-tests")
	}
	for _, entry := range cleanEnvironment() {
		if strings.Contains(entry, "must-not-reach-tests") {
			t.Fatal("test environment retained live credential or opt-in")
		}
	}
}

func TestDiffIncludesStagedUnstagedUntrackedAndBothRenameSides(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := gitFixture(t)
	writeFixture(t, root, "before.md", "old\n")
	gitFixtureCommand(t, ctx, root, "add", ".")
	gitFixtureCommand(t, ctx, root, "commit", "-m", "base")
	gitFixtureCommand(t, ctx, root, "mv", "before.md", "after with spaces.md")
	writeFixture(t, root, "after with spaces.md", "new\n")
	writeFixture(t, root, "untracked\nname.md", "new\n")
	files, err := changedFiles(ctx, root, options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"after with spaces.md", "before.md", "untracked\nname.md"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("changed paths: %q, want %q", files, want)
	}
}

func TestBuildPlanRetainsResolvedGoPackages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := gitFixture(t)
	writeFixture(t, root, "go.mod", "module example\n\ngo 1.27.3\n")
	writeFixture(t, root, "internal/checkpolicy/policy.go", "package checkpolicy\n")
	writeFixture(t, root, "cmd/checks/main.go", "package main\nimport _ \"example/internal/checkpolicy\"\nfunc main(){}\n")
	gitFixtureCommand(t, ctx, root, "add", ".")
	gitFixtureCommand(t, ctx, root, "commit", "-m", "base")
	writeFixture(t, root, "internal/checkpolicy/policy.go", "package checkpolicy\n// changed\n")
	p, err := buildPlan(ctx, root, options{})
	if err != nil || !reflect.DeepEqual(p.Go, []string{"./cmd/checks", "./internal/checkpolicy"}) || p.Full {
		t.Fatalf("resolved packages were lost: %+v %v", p, err)
	}
}

func TestSingleCommandBuildDoesNotPolluteWorkingTree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := gitFixture(t)
	writeFixture(t, root, "go.mod", "module example\n\ngo 1.27.3\n")
	writeFixture(t, root, "cmd/example/main.go", "package main\nfunc main(){}\n")
	gitFixtureCommand(t, ctx, root, "add", ".")
	gitFixtureCommand(t, ctx, root, "commit", "-m", "base")
	if err := buildPackages(ctx, root, []string{"./cmd/example"}); err != nil {
		t.Fatal(err)
	}
	files, err := changedFiles(ctx, root, options{})
	if err != nil || len(files) != 0 {
		t.Fatalf("build artifacts changed the next verification scope: %q %v", files, err)
	}
}

func TestFullLaneRunsOneRaceEnabledIntegrationBuild(t *testing.T) {
	t.Parallel()
	testCommands := 0
	for _, args := range fullGoCommands() {
		if len(args) < 2 || args[0] != "go" || args[1] != "test" {
			continue
		}
		testCommands++
		command := " " + strings.Join(args, " ") + " "
		if !strings.Contains(command, " -race ") || !strings.Contains(command, " -tags=integration ") {
			t.Fatal("full lane split race and integration coverage")
		}
	}
	if testCommands != 1 {
		t.Fatalf("full lane repeats or omits Go tests: %d", testCommands)
	}
}

func gitFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitFixtureCommand(t, context.Background(), root, "init", "--initial-branch=main")
	return root
}

func writeFixture(t *testing.T, root, name, value string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitFixtureCommand(t *testing.T, ctx context.Context, root string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.test", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = root
	for _, entry := range cleanEnvironment() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fixture: %v %s", err, data)
	}
}

func TestCommitRangeUsesMergeBaseAndDoesNotIncludeWorkingTree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := gitFixture(t)
	writeFixture(t, root, "base.md", "base\n")
	gitFixtureCommand(t, ctx, root, "add", ".")
	gitFixtureCommand(t, ctx, root, "commit", "-m", "base")
	gitFixtureCommand(t, ctx, root, "checkout", "-b", "feature")
	writeFixture(t, root, "feature.md", "feature\n")
	gitFixtureCommand(t, ctx, root, "add", ".")
	gitFixtureCommand(t, ctx, root, "commit", "-m", "feature")
	gitFixtureCommand(t, ctx, root, "checkout", "main")
	writeFixture(t, root, "main.md", "main\n")
	gitFixtureCommand(t, ctx, root, "add", ".")
	gitFixtureCommand(t, ctx, root, "commit", "-m", "main")
	writeFixture(t, root, "untracked.md", "local\n")
	files, err := changedFiles(ctx, root, options{base: "main", head: "feature"})
	if err != nil || !reflect.DeepEqual(files, []string{"feature.md"}) {
		t.Fatalf("commit comparison included unrelated changes: %q %v", files, err)
	}
}
