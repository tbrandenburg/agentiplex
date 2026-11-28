package examples_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/tbrandenburg/agentiplex/core"
)

type actorCriterion struct {
	ID, Description, Owner, Status, Evidence, Candidate string
	Turn int
}

type actorHandoff struct {
	Seq int
	From, To, Reason string
}

type actorContractState struct {
	Owner, Status, Blocker string
	Handoffs []actorHandoff
	Criteria []actorCriterion
}

func actorCommands(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../examples/repository-team/README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, tail, ok := strings.Cut(string(raw), "<!-- actors-quickstart:start -->")
	block, _, end := strings.Cut(tail, "<!-- actors-quickstart:end -->")
	block = strings.TrimSpace(block)
	if !ok || !end || !strings.HasPrefix(block, "```sh\n") || !strings.HasSuffix(block, "\n```") {
		t.Fatal("missing actor contract quickstart")
	}
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(block, "```sh\n"), "\n```"), "\n")
	want := []string{
		"agentiplex state put repository objective --file objective.actors.md --revision 0",
		"agentiplex state put repository actors-contract --file contract.actors.json --revision 0",
		"agentiplex send pm --file objective.actors.md --idempotency-key actors-objective --wait",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatal("expected ordinary native submission, not a workflow runner:", lines)
	}
	return lines
}

func TestActorREADMECommands(t *testing.T) {
	actorCommands(t)
	for _, name := range []string{"actors-reader", "actors-writer"} {
		base, tools := policyResources(t, "repository-team", name)
		if (name == "actors-reader" && base != "read-only") || (name == "actors-writer" && base != "full-access") {
			t.Fatal("unexpected actor policy base", name)
		}
		found := false
		for _, tool := range tools {
			if tool == "agentiplex_send_message:*" { found = true }
			if tool == "agentiplex_ask:*" || tool == "agentiplex_resolve_request:*" {
				t.Fatal("unexpected explicit coordinator grant", name, tool)
			}
		}
		if !found { t.Fatal("missing direct messaging grant", name) }
	}
}

func actorMessage(seq int) string {
	return fmt.Sprintf("contract actors handoff %d; read repository/actors-contract", seq)
}

func actorReceiptID(key string) string {
	// Trusted-local caller scope is empty; check the actual stable admission key.
	return fmt.Sprintf("%x", sha256.Sum256([]byte("\x00"+key)))
}

func actorDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && strings.ToLower(s) == s
}

func validateActorInitial(a actorContractState) error {
	if a.Owner != "pm" || a.Status != "open" || a.Blocker != "" || len(a.Handoffs) != 0 || len(a.Criteria) != 4 {
		return fmt.Errorf("invalid approved initial state")
	}
	owners := []string{"tester", "developer", "tester", "quality"}
	ids := []string{"baseline", "implementation", "tests", "quality"}
	for i, item := range a.Criteria {
		if item.ID != ids[i] || item.Owner != owners[i] || item.Description == "" || item.Status != "pending" || item.Evidence != "" || item.Turn != 0 || item.Candidate != "" {
			return fmt.Errorf("invalid approved criterion %d", i)
		}
	}
	return nil
}

// Validate each actually observed contract transition, not just the final
// agent-authored story. Ownership is a trusted-local working agreement.
func verifyActorTransition(old, next actorContractState) error {
	if old.Status != "open" || len(old.Criteria) != len(next.Criteria) || next.Blocker != "" {
		return fmt.Errorf("blocked/closed or malformed contract transition: %s", next.Blocker)
	}
	for i, before := range old.Criteria {
		after := next.Criteria[i]
		if before.ID != after.ID || before.Description != after.Description || before.Owner != after.Owner {
			return fmt.Errorf("approved criterion changed: %s", before.ID)
		}
		if before != after {
			if before.Owner != old.Owner || after.Turn != len(old.Handoffs) || strings.TrimSpace(after.Evidence) == "" {
				return fmt.Errorf("wrong artifact owner or missing current evidence: %s", before.ID)
			}
			if after.Status != "rework" && after.Status != "verified" {
				return fmt.Errorf("invalid criterion status: %s", before.ID)
			}
			if before.ID != "baseline" && after.Status == "verified" && !actorDigest(after.Candidate) {
				return fmt.Errorf("missing candidate: %s", before.ID)
			}
		}
	}
	n := len(old.Handoffs)
	if next.Status == "done" {
		if old.Owner != "pm" || next.Owner != "pm" || !reflect.DeepEqual(old.Handoffs, next.Handoffs) || !reflect.DeepEqual(old.Criteria, next.Criteria) {
			return fmt.Errorf("closure must be the PM's separate final turn")
		}
		return verifyActorFinal(next)
	}
	if next.Status != "open" || len(next.Handoffs) != n+1 || n >= 16 {
		return fmt.Errorf("expected exactly one bounded handoff per nonterminal turn")
	}
	for i, h := range old.Handoffs {
		if h != next.Handoffs[i] { return fmt.Errorf("handoff history rewritten") }
	}
	h := next.Handoffs[n]
	if h.Seq != n+1 || h.From != old.Owner || h.To != next.Owner || h.From == h.To || strings.TrimSpace(h.Reason) == "" {
		return fmt.Errorf("invalid responsibility transfer: %+v", h)
	}
	switch h.To {
	case "pm", "tester", "developer", "quality":
	default:
		return fmt.Errorf("unexpected owner %q", h.To)
	}
	// The initial actual failing checkpoint must be stored before the first
	// implementation activation. It belongs to the tester, not the PM.
	if n == 0 && h.To != "tester" { return fmt.Errorf("baseline owner must act first") }
	if n == 1 {
		p := next.Criteria
		if h.From != "tester" || h.To != "developer" || p[0].Status != "verified" || p[0].Turn != 1 ||
			p[1].Status != "pending" || p[2].Status != "rework" || p[2].Turn != 1 || p[3].Status != "pending" ||
			!strings.Contains(p[0].Evidence, "go test -count=1 ./...") || !strings.Contains(p[0].Evidence, "TestClamp") {
			return fmt.Errorf("missing observed failed-baseline checkpoint before repair")
		}
	}
	return nil
}

