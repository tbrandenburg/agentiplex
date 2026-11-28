package examples_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tbrandenburg/agentiplex/core"
)

type discussionActorDraft struct {
	Version int
	Comparison, Consistency, Recovery string
}

type discussionActorReview struct {
	Version int
	Decision, Finding string
}

type discussionActorHandoff struct {
	Seq int
	From, To, Reason string
}

type discussionActorContract struct {
	Owner, Status, Blocker string
	Draft discussionActorDraft
	Review discussionActorReview
	Handoffs []discussionActorHandoff
	Checked [5]bool
}

// The Markdown itself is the durable contract. Parse the deliberately small
// documented format in the test observer only; the middleware sees a string.
var discussionActorCriteria = [...]string{
	"Compare event sourcing and a transactional relational database (expert)",
	"Explain consistency trade-offs (expert)",
	"Explain failure recovery (expert)",
	"Independent review of current draft (critic)",
	"Final acceptance (lead)",
}

func parseDiscussionActorMarkdown(raw string) (discussionActorContract, error) {
	var a discussionActorContract
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	var err error
	pop := func() string {
		if len(lines) == 0 {
			if err == nil { err = fmt.Errorf("truncated Markdown contract") }
			return ""
		}
		line := lines[0]
		lines = lines[1:]
		return line
	}
	expect := func(want string) {
		if got := pop(); got != want && err == nil {
			err = fmt.Errorf("expected Markdown line %q, got %q", want, got)
		}
	}
	field := func(label string) string {
		line := pop()
		value, ok := strings.CutPrefix(line, label+": ")
		if !ok || strings.TrimSpace(value) == "" {
			if err == nil { err = fmt.Errorf("missing Markdown field %q", label) }
			return ""
		}
		if value == "-" { return "" }
		return value
	}
	number := func(label string) int {
		v := field(label)
		n, e := strconv.Atoi(v)
		if e != nil || n < 0 || strconv.Itoa(n) != v {
			if err == nil { err = fmt.Errorf("invalid number in %s", label) }
		}
		return n
	}
	expect("# Living decision contract")
	expect("## Responsibility")
	a.Owner, a.Status, a.Blocker = field("Owner"), field("Status"), field("Blocker")
	expect("## Acceptance criteria")
	for i, criterion := range discussionActorCriteria {
		switch got := pop(); got {
		case "- [ ] " + criterion:
		case "- [x] " + criterion:
			a.Checked[i] = true
		default:
			if err == nil { err = fmt.Errorf("immutable criterion %d changed: %q", i+1, got) }
		}
	}
	expect("## Expert findings")
	a.Draft.Version = number("Draft version")
	a.Draft.Comparison, a.Draft.Consistency, a.Draft.Recovery = field("Comparison"), field("Consistency"), field("Recovery")
	expect("## Critic findings")
	a.Review.Version = number("Review version")
	a.Review.Decision, a.Review.Finding = field("Decision"), field("Finding")
	expect("## Handover history")
	for _, line := range lines {
		body, ok := strings.CutPrefix(line, "- ")
		parts := strings.SplitN(body, " | ", 4)
		if !ok || len(parts) != 4 || strings.Contains(parts[3], "|") {
			return a, fmt.Errorf("invalid Markdown handover: %q", line)
		}
		seq, e := strconv.Atoi(parts[0])
		if e != nil || seq != len(a.Handoffs)+1 || strconv.Itoa(seq) != parts[0] {
			return a, fmt.Errorf("invalid Markdown handover sequence: %q", line)
		}
		a.Handoffs = append(a.Handoffs, discussionActorHandoff{seq,parts[1],parts[2],parts[3]})
	}
	return a, err
}

