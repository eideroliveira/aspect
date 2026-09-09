package workspace

import (
	"strings"
	"testing"
)

func TestCheckGoImports(t *testing.T) {
	files := []File{
		{Path: "a/a.go", Content: "package a\n\nimport (\n\t\"fmt\"\n\t\"example.com/sys/ledger\"\n\t\"gorm.io/gorm\"\n\t\"github.com/qor5/admin/v3/presets\"\n\t\"github.com/stretchr/testify/assert\"\n)\n"},
		{Path: "a/b.go", Content: "package a\n\nimport \"golang.org/x/sync/errgroup\"\n"},
		{Path: "a/bad.go", Content: "package a\n\nimport (\n"},
		{Path: "a/notes.md", Content: "import nothing"},
	}
	allowed := []string{"example.com/sys", "gorm.io/gorm", "github.com/qor5/"}
	got := CheckGoImports(files, allowed)
	want := []string{
		`a/a.go imports "github.com/stretchr/testify/assert"`,
		`a/b.go imports "golang.org/x/sync/errgroup"`,
		`a/bad.go imports "<parse error`,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d violations: %v", len(got), got)
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i].String(), w) {
			t.Errorf("violation %d = %q, want prefix %q", i, got[i], w)
		}
	}
	if !strings.Contains(FormatViolations(got, allowed), "gorm.io/gorm") {
		t.Error("feedback must list what is allowed")
	}
}

func TestIsStdlib(t *testing.T) {
	for path, want := range map[string]bool{"fmt": true, "net/http": true, "go/parser": true, "example.com/x": false, "gorm.io/gorm": false} {
		if got := isStdlib(path); got != want {
			t.Errorf("isStdlib(%q) = %v", path, got)
		}
	}
}
