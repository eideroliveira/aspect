// Package pipeline runs the agents over a plan: for each module in dependency
// order it asks the Coder for code, the Tester for tests, runs them for real,
// loops repairs through the Coder, and finally asks the Validator for a
// verdict per goal. The output is a Report the user can read and diff.
package pipeline

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/eideroliveira/aspect/internal/agents"
	"github.com/eideroliveira/aspect/internal/llm"
	"github.com/eideroliveira/aspect/internal/plan"
	"github.com/eideroliveira/aspect/internal/spec"
	"github.com/eideroliveira/aspect/internal/workspace"
)

// Options tune a run.
type Options struct {
	// OutDir is where the generated module and the report are written.
	OutDir string
	// MaxRepairs bounds the Coder repair loop per module.
	MaxRepairs int
	// Log receives progress lines; nil silences them.
	Log io.Writer
	// Model is recorded in the report for provenance.
	Model string
}

// ModuleReport is the outcome for one module.
type ModuleReport struct {
	Module     string         `json:"module"`
	Iterations int            `json:"iterations"`
	TestsOK    bool           `json:"tests_ok"`
	TestOutput string         `json:"test_output"`
	CoderNotes string         `json:"coder_notes"`
	Concerns   []string       `json:"concerns"`
	Verdict    agents.Verdict `json:"verdict"`
	Files      []string       `json:"files"`
	Usage      llm.Usage      `json:"usage"`
	Duration   time.Duration  `json:"duration"`
	Error      string         `json:"error,omitempty"`
}

// Report is the outcome of a whole run.
type Report struct {
	System   string         `json:"system"`
	Model    string         `json:"model"`
	Started  time.Time      `json:"started"`
	Finished time.Time      `json:"finished"`
	Modules  []ModuleReport `json:"modules"`
	Usage    llm.Usage      `json:"usage"`
	// Goals summarises every system goal across modules.
	Goals []GoalSummary `json:"goals"`
}

// GoalSummary rolls a goal's verdicts up across the modules that own it.
type GoalSummary struct {
	ID        string                       `json:"id"`
	Statement string                       `json:"statement"`
	Verify    spec.VerifyMethod            `json:"verify"`
	Status    agents.GoalStatus            `json:"status"`
	ByModule  map[string]agents.GoalStatus `json:"by_module"`
}

// Runner wires agents to a workspace.
type Runner struct {
	Coder     *agents.Coder
	Tester    *agents.Tester
	Validator *agents.Validator
	Opts      Options
}

