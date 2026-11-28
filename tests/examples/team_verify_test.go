package examples_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Small wire views of the public REST/CLI responses. Never trust a model's
// reported success in place of the durable records or actual repository state.
type teamDelivery struct {
	ID, Target, State, Text, AssignedAgent string
	Result json.RawMessage
	Children []struct{ ID string }
}

type teamState struct {
	Key, Value string
	Revision uint64
}

func (d teamDelivery) recipient() string {
	if d.AssignedAgent != "" { return d.AssignedAgent }
	return d.Target
}

func (d teamDelivery) hasResult() bool {
	v := bytes.TrimSpace(d.Result)
	return len(v) > 0 && !bytes.Equal(v, []byte("null")) && !bytes.Equal(v, []byte("{}")) && !bytes.Equal(v, []byte("[]")) && !bytes.Equal(v, []byte("\"\""))
}

func terminalTeamState(s string) bool {
	switch s {
	case "completed", "failed", "partial", "uncertain", "input-required", "auth-required": return true
	}
	return false
}

func verifyTeamEvidence(root teamDelivery, deliveries []teamDelivery, state []teamState, objective string, revision uint64, scenario string) error {
	if root.Target != "pm" || root.State != "completed" || !root.hasResult() || root.Text != objective {
		return errors.New("PM did not produce a completed result for the original objective")
	}
	workers := map[string]bool{}
	for _, d := range deliveries {
		if d.State != "completed" { return errors.New("unresolved or failed delivery") }
		if d.ID != root.ID && d.hasResult() { workers[d.recipient()] = true }
	}
	if scenario == "autonomous" {
		if !workers["architect"] { return errors.New("missing independent real worker result") }
		var pm, architect bool
		for _, s := range state {
			if s.Revision == 0 || s.Value == "" { continue }
			pm = pm || s.Key == "pm"
			architect = architect || s.Key == "architect" || strings.HasPrefix(s.Key, "note-architect-")
		}
		if !pm || !architect { return errors.New("missing PM or real worker shared-state evidence") }
		return nil
	}
	if !workers["developer"] || (!workers["tester"] && !workers["quality"]) {
		return errors.New("missing implementer or independent reviewer")
	}
	original := false
	notes := map[string]bool{}
	for _, s := range state {
		if s.Key == "objective" && s.Value == objective && s.Revision == revision && revision > 0 { original = true }
		if s.Revision == 0 { continue }
		for _, role := range []string{"pm", "developer", "tester", "quality"} {
			if !strings.HasPrefix(s.Key, "note-"+role+"-") { continue }
			var n struct{ Text string }
			if json.Unmarshal([]byte(s.Value), &n) == nil && strings.TrimSpace(n.Text) != "" { notes[role] = true }
		}
	}
	if !original { return errors.New("original shared objective missing, altered or rewritten") }
	if !notes["pm"] || !notes["developer"] || (!(workers["tester"] && notes["tester"]) && !(workers["quality"] && notes["quality"])) {
		return errors.New("missing coordinator, implementer or reviewer notes")
	}
	if scenario == "integration" && !(workers["tester"] && workers["quality"] && notes["tester"] && notes["quality"]) {
		return errors.New("integration requires independent tester and quality findings")
	}
	return nil
}

