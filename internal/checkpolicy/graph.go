package checkpolicy

import (
	"path/filepath"
)

type Package struct {
	ImportPath   string
	Dir          string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

// ResolveGo includes transitive consumers, including imports used only by tests.
// Deleted packages still seed the graph so their remaining consumers are checked.
func (p *Plan) ResolveGo(root, module string, packages []Package) error {
	selected := make(map[string]bool, len(p.goSeeds))
	for _, seed := range p.goSeeds {
		selected[module+"/"+seed] = true
	}
	for changed := true; changed; {
		changed = false
		for _, pkg := range packages {
			if !selected[pkg.ImportPath] && importsSelected(pkg, selected) {
				selected[pkg.ImportPath], changed = true, true
			}
		}
	}
	// Editing a test does not change the package consumed by other packages.
	for _, seed := range p.testSeeds {
		selected[module+"/"+seed] = true
	}
	for _, pkg := range packages {
		if !selected[pkg.ImportPath] && !p.Full {
			continue
		}
		relative, err := filepath.Rel(root, pkg.Dir)
		if err != nil {
			return err
		}
		p.Go = append(p.Go, "./"+filepath.ToSlash(relative))
	}
	p.Go = unique(p.Go)
	return nil
}

func importsSelected(pkg Package, selected map[string]bool) bool {
	for _, imports := range [][]string{pkg.Imports, pkg.TestImports, pkg.XTestImports} {
		for _, imported := range imports {
			if selected[imported] {
				return true
			}
		}
	}
	return false
}

func (p Plan) NeedsGo() bool { return len(p.goSeeds)+len(p.testSeeds) > 0 || p.Full }
