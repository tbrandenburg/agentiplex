package examples_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/tbrandenburg/agentiplex/core"
)

type discussionActorDraft struct {
	Version int
	Text, Recovery string
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
		"agentiplex state put discussion-actors objective --file contract-actors.md --revision 0",
		"agentiplex state put discussion-actors contract --file contract-actors.json --revision 0",
		"agentiplex send lead --file contract-actors.md --idempotency-key discussion-actors-1 --wait",
	}
	if !reflect.DeepEqual(lines, want) { t.Fatal("expected ordinary native submission:", lines) }
	return lines
}

func TestDiscussionActorREADMECommands(t *testing.T) {
	discussionActorCommands(t)
}

func initialDiscussionActors(a discussionActorContract) bool {
	return a.Owner == "lead" && a.Status == "open" && a.Blocker == "" &&
		a.Draft == (discussionActorDraft{}) && a.Review == (discussionActorReview{Decision:"pending"}) && len(a.Handoffs)==0
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
		strings.TrimSpace(a.Draft.Text)!="" && strings.TrimSpace(a.Draft.Recovery)!="" && strings.TrimSpace(a.Review.Finding)!=""
}

// Verify observed transitions, not a final model-authored account. The
// synthetic recovery omission deliberately forces a direct corrective handoff.
func verifyDiscussionActorTransition(old, next discussionActorContract) error {
	if old.Status!="open" || next.Blocker!="" { return fmt.Errorf("blocked or closed contract: %s",next.Blocker) }
	n:=len(old.Handoffs)
	if next.Status=="done" {
		if old.Owner!="lead" || next.Owner!="lead" || n<5 || next.Draft!=old.Draft || next.Review!=old.Review ||
			!reflect.DeepEqual(old.Handoffs,next.Handoffs) || !acceptedDiscussionActors(next) {
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
			strings.TrimSpace(next.Draft.Text)=="" {
			return fmt.Errorf("expert must advance only its draft and hand off to critic")
		}
		if next.Draft.Version==1 {
			if next.Draft.Recovery!="" {return fmt.Errorf("missing intentionally incomplete checkpoint")}
		} else if old.Review.Decision!="rejected" || old.Review.Version!=old.Draft.Version || strings.TrimSpace(next.Draft.Recovery)=="" {
			return fmt.Errorf("correction must address the current rejected review")
		}
	case "critic":
		if next.Draft!=old.Draft || next.Review.Version!=old.Draft.Version || strings.TrimSpace(next.Review.Finding)=="" {
			return fmt.Errorf("critic must independently review the current draft")
		}
		if old.Draft.Version==1 && (next.Review.Decision!="rejected" || !strings.Contains(strings.ToLower(next.Review.Finding),"recovery")) {
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
	raw,err:=os.ReadFile("../../examples/discussion-team/contract-actors.json")
	if err!=nil {t.Fatal(err)}
	var a discussionActorContract
	if json.Unmarshal(raw,&a)!=nil || !initialDiscussionActors(a){t.Fatal("invalid fixture")}
	advance:=func(to string, mutate func(*discussionActorContract)){
		next:=a
		next.Handoffs=append(append([]discussionActorHandoff(nil),a.Handoffs...),discussionActorHandoff{len(a.Handoffs)+1,a.Owner,to,"open criterion"})
		next.Owner=to
		if mutate!=nil{mutate(&next)}
		if err:=verifyDiscussionActorTransition(a,next);err!=nil{t.Fatal(err)}
		a=next
	}
	advance("expert",nil)
	advance("critic",func(n *discussionActorContract){n.Draft=discussionActorDraft{1,"comparison and consistency",""}})
	bad:=a;bad.Status="done";bad.Owner="lead"
	if verifyDiscussionActorTransition(a,bad)==nil{t.Fatal("accepted premature closure")}
	advance("expert",func(n *discussionActorContract){n.Review=discussionActorReview{1,"rejected","missing recovery"}})
	advance("critic",func(n *discussionActorContract){n.Draft=discussionActorDraft{2,"comparison and consistency","restore and replay"}})
	advance("lead",func(n *discussionActorContract){n.Review=discussionActorReview{2,"accepted","recovery addressed"}})
	closed:=a;closed.Status="done"
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
