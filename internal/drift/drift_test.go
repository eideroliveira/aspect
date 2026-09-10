package drift

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
    scenarios:
      - {id: S1, given: a new counter, when: Inc twice, then: the result is 2, goals: [G1]}
`

// layout writes a generated-looking workspace by hand: what `aspect run`
// would have left behind.
func layout(t *testing.T, out string, files map[string]string) {
	t.Helper()
	root := filepath.Join(out, "counter")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	files["go.mod"] = "module example.com/counter\n\ngo 1.24\n"
	for p, c := range files {
		abs := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func good() map[string]string {
	return map[string]string{
		"counter/counter.go":      "package counter\n\ntype Counter struct{ n int }\n\nfunc (c *Counter) Inc() int { c.n++; return c.n }\n",
		"counter/counter_test.go": "package counter\n\nimport \"testing\"\n\nfunc TestInc_S1(t *testing.T) {\n\tvar c Counter\n\tc.Inc()\n\tif c.Inc() != 2 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
}

func parse(t *testing.T, src string) (*spec.Spec, *plan.Plan) {
	t.Helper()
	s, err := spec.Parse([]byte(src))
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
	return s, p
}

func TestDriftNoLLMDetectsMissingOrphansAndFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes the go toolchain")
	}
	out := t.TempDir()
	layout(t, out, good())
	s, p := parse(t, counterSpec)
	rep, err := Run(context.Background(), nil, s, p, Options{OutDir: out, NoLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Drifted() || !rep.Modules[0].Present || !rep.Modules[0].TestsOK || len(rep.Orphans) != 0 {
		t.Fatalf("clean workspace must not drift: %+v orphans=%v", rep.Modules[0], rep.Orphans)
	}
	if rep.Goals[0].Status != agents.Unverifiable || rep.Goals[0].Change != NoBaseline {
		t.Fatalf("without verdicts goals are unverifiable and without baseline new: %+v", rep.Goals[0])
	}

	// The spec grows a module the code does not have; the code grows a
	// directory the spec does not name; the tests start failing.
	changed := strings.Replace(counterSpec, "modules:\n", "modules:\n  - {name: audit, intent: log every change, goals: [G1], scenarios: [{id: S1, when: Inc, then: logged}]}\n", 1)
	s2, p2 := parse(t, changed)
	files := good()
	files["legacy/legacy.go"] = "package legacy\n"
	files["counter/counter.go"] = "package counter\n\ntype Counter struct{ n int }\n\nfunc (c *Counter) Inc() int { return c.n }\n"
	layout(t, out, files)
	rep, err = Run(context.Background(), nil, s2, p2, Options{OutDir: out, NoLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Drifted() {
		t.Fatal("drift expected")
	}
	if got := strings.Join(rep.Missing(), ","); got != "audit" {
		t.Fatalf("missing = %s", got)
	}
	if got := strings.Join(rep.Orphans, ","); got != "legacy" {
		t.Fatalf("orphans = %s", got)
	}
	if got := strings.Join(rep.Failing(), ","); got != "counter" {
		t.Fatalf("failing = %s", got)
	}
	if rep.Goals[0].Status != agents.NotAchieved {
		t.Fatalf("a missing module makes its goal not achieved: %+v", rep.Goals[0])
	}
	if err := rep.Write(filepath.Join(out, "counter")); err != nil {
		t.Fatal(err)
	}
	md, _ := os.ReadFile(filepath.Join(out, "counter", "DRIFT.md"))
	for _, want := range []string{"## Attention", "no code: audit", "no module claims: legacy", "tests fail: counter", "**Missing:**"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("DRIFT.md lacks %q:\n%s", want, md)
		}
	}
}

// verdictLLM answers the Validator and System Validator with fixed statuses.
type verdictLLM struct {
	module agents.GoalStatus
	system agents.GoalStatus
}

func (v *verdictLLM) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	var out any
	if strings.HasPrefix(req.System, "You are the System Validator") {
		out = agents.SystemVerdict{Goals: []agents.GoalVerdict{{ID: "G1", Status: v.system, Evidence: "sys", Gaps: []string{}, Confidence: 0.8}}, IntentStatus: agents.Aligned, Integration: []agents.IntegrationFinding{}, Recommendations: []string{}}
	} else {
		out = agents.Verdict{Module: "counter", Goals: []agents.GoalVerdict{{ID: "G1", Status: v.module, Evidence: "TestInc_S1", Gaps: []string{}, Confidence: 0.9}}, IntentStatus: agents.Aligned, IntentRationale: "ok", Scenarios: []agents.ScenarioVerdict{}, Recommendations: []string{}}
	}
	b, _ := json.Marshal(out)
	return llm.Response{Text: string(b), InputTokens: 10}, nil
}

func TestDriftComparesVerdictsAgainstBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes the go toolchain")
	}
	out := t.TempDir()
	layout(t, out, good())
	s, p := parse(t, counterSpec)

	first, err := Run(context.Background(), &verdictLLM{module: agents.Achieved, system: agents.Achieved}, s, p, Options{OutDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if first.Goals[0].Status != agents.Achieved || first.Usage.Calls != 2 {
		t.Fatalf("first = %+v calls=%d", first.Goals[0], first.Usage.Calls)
	}
	if err := first.Write(filepath.Join(out, "counter")); err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(out, "counter", "drift.json")

	second, err := Run(context.Background(), &verdictLLM{module: agents.Achieved, system: agents.Partial}, s, p, Options{OutDir: out, Baseline: baseline})
	if err != nil {
		t.Fatal(err)
	}
	g := second.Goals[0]
	if g.Status != agents.Partial || g.Previous != agents.Achieved || g.Change != Regressed || !second.Drifted() {
		t.Fatalf("expected a regression against the baseline: %+v", g)
	}
	if strings.Join(second.Regressions(), ",") != "G1" {
		t.Fatalf("regressions = %v", second.Regressions())
	}
	if _, err := workspace.New(filepath.Join(out, "counter"), workspace.Toolchain{}); err != nil {
		t.Fatal(err)
	}
}
