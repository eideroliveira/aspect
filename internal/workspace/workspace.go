// Package workspace owns the directory where generated code lives and knows
// how to compile and test it. Agents never touch the filesystem or run tools
// directly; they propose files, and the workspace applies them. That boundary
// is what makes the pipeline auditable: every file on disk came through here.
package workspace

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// File is a path relative to the workspace root plus its full content.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Workspace is one generated Go module on disk.
type Workspace struct {
	Root       string
	ModulePath string
	// TestTimeout bounds one `go test` run. Generated code can loop forever;
	// the pipeline must not.
	TestTimeout time.Duration
}

// Result of a compile-and-test run.
type Result struct {
	OK       bool          `json:"ok"`
	Output   string        `json:"output"`
	Duration time.Duration `json:"duration"`
}

// New creates the root directory and a go.mod for the module if none exists.
func New(root, modulePath string) (*Workspace, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	w := &Workspace{Root: root, ModulePath: modulePath, TestTimeout: 2 * time.Minute}
	gomod := filepath.Join(root, "go.mod")
	if _, err := os.Stat(gomod); os.IsNotExist(err) {
		goVersion := strings.TrimPrefix(runtime.Version(), "go")
		if i := strings.Index(goVersion, " "); i > 0 {
			goVersion = goVersion[:i]
		}
		content := fmt.Sprintf("module %s\n\ngo %s\n", modulePath, goVersion)
		if err := os.WriteFile(gomod, []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	return w, nil
}

// WriteFiles applies a set of files. Paths must stay inside the root; an agent
// proposing "../../etc/passwd" is rejected rather than trusted.
func (w *Workspace) WriteFiles(files []File) error {
	for _, f := range files {
		abs, err := w.safePath(f.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(f.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// DeleteFiles removes files previously written (used when a repair attempt
// renames a file and the old one would otherwise keep failing the build).
func (w *Workspace) DeleteFiles(paths []string) error {
	for _, p := range paths {
		abs, err := w.safePath(p)
		if err != nil {
			return err
		}
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (w *Workspace) safePath(p string) (string, error) {
	if p == "" || filepath.IsAbs(p) {
		return "", fmt.Errorf("workspace: invalid path %q", p)
	}
	clean := filepath.Clean(p)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("workspace: path %q escapes the workspace", p)
	}
	if filepath.Base(clean) == "go.mod" || filepath.Base(clean) == "go.sum" {
		return "", fmt.Errorf("workspace: agents may not rewrite %s", clean)
	}
	return filepath.Join(w.Root, clean), nil
}

// ReadTree returns every .go file under the root, sorted by path, so agents
// can be shown the current state of the module.
func (w *Workspace) ReadTree() ([]File, error) {
	var files []File
	err := filepath.WalkDir(w.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(w.Root, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, File{Path: filepath.ToSlash(rel), Content: string(b)})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

// Test runs `go mod tidy`, `go vet` and `go test` for the whole module and
// returns the combined output. A non-nil error means the tooling itself could
// not run; a failing build or test is reported through Result.OK.
func (w *Workspace) Test(ctx context.Context, pkgPattern string) (Result, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, w.TestTimeout)
	defer cancel()

	var out bytes.Buffer
	steps := [][]string{
		{"go", "mod", "tidy"},
		{"go", "vet", pkgPattern},
		{"go", "test", "-count=1", "-race", pkgPattern},
	}
	for _, args := range steps {
		fmt.Fprintf(&out, "$ %s\n", strings.Join(args, " "))
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = w.Root
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
		b, err := cmd.CombinedOutput()
		out.Write(b)
		if ctx.Err() != nil {
			fmt.Fprintf(&out, "\n[aspect] aborted: %v\n", ctx.Err())
			return Result{OK: false, Output: out.String(), Duration: time.Since(start)}, nil
		}
		if err != nil {
			var exitErr *exec.ExitError
			if !asExitError(err, &exitErr) {
				return Result{}, fmt.Errorf("run %v: %w", args, err)
			}
			fmt.Fprintf(&out, "\n[aspect] %s exited with %v\n", args[1], exitErr)
			return Result{OK: false, Output: out.String(), Duration: time.Since(start)}, nil
		}
	}
	return Result{OK: true, Output: out.String(), Duration: time.Since(start)}, nil
}
