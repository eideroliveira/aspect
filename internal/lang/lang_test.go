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
		{Path: "Sources/Stock/A.swift"},
		{Path: "./Sources/Stock/B.swift"},
		{Path: "Sources/Ledger/L.swift"},
		{Path: "Tests/StockTests/T.swift"},
		{Path: "Sources/Stock/README.md"},
	}
	code := p.KeepModuleFiles("stock", files, false)
	tests := p.KeepModuleFiles("stock", files, true)
	if len(code) != 2 || code[1].Path != "Sources/Stock/B.swift" || len(tests) != 1 {
		t.Fatalf("code=%v tests=%v", code, tests)
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
	if err := swiftSync(root, s, s.EffectiveTiers()[0], []string{"ledger", "stock"}); err != nil {
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
}

func TestPascalCase(t *testing.T) {
	for in, want := range map[string]string{"stock": "Stock", "stock_ledger": "StockLedger", "a__b": "AB"} {
		if got := PascalCase(in); got != want {
			t.Errorf("PascalCase(%q) = %q", in, got)
		}
	}
}
