package importer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/eideroliveira/aspect/internal/agents"
	"github.com/eideroliveira/aspect/internal/llm"
	"github.com/eideroliveira/aspect/internal/spec"
)

const fixture = "../analyze/testdata/app"

// routingLLM answers Describer calls per package and Synthesizer calls once,
// by looking at the prompt.
type routingLLM struct {
	t         *testing.T
	fragments map[string]agents.Fragment
	synthesis agents.Synthesis
	calls     atomic.Int32
}

func (r *routingLLM) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	r.calls.Add(1)
	var out any
	switch {
	case strings.HasPrefix(req.System, "You are the Describer"):
		for dir, f := range r.fragments {
			if strings.Contains(req.Prompt, "(dir "+dir+",") {
				out = f
			}
		}
		if out == nil {
			r.t.Fatalf("no fragment for prompt:\n%s", req.Prompt[:200])
		}
	case strings.HasPrefix(req.System, "You are the Synthesizer"):
		out = r.synthesis
	default:
		r.t.Fatalf("unexpected system prompt: %s", req.System[:60])
	}
	b, _ := json.Marshal(out)
	return llm.Response{Text: string(b), InputTokens: 100, OutputTokens: 50}, nil
}

func fragments() map[string]agents.Fragment {
	return map[string]agents.Fragment{
		"store": {
			Intent: "Persist products.",
			Goals:  []agents.FragmentGoal{{Label: "store-1", Statement: "Products persist", Verify: "test"}},
			Entities: []spec.Entity{{Name: "Product", Intent: "a sellable item", Fields: []spec.Field{
				{Name: "ID", Type: "int", Key: "primary"}, {Name: "SKU", Type: "", Unique: true}}, Relations: []spec.Relation{{Kind: "has_many", Entity: "Ghost"}}}},
			Operations: []spec.Operation{{Name: "Find", Signature: "Find(sku: string) -> Product | error"}},
			Scenarios:  []spec.Scenario{{ID: "S1", When: "Find(A)", Then: "returns A"}, {ID: "S1", When: "Find(Z)", Then: "error"}},
		},
		"web": {
			Intent: "Serve the HTTP API.",
			Interfaces: []spec.Interface{{Name: "API", Kind: "http", Intent: "programmatic access", Surfaces: []spec.Surface{
				{Name: "product", Route: "/products/{sku}", Method: "GET", Entity: "Product"},
				{Name: "reserve", Route: "/reservations", Method: "", Entity: "Nope"},
			}}},
			Operations: []spec.Operation{{Name: "New", Signature: ""}},
		},
	}
}

func TestRunMirrorsPackages(t *testing.T) {
	fake := &routingLLM{t: t, fragments: fragments(), synthesis: agents.Synthesis{
		Name: "App", Intent: "Sell products.",
		Goals: []agents.SynthesisGoal{
			{ID: "G1", Statement: "Products persist", Verify: "test", Modules: []string{"store", "web"}},
			{ID: "G2", Statement: "Orphan", Verify: "test", Modules: []string{"nowhere"}},
		},
		Stack:      spec.LanguageStack{Frameworks: []spec.Framework{{Name: "chi", Module: "github.com/go-chi/chi/v5"}}, ORM: &spec.Framework{Name: "gorm", Module: "gorm.io/gorm"}},
		Interfaces: []spec.Interface{{Name: "api", Kind: "http", Intent: "what clients call", Framework: "chi", Auth: "token"}},
	}}
	cache := t.TempDir()
	res, err := Run(context.Background(), fake, Options{Root: fixture, CacheDir: cache, Repository: "git@example.com:app.git", Commit: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Spec
	if s.System.Name != "app" || s.System.Language != "go" || s.System.ModulePath != "example.com/app" {
		t.Fatalf("system = %+v", s.System)
	}
	if s.System.Source == nil || s.System.Source.Commit != "abc123" || strings.Join(s.System.Source.Frameworks, ",") != "chi,gorm" {
		t.Fatalf("source = %+v", s.System.Source)
	}
	if len(s.Modules) != 2 || s.Modules[0].Name != "store" || s.Modules[1].Name != "web" || strings.Join(s.Modules[1].DependsOn, ",") != "store" {
		t.Fatalf("modules = %+v", s.Modules)
	}
	if s.System.Database == nil || s.System.Database.Engine != "postgres" || len(s.System.Database.Entities) != 1 || strings.Join(s.Modules[0].Entities, ",") != "Product" {
		t.Fatalf("database = %+v, store entities = %v", s.System.Database, s.Modules[0].Entities)
	}
	if len(s.System.Interfaces) != 1 || s.System.Interfaces[0].Name != "api" || s.System.Interfaces[0].Framework != "chi" || len(s.System.Interfaces[0].Surfaces) != 2 {
		t.Fatalf("interfaces = %+v", s.System.Interfaces)
	}
	if got := strings.Join(s.Modules[1].Surfaces, ","); got != "api.product,api.reserve" {
		t.Fatalf("web surfaces = %s", got)
	}
	if len(s.System.Goals) != 1 || strings.Join(s.Modules[0].Goals, ",") != "G1" {
		t.Fatalf("goals = %+v / %v", s.System.Goals, s.Modules[0].Goals)
	}
	text := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"goal G2", "field SKU had no type", "relation to unknown entity Ghost", `unknown entity "Nope"`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing warning %q in:\n%s", want, text)
		}
	}
	if res.Issues.HasErrors() {
		t.Fatalf("assembled spec must validate; issues:\n%v", res.Issues)
	}
	if res.Usage.Calls != 3 || fake.calls.Load() != 3 {
		t.Fatalf("calls = %d / %d", res.Usage.Calls, fake.calls.Load())
	}

	// The YAML round-trips through the loader.
	path := filepath.Join(t.TempDir(), "aspect.yaml")
	if err := Write(path, s, res.Warnings); err != nil {
		t.Fatal(err)
	}
	back, err := spec.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if issues := spec.Validate(back); issues.HasErrors() {
		t.Fatalf("round-tripped spec invalid:\n%v", issues)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "# Assembly warnings:") || strings.Contains(string(b), "key: \"\"") {
		t.Fatalf("yaml should carry warnings and omit empty fields:\n%s", b)
	}

	// A second run hits the cache and makes no calls.
	fake.calls.Store(0)
	if _, err := Run(context.Background(), fake, Options{Root: fixture, CacheDir: cache}); err != nil {
		t.Fatal(err)
	}
	if n := fake.calls.Load(); n != 0 {
		t.Fatalf("cache miss: %d calls", n)
	}
}