const brokenReadme = "# Synthetic repository\n\n[Usage](docs/missing.md)\n"
const brokenCalc = "package fixture\n\nfunc Clamp(value, lower, upper int) int {\n\treturn min(lower, max(upper, value))\n}\n"
const fixedCalc = "package fixture\n\nfunc Clamp(value, lower, upper int) int {\n\treturn min(upper, max(lower, value))\n}\n"
const fixtureModule = "module fixture\n\ngo 1.23.0\n"
const fixtureCheck = "package fixture\n\nimport \"testing\"\n\nfunc TestClamp(t *testing.T) {\n\tfor _, c := range [][4]int{{-3,0,10,0},{0,0,10,0},{4,0,10,4},{10,0,10,10},{12,0,10,10},{-2,-5,-1,-2},{5,2,2,2}} {\n\t\tif got := Clamp(c[0],c[1],c[2]); got != c[3] { t.Errorf(\"Clamp(%d,%d,%d) = %d, want %d\",c[0],c[1],c[2],got,c[3]) }\n\t}\n}\n"
const independentCheck = "package fixture\n\nimport \"testing\"\n\nfunc TestIndependentClamp(t *testing.T) {\n\tfor _, c := range [][4]int{{-9,-5,-1,-5},{3,-5,-1,-1},{-3,-5,-1,-3},{-10,-2,7,-2},{99,-2,7,7},{0,-2,7,0},{-9,3,3,3}} {\n\t\tif got := Clamp(c[0],c[1],c[2]); got != c[3] { t.Errorf(\"independent clamp: got %d want %d\",got,c[3]) }\n\t}\n}\n"

func writeTeamFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, []byte(text), 0600); err != nil { t.Fatal(err) }
}

func commandOutput(dir string, seconds time.Duration, env []string, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), seconds)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil { return out, fmt.Errorf("%s: %w", name, ctx.Err()) }
	return out, err
}

func gitTeam(dir string, args ...string) (string, error) {
	out, err := commandOutput(dir, 30*time.Second, nil, "git", args...)
	if err != nil { return "", fmt.Errorf("git %s: %w: %s", args[0], err, out) }
	return strings.TrimSpace(string(out)), nil
}

func mustTeamGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitTeam(dir, args...)
	if err != nil { t.Fatal(err) }
	return out
}

func initTeamFixture(t *testing.T, dir string) string {
	t.Helper()
	writeTeamFile(t, filepath.Join(dir, "README.md"), brokenReadme)
	writeTeamFile(t, filepath.Join(dir, "docs/usage.md"), "Call Clamp(value, lower, upper).\n")
	writeTeamFile(t, filepath.Join(dir, "go.mod"), fixtureModule)
	writeTeamFile(t, filepath.Join(dir, "calc.go"), brokenCalc)
	writeTeamFile(t, filepath.Join(dir, "calc_test.go"), fixtureCheck)
	mustTeamGit(t, dir, "init", "-q", "-b", "main")
	mustTeamGit(t, dir, "config", "user.name", "Example CI")
	mustTeamGit(t, dir, "config", "user.email", "ci@example.invalid")
	mustTeamGit(t, dir, "add", ".")
	mustTeamGit(t, dir, "commit", "-qm", "synthetic fixture")
	return mustTeamGit(t, dir, "rev-parse", "HEAD")
}

func teamSnapshot(dir string) (string, error) {
	var b strings.Builder
	for _, args := range [][]string{{"rev-parse","HEAD"},{"symbolic-ref","--short","HEAD"},{"status","--porcelain=v1","--untracked-files=all"},{"diff","--no-ext-diff","--binary","HEAD","--"},{"for-each-ref","--format=%(refname):%(objectname)","refs/heads"}} {
		out, err := gitTeam(dir,args...)
		if err != nil { return "", err }
		b.WriteString(out)
		b.WriteByte(0)
	}
	return b.String(), nil
}

func verifyTeamCommand(dir string, timeout time.Duration, env []string, name string, args ...string) error {
	before, err := teamSnapshot(dir)
	if err != nil { return err }
	out, err := commandOutput(dir, timeout, env, name, args...)
	if err != nil { return fmt.Errorf("verification command failed: %w: %s",err,out) }
	after, err := teamSnapshot(dir)
	if err != nil { return err }
	if after != before { return errors.New("verification command changed Git-visible state") }
	return nil
}

// Execute fixture tests without dependency downloads or an enclosing go.work.
func fixtureEnv() []string {
	env := []string{}
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e,"=")
		switch k {
		case "PATH","HOME","TMPDIR","LANG","LC_ALL","USER","LOGNAME","GOCACHE","GOMODCACHE","GOROOT": env=append(env,e)
		}
	}
	return append(env,"GOWORK=off","GOPROXY=off","GOSUMDB=off","GOTOOLCHAIN=local","GOFLAGS=")
}

