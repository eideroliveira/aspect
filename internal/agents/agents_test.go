package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eideroliveira/aspect/internal/llm"
	"github.com/eideroliveira/aspect/internal/spec"
	"github.com/eideroliveira/aspect/internal/workspace"
)

// fakeLLM answers with a canned JSON document and records the prompt.
type fakeLLM struct {
	answer string
	prompt string
	system string
	schema map[string]any
}

func (f *fakeLLM) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	f.prompt, f.system, f.schema = req.Prompt, req.System, req.Schema
	return llm.Response{Text: f.answer, InputTokens: 10, OutputTokens: 5}, nil
}

func module() spec.Module {
	return spec.Module{
		Name:   "stock",
		Intent: "count things",
		Goals:  []string{"G1"},
		Scenarios: []spec.Scenario{
			{ID: "S1", When: "Receive", Then: "Available == 5", Goals: []string{"G1"}},
		},
	}
}

func TestCoderKeepsOnlyModuleSourceFiles(t *testing.T) {
	out := CodeOutput{
		Files: []workspace.File{
			{Path: "stock/stock.go", Content: "package stock"},
			{Path: "./stock/extra.go", Content: "package stock"},
			{Path: "stock/stock_test.go", Content: "package stock"},
			{Path: "ledger/ledger.go", Content: "package ledger"},
			{Path: "stock/README.md", Content: "nope"},
		},
		Notes: "n",
	}
	answer, _ := json.Marshal(out)
	f := &fakeLLM{answer: "```json\n" + string(answer) + "\n```"}
	c := &Coder{LLM: f}
	got, _, err := c.Generate(context.Background(), CodeInput{
		System: spec.System{Name: "inv", ModulePath: "example.com/inv"},
		Module: module(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 2 || got.Files[0].Path != "stock/stock.go" || got.Files[1].Path != "stock/extra.go" {
		t.Fatalf("files = %+v", got.Files)
	}
	for _, want := range []string{"example.com/inv/stock", "name: stock", "S1"} {
		if !strings.Contains(f.prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if f.schema == nil || f.system != coderSystem {
		t.Error("coder must send its system prompt and schema")
	}
}

func TestCoderRejectsEmptyProposal(t *testing.T) {
	f := &fakeLLM{answer: `{"files":[{"path":"other/x.go","content":"x"}],"notes":"","concerns":[]}`}
	if _, _, err := (&Coder{LLM: f}).Generate(context.Background(), CodeInput{Module: module()}); err == nil {
		t.Fatal("want error when no file lands in the module directory")
	}
}

func TestTesterKeepsOnlyTestFiles(t *testing.T) {
	f := &fakeLLM{answer: `{"files":[{"path":"stock/stock.go","content":"x"},{"path":"stock/stock_test.go","content":"package stock"}],"coverage":[{"scenario":"S1","tests":["TestReceive_S1"]}],"notes":"","concerns":[]}`}
	got, _, err := (&Tester{LLM: f}).Generate(context.Background(), TestInput{Module: module()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "stock/stock_test.go" {
		t.Fatalf("files = %+v", got.Files)
	}
	if len(got.Coverage) != 1 || got.Coverage[0].Scenario != "S1" {
		t.Fatalf("coverage = %+v", got.Coverage)
	}
}

func TestValidatorFillsMissingGoalsAsUnverifiable(t *testing.T) {
	f := &fakeLLM{answer: `{"module":"stock","goals":[{"id":"G1","status":"achieved","evidence":"TestReceive_S1","gaps":[],"confidence":0.9},{"id":"G9","status":"achieved","evidence":"invented","gaps":[],"confidence":1}],"intent_status":"aligned","intent_rationale":"ok","scenarios":[],"recommendations":[]}`}
	v, _, err := (&Validator{LLM: f}).Judge(context.Background(), ValidateInput{
		Module:     module(),
		GoalIDs:    []string{"G1", "G2"},
		TestResult: workspace.Result{OK: false, Output: "FAIL"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Goals) != 2 || v.Goals[0].Status != Achieved || v.Goals[1].ID != "G2" || v.Goals[1].Status != Unverifiable {
		t.Fatalf("goals = %+v", v.Goals)
	}
	if !strings.Contains(f.prompt, "Test run: FAILED") {
		t.Error("validator prompt must state the real test outcome")
	}
}
