package examples_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex/core"
	"github.com/tbrandenburg/agentiplex/internal/simagent"
	"go.yaml.in/yaml/v3"
)

var repositoryIDs = []string{"pm", "architect", "developer", "tester", "quality"}

var delegation = []string{"ask", "send_message", "broadcast", "dispatch"}
var listen = regexp.MustCompile(`listen=(\S+)`)

func read(t *testing.T, parts ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "examples"}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func policyResources(t *testing.T, example, name string) (string, []string) {
	t.Helper()
	var p struct {
		Base     string
		Policies []struct{ Action, Resource, Effect string }
	}
	if err := json.Unmarshal(read(t, example, "policies", name+".json"), &p); err != nil {
		t.Fatal(err)
	}
	var tools []string
	for _, r := range p.Policies {
		if r.Effect != "allow" {
			t.Fatalf("%s/%s: non-allow rule %+v", example, name, r)
		}
		tools = append(tools, strings.TrimSuffix(strings.TrimPrefix(r.Resource, "agentiplex_"), ":*"))
	}
	return p.Base, tools
}

func TestTeamYAMLStrictRegistryAndExplicitCoordinator(t *testing.T) {
	for _, ex := range []string{"repository-team", "autonomous-team"} {
		reg, err := core.ParseRegistry(read(t, ex, "team.yaml"))
		if err != nil {
			t.Fatalf("%s: %v", ex, err)
		}
		if ex == "repository-team" {
			if len(reg.Agents) != 0 || len(reg.Groups) != 0 || len(reg.Pools) != 0 {
				t.Fatal("repository-team must start MCP before registering endpoints")
			}
			continue
		}
		if _, ok := reg.Agents["pm"]; !ok {
			t.Fatalf("%s: coordinator pm not registered", ex)
		}
		for kind, set := range map[string]map[string][]string{"group": reg.Groups, "pool": reg.Pools} {
			for name, members := range set {
				if slices.Contains(members, "pm") {
					t.Fatalf("%s %s %s contains the coordinator", ex, kind, name)
				}
				for _, m := range members {
					if _, ok := reg.Agents[m]; !ok {
						t.Fatalf("%s %s %s: unregistered member %s", ex, kind, name, m)
					}
				}
			}
		}
	}
}

func TestPoliciesAreLeastPrivilegeAndOnlyCoordinatorDelegates(t *testing.T) {
	files := map[string][]string{"repository-team": {"coordinator", "reader", "writer"}, "autonomous-team": {"coordinator", "worker"}}
	for ex, names := range files {
		for _, name := range names {
			base, tools := policyResources(t, ex, name)
			for _, must := range []string{"discover_agents", "note", "read_notes", "put_state"} {
				if !slices.Contains(tools, must) {
					t.Fatalf("%s/%s lacks %s", ex, name, must)
				}
			}
			for _, d := range delegation {
				if slices.Contains(tools, d) != (name == "coordinator") {
					t.Fatalf("%s/%s: delegation tool %s grant mismatch", ex, name, d)
				}
			}
			for _, tool := range tools {
				if strings.Contains(tool, "*") || strings.Contains(tool, "shell") || strings.Contains(tool, "edit") {
					t.Fatalf("%s/%s: unexpected grant %s", ex, name, tool)
				}
			}
			if want := map[bool]string{true: "full-access", false: "read-only"}[name == "writer"]; base != want {
				t.Fatalf("%s/%s base %s want %s", ex, name, base, want)
			}
		}
	}
}

