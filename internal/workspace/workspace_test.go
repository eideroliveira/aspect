package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewWritesGoMod(t *testing.T) {
	root := t.TempDir()
	w, err := New(filepath.Join(root, "sys"), "example.com/sys")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(w.Root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "module example.com/sys\n") {
		t.Fatalf("go.mod = %q", b)
	}
}

func TestWriteFilesRejectsEscapes(t *testing.T) {
	w, err := New(t.TempDir(), "example.com/sys")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../x.go", "/abs.go", "go.mod", "sub/../../x.go", ""} {
		if err := w.WriteFiles([]File{{Path: bad, Content: "x"}}); err == nil {
			t.Errorf("path %q was accepted", bad)
		}
	}
}

func TestTestRunsGeneratedModule(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes the go toolchain")
	}
	w, err := New(t.TempDir(), "example.com/sys")
	if err != nil {
		t.Fatal(err)
	}
	files := []File{
		{Path: "add/add.go", Content: "package add\n\nfunc Add(a, b int) int { return a + b }\n"},
		{Path: "add/add_test.go", Content: "package add\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 3) != 5 {\n\t\tt.Fatal(\"nope\")\n\t}\n}\n"},
	}
	if err := w.WriteFiles(files); err != nil {
		t.Fatal(err)
	}
	res, err := w.Test(context.Background(), "./...")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("expected passing run, output:\n%s", res.Output)
	}

	// Now break it and make sure the failure is reported, not swallowed.
	files[1].Content = strings.Replace(files[1].Content, "!= 5", "!= 6", 1)
	if err := w.WriteFiles(files[1:]); err != nil {
		t.Fatal(err)
	}
	res, err = w.Test(context.Background(), "./...")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Output, "FAIL") {
		t.Fatalf("expected failing run, ok=%v output:\n%s", res.OK, res.Output)
	}

	tree, err := w.ReadTree()
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 || tree[0].Path != "add/add.go" {
		t.Fatalf("tree = %+v", tree)
	}
}
