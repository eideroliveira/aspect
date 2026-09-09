package analyze

import (
	"bytes"
	"strings"
	"testing"
)

func TestGoInventory(t *testing.T) {
	inv, err := Go("testdata/app", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if inv.ModulePath != "example.com/app" || len(inv.Packages) != 2 {
		t.Fatalf("inventory = %+v", inv)
	}
	if got := strings.Join(inv.Frameworks, ","); got != "chi,gorm" {
		t.Fatalf("frameworks = %s", got)
	}

	st := inv.Package("store")
	if st == nil || st.Name != "store" || st.Doc != "Package store persists products." {
		t.Fatalf("store = %+v", st)
	}
	if len(st.Types) != 2 || st.Types[0].Name != "Product" || !st.Types[0].Persistent || st.Types[1].Persistent {
		t.Fatalf("types = %+v", st.Types)
	}
	if f := st.Types[0].Fields; len(f) != 3 || f[0].Name != "(embedded)" || f[1].Tag != `gorm:"uniqueIndex" json:"sku"` {
		t.Fatalf("fields = %+v", f)
	}
	if len(st.Funcs) != 2 || st.Funcs[0].Name != "Open" || st.Funcs[0].Doc != "Open migrates and returns a store." || st.Funcs[1].Receiver != "*Store" {
		t.Fatalf("funcs = %+v", st.Funcs)
	}
	if !strings.HasPrefix(st.Funcs[1].Signature, "func (s *Store) Find(sku string) (*Product, error)") {
		t.Fatalf("signature = %q", st.Funcs[1].Signature)
	}
	if got := strings.Join(st.Tests, ","); got != "TestFind,TestOpen_Migrates" {
		t.Fatalf("tests = %s", got)
	}

	web := inv.Package("web")
	if got := strings.Join(web.Imports, ","); got != "example.com/app/store" {
		t.Fatalf("imports = %s", got)
	}
	var routes []string
	for _, r := range web.Routes {
		routes = append(routes, r.Method+" "+r.Path)
	}
	if got := strings.Join(routes, ","); got != "GET /products/{sku},POST /reservations,GROUP /admin,ANY /healthz" {
		t.Fatalf("routes = %s", got)
	}
	if len(web.Types) != 1 || web.Types[0].Kind != "interface" || web.Types[0].Fields[0].Name != "ServeHTTP" {
		t.Fatalf("web types = %+v", web.Types)
	}

	var buf bytes.Buffer
	inv.Summary(&buf)
	if !strings.Contains(buf.String(), "2 packages") || !strings.Contains(buf.String(), "frameworks: chi, gorm") {
		t.Fatalf("summary:\n%s", buf.String())
	}
	md := st.Render(0)
	for _, want := range []string{"## Package example.com/app/store", "[persistent]", "SKU string `gorm:", "func Open(db *gorm.DB)", "### Tests (2)"} {
		if !strings.Contains(md, want) {
			t.Errorf("render lacks %q:\n%s", want, md)
		}
	}
	if r := st.Render(80); !strings.Contains(r, "truncated") || len(r) > 200 {
		t.Fatalf("budget not applied: %q", r)
	}
}

func TestGoInventoryFilters(t *testing.T) {
	inv, err := Go("testdata/app", Options{Include: []string{"web"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Packages) != 1 || inv.Packages[0].Dir != "web" {
		t.Fatalf("include filter: %+v", inv.Packages)
	}
	inv, err = Go("testdata/app", Options{Exclude: []string{"web"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Packages) != 1 || inv.Packages[0].Dir != "store" {
		t.Fatalf("exclude filter: %+v", inv.Packages)
	}
}