// Both native configs are tested with isolated ports/storage and provider-free agents.
// Repository-team starts MCP first and registers endpoints via the same REST API as the CLI;
// autonomous-team still exercises declarative registry provisioning.
func TestRealDaemonStartsEachTeamYAMLWithSimulatedAgents(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agentiplexd")
	if o, err := exec.Command("go", "build", "-o", bin, "github.com/tbrandenburg/agentiplex/cmd/agentiplexd").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, o)
	}
	for _, ex := range []string{"repository-team", "autonomous-team"} {
		t.Run(ex, func(t *testing.T) {
			var doc map[string]any
			if err := yaml.Unmarshal(read(t, ex, "team.yaml"), &doc); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			doc["server"] = map[string]any{"listen": "127.0.0.1:0"}
			doc["broker"] = map[string]any{"mode": "embedded", "storage": filepath.Join(dir, "data")}
			doc["observability"] = map[string]any{"dashboard": false}
			registry := doc["registry"].(map[string]any)
			agents, _ := registry["agents"].(map[string]any)
			if ex == "repository-team" {
				agents = make(map[string]any, len(repositoryIDs))
				for _, id := range repositoryIDs {
					agents[id] = nil
				}
			}
			for id := range agents {
				srv := httptest.NewServer(nil)
				srv.Config.Handler = simagent.Handler(srv.URL, id, "demo", nil)
				t.Cleanup(srv.Close)
				agents[id] = map[string]any{"url": srv.URL}
			}
			raw, _ := yaml.Marshal(doc)
			cfg := filepath.Join(dir, "team.yaml")
			if err := os.WriteFile(cfg, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "--config", cfg)
			out, _ := cmd.StderrPipe()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
			addr := make(chan string, 1)
			go func() {
				sc := bufio.NewScanner(out)
				for sc.Scan() {
					if m := listen.FindStringSubmatch(sc.Text()); m != nil && strings.Contains(sc.Text(), "agentiplexd ready") {
						addr <- m[1]
					}
				}
			}()
			var api string
			select {
			case a := <-addr:
				api = "http://" + a
			case <-time.After(30 * time.Second):
				t.Fatal("daemon did not become ready")
			}
			if ex == "repository-team" {
				for _, id := range repositoryIDs {
					body, _ := json.Marshal(map[string]string{"id": id, "url": agents[id].(map[string]any)["url"].(string)})
					resp, err := http.Post(api+"/api/v1/agents", "application/json", bytes.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					resp.Body.Close()
					if resp.StatusCode/100 != 2 {
						t.Fatalf("register %s: %d", id, resp.StatusCode)
					}
				}
			}
			var got []core.Agent
			getJSON(t, api+"/api/v1/agents", &got)
			if len(got) != len(agents) {
				t.Fatalf("registered %d agents, want %d", len(got), len(agents))
			}
			reg, _ := core.ParseRegistry(read(t, ex, "team.yaml"))
			for name, members := range reg.Groups {
				var c core.Collection
				getJSON(t, api+"/api/v1/groups/"+name, &c)
				if len(c.Members) != len(members) {
					t.Fatalf("group %s: %+v", name, c)
				}
			}
			for name, members := range reg.Pools {
				var c core.Collection
				getJSON(t, api+"/api/v1/pools/"+name, &c)
				if len(c.Members) != len(members) {
					t.Fatalf("pool %s: %+v", name, c)
				}
			}
			sendAndComplete(t, api, "pm")
		})
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func sendAndComplete(t *testing.T, api, target string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"target": target, "text": "review the fixture", "idempotencyKey": "example-check"})
	resp, err := http.Post(api+"/api/v1/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var d core.Delivery
	_ = json.NewDecoder(resp.Body).Decode(&d)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 || d.ID == "" {
		t.Fatalf("send: %d %+v", resp.StatusCode, d)
	}
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		getJSON(t, fmt.Sprintf("%s/api/v1/deliveries/%s", api, d.ID), &d)
		if d.State == "completed" {
			return
		}
	}
	t.Fatalf("delivery did not complete: %+v", d)
}

func TestRepositoryTeamHasRolePerAgentAndBoundedObjective(t *testing.T) {
	for _, id := range repositoryIDs {
		role := bytes.TrimSpace(read(t, "repository-team", "roles", id+".md"))
		if len(role) == 0 || len(role) > 64*1024 || !bytes.Contains(role, []byte("objective")) {
			t.Fatalf("invalid or objective-unaware system prompt for %s", id)
		}
	}
	objective := read(t, "repository-team", "objective.md")
	if len(bytes.TrimSpace(objective)) == 0 || len(objective) > 4096 {
		t.Fatalf("objective.md must be nonempty and stay within the 4096-byte submission limit (%d bytes)", len(objective))
	}
}

func TestAutonomousObjectiveIsBounded(t *testing.T) {
	if n := len(bytes.TrimSpace(read(t, "autonomous-team", "objective.md"))); n == 0 || n > 4096 {
		t.Fatalf("autonomous-team/objective.md must be non-empty and within the 4096-byte submission limit (%d bytes)", n)
	}
}

func TestDiscussionExampleReadOnly(t *testing.T) {
	for _,name:=range []string{"lead","expert","critic"}{
		prompt:=bytes.TrimSpace(read(t,"discussion-team","roles",name+".md"))
		if len(prompt)==0||len(prompt)>64*1024||!bytes.Contains(prompt,[]byte("discussion"))||!bytes.Contains(prompt,[]byte("agentiplex_note")){
			t.Fatalf("invalid discussion role %s",name)
		}
	}
	for _,name:=range []string{"coordinator","reader"}{
		base,tools:=policyResources(t,"discussion-team",name)
		if base!="read-only"{t.Fatal("discussion must be read-only")}
		expected:=[]string{"agentiplex","note","read_notes"}
		if name=="coordinator"{expected=append(expected,"discover_agents","get_delivery","ask")}
		slices.Sort(expected)
		slices.Sort(tools)
		if !slices.Equal(tools,expected){t.Fatalf("%s unexpected grants %v",name,tools)}
	}
	var cfg struct{
		MCP struct{Servers map[string]struct{URL string; Codemode bool}}
	}
	if err:=json.Unmarshal(read(t,"discussion-team","workspace","opencode.json"),&cfg);err!=nil{t.Fatal(err)}
	if len(cfg.MCP.Servers)!=1||cfg.MCP.Servers["agentiplex"].URL!="http://127.0.0.1:8080/mcp"||cfg.MCP.Servers["agentiplex"].Codemode{
		t.Fatal("discussion example must use the default loopback MCP endpoint and individual tools")
	}
}
