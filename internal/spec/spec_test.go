package spec

import (
	"strings"
	"testing"
)

func mustLoad(t *testing.T, path string) *Spec {
	t.Helper()
	s, err := Load(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return s
}

func TestLoadExample(t *testing.T) {
	s := mustLoad(t, "testdata/inventory.yaml")
	if s.System.Name != "inventory" {
		t.Fatalf("system.name = %q", s.System.Name)
	}
	if got := len(s.Modules); got != 2 {
		t.Fatalf("modules = %d, want 2", got)
	}
	if s.System.Language != "go" {
		t.Fatalf("default language not applied: %q", s.System.Language)
	}
	if issues := Validate(s); issues.HasErrors() {
		t.Fatalf("example spec must validate cleanly, got:\n%s", join(issues))
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	_, err := Parse([]byte("aspect: 1\nsystem:\n  name: x\n  intents: nope\n"))
	if err == nil || !strings.Contains(err.Error(), "intents") {
		t.Fatalf("want unknown-field error naming `intents`, got %v", err)
	}
}

func TestValidateFindsEveryProblemAtOnce(t *testing.T) {
	src := `
aspect: 2
system:
  name: Bad-Name
  intent: ""
  module_path: ""
  goals:
    - id: G1
      statement: dup
    - id: G1
      statement: dup again
      verify: magic
    - id: G9
      statement: nobody owns me
modules:
  - name: a
    intent: a
    goals: [G1, GX]
    depends_on: [b]
  - name: b
    intent: b
    goals: [G1]
    depends_on: [a]
    scenarios:
      - id: S1
        when: x
      - id: S1
        when: x
        then: y
`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	issues := Validate(s)
	want := []string{
		"unsupported spec version 2",
		`"Bad-Name" must match`,
		"system.intent: is required",
		"system.module_path: is required",
		"duplicate goal id \"G1\"",
		`"magic" is not one of`,
		`unknown goal "GX"`,
		"dependency cycle: a -> b -> a",
		`goal "G9" is not owned`,
		"scenario needs at least `when` and `then`",
		`duplicate scenario id "S1"`,
	}
	text := join(issues)
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("missing issue containing %q\nall issues:\n%s", w, text)
		}
	}
	if !issues.HasErrors() {
		t.Error("HasErrors() = false")
	}
}

func TestValidateWarnsWithoutBlocking(t *testing.T) {
	src := `
aspect: 1
system:
  name: sys
  intent: do things
  module_path: example.com/sys
  goals:
    - id: G1
      statement: works
modules:
  - name: core
    intent: core
    scenarios:
      - id: S1
        when: call
        then: ok
        goals: [G1]
`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	issues := Validate(s)
	if issues.HasErrors() {
		t.Fatalf("unexpected errors:\n%s", join(issues))
	}
	if len(issues) == 0 || issues[0].Severity != Warning {
		t.Fatalf("want a warning about the module owning no goal, got:\n%s", join(issues))
	}
}

func join(is Issues) string {
	var b strings.Builder
	for _, i := range is {
		b.WriteString(i.String())
		b.WriteByte('\n')
	}
	return b.String()
}

