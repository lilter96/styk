// Package checkpolicy selects verification from changed files and Go dependencies.
// It does not import application packages or contact providers.
package checkpolicy

import (
	"path"
	"sort"
	"strings"
)

type Reason struct {
	Path string `json:"path"`
	Rule string `json:"rule"`
}

type Plan struct {
	Files       []string            `json:"files"`
	Go          []string            `json:"go_packages"`
	GoFiles     []string            `json:"go_files"`
	Web         bool                `json:"web"`
	Browser     []string            `json:"browser_specs"`
	Integration map[string][]string `json:"integration"`
	Full        bool                `json:"full"`
	Policy      bool                `json:"policy"`
	Reasons     []Reason            `json:"reasons"`
	goSeeds     []string
	testSeeds   []string
	integration []string
}

// Select is deliberately conservative for unknown files. A new boundary must
// receive an explicit rule before its regression coverage can be narrowed.
func Select(files []string) Plan {
	p := Plan{Files: unique(files), Integration: make(map[string][]string)}
	for _, file := range p.Files {
		if strings.HasSuffix(file, ".go") {
			p.GoFiles = append(p.GoFiles, file)
			if strings.HasSuffix(file, "_test.go") {
				p.testSeeds = append(p.testSeeds, path.Dir(file))
			} else {
				p.goSeeds = append(p.goSeeds, path.Dir(file))
			}
		}
		p.classify(file)
	}
	p.Browser = unique(p.Browser)
	p.integration = unique(p.integration)
	return p
}

func (p *Plan) classify(file string) {
	switch {
	case policyFile(file):
		p.Policy = true
		p.goSeeds = append(p.goSeeds, "internal/checkpolicy", "cmd/checks")
		if file == "web/e2e/run-ui.mjs" {
			p.Web = true
			p.Browser = append(p.Browser, "e2e/orders-console.spec.ts", "e2e/redesign.spec.ts")
		}
		p.reason(file, "verification policy: self-tests and workflow validation")
	case strings.HasSuffix(file, ".md"), strings.HasSuffix(file, ".txt"), file == "LICENSE":
		p.reason(file, "documentation: whitespace only")
	case strings.HasPrefix(file, "web/"):
		p.webFile(file)
	case strings.HasPrefix(file, "migrations/"), file == "go.mod", file == "go.sum":
		p.requireFull(file, "schema or Go dependency change")
	case strings.HasPrefix(file, "tests/integration/"):
		p.integrationFile(file)
	case strings.HasSuffix(file, ".go"):
		p.backendFile(file)
	default:
		p.requireFull(file, "unclassified impact; full regression required")
	}
}

func policyFile(file string) bool {
	return file == "AGENTS.md" || file == "Makefile" ||
		file == "web/e2e/run-ui.mjs" ||
		strings.HasPrefix(file, ".github/") ||
		strings.HasPrefix(file, "cmd/checks/") || strings.HasPrefix(file, "internal/checkpolicy/")
}

func (p *Plan) webFile(file string) {
	p.Web = true
	p.reason(file, "frontend: typecheck, lint, unit tests and build")
	if file == "web/src/api.ts" || file == "web/src/CredentialSubmission.tsx" {
		p.requireFull(file, "authentication or credential transport changed")
		return
	}
	if file == "web/package.json" || file == "web/package-lock.json" {
		p.Browser = append(p.Browser, "e2e/orders-console.spec.ts", "e2e/redesign.spec.ts")
		return
	}
	if strings.HasSuffix(file, ".test.tsx") || strings.HasSuffix(file, ".test.ts") {
		return
	}
	if strings.HasPrefix(file, "web/e2e/") && !presentationFile(file) {
		p.requireFull(file, "process-backed browser harness changed")
		return
	}
	if strings.Contains(path.Base(file), "Orders") || strings.Contains(path.Base(file), "OrderDetail") || strings.Contains(file, "orders-console") {
		p.Browser = append(p.Browser, "e2e/orders-console.spec.ts")
		return
	}
	p.Browser = append(p.Browser, "e2e/redesign.spec.ts")
	if strings.HasSuffix(file, ".css") || strings.Contains(file, "IntegrationDialog") || strings.Contains(file, "UI.tsx") {
		p.Browser = append(p.Browser, "e2e/orders-console.spec.ts")
	}
}