func verifyActorFinal(a actorContractState) error {
	if a.Status != "done" || a.Owner != "pm" || a.Blocker != "" || len(a.Criteria) != 4 || len(a.Handoffs) < 5 || len(a.Handoffs) > 16 {
		return fmt.Errorf("not a fulfilled actor-owned contract")
	}
	for _, item := range a.Criteria {
		if item.Status != "verified" || strings.TrimSpace(item.Evidence) == "" || item.Turn < 1 || item.Turn > len(a.Handoffs) || a.Handoffs[item.Turn-1].To != item.Owner {
			return fmt.Errorf("missing independent owner activation for %s", item.ID)
		}
	}
	p := a.Criteria
	if p[0].Turn != 1 || p[1].Turn <= p[0].Turn || p[2].Turn <= p[1].Turn || p[3].Turn <= p[1].Turn ||
		!actorDigest(p[1].Candidate) || p[2].Candidate != p[1].Candidate || p[3].Candidate != p[1].Candidate ||
		!strings.Contains(p[2].Evidence, "go test -count=1 ./...") {
		return fmt.Errorf("stale or missing independent verification for the final candidate")
	}
	return nil
}

func verifyActorReceipts(a actorContractState, ds []core.Delivery, objective string) error {
	if len(ds) != len(a.Handoffs)+1 { return fmt.Errorf("extra or missing ordinary deliveries: %d", len(ds)) }
	expected := map[string]struct{ target, text string }{
		actorReceiptID("actors-objective"): {"pm", objective},
	}
	for _, h := range a.Handoffs {
		expected[actorReceiptID(fmt.Sprintf("actors-handoff-%d", h.Seq))] = struct{ target, text string }{h.To, actorMessage(h.Seq)}
	}
	for _, d := range ds {
		want, ok := expected[d.ID]
		if !ok || d.Target != want.target || d.Text != want.text || d.Kind != "" || d.ParentID != "" || d.Follow != nil || d.State != "completed" || !(teamDelivery{Result: d.Result}).hasResult() {
			return fmt.Errorf("unexpected or incomplete ordinary handoff: %s %s %s", d.ID, d.Target, d.State)
		}
		delete(expected, d.ID)
	}
	if len(expected) != 0 { return fmt.Errorf("missing admitted handoff receipts") }
	return nil
}

func TestActorTransitionRejectsInvalidOwnership(t *testing.T) {
	raw, err := os.ReadFile("../../examples/repository-team/contract.actors.json")
	if err != nil { t.Fatal(err) }
	var initial actorContractState
	if err := json.Unmarshal(raw, &initial); err != nil { t.Fatal(err) }
	if err := validateActorInitial(initial); err != nil { t.Fatal(err) }
	clone := func(a actorContractState) actorContractState {
		b, _ := json.Marshal(a)
		var out actorContractState
		if err := json.Unmarshal(b, &out); err != nil { t.Fatal(err) }
		return out
	}
	first := clone(initial)
	first.Owner = "tester"
	first.Handoffs = []actorHandoff{{1, "pm", "tester", "baseline"}}
	if err := verifyActorTransition(initial, first); err != nil { t.Fatal(err) }
	checkpoint := clone(first)
	checkpoint.Owner = "developer"
	checkpoint.Handoffs = append(checkpoint.Handoffs, actorHandoff{2,"tester","developer","TestClamp failed"})
	checkpoint.Criteria[0].Status, checkpoint.Criteria[0].Turn, checkpoint.Criteria[0].Evidence = "verified",1,"go test -count=1 ./... exit 1 TestClamp failed"
	checkpoint.Criteria[2].Status, checkpoint.Criteria[2].Turn, checkpoint.Criteria[2].Evidence = "rework",1,checkpoint.Criteria[0].Evidence
	if err := verifyActorTransition(first, checkpoint); err != nil { t.Fatal(err) }
	for _, tc := range []struct {
		name string
		mutate func(*actorContractState)
	}{
		{"wrong owner", func(a *actorContractState){ a.Criteria[1].Status="verified"; a.Criteria[1].Evidence="not yours"; a.Criteria[1].Turn=1 }},
		{"rewritten history", func(a *actorContractState){ a.Handoffs[0].Reason="changed" }},
		{"stale evidence", func(a *actorContractState){ a.Criteria[0].Turn=0 }},
		{"missing failing checkpoint", func(a *actorContractState){ a.Criteria[2].Status="pending" }},
		{"wrong next owner", func(a *actorContractState){ a.Owner="quality" }},
		{"premature closure", func(a *actorContractState){ a.Status="done"; a.Owner="pm" }},
	}{
		t.Run(tc.name,func(t *testing.T){
			bad:=clone(checkpoint);tc.mutate(&bad)
			if verifyActorTransition(first,bad)==nil{t.Fatal("accepted invalid transition")}
		})
	}
}
