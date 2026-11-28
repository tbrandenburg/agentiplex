package examples_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex/core"
	"go.yaml.in/yaml/v3"
)

const discussionTopic = "Should we use event sourcing?"
const discussionStart = "<!-- discussion-quickstart:start -->"
const discussionEnd = "<!-- discussion-quickstart:end -->"

var discussionRoles = []string{"lead", "expert", "critic"}

// The README, not a second launch script, owns the commands. Only the marked
// quickstart's five shell blocks are executable by the live test.
func discussionCommands(t *testing.T) [][]string {
	t.Helper()
	raw, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, tail, ok := strings.Cut(string(raw), discussionStart)
	if !ok {
		t.Fatal("missing README discussion start marker")
	}
	section, _, ok := strings.Cut(tail, discussionEnd)
	if !ok {
		t.Fatal("missing README discussion end marker")
	}
	var blocks [][]string
	var block []string
	inBlock := false
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "\x60\x60\x60sh":
			if inBlock {
				t.Fatal("nested README shell block")
			}
			inBlock = true
			block = nil
		case line == "\x60\x60\x60" && inBlock:
			blocks = append(blocks, block)
			inBlock = false
		case inBlock && line != "":
			if strings.HasSuffix(line, "\\") || strings.HasSuffix(line, "&") {
				t.Fatal("use separate complete foreground commands in the README")
			}
			block = append(block, line)
		}
	}
	counts := []int{2, 1, 1, 3, 5}
	if inBlock || len(blocks) != len(counts) {
		t.Fatal("README discussion must contain setup, example directory, daemon, gateways and discussion blocks")
	}
	for i, count := range counts {
		if len(blocks[i]) != count {
			t.Fatalf("README discussion block %d: want %d commands, got %d", i+1, count, len(blocks[i]))
		}
	}
	return blocks
}

// The contract quickstart is documented alongside the ordinary discussion.
func discussionContractCommand(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, tail, ok := strings.Cut(string(raw), "<!-- discussion-contract:start -->")
	block, _, end := strings.Cut(tail, "<!-- discussion-contract:end -->")
	if !ok || !end {
		t.Fatal("missing README until-done quickstart")
	}
	command := strings.TrimSpace(block)
	if !strings.HasPrefix(command, "\x60\x60\x60sh\n") || !strings.HasSuffix(command, "\n\x60\x60\x60") {
		t.Fatal("expected a single README shell block")
	}
	command = strings.TrimPrefix(command, "\x60\x60\x60sh\n")
	command = strings.TrimSuffix(command, "\n\x60\x60\x60")
	if strings.Contains(command, "\n") || !strings.HasPrefix(command, "agentiplex send lead --file contract.md ") ||
		!strings.Contains(command, " --idempotency-key discussion-contract-1 ") ||
		!strings.Contains(command, " --until-done ") ||
		!strings.HasSuffix(command, " --wait") || strings.Contains(command, "--nudge-after") {
		t.Fatal("unexpected native contract quickstart")
	}
	return command
}

func TestDiscussionContractREADMECommand(t *testing.T) {
	discussionContractCommand(t)
}

func TestDiscussionREADMECommands(t *testing.T) {
	blocks := discussionCommands(t)
	raw, err := os.ReadFile("../../.github/workflows/discussion-quickstart.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string
				Run  string
			}
		}
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	var setup string
	for _, step := range workflow.Jobs["readme-live"].Steps {
		if step.Name == "README setup (build and install)" {
			setup = step.Run
		}
	}
	if !strings.HasPrefix(setup, strings.Join(blocks[0], "\n")+"\n") {
		t.Fatal("CI setup commands differ from the README")
	}
	if blocks[1][0] != "cd examples/discussion-team" || blocks[2][0] != `agentiplexd --mcp --storage "$HOME/.local/state/agentiplex-discussion"` {
		t.Fatal("quickstart must use the existing example directory and private HOME storage")
	}
	for i, role := range discussionRoles {
		if !strings.HasPrefix(blocks[3][i], "opencode-a2a-lite --name "+role+" ") ||
			!strings.Contains(blocks[3][i], " --no-env") {
			t.Fatalf("README gateway %s must have explicit identity and no credential forwarding", role)
		}
		if !strings.HasPrefix(blocks[4][i], "agentiplex register "+role+" ") {
			t.Fatalf("README missing registration for %s", role)
		}
	}
	if !strings.Contains(blocks[4][3], "send lead \""+discussionTopic+"\"") ||
		!strings.Contains(blocks[4][3], " --wait") ||
		blocks[4][4] != "agentiplex notes list discussion" {
		t.Fatal("README must submit the test topic and retrieve the verdict and notes using the native CLI")
	}
}