// Used only by deterministic verifier tests, not by agents or the live runner.
func renderDiscussionActorMarkdown(a discussionActorContract) string {
	var b strings.Builder
	value := func(v string) string {
		if v == "" { return "-" }
		return v
	}
	fmt.Fprintf(&b,"# Living decision contract\n\n## Responsibility\nOwner: %s\nStatus: %s\nBlocker: %s\n\n## Acceptance criteria\n",a.Owner,a.Status,value(a.Blocker))
	for i,criterion:=range discussionActorCriteria {
		mark:=" "
		if a.Checked[i] {mark="x"}
		fmt.Fprintf(&b,"- [%s] %s\n",mark,criterion)
	}
	fmt.Fprintf(&b,"\n## Expert findings\nDraft version: %d\nComparison: %s\nConsistency: %s\nRecovery: %s\n\n## Critic findings\nReview version: %d\nDecision: %s\nFinding: %s\n\n## Handover history\n",
		a.Draft.Version,value(a.Draft.Comparison),value(a.Draft.Consistency),value(a.Draft.Recovery),a.Review.Version,a.Review.Decision,value(a.Review.Finding))
	for _,h:=range a.Handoffs {fmt.Fprintf(&b,"- %d | %s | %s | %s\n",h.Seq,h.From,h.To,h.Reason)}
	return b.String()
}

func discussionActorCommands(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../README.md")
	if err != nil { t.Fatal(err) }
	_, tail, ok := strings.Cut(string(raw), "<!-- discussion-actors:start -->")
	block, _, end := strings.Cut(tail, "<!-- discussion-actors:end -->")
	block = strings.TrimSpace(block)
	if !ok || !end || !strings.HasPrefix(block, "```sh\n") || !strings.HasSuffix(block, "\n```") {
		t.Fatal("missing actor-owned quickstart")
	}
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(block, "```sh\n"), "\n```"), "\n")
	want := []string{
		"../../bin/agentiplex state put discussion-actors objective --file objective-actors.md --revision 0",
		"../../bin/agentiplex state put discussion-actors contract --file contract-actors.md --revision 0",
		"../../bin/agentiplex send lead --file objective-actors.md --idempotency-key discussion-actors-1 --wait",
	}
	if !reflect.DeepEqual(lines, want) { t.Fatal("expected ordinary native submission:", lines) }
	return lines
}

func TestDiscussionActorREADMECommands(t *testing.T) {
	discussionActorCommands(t)
}

func initialDiscussionActors(a discussionActorContract) bool {
	return a.Owner == "lead" && a.Status == "open" && a.Blocker == "" &&
		a.Draft == (discussionActorDraft{}) && a.Review == (discussionActorReview{Decision:"pending"}) && len(a.Handoffs)==0 && a.Checked==([5]bool{})
}

func discussionActorText(seq int) string {
	return fmt.Sprintf("discussion actors handoff %d; read discussion-actors/contract", seq)
}

func discussionActorID(key string) string {
	// Trusted-local caller scope is empty; require the actual stable key.
	return fmt.Sprintf("%x", sha256.Sum256([]byte("\x00"+key)))
}

func acceptedDiscussionActors(a discussionActorContract) bool {
	return a.Draft.Version >= 2 && a.Review.Version == a.Draft.Version && a.Review.Decision == "accepted" &&
		a.Checked[0] && a.Checked[1] && a.Checked[2] && a.Checked[3] &&
		strings.TrimSpace(a.Draft.Comparison)!="" && strings.TrimSpace(a.Draft.Consistency)!="" && strings.TrimSpace(a.Draft.Recovery)!="" && strings.TrimSpace(a.Review.Finding)!=""
}

