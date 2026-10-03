package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/eideroliveira/aspect/internal/gate"
)

// runGate dispatches `aspect gate plan|check|render`.
func runGate(sub string, args []string) error {
	switch sub {
	case "plan":
		return runGatePlan(args)
	case "check":
		return runGateCheck(args, false)
	case "render":
		return runGateCheck(args, true)
	case "extract":
		return runGateExtract(args)
	}
	return fmt.Errorf("unknown gate subcommand %q (want plan, check, render or extract)", sub)
}

// runGateExtract saves the report an agent printed as the last fenced json
// block of its final message. Input is the --output-format json result of
// Claude Code or Gemini CLI, or plain text.
func runGateExtract(args []string) error {
	fs := flag.NewFlagSet("gate extract", flag.ContinueOnError)
	out := fs.String("o", "", "where to write the report (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("gate extract: -o is required")
	}
	in := os.Stdin
	if fs.NArg() > 0 {
		f, err := os.Open(fs.Arg(0))
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}
	raw, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	text := gate.AgentText(raw)
	b, err := gate.LastJSONBlock(text)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(*out, b, 0o644)
}

func runGatePlan(args []string) error {
	fs := flag.NewFlagSet("gate plan", flag.ContinueOnError)
	config := fs.String("config", gate.DefaultConfigPath, "gates.yaml")
	base := fs.String("base", "origin/main", "base ref; the diff starts at its merge base with head")
	head := fs.String("head", "HEAD", "head ref")
	out := fs.String("o", "", "also write the plan to this file (e.g. .review/<run-id>/plan.json)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := gate.LoadConfig(*config)
	if err != nil {
		return err
	}
	headSHA, err := git("rev-parse", "--verify", *head+"^{commit}")
	if err != nil {
		return err
	}
	baseSHA, err := git("merge-base", *base, headSHA)
	if err != nil {
		return err
	}
	changes, err := diffChanges(baseSHA, headSHA)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(gate.BuildPlan(c, baseSHA, headSHA, changes), "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if *out != "" {
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			return err
		}
	}
	_, err = os.Stdout.Write(b)
	return err
}

// runGateCheck validates the reports in a run directory against its
// plan.json. check prints a summary and fails on block; render prints the
// Markdown comment and always succeeds once the inputs load.
func runGateCheck(args []string, render bool) error {
	name := "gate check"
	if render {
		name = "gate render"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	config := fs.String("config", gate.DefaultConfigPath, "gates.yaml")
	override := fs.Bool("override", false, "a maintainer overrode the gate (the review-gates/override label)")
	public := fs.Bool("public", false, "redact security findings at public_redaction severities")
	asJSON := fs.Bool("json", false, "check: print the result as JSON")
	dir, err := parseWithPositional(fs, args)
	if err != nil {
		return err
	}
	c, err := gate.LoadConfig(*config)
	if err != nil {
		return err
	}
	pb, err := os.ReadFile(filepath.Join(dir, "plan.json"))
	if err != nil {
		return fmt.Errorf("%w (write it with aspect gate plan -o %s/plan.json)", err, dir)
	}
	var p gate.Plan
	if err := json.Unmarshal(pb, &p); err != nil {
		return fmt.Errorf("plan.json: %w", err)
	}
	if p.Schema != gate.PlanSchema {
		return fmt.Errorf("plan.json: schema %q, want %q", p.Schema, gate.PlanSchema)
	}
	reports := map[string]gate.Input{}
	for _, a := range p.Agents {
		path := filepath.Join(dir, a.Agent+".json")
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		reports[a.Agent] = gate.Input{Data: b, Err: err}
	}
	res := gate.Check(c, &p, reports, *override)
	if render {
		fmt.Print(gate.Render(c, &p, res, gate.RenderOptions{Public: *public}))
		return nil
	}
	if *asJSON {
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(b))
	} else {
		for _, a := range res.Agents {
			line := fmt.Sprintf("%-22s %-8s %-8s blocking=%d warnings=%d", a.Agent, a.Mode, a.Status, len(a.Blocking), len(a.Warnings))
			if a.Error != "" {
				line += "  " + a.Error
			}
			fmt.Println(line)
		}
		fmt.Println("outcome:", res.Outcome)
	}
	if res.Failed() {
		return fmt.Errorf("review gates blocked")
	}
	return nil
}

// parseWithPositional parses flags given before or after the single
// positional argument, so `check DIR -override` and `check -override DIR`
// both work.
func parseWithPositional(fs *flag.FlagSet, args []string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() == 0 {
		return "", fmt.Errorf("%s: missing run directory", fs.Name())
	}
	pos := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil {
		return "", err
	}
	if fs.NArg() > 0 {
		return "", fmt.Errorf("%s: unexpected arguments %v", fs.Name(), fs.Args())
	}
	return pos, nil
}

// diffChanges lists the files changed between base and head with their
// line counts. Renames are reported as a delete and an add so both paths
// are matched against the gates.
func diffChanges(base, head string) ([]gate.Change, error) {
	out, err := git("diff", "--numstat", "--no-renames", "-z", base, head)
	if err != nil {
		return nil, err
	}
	var changes []gate.Change
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected git diff --numstat record %q", rec)
		}
		added, _ := strconv.Atoi(parts[0]) // "-" for binary files counts as 0
		deleted, _ := strconv.Atoi(parts[1])
		changes = append(changes, gate.Change{Path: parts[2], Added: added, Deleted: deleted})
	}
	return changes, nil
}

func git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}
