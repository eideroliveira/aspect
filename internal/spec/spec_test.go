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