func verifyTeamWorkspace(dir, base, scenario string) error {
	// Compare actual Git objects rather than a reported branch name or test result.
	require := func(want string,args ...string) error {
		out, err := gitTeam(dir,args...)
		if err != nil { return err }
		if out != want { return fmt.Errorf("git %s: unexpected repository state", args[0]) }
		return nil
	}
	wanted := "calc.go"
	if scenario == "docs" { wanted = "README.md" }
	if err := require("main","branch","--show-current"); err != nil { return err }
	if err := require("","remote"); err != nil { return err }
	if scenario == "integration" {
		if err := require("","status","--porcelain"); err != nil { return err }
		line, err := gitTeam(dir,"rev-list","--parents","-n","1","HEAD")
		if err != nil { return err }
		parents := strings.Fields(line)
		if len(parents)!=3 || parents[1]!=base || parents[2]==base { return errors.New("expected a genuine two-parent merge into original main") }
		candidate := parents[2]
		branches, err := gitTeam(dir,"for-each-ref","--points-at="+candidate,"--format=%(refname:short)","refs/heads")
		if err != nil { return err }
		if branches=="" { return errors.New("retained candidate branch missing") }
		if err := require(base,"merge-base",base,candidate); err != nil { return err }
		if err := require("","diff","--name-only",candidate,"HEAD"); err != nil { return err }
		if err := require(wanted,"diff","--name-only",base,candidate); err != nil { return err }
		fmt.Printf("Verified local merge %s, candidate %s\n",parents[0],candidate)
	} else {
		if err := require(base,"rev-parse","HEAD"); err != nil { return err }
		if err := require("main","for-each-ref","--format=%(refname:short)","refs/heads"); err != nil { return err }
	}
	for _, args := range [][]string{{"diff","--cached","--name-only"},{"ls-files","--others","--exclude-standard"},{"diff","--check",base}} {
		if err := require("",args...); err != nil { return err }
	}
	if err := require(wanted,"diff","--name-only",base); err != nil { return err }
	info, err := os.Lstat(filepath.Join(dir,wanted))
	if err != nil || !info.Mode().IsRegular() { return errors.New("changed file must be regular") }
	if scenario == "docs" {
		raw, err := os.ReadFile(filepath.Join(dir,wanted))
		if err != nil { return err }
		if string(raw)!=strings.Replace(brokenReadme,"docs/missing.md","docs/usage.md",1) && string(raw)!=strings.Replace(brokenReadme,"docs/missing.md","./docs/usage.md",1) {
			return errors.New("README link not repaired while preserving unrelated content")
		}
		return nil
	}
	if err := verifyTeamCommand(dir,90*time.Second,fixtureEnv(),"go","test","-count=1","./..."); err != nil { return err }
	// The separate operator-owned tests run against an exact copy of the
	// accepted implementation, outside the agent workspace.
	tmp, err := os.MkdirTemp("","agentiplex-independent-")
	if err != nil { return err }
	defer os.RemoveAll(tmp)
	raw, err := os.ReadFile(filepath.Join(dir,wanted))
	if err != nil { return err }
	for name, content := range map[string]string{"go.mod":fixtureModule,"calc.go":string(raw),"independent_test.go":independentCheck} {
		if err := os.WriteFile(filepath.Join(tmp,name),[]byte(content),0600); err != nil { return err }
	}
	out, err := commandOutput(tmp,90*time.Second,fixtureEnv(),"go","test","-count=1","./...")
	if err != nil { return fmt.Errorf("independent test failed: %w: %s",err,out) }
	return nil
}

func sortedTeamIDs(m map[string]bool) []string {
	out:=make([]string,0,len(m))
	for id:=range m { out=append(out,id) }
	sort.Strings(out)
	return out
}