func TestValidateStackDatabaseInterfaces(t *testing.T) {
	src := `
aspect: 1
system:
  name: shop
  intent: sell things
  module_path: example.com/shop
  goals:
    - {id: G1, statement: products persist}
  stack:
    go:
      frameworks:
        - {name: qor5, module: github.com/qor5/admin/v3, purpose: admin UI}
      orm: {name: gorm, module: gorm.io/gorm}
      allowed_modules: [github.com/theplant/]
    python:
      frameworks: []
  database:
    engine: postgres
    entities:
      - name: Product
        intent: a sellable item
        fields:
          - {name: ID, type: uint, key: primary}
          - {name: SKU, type: string, unique: true, required: true}
        relations:
          - {kind: has_many, entity: Movement}
      - name: Movement
        fields:
          - {name: ID, type: uint, key: primary}
          - {name: Qty, type: int}
  interfaces:
    - name: admin
      kind: web
      intent: back office
      framework: qor5
      surfaces:
        - {name: products, route: /admin/products, entity: Product, operations: [list, create]}
    - name: api
      kind: http
      intent: integrations
      surfaces:
        - {name: reserve, route: /api/reserve, method: POST}
        - {name: broken}
modules:
  - name: catalog
    intent: own products
    goals: [G1]
    entities: [Product]
    surfaces: [admin]
    scenarios:
      - {id: S1, when: create, then: exists}
  - name: stock
    intent: own movements
    entities: [Movement]
    surfaces: [api.reserve, api.nope]
    scenarios:
      - {id: S1, when: reserve, then: ok}
`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if s.System.Database.Migrations != "auto" || s.System.Database.Test.Engine != "sqlite" {
		t.Fatalf("database defaults not applied: %+v", s.System.Database)
	}
	issues := Validate(s)
	text := join(issues)
	for _, want := range []string{
		`system.stack.python: configured for "python"`,
		`entity "Movement" has no intent`,
		`http surfaces need route and method`,
		`surface "api.broken" is not implemented`,
		`unknown surface "nope" in interface "api"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing issue containing %q\nall issues:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{`unknown entity`, `"Product" is not owned`, `framework`} {
		if strings.Contains(text, unwanted) {
			t.Errorf("unexpected issue containing %q\nall issues:\n%s", unwanted, text)
		}
	}
	allowed := s.AllowedImports()
	want := []string{"example.com/shop", "github.com/qor5/admin/v3", "gorm.io/gorm", "github.com/theplant/"}
	if strings.Join(allowed, ",") != strings.Join(want, ",") {
		t.Fatalf("AllowedImports = %v, want %v", allowed, want)
	}
	refs, errs := s.ResolveSurfaces([]string{"admin", "api.reserve"})
	if len(errs) != 0 || len(refs) != 2 || refs[0].ID() != "admin.products" || refs[1].ID() != "api.reserve" {
		t.Fatalf("ResolveSurfaces = %+v, %v", refs, errs)
	}
}

func TestValidateOwnershipConflicts(t *testing.T) {
	src := `
aspect: 1
system:
  name: shop
  intent: sell things
  module_path: example.com/shop
  goals: [{id: G1, statement: x}]
  database:
    engine: mysql
    test: {engine: mysql}
    entities:
      - {name: A, intent: a, fields: [{name: ID, type: int, key: primary}]}
      - {name: B, intent: b, fields: [{name: ID, type: int, key: primary}]}
  interfaces:
    - name: api
      kind: http
      intent: i
      framework: ghost
      surfaces: [{name: x, route: /x, method: GET}]
modules:
  - {name: m1, intent: i, goals: [G1], entities: [A], surfaces: [api.x]}
  - {name: m2, intent: i, entities: [A], surfaces: [api.x]}
`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	text := join(Validate(s))
	for _, want := range []string{
		`entity "A" is owned by several modules (m1, m2)`,
		`entity "B" is not owned by any module`,
		`surface "api.x" is implemented by several modules`,
		`"ghost" is not declared in the providing tier's stack`,
		`tests run against mysql but no dsn_env is set`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing issue containing %q\nall issues:\n%s", want, text)
		}
	}
}