// Verify observed transitions, not a final model-authored account. The
// synthetic recovery omission deliberately forces a direct corrective handoff.
func verifyDiscussionActorTransition(old, next discussionActorContract) error {
	if old.Status!="open" || next.Blocker!="" { return fmt.Errorf("blocked or closed contract: %s",next.Blocker) }
	owners := [...]string{"expert","expert","expert","critic","lead"}
	for i, checked := range next.Checked {
		if old.Checked[i] != checked && old.Owner != owners[i] {
			return fmt.Errorf("checkbox %d changed by non-owner %s",i+1,old.Owner)
		}
	}
	evidence := [...]string{next.Draft.Comparison,next.Draft.Consistency,next.Draft.Recovery}
	for i, text := range evidence {
		if next.Checked[i] != (strings.TrimSpace(text)!="") {
			return fmt.Errorf("expert checkbox %d does not match evidence",i+1)
		}
	}
	if next.Checked[3] != (next.Review.Decision=="accepted" && next.Review.Version==next.Draft.Version) {
		return fmt.Errorf("review checkbox does not match current acceptance")
	}
	if next.Checked[4] != (next.Status=="done") {
		return fmt.Errorf("final checkbox does not match closure")
	}
	n:=len(old.Handoffs)
	if next.Status=="done" {
		if old.Owner!="lead" || next.Owner!="lead" || n<5 || next.Draft!=old.Draft || next.Review!=old.Review ||
			!reflect.DeepEqual(old.Handoffs,next.Handoffs) || !acceptedDiscussionActors(next) || !next.Checked[4] || old.Checked[4] {
			return fmt.Errorf("invalid closure")
		}
		return nil
	}
	if next.Status!="open" || len(next.Handoffs)!=n+1 || n>=12 {
		return fmt.Errorf("expected exactly one bounded handoff")
	}
	for i,h:=range old.Handoffs {
		if h!=next.Handoffs[i] {return fmt.Errorf("handoff history rewritten")}
	}
	h:=next.Handoffs[n]
	if h.Seq!=n+1 || h.From!=old.Owner || h.To!=next.Owner || h.From==h.To || strings.TrimSpace(h.Reason)=="" {
		return fmt.Errorf("invalid handoff: %+v",h)
	}
	switch old.Owner {
	case "lead":
		if n!=0 || h.To!="expert" || next.Draft!=old.Draft || next.Review!=old.Review {
			return fmt.Errorf("lead must initiate, not relay review")
		}
	case "expert":
		if h.To!="critic" || next.Review!=old.Review || next.Draft.Version!=old.Draft.Version+1 ||
			!next.Checked[0] || !next.Checked[1] {
			return fmt.Errorf("expert must advance only its draft and hand off to critic")
		}
		if next.Draft.Version==1 {
			if next.Draft.Recovery!="" || next.Checked[2] {return fmt.Errorf("missing intentionally incomplete checkpoint")}
		} else if old.Review.Decision!="rejected" || old.Review.Version!=old.Draft.Version || !next.Checked[2] {
			return fmt.Errorf("correction must address the current rejected review")
		}
	case "critic":
		if next.Draft!=old.Draft || next.Review.Version!=old.Draft.Version || strings.TrimSpace(next.Review.Finding)=="" {
			return fmt.Errorf("critic must independently review the current draft")
		}
		if old.Draft.Version==1 && (next.Checked[3] || next.Review.Decision!="rejected" || !strings.Contains(strings.ToLower(next.Review.Finding),"recovery")) {
			return fmt.Errorf("critic did not reject missing recovery")
		}
		switch next.Review.Decision {
		case "rejected":
			if h.To!="expert" { return fmt.Errorf("rework must return directly to expert") }
		case "accepted":
			if h.To!="lead" || !acceptedDiscussionActors(next) {return fmt.Errorf("stale or premature acceptance")}
		default: return fmt.Errorf("missing review decision")
		}
	default: return fmt.Errorf("unknown actor %q",old.Owner)
	}
	return nil
}

func verifyDiscussionActorReceipts(a discussionActorContract, deliveries []core.Delivery, objective string) error {
	if len(deliveries)!=len(a.Handoffs)+1 {return fmt.Errorf("extra or missing ordinary deliveries")}
	expected:=map[string]struct{target,text string}{
		discussionActorID("discussion-actors-1"):{"lead",objective},
	}
	for _,h:=range a.Handoffs {
		expected[discussionActorID(fmt.Sprintf("discussion-actors-handoff-%d",h.Seq))]=struct{target,text string}{h.To,discussionActorText(h.Seq)}
	}
	for _,d:=range deliveries {
		w,ok:=expected[d.ID]
		if !ok || d.Target!=w.target || d.Text!=w.text || d.Kind!="" || d.ParentID!="" || d.Follow!=nil ||
			d.State!="completed" || !(teamDelivery{Result:d.Result}).hasResult(){
			return fmt.Errorf("invalid direct delivery: %s %s %s",d.ID,d.Target,d.State)
		}
		delete(expected,d.ID)
	}
	if len(expected)!=0 {return fmt.Errorf("missing expected handoffs")}
	return nil
}