// Completion is not a semantic grade. Independently inspect durable work and
// note topics as well as the actual human-readable output of both CLI commands.
func verifyDiscussion(deliveries []core.Delivery, notes []core.Note, verdict, printedNotes string) error {
	if len(deliveries) < 3 || len(deliveries) >= 100 {
		return fmt.Errorf("expected bounded delivery evidence for all three agents")
	}
	seen := map[string]bool{}
	var lead core.Delivery
	workers := map[string]bool{}
	for _, d := range deliveries {
		if d.ID == "" || seen[d.ID] {
			return fmt.Errorf("missing or duplicate delivery ID")
		}
		seen[d.ID] = true
		if d.State != "completed" || !(teamDelivery{Result: d.Result}).hasResult() {
			return fmt.Errorf("delivery %s (%s) did not complete with a result: %s", d.ID, d.Target, d.State)
		}
		switch d.Target {
		case "lead":
			if lead.ID != "" || d.Text != discussionTopic {
				return fmt.Errorf("expected one lead delivery for the original topic")
			}
			lead = d
		case "expert", "critic":
			if !strings.Contains(d.Text, discussionTopic) {
				return fmt.Errorf("worker %s did not receive the original topic", d.Target)
			}
			workers[d.Target] = true
		default:
			return fmt.Errorf("unexpected recipient %s", d.Target)
		}
	}
	if lead.ID == "" || !workers["expert"] || !workers["critic"] {
		return fmt.Errorf("missing lead, expert or critic result")
	}
	header := "lead: completed (" + lead.ID + ")"
	_, body, ok := strings.Cut(verdict, "\n\n")
	if !strings.Contains(verdict, header) || !ok || strings.TrimSpace(body) == "" {
		return fmt.Errorf("send --wait did not print the lead verdict")
	}
	topics := map[string]bool{}
	for _, n := range notes {
		if n.Key != "" && !n.At.IsZero() && strings.TrimSpace(n.Text) != "" &&
			strings.Contains(printedNotes, "["+n.Topic+"] ") {
			topics[n.Topic] = true
		}
	}
	for _, role := range discussionRoles {
		if !topics[role] {
			return fmt.Errorf("missing persisted or printed %s notes", role)
		}
	}
	return nil
}

func TestVerifyDiscussionEvidence(t *testing.T) {
	result := json.RawMessage("{\"parts\":[{\"text\":\"finding\"}]}")
	deliveries := []core.Delivery{
		{ID: "lead-id", Target: "lead", Text: discussionTopic, State: "completed", Result: result},
		{ID: "expert-id", Target: "expert", Text: discussionTopic, State: "completed", Result: result},
		{ID: "critic-id", Target: "critic", Text: "Review: "+discussionTopic, State: "completed", Result: result},
	}
	var notes []core.Note
	for _, role := range discussionRoles {
		notes = append(notes, core.Note{Key: "note-"+role, Topic: role, Text: "finding", At: time.Now()})
	}
	verdict := "lead: completed (lead-id)\n\nReasoned verdict"
	printed := "[lead] local\nfinding\n[expert] local\nfinding\n[critic] local\nfinding"
	if err := verifyDiscussion(deliveries, notes, verdict, printed); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		d []core.Delivery
		n []core.Note
		v, p string
	}{
		{"missing worker", deliveries[:2], notes, verdict, printed},
		{"missing notes", deliveries, notes[:2], verdict, printed},
		{"missing verdict", deliveries, notes, "lead: completed (lead-id)", printed},
		{"missing printed notes", deliveries, notes, verdict, "[expert] local"},
		{"failed worker", append(append([]core.Delivery(nil), deliveries[:2]...), core.Delivery{ID:"critic-id",Target:"critic",State:"failed",Result:result}), notes, verdict, printed},
		{"wrong topic", append(append([]core.Delivery(nil), deliveries[:2]...), core.Delivery{ID:"critic-id",Target:"critic",Text:"other topic",State:"completed",Result:result}), notes, verdict, printed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := verifyDiscussion(tc.d, tc.n, tc.v, tc.p); err == nil {
				t.Fatal("accepted incomplete discussion")
			}
		})
	}
}