// New builds a Runner where all three agents share one client.
func New(c llm.Client, opts Options) *Runner {
	if opts.MaxRepairs == 0 {
		opts.MaxRepairs = 3
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	return &Runner{
		Coder:     &agents.Coder{LLM: c},
		Tester:    &agents.Tester{LLM: c},
		Validator: &agents.Validator{LLM: c},
		Opts:      opts,
	}
}

func (r *Runner) logf(format string, args ...any) {
	fmt.Fprintf(r.Opts.Log, format+"\n", args...)
}

// Run executes the plan. It keeps going after a module fails so the report
// shows every module's state; the error returned summarises failures.
func (r *Runner) Run(ctx context.Context, s *spec.Spec, p *plan.Plan) (*Report, error) {
	root := filepath.Join(r.Opts.OutDir, s.System.Name)
	ws, err := workspace.New(root, s.System.ModulePath)
	if err != nil {
		return nil, err
	}
	rep := &Report{System: s.System.Name, Model: r.Opts.Model, Started: time.Now()}
	var failed []string

	for _, step := range p.Steps {
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		r.logf("== module %s (goals %s)", step.Module, strings.Join(step.Goals, ","))
		mr := r.runModule(ctx, s, step, ws)
		rep.Modules = append(rep.Modules, mr)
		rep.Usage.Calls += mr.Usage.Calls
		rep.Usage.InputTokens += mr.Usage.InputTokens
		rep.Usage.OutputTokens += mr.Usage.OutputTokens
		rep.Usage.CacheReadTokens += mr.Usage.CacheReadTokens
		if mr.Error != "" {
			failed = append(failed, step.Module)
			r.logf("   error: %s", mr.Error)
		}
	}
	rep.Finished = time.Now()
	rep.Goals = summarise(s, rep.Modules)
	if len(failed) > 0 {
		return rep, fmt.Errorf("pipeline: %d module(s) failed: %s", len(failed), strings.Join(failed, ", "))
	}
	return rep, nil
}

func (r *Runner) runModule(ctx context.Context, s *spec.Spec, step plan.Step, ws *workspace.Workspace) ModuleReport {
	start := time.Now()
	mr := ModuleReport{Module: step.Module}
	fail := func(err error) ModuleReport {
		mr.Error = err.Error()
		mr.Duration = time.Since(start)
		return mr
	}
	mod := s.Module(step.Module)
	deps, err := dependencyFiles(ws, step.DependsOn)
	if err != nil {
		return fail(err)
	}

	// 1. Code.
	r.logf("   coder: writing implementation")
	code, resp, err := r.Coder.Generate(ctx, agents.CodeInput{System: s.System, Module: *mod, Dependencies: deps})
	mr.Usage.Add(resp)
	if err != nil {
		return fail(err)
	}
	if err := ws.WriteFiles(code.Files); err != nil {
		return fail(err)
	}
	mr.CoderNotes = code.Notes
	mr.Concerns = append(mr.Concerns, code.Concerns...)

	// 2. Tests, derived from the spec with the code visible for identifiers.
	r.logf("   tester: writing tests")
	tests, resp, err := r.Tester.Generate(ctx, agents.TestInput{System: s.System, Module: *mod, Code: code.Files, Dependencies: deps})
	mr.Usage.Add(resp)
	if err != nil {
		return fail(err)
	}
	if err := ws.WriteFiles(tests.Files); err != nil {
		return fail(err)
	}
	mr.Concerns = append(mr.Concerns, tests.Concerns...)

	// 3. Run, and repair the implementation while it fails.
	pattern := "./" + step.Module + "/..."
	var result workspace.Result
	for attempt := 0; ; attempt++ {
		mr.Iterations = attempt + 1
		r.logf("   test run %d", mr.Iterations)
		result, err = ws.Test(ctx, pattern)
		if err != nil {
			return fail(err)
		}
		if result.OK || attempt >= r.Opts.MaxRepairs {
			break
		}
		r.logf("   coder: repairing (%d of %d)", attempt+1, r.Opts.MaxRepairs)
		previous := code.Files
		code, resp, err = r.Coder.Generate(ctx, agents.CodeInput{
			System: s.System, Module: *mod, Dependencies: deps,
			Existing: previous, Tests: tests.Files, Feedback: result.Output,
		})
		mr.Usage.Add(resp)
		if err != nil {
			return fail(err)
		}
		// A repair that renames a file must not leave the old one behind.
		if err := ws.DeleteFiles(removed(previous, code.Files)); err != nil {
			return fail(err)
		}
		if err := ws.WriteFiles(code.Files); err != nil {
			return fail(err)
		}
		mr.Concerns = append(mr.Concerns, code.Concerns...)
	}
	mr.TestsOK = result.OK
	mr.TestOutput = tail(result.Output, 8000)
	for _, f := range append(append([]workspace.File{}, code.Files...), tests.Files...) {
		mr.Files = append(mr.Files, f.Path)
	}

	// 4. Judge.
	r.logf("   validator: judging goals")
	verdict, resp, err := r.Validator.Judge(ctx, agents.ValidateInput{
		System: s.System, Module: *mod, GoalIDs: step.Goals,
		Code: code.Files, Tests: tests.Files, TestResult: result,
		Coverage: tests.Coverage, Concerns: mr.Concerns,
	})
	mr.Usage.Add(resp)
	if err != nil {
		return fail(err)
	}
	mr.Verdict = verdict
	mr.Duration = time.Since(start)
	return mr
}

// dependencyFiles collects the source (not tests) of the named modules.
func dependencyFiles(ws *workspace.Workspace, deps []string) ([]workspace.File, error) {
	if len(deps) == 0 {
		return nil, nil
	}
	tree, err := ws.ReadTree()
	if err != nil {
		return nil, err
	}
	var out []workspace.File
	for _, f := range tree {
		if strings.HasSuffix(f.Path, "_test.go") {
			continue
		}
		for _, d := range deps {
			if strings.HasPrefix(f.Path, d+"/") {
				out = append(out, f)
				break
			}
		}
	}
	return out, nil
}

func removed(before, after []workspace.File) []string {
	keep := map[string]bool{}
	for _, f := range after {
		keep[f.Path] = true
	}
	var gone []string
	for _, f := range before {
		if !keep[f.Path] {
			gone = append(gone, f.Path)
		}
	}
	return gone
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…\n" + s[len(s)-n:]
}

// summarise rolls goal verdicts up: a goal is only as achieved as its weakest
// owning module.
func summarise(s *spec.Spec, mods []ModuleReport) []GoalSummary {
	rank := map[agents.GoalStatus]int{agents.Achieved: 0, agents.Partial: 1, agents.Unverifiable: 2, agents.NotAchieved: 3}
	var out []GoalSummary
	for _, g := range s.System.Goals {
		gs := GoalSummary{ID: g.ID, Statement: g.Statement, Verify: g.Verify, ByModule: map[string]agents.GoalStatus{}}
		worst := agents.GoalStatus("")
		for _, m := range mods {
			for _, v := range m.Verdict.Goals {
				if v.ID != g.ID {
					continue
				}
				gs.ByModule[m.Module] = v.Status
				if worst == "" || rank[v.Status] > rank[worst] {
					worst = v.Status
				}
			}
			if m.Error != "" && owns(s.Module(m.Module), g.ID) {
				gs.ByModule[m.Module] = agents.Unverifiable
				if rank[agents.Unverifiable] > rank[worst] || worst == "" {
					worst = agents.Unverifiable
				}
			}
		}
		if worst == "" {
			worst = agents.Unverifiable
		}
		gs.Status = worst
		out = append(out, gs)
	}
	return out
}

func owns(m *spec.Module, goal string) bool {
	if m == nil {
		return false
	}
	for _, g := range m.Goals {
		if g == goal {
			return true
		}
	}
	for _, sc := range m.Scenarios {
		for _, g := range sc.Goals {
			if g == goal {
				return true
			}
		}
	}
	return false
}