func TestDiscussionActorContractTransitions(t *testing.T) {
	raw,err:=os.ReadFile("../../examples/discussion-team/contract-actors.md")
	if err!=nil {t.Fatal(err)}
	a,err:=parseDiscussionActorMarkdown(string(raw))
	if err!=nil || !initialDiscussionActors(a){t.Fatal("invalid fixture",err)}
	if renderDiscussionActorMarkdown(a)!=string(raw){t.Fatal("fixture is not the documented Markdown format")}
	for _,bad:=range []string{
		strings.Replace(string(raw),discussionActorCriteria[0],"Weakened comparison",1),
		strings.Replace(string(raw),"- [ ] "+discussionActorCriteria[2]+"\n","",1),
		strings.Replace(string(raw),"## Expert findings","- [ ] Extra criterion\n## Expert findings",1),
		strings.Replace(string(raw),"- [ ] ","- [X] ",1),
	}{
		if _,err:=parseDiscussionActorMarkdown(bad);err==nil{t.Fatal("accepted changed checklist format")}
	}
	advance:=func(to string, mutate func(*discussionActorContract)){
		next:=a
		next.Handoffs=append(append([]discussionActorHandoff(nil),a.Handoffs...),discussionActorHandoff{len(a.Handoffs)+1,a.Owner,to,"open criterion"})
		next.Owner=to
		if mutate!=nil{mutate(&next)}
		parsed,err:=parseDiscussionActorMarkdown(renderDiscussionActorMarkdown(next))
		if err!=nil || !reflect.DeepEqual(parsed,next){t.Fatal("Markdown transition round trip",err)}
		if err:=verifyDiscussionActorTransition(a,parsed);err!=nil{t.Fatal(err)}
		a=parsed
	}
	advance("expert",nil)
	advance("critic",func(n *discussionActorContract){
		n.Draft=discussionActorDraft{1,"comparison","consistency",""}
		n.Checked[0],n.Checked[1]=true,true
	})
	bad:=a;bad.Status="done";bad.Owner="lead"
	if verifyDiscussionActorTransition(a,bad)==nil{t.Fatal("accepted premature closure")}
	advance("expert",func(n *discussionActorContract){n.Review=discussionActorReview{1,"rejected","missing recovery"}})
	advance("critic",func(n *discussionActorContract){
		n.Draft=discussionActorDraft{2,"comparison","consistency","restore and replay"}
		n.Checked[2]=true
	})
	advance("lead",func(n *discussionActorContract){
		n.Review=discussionActorReview{2,"accepted","recovery addressed"}
		n.Checked[3]=true
	})
	closed:=a;closed.Status="done";closed.Checked[4]=true
	for _,i:=range []int{0,1,2,3,4}{
		bad=closed;bad.Checked[i]=false
		if verifyDiscussionActorTransition(a,bad)==nil{t.Fatal("closed with unchecked criterion",i)}
	}
	bad=closed;bad.Draft.Recovery=""
	if verifyDiscussionActorTransition(a,bad)==nil{t.Fatal("accepted checkbox without evidence")}
	if err:=verifyDiscussionActorTransition(a,closed);err!=nil{t.Fatal(err)}
	bad=closed;bad.Review.Version=1
	if verifyDiscussionActorTransition(a,bad)==nil{t.Fatal("accepted stale review")}
	result:=json.RawMessage(`{"parts":[{"text":"result"}]}`)
	const objective="approved objective"
	ds:=[]core.Delivery{{ID:discussionActorID("discussion-actors-1"),Target:"lead",Text:objective,State:"completed",Result:result}}
	for _,h:=range closed.Handoffs{
		ds=append(ds,core.Delivery{ID:discussionActorID(fmt.Sprintf("discussion-actors-handoff-%d",h.Seq)),Target:h.To,Text:discussionActorText(h.Seq),State:"completed",Result:result})
	}
	if err:=verifyDiscussionActorReceipts(closed,ds,objective);err!=nil{t.Fatal(err)}
	ds[1].State="uncertain"
	if verifyDiscussionActorReceipts(closed,ds,objective)==nil{t.Fatal("accepted uncertain receipt")}
	ds[1].State="completed";ds[1].ID=ds[0].ID
	if verifyDiscussionActorReceipts(closed,ds,objective)==nil{t.Fatal("accepted duplicate receipt")}
}