func TestRunRetargetsToSwift(t *testing.T) {
	fake := &routingLLM{t: t, fragments: fragments(), synthesis: agents.Synthesis{
		Name: "Shop App", Intent: "Let members browse and reserve products from their phone.",
		Goals: []agents.SynthesisGoal{{ID: "G1", Statement: "Members can reserve", Verify: "test", Modules: []string{"Api Client", "catalog_feature"}}},
		Interfaces: []spec.Interface{
			{Name: "api", Kind: "http", Role: "consumer", Intent: "the existing backend", Surfaces: []spec.Surface{{Name: "reserve", Route: "/reservations", Method: "POST"}}},
			{Name: "app", Kind: "app", Intent: "screens", Surfaces: []spec.Surface{{Name: "Product List", Route: "/products"}}},
		},
		Database: &spec.Database{Engine: "sqlite", Migrations: "auto", Test: spec.DBTest{Engine: "sqlite"}, Entities: []spec.Entity{{Name: "Product", Intent: "cached product", Fields: []spec.Field{{Name: "id", Type: "int", Key: "primary"}}}}},
		Modules: []spec.Module{
			{Name: "Api Client", Intent: "talk to the backend", Surfaces: []string{"api.reserve"}, Scenarios: []spec.Scenario{{ID: "S1", When: "POST", Then: "ok"}}},
			{Name: "catalog_feature", Intent: "browse", DependsOn: []string{"Api Client"}, Entities: []string{"Product"}, Surfaces: []string{"app.Product List"}, Scenarios: []spec.Scenario{{ID: "S1", When: "open", Then: "list"}}},
		},
	}}
	res, err := Run(context.Background(), fake, Options{Root: fixture, Target: "swift", TargetHint: "an iOS app for members"})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Spec
	if s.System.Language != "swift" || s.System.ModulePath != "com.example.shop_app" || s.System.Source.Language != "go" {
		t.Fatalf("system = %+v", s.System)
	}
	if len(s.Modules) != 2 || s.Modules[0].Name != "api_client" || s.Modules[1].DependsOn[0] != "api_client" || s.Modules[1].Surfaces[0] != "app.product_list" {
		t.Fatalf("modules = %+v", s.Modules)
	}
	if s.System.Interfaces[0].Role != "consumer" || s.System.Interfaces[1].Surfaces[0].Name != "product_list" {
		t.Fatalf("interfaces = %+v", s.System.Interfaces)
	}
	if res.Issues.HasErrors() {
		t.Fatalf("retargeted spec must validate; issues:\n%v", res.Issues)
	}
}

func TestIdent(t *testing.T) {
	for in, want := range map[string]string{"modules/translation": "modules_translation", "Api Client": "api_client", "3d": "p_3d", "": "x", ".": "root"} {
		got := Ident(in)
		if in == "." {
			got = ModuleName(in)
		}
		if got != want {
			t.Errorf("Ident(%q) = %q, want %q", in, got, want)
		}
	}
}
