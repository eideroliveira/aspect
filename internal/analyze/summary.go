package analyze

import (
	"fmt"
	"io"
	"strings"
)

// Summary prints a human-readable overview of the inventory.
func (inv *Inventory) Summary(w io.Writer) {
	lines, files, types, persistent, funcs, routes, tests := 0, 0, 0, 0, 0, 0, 0
	for _, p := range inv.Packages {
		lines += p.Lines
		files += p.Files
		types += len(p.Types)
		funcs += len(p.Funcs)
		routes += len(p.Routes)
		tests += len(p.Tests)
		for _, t := range p.Types {
			if t.Persistent {
				persistent++
			}
		}
	}
	fmt.Fprintf(w, "%s (%s)\n", inv.ModulePath, inv.Root)
	fmt.Fprintf(w, "  %d packages, %d files, %d lines\n", len(inv.Packages), files, lines)
	fmt.Fprintf(w, "  %d exported types (%d persistent), %d exported funcs, %d routes, %d tests\n", types, persistent, funcs, routes, tests)
	if len(inv.Frameworks) > 0 {
		fmt.Fprintf(w, "  frameworks: %s\n", strings.Join(inv.Frameworks, ", "))
	}
	fmt.Fprintln(w)
	for _, p := range inv.Packages {
		persist := 0
		for _, t := range p.Types {
			if t.Persistent {
				persist++
			}
		}
		fmt.Fprintf(w, "  %-40s %5d lines  %3d types (%d persistent) %3d funcs %3d routes %3d tests  deps=%d\n",
			p.Dir, p.Lines, len(p.Types), persist, len(p.Funcs), len(p.Routes), len(p.Tests), len(p.Imports))
	}
}

// Render writes one package as Markdown for a prompt. Budget caps the number
// of characters; the least informative parts (method lists) are trimmed first.
func (p *Package) Render(budget int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Package %s (dir %s, %d files, %d lines)\n\n", p.ImportPath, p.Dir, p.Files, p.Lines)
	if p.Doc != "" {
		fmt.Fprintf(&b, "%s\n\n", p.Doc)
	}
	if len(p.Frameworks) > 0 {
		fmt.Fprintf(&b, "Frameworks: %s\n\n", strings.Join(p.Frameworks, ", "))
	}
	if len(p.Imports) > 0 {
		fmt.Fprintf(&b, "Depends on (in-module): %s\n\n", strings.Join(p.Imports, ", "))
	}
	if len(p.External) > 0 {
		fmt.Fprintf(&b, "External imports: %s\n\n", strings.Join(p.External, ", "))
	}
	if len(p.Routes) > 0 {
		b.WriteString("### Routes\n\n")
		for _, r := range p.Routes {
			fmt.Fprintf(&b, "- %s %s -> %s (%s)\n", r.Method, r.Path, r.Func, r.File)
		}
		b.WriteString("\n")
	}
	if len(p.Types) > 0 {
		b.WriteString("### Types\n\n")
		for _, t := range p.Types {
			tag := ""
			if t.Persistent {
				tag = " [persistent]"
			}
			fmt.Fprintf(&b, "- %s %s%s", t.Kind, t.Name, tag)
			if t.Doc != "" {
				fmt.Fprintf(&b, ": %s", t.Doc)
			}
			b.WriteString("\n")
			for _, f := range t.Fields {
				if f.Tag != "" {
					fmt.Fprintf(&b, "    - %s %s `%s`\n", f.Name, f.Type, f.Tag)
				} else {
					fmt.Fprintf(&b, "    - %s %s\n", f.Name, f.Type)
				}
			}
		}
		b.WriteString("\n")
	}
	if len(p.Funcs) > 0 {
		b.WriteString("### Exported API\n\n")
		for _, f := range p.Funcs {
			fmt.Fprintf(&b, "- %s", f.Signature)
			if f.Doc != "" {
				fmt.Fprintf(&b, "  // %s", f.Doc)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(p.Tests) > 0 {
		fmt.Fprintf(&b, "### Tests (%d)\n\n%s\n\n", len(p.Tests), strings.Join(p.Tests, ", "))
	}
	for _, d := range p.Docs {
		fmt.Fprintf(&b, "### Document %s (the owner's own description; trust it over inference)\n\n%s\n\n", d.Name, strings.TrimSpace(d.Content))
	}
	s := b.String()
	if budget > 0 && len(s) > budget {
		s = s[:budget] + "\n…(inventory truncated to fit the prompt budget)\n"
	}
	return s
}
