package checkpolicy

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestChangeClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, file        string
		web, full, policy bool
		integration       int
	}{
		{"docs", "README.md", false, false, false, 0},
		{"policy instructions", "AGENTS.md", false, false, true, 0},
		{"CI", ".github/workflows/ci.yml", false, false, true, 0},
		{"style", "web/src/imports.css", true, false, false, 0},
		{"component", "web/src/OrdersPostings.tsx", true, false, false, 0},
		{"frontend test", "web/src/Orders.test.tsx", true, false, false, 0},
		{"warehouse browser", "web/e2e/warehouse-review.spec.ts", true, false, false, 1},
		{"warehouse confirmation browser", "web/e2e/warehouse-confirmation.spec.ts", true, false, false, 1},
		{"browser runner", "web/e2e/run-ui.mjs", true, false, true, 0},
		{"browser auth transport", "web/src/api.ts", true, true, false, 0},
		{"orders query", "internal/postgres/orders_query.go", false, false, false, 1},
		{"catalog", "internal/catalogsync/engine.go", false, false, false, 1},
		{"stock", "internal/commitment/calculate.go", false, false, false, 2},
		{"unit test", "internal/domain/stock_test.go", false, false, false, 0},
		{"migration", "migrations/00067_example.sql", false, true, false, 0},
		{"dependencies", "go.mod", false, true, false, 0},
		{"identity", "internal/identity/session.go", false, true, false, 0},
		{"dispatch", "internal/orderflow/dispatch.go", false, true, false, 0},
		{"shared", "internal/postgres/transaction.go", false, true, false, 0},
		{"unknown", "deployment/new-boundary.conf", false, true, false, 0},
		{"process helper", "tests/integration/orders_process_child_test.go", false, true, false, 0},
		{"new policy code", "cmd/checks/main.go", false, false, true, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := Select([]string{tc.file})
			if p.Web != tc.web || p.Full != tc.full || p.Policy != tc.policy || len(p.integration) != tc.integration || len(p.Reasons) == 0 {
				t.Fatalf("unexpected plan: %+v", p)
			}
		})
	}
}

func TestFrontendDoesNotSelectDatabaseOrGo(t *testing.T) {
	t.Parallel()
	p := Select([]string{"web/src/imports.css", "web/src/Orders.css"})
	if p.NeedsGo() || len(p.integration) != 0 || p.Full || !p.Web || len(p.Browser) != 2 {
		t.Fatalf("frontend change escaped its scope: %+v", p)
	}
	unit := Select([]string{"web/src/Orders.test.tsx"})
	if len(unit.Browser) != 0 {
		t.Fatal("unit test change selected browser acceptance")
	}
}

func TestReverseDependenciesIncludeTestImportsAndDeletedPackages(t *testing.T) {
	t.Parallel()
	packages := []Package{
		{ImportPath: "example/internal/a", Dir: "/repo/internal/a"},
		{ImportPath: "example/internal/b", Dir: "/repo/internal/b", TestImports: []string{"example/internal/a"}},
		{ImportPath: "example/cmd/api", Dir: "/repo/cmd/api", Imports: []string{"example/internal/b"}},
		{ImportPath: "example/internal/unrelated", Dir: "/repo/internal/unrelated"},
		{ImportPath: "example/internal/dependent", Dir: "/repo/internal/dependent", XTestImports: []string{"example/internal/deleted"}},
	}
	p := Plan{goSeeds: []string{"internal/a", "internal/deleted"}}
	if err := p.ResolveGo("/repo", "example", packages); err != nil {
		t.Fatal(err)
	}
	want := []string{"./cmd/api", "./internal/a", "./internal/b", "./internal/dependent"}
	if !reflect.DeepEqual(p.Go, want) {
		t.Fatalf("selected %v, want %v", p.Go, want)
	}
}

func TestUnitTestEditDoesNotRetestConsumers(t *testing.T) {
	t.Parallel()
	p := Select([]string{"internal/orders/value_test.go"})
	packages := []Package{
		{ImportPath: "example/internal/orders", Dir: "/repo/internal/orders"},
		{ImportPath: "example/cmd/api", Dir: "/repo/cmd/api", Imports: []string{"example/internal/orders"}},
	}
	if err := p.ResolveGo("/repo", "example", packages); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Go, []string{"./internal/orders"}) {
		t.Fatalf("test-only change invalidated consumers: %v", p.Go)
	}
}

func TestIntegrationSelectionFailsClosedAndDeduplicates(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "tests/integration")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "sample_test.go")
	source := "package integration\nimport alias \"testing\"\nfunc TestAlpha(t *alias.T) {}\nfunc TestBeta(t *alias.T) {}\nfunc Testhelper() {}\nfunc testHelper() {}\n"
	if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	p := Select([]string{"tests/integration/sample_test.go", "tests/integration/sample_test.go"})
	if err := p.ResolveIntegration(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if p.Full || !reflect.DeepEqual(p.Integration["./tests/integration"], []string{"TestAlpha", "TestBeta"}) {
		t.Fatalf("wrong declared tests: %+v", p)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	deleted := Select([]string{"tests/integration/sample_test.go"})
	if err := deleted.ResolveIntegration(context.Background(), root); err != nil || !deleted.Full {
		t.Fatalf("deleted tests reduced coverage: %+v %v", deleted, err)
	}
}

func TestContractManifestStillSelectsRealTests(t *testing.T) {
	t.Parallel()
	p := Select([]string{"internal/postgres/orders_query.go", "internal/catalogsync/engine.go", "internal/stocksync/service.go"})
	if err := p.ResolveIntegration(context.Background(), filepath.Join("..", "..")); err != nil {
		t.Fatal(err)
	}
	if p.Full || len(p.Integration) != 2 || len(p.Integration["./tests/integration"]) < 3 {
		t.Fatalf("contract manifest is stale or incomplete: %+v", p)
	}
}