const twoTier = `
aspect: 1
system:
  name: shop
  intent: sell things to members on the web and on their phones
  topology: api_backend
  goals:
    - {id: G1, statement: members can reserve products, verify: test}
    - {id: G2, statement: the catalog is one source of truth, verify: invariant}
  database:
    engine: postgres
    tier: backend
    entities:
      - {name: Product, intent: a sellable item, fields: [{name: ID, type: int, key: primary}, {name: SKU, type: string, unique: true}]}
  interfaces:
    - name: api
      kind: http
      intent: what the app calls
      provider: backend
      surfaces:
        - {name: reserve, route: /api/reservations, method: POST, entity: Product}
    - name: app
      kind: app
      intent: member screens
      provider: mobile
      surfaces:
        - {name: catalog, route: /catalog, entity: Product}
    - name: push
      kind: http
      intent: notifications
      provider: external
      service: Firebase Cloud Messaging
      surfaces:
        - {name: send, route: /v1/send, method: POST}
tiers:
  - name: backend
    intent: own the data and serve the API
    language: go
    module_path: example.com/shop
    modules:
      - name: catalog
        intent: own products
        goals: [G2]
        entities: [Product]
        scenarios: [{id: S1, when: create, then: exists}]
      - name: api
        intent: serve the app
        goals: [G1]
        depends_on: [catalog]
        surfaces: [api]
        scenarios: [{id: S1, when: POST, then: 201}]
  - name: mobile
    intent: the members' iOS app
    language: swift
    module_path: com.example.shop
    depends_on: [backend]
    database:
      engine: sqlite
      entities:
        - {name: CachedProduct, intent: offline copy, fields: [{name: id, type: int, key: primary}]}
    modules:
      - name: api_client
        intent: talk to the backend
        goals: [G1]
        consumes: [api.reserve, push.send]
        scenarios: [{id: S1, when: reserve, then: ok}]
      - name: catalog_feature
        intent: browse
        goals: [G1]
        depends_on: [api_client]
        entities: [CachedProduct]
        surfaces: [app.catalog]
        scenarios: [{id: S1, when: open, then: list}]
`

func TestValidateTwoTierSpec(t *testing.T) {
	s, err := Parse([]byte(twoTier))
	if err != nil {
		t.Fatal(err)
	}
	issues := Validate(s)
	if issues.HasErrors() {
		t.Fatalf("two-tier spec must validate:\n%s", join(issues))
	}
	if s.EffectiveTopology() != APIBackend || len(s.EffectiveTiers()) != 2 || s.TierOf("api_client").Name != "mobile" {
		t.Fatalf("shape: topology=%s tiers=%d", s.EffectiveTopology(), len(s.EffectiveTiers()))
	}
	if s.Entity("CachedProduct") == nil || s.Module("catalog_feature") == nil {
		t.Fatal("lookups must span tiers and tier-local databases")
	}
	if got := s.Tier("mobile").AllowedImports(); len(got) != 1 || got[0] != "com.example.shop" {
		t.Fatalf("mobile imports = %v", got)
	}
}

func TestValidateTierRules(t *testing.T) {
	src := strings.NewReplacer(
		"depends_on: [catalog]\n        surfaces: [api]", "depends_on: [api_client]\n        surfaces: [api, app.catalog]",
		"consumes: [api.reserve, push.send]", "consumes: [api.reserve, push.send, app.catalog]\n        surfaces: [push.send]",
		"provider: backend\n      surfaces:\n        - {name: reserve", "surfaces:\n        - {name: reserve",
		"tier: backend", "tier: nowhere",
	).Replace(twoTier)
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	text := join(Validate(s))
	for _, want := range []string{
		`system.database.tier: unknown tier "nowhere"`,
		`system.interfaces[0].provider: is required in a multi-tier spec`,
		`module "api_client" is in tier "mobile"; cross-tier dependencies go through interfaces`,
		`surface "app.catalog" is provided by tier "mobile"; only that tier's modules may implement it`,
		`surface "push.send" is served by an external service; consume it instead`,
		`surface "app.catalog" is provided by this tier; depend on the implementing module`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing issue containing %q\nall issues:\n%s", want, text)
		}
	}
}

func TestSingleTierSpecIsAnImplicitTier(t *testing.T) {
	s := mustLoad(t, "testdata/inventory.yaml")
	tiers := s.EffectiveTiers()
	if len(tiers) != 1 || tiers[0].Name != "" || tiers[0].Language != "go" || len(tiers[0].Modules) != 2 {
		t.Fatalf("implicit tier = %+v", tiers[0])
	}
	if s.EffectiveTopology() != Monolith {
		t.Fatalf("topology = %s", s.EffectiveTopology())
	}
	if s.TierOf("stock") == nil || s.TierOf("stock").Name != "" {
		t.Fatal("TierOf must find modules of the implicit tier")
	}
}
