package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eideroliveira/aspect/internal/agents"
	"github.com/eideroliveira/aspect/internal/llm"
	"github.com/eideroliveira/aspect/internal/plan"
	"github.com/eideroliveira/aspect/internal/spec"
	"github.com/eideroliveira/aspect/internal/workspace"
)

// scriptedLLM plays back answers in order and records every prompt, so the
// test can assert what each agent was shown.
type scriptedLLM struct {
	answers []string
	prompts []string
}

func (s *scriptedLLM) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	s.prompts = append(s.prompts, req.Prompt)
	if len(s.answers) == 0 {
		return llm.Response{}, context.Canceled
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return llm.Response{Text: a, InputTokens: 100, OutputTokens: 50}, nil
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const counterSpec = `
aspect: 1
system:
  name: counter
  intent: count events
  module_path: example.com/counter
  goals:
    - {id: G1, statement: the count never decreases, verify: invariant}
modules:
  - name: counter
    intent: hold a monotonically increasing count
    goals: [G1]
    interface:
      - {name: Inc, signature: "func (c *Counter) Inc() int"}
    scenarios:
      - {id: S1, given: a new counter, when: Inc twice, then: the result is 2, goals: [G1]}
`

func TestRunRepairsUntilTestsPassThenValidates(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes the go toolchain")
	}
	s, err := spec.Parse([]byte(counterSpec))
	if err != nil {
		t.Fatal(err)
	}
	if issues := spec.Validate(s); issues.HasErrors() {
		t.Fatal(issues)
	}
	p, err := plan.Build(s)
	if err != nil {
		t.Fatal(err)
	}

	buggy := agents.CodeOutput{Files: []workspace.File{{Path: "counter/counter.go",
		Content: "package counter\n\ntype Counter struct{ n int }\n\nfunc (c *Counter) Inc() int { return c.n }\n"}}, Notes: "first try", Concerns: []string{}}
	fixed := agents.CodeOutput{Files: []workspace.File{{Path: "counter/counter.go",
		Content: "package counter\n\ntype Counter struct{ n int }\n\nfunc (c *Counter) Inc() int { c.n++; return c.n }\n"}}, Notes: "fixed", Concerns: []string{}}
	tests := agents.TestOutput{Files: []workspace.File{{Path: "counter/counter_test.go",
		Content: "package counter\n\nimport \"testing\"\n\nfunc TestInc_S1(t *testing.T) {\n\tvar c Counter\n\tc.Inc()\n\tif got := c.Inc(); got != 2 {\n\t\tt.Fatalf(\"got %d\", got)\n\t}\n}\n"}},
		Coverage: []agents.ScenarioCoverage{{Scenario: "S1", Tests: []string{"TestInc_S1"}}}, Concerns: []string{}}
	verdict := agents.Verdict{Module: "counter", Goals: []agents.GoalVerdict{{ID: "G1", Status: agents.Achieved, Evidence: "TestInc_S1", Gaps: []string{}, Confidence: 0.9}},
		IntentStatus: agents.Aligned, IntentRationale: "does what it says", Scenarios: []agents.ScenarioVerdict{{Scenario: "S1", Covered: true, Test: "TestInc_S1"}}, Recommendations: []string{}}

	fake := &scriptedLLM{answers: []string{mustJSON(t, buggy), mustJSON(t, tests), mustJSON(t, fixed), mustJSON(t, verdict)}}
	out := t.TempDir()
	r := New(fake, Options{OutDir: out, MaxRepairs: 2, Model: "fake"})
	rep, err := r.Run(context.Background(), s, p)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(fake.answers) != 0 {
		t.Fatalf("%d scripted answers unused", len(fake.answers))
	}
	m := rep.Modules[0]
	if !m.TestsOK || m.Iterations != 2 {
		t.Fatalf("tests_ok=%v iterations=%d output:\n%s", m.TestsOK, m.Iterations, m.TestOutput)
	}
	if m.Usage.Calls != 4 || rep.Usage.InputTokens != 400 {
		t.Fatalf("usage = %+v / %+v", m.Usage, rep.Usage)
	}
	if !strings.Contains(fake.prompts[2], "Failing output") || !strings.Contains(fake.prompts[2], "got 0") {
		t.Fatalf("repair prompt must carry the failing test output, got:\n%s", fake.prompts[2])
	}
	if !strings.Contains(fake.prompts[3], "Test run: PASSED") {
		t.Fatal("validator must see the final, passing test run")
	}
	if rep.Goals[0].Status != agents.Achieved || rep.Goals[0].ByModule["counter"] != agents.Achieved {
		t.Fatalf("goal summary = %+v", rep.Goals[0])
	}

	if err := rep.Write(out); err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(filepath.Join(out, "counter", "REPORT.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"✅ achieved", "Tests passed after 2 run(s)", "TestInc_S1"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("REPORT.md lacks %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "counter", "counter", "counter.go")); err != nil {
		t.Fatal("generated code missing from workspace")
	}
}

func TestRunReportsFailedModuleAsUnverifiable(t *testing.T) {
	s, _ := spec.Parse([]byte(counterSpec))
	p, _ := plan.Build(s)
	fake := &scriptedLLM{answers: []string{"not json at all"}}
	r := New(fake, Options{OutDir: t.TempDir()})
	rep, err := r.Run(context.Background(), s, p)
	if err == nil || rep == nil {
		t.Fatalf("want error and partial report, got err=%v rep=%v", err, rep)
	}
	if rep.Modules[0].Error == "" || rep.Goals[0].Status != agents.Unverifiable {
		t.Fatalf("report = %+v", rep)
	}
}
