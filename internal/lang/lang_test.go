package lang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eideroliveira/aspect/internal/spec"
	"github.com/eideroliveira/aspect/internal/workspace"
)

func TestNames(t *testing.T) {
	if got := strings.Join(Names(), ","); got != "go,swift" {
		t.Fatalf("Names = %s", got)
	}
	if _, err := For("cobol"); err == nil {
		t.Fatal("want error for unknown language")
	}
}

func TestKeepModuleFilesSwift(t *testing.T) {
	p, _ := For("swift")
	files := []workspace.File{
		{Path: "Sources/Stock/A.swift", Content: "a"},
		{Path: "./Sources/Stock/B.swift", Content: "b"},
		{Path: "Sources/Ledger/L.swift", Content: "l"},
		{Path: "Tests/StockTests/T.swift", Content: "t"},
		{Path: "Sources/Stock/README.md", Content: "r"},
	}
	code := p.KeepModuleFiles("stock", files, false)
	tests := p.KeepModuleFiles("stock", files, true)
	if len(code) != 2 || code[1].Path != "Sources/Stock/B.swift" || len(tests) != 1 {
		t.Fatalf("code=%v tests=%v", code, tests)
	}
}

func TestSplitModuleFilesRefusesEscapesDuplicatesAndEmptyFiles(t *testing.T) {
	p, _ := For("go")
	files := []workspace.File{
		{Path: "stock/stock.go", Content: "package stock"},
		{Path: "stock/../ledger/ledger.go", Content: "package ledger"},
		{Path: "stock/./sub/../model.go", Content: "package stock"},
		{Path: "/stock/abs.go", Content: "package stock"},
		{Path: "stock/stock.go", Content: "package stock // again"},
		{Path: "stock/empty.go", Content: "  \n"},
		{Path: "stock/stock_test.go", Content: "package stock"},
		{Path: "", Content: "x"},
		{Path: "../outside/stock/x.go", Content: "package stock"},
	}
	kept, dropped := p.SplitModuleFiles("stock", files, false)
	if len(kept) != 2 || kept[0].Path != "stock/stock.go" || kept[1].Path != "stock/model.go" {
		t.Fatalf("kept = %+v", kept)
	}
	if kept[0].Content != "package stock" {
		t.Fatalf("the first copy of a repeated path must win, got %q", kept[0].Content)
	}
	if len(dropped) != 7 {
		t.Fatalf("dropped = %+v", dropped)
	}
	reasons := map[string]string{}
	for _, d := range dropped {
		reasons[d.Path] = d.Reason
	}
	if !strings.Contains(reasons["stock/../ledger/ledger.go"], "outside the module") {
		t.Errorf("traversal into a sibling module: %q", reasons["stock/../ledger/ledger.go"])
	}
	if !strings.Contains(reasons["../outside/stock/x.go"], "leaves the module") {
		t.Errorf("climb above the root: %q", reasons["../outside/stock/x.go"])
	}
	if !strings.Contains(reasons["stock/stock.go"], "repeated") || !strings.Contains(reasons["stock/empty.go"], "no content") {
		t.Errorf("reasons = %v", reasons)
	}
}

func TestSwiftSyncGeneratesManifest(t *testing.T) {
	s, err := spec.Parse([]byte(`
aspect: 1
system:
  name: shop_app
  intent: i
  language: swift
  module_path: com.example.shop
  goals: [{id: G1, statement: x}]
  stack:
    swift:
      frameworks:
        - {name: ComposableArchitecture, module: https://github.com/pointfreeco/swift-composable-architecture.git, version: "1.15.0"}
modules:
  - {name: ledger, intent: i, goals: [G1]}
  - {name: stock, intent: i, depends_on: [ledger]}
`))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, d := range []string{"Sources/Ledger", "Sources/Stock", "Tests/LedgerTests", "Tests/StockTests"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := swiftSync(root, s, s.EffectiveTiers()[0], []string{"ledger", "stock", "ghost"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "Package.swift"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`name: "ShopApp"`,
		`.iOS(.v17), .macOS(.v14)`,
		`.package(url: "https://github.com/pointfreeco/swift-composable-architecture.git", from: "1.15.0")`,
		`.target(name: "Ledger", dependencies: [.product(name: "ComposableArchitecture", package: "swift-composable-architecture")])`,
		`.target(name: "Stock", dependencies: ["Ledger", .product(`,
		`.testTarget(name: "StockTests", dependencies: ["Stock"])`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("Package.swift lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "Ghost") {
		t.Fatal("a module without a Sources directory must not be declared")
	}
}

func TestPascalCase(t *testing.T) {
	for in, want := range map[string]string{"stock": "Stock", "stock_ledger": "StockLedger", "a__b": "AB"} {
		if got := PascalCase(in); got != want {
			t.Errorf("PascalCase(%q) = %q", in, got)
		}
	}
}