func presentationFile(file string) bool {
	return file == "web/e2e/orders-console.spec.ts" || file == "web/e2e/redesign.spec.ts" || file == "web/e2e/hover-contrast.ts"
}

func (p *Plan) integrationFile(file string) {
	for _, marker := range []string{"fixture", "helper", "process", "browser"} {
		if strings.Contains(path.Base(file), marker) {
			p.requireFull(file, "shared or process-backed integration harness changed")
			return
		}
	}
	if strings.HasSuffix(file, "_test.go") {
		p.integration = append(p.integration, file)
		p.reason(file, "integration test change: tests declared in this file")
		return
	}
	p.requireFull(file, "shared integration harness changed")
}

func (p *Plan) backendFile(file string) {
	if criticalGo(file) || strings.HasSuffix(file, "_integration.go") {
		p.requireFull(file, "security, provider dispatch, or shared integration boundary")
		return
	}
	if strings.HasSuffix(file, "_integration_test.go") {
		p.integrationFile(file)
		return
	}
	if strings.HasSuffix(file, "_test.go") {
		p.reason(file, "unit test change: declaring package only")
		return
	}
	if file == "internal/postgres/orders_query.go" || file == "internal/postgres/orders_posting_filters.go" {
		p.integration = append(p.integration, "internal/postgres/orders_posting_filters_integration_test.go")
		p.reason(file, "posting reads: status/search, keyset pagination and account isolation")
		return
	}
	switch backendArea(file) {
	case "orders":
		p.integration = append(p.integration, orderContracts...)
		p.reason(file, "orders: scoped storage and normal multi-process contract")
	case "catalog":
		p.integration = append(p.integration, "tests/integration/catalog_sync_runner_test.go")
		p.reason(file, "catalog: scoped persistence contract")
	case "stock":
		p.integration = append(p.integration, "tests/integration/stock_commitment_test.go", "tests/integration/warehouse_mapping_test.go")
		p.reason(file, "stock: commitment and warehouse isolation contracts")
	default:
		p.requireFull(file, "shared backend boundary or unknown module")
	}
}

var orderContracts = []string{
	"internal/postgres/orders_posting_filters_integration_test.go",
	"tests/integration/orders_process_test.go",
}

func criticalGo(file string) bool {
	for _, prefix := range []string{"internal/identity/", "internal/credentials/", "internal/orderprivacy/", "internal/ordercontract/", "internal/providercall/", "internal/orderprovider/", "internal/catalogprovider/"} {
		if strings.HasPrefix(file, prefix) && !strings.HasSuffix(file, "_test.go") {
			return true
		}
	}
	name := path.Base(file)
	for _, risk := range []string{"dispatch", "fence", "lease", "retry", "reconcile", "write"} {
		if !strings.HasSuffix(file, "_test.go") && strings.Contains(name, risk) {
			return true
		}
	}
	return false
}

func backendArea(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) < 3 || parts[0] != "internal" {
		return ""
	}
	name := parts[1]
	if name == "postgres" || name == "console" {
		name = path.Base(file)
	}
	switch {
	case strings.HasPrefix(name, "order"), name == "delivery":
		return "orders"
	case strings.HasPrefix(name, "catalog"):
		return "catalog"
	case strings.HasPrefix(name, "stock"), name == "commitment", strings.HasPrefix(name, "warehouse"):
		return "stock"
	default:
		return ""
	}
}

func (p *Plan) requireFull(file, why string) { p.Full = true; p.reason(file, why) }
func (p *Plan) reason(file, why string)      { p.Reasons = append(p.Reasons, Reason{file, why}) }

func unique(values []string) []string {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
