//go:build linux

package examples_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex/core"
)

// Execute the README's exact foreground commands under a private HOME.
// The test owns process lifetime/readiness only; the lead owns collaboration.
func runDiscussionQuickstart(t *testing.T) {
	t.Helper()
	blocks := discussionCommands(t)
	l := newLiveRun(t, "discussion-team")
	l.api = "http://127.0.0.1:8080"
	// The workflow ran the README's build/install block. The test deliberately
	// does not install global packages on an operator's local machine.
	for _, name := range []string{"agentiplex", "agentiplexd"} {
		if _, err := os.Stat(filepath.Join(l.root, "bin", name)); err != nil {
			t.Fatal("run the README setup first: ", err)
		}
	}
	// Execute the documented cd in a shell and use that actual directory for
	// every subsequent terminal. The test's private HOME isolates daemon
	// storage and runtime state; no copied example or PATH override is needed.
	out, err := commandOutput(l.root, 30*time.Second, l.env, "bash", "-ec", blocks[1][0]+"\npwd -P")
	if err != nil {
		t.Fatalf("README example directory: %v: %.1000q", err, out)
	}
	dir := strings.TrimSpace(string(out))
	wantDir, err := filepath.EvalSymlinks(l.example)
	if err != nil || dir != wantDir {
		t.Fatalf("README directory %q differs from checkout example %q: %v", dir, wantDir, err)
	}
	for _, addr := range []string{"127.0.0.1:8080", "127.0.0.1:8001", "127.0.0.1:8002", "127.0.0.1:8003"} {
		assertFreeTeamPort(t, addr)
	}

	l.daemon = l.spawn("daemon", dir, l.env, "bash", "-c", "exec "+blocks[2][0])
	if err := l.waitReady(l.daemon, l.api+"/readyz", 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	procs := make([]*liveProcess, len(discussionRoles))
	// These are the three independent terminals in the README.
	for i, role := range discussionRoles {
		t.Log("README start:", role)
		procs[i] = l.spawn(role, dir, l.env, "bash", "-c", "exec "+blocks[3][i])
	}
	ports := []string{"8001", "8002", "8003"}
	for i, role := range discussionRoles {
		endpoint := "http://127.0.0.1:"+ports[i]+"/.well-known/agent-card.json"
		if err := l.waitReady(procs[i], endpoint, 5*time.Minute); err != nil {
			t.Fatalf("%s readiness: %v", role, err)
		}
		var card struct{ Name string }
		if err := l.get(endpoint, &card); err != nil || card.Name != role {
			t.Fatalf("expected ready Agent Card %s, got %q: %v", role, card.Name, err)
		}
		t.Log("Ready:", role)
	}
	for _, line := range blocks[4][:3] {
		if out, err := commandOutput(dir, 30*time.Second, l.env, "bash", "-c", "exec "+line); err != nil {
			t.Fatalf("README registration: %v: %.1000q", err, out)
		}
	}
	var agents []core.Agent
	if err := l.get(l.api+"/api/v1/agents", &agents); err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected exactly three registered real agents, got %d", len(agents))
	}
	// Readiness is not collaboration evidence. Execute the actual README
	// send --wait and notes list, including their ordinary readable output.
	t.Log("README send --wait: ", discussionTopic)
	verdict, sendErr := commandOutput(dir, 10*time.Minute, l.env, "bash", "-c", "exec "+blocks[4][3])
	printed, notesErr := commandOutput(dir, 30*time.Second, l.env, "bash", "-c", "exec "+blocks[4][4])
	writeTeamFile(t, filepath.Join(l.run,"verdict.txt"), string(verdict))
	writeTeamFile(t, filepath.Join(l.run,"notes.txt"), string(printed))
	t.Logf("README verdict output: %.4000q", verdict)
	t.Logf("README notes output: %.6000q", printed)

	var summaries []core.DeliverySummary
	if err := l.get(l.api+"/api/v1/deliveries?limit=100", &summaries); err != nil {
		t.Fatal(err)
	}
	var deliveries []core.Delivery
	for _, s := range summaries {
		var d core.Delivery
		if err := l.get(l.api+"/api/v1/deliveries/"+s.ID, &d); err != nil {
			t.Fatal(err)
		}
		deliveries = append(deliveries, d)
		t.Logf("Delivery %s target=%s state=%s has_result=%t detail=%.200q", d.ID,d.Target,d.State,(teamDelivery{Result:d.Result}).hasResult(),d.Detail)
	}
	// A second, read-only JSON view is independent evidence, not a replacement
	// for either of the documented human-facing commands.
	raw, err := commandOutput(dir, 30*time.Second, l.env, "bash", "-c", "exec "+blocks[4][4]+" --json")
	if err != nil {
		t.Fatalf("notes JSON: %v", err)
	}
	var notes []core.Note
	if err := json.Unmarshal(raw, &notes); err != nil {
		t.Fatal("invalid notes JSON: ",err)
	}
	for _, n := range notes {
		t.Logf("Note topic=%s key=%s bytes=%d",n.Topic,n.Key,len(n.Text))
	}
	evidence, err := json.MarshalIndent(map[string]any{"deliveries":deliveries,"notes":notes},"","  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTeamFile(t,filepath.Join(l.run,"evidence.json"),string(evidence))
	if sendErr != nil || notesErr != nil {
		t.Fatalf("README commands failed: send --wait: %v; notes list: %v",sendErr,notesErr)
	}
	if err := verifyDiscussion(deliveries,notes,string(verdict),string(printed)); err != nil {
		t.Fatal(err)
	}
	t.Log("PASS: exact README commands, three real completed agents, readable verdict and all three durable note topics.")
 runDiscussionContract(l,dir)
 runDiscussionActors(l,dir)
}

func runDiscussionContract(l *liveTeam,dir string){
 t:=l.t;t.Helper()
 command:=discussionContractCommand(t)
 out,err:=commandOutput(dir,13*time.Minute,l.env,"bash","-ec","exec "+command)
 t.Logf("Until-done result: %.4000q",out)
 if err!=nil{t.Fatalf("native until-done command: %v",err)}
 var summaries []core.DeliverySummary
 if err=l.get(l.api+"/api/v1/deliveries?limit=100",&summaries);err!=nil||len(summaries)>=100{t.Fatal("cannot inspect complete delivery set",err)}
 var root core.Delivery
 for _,s:=range summaries{
  if s.State!="completed"{t.Fatal("unresolved delivery",s.ID)}
  if s.Kind=="until-done"{
   if root.ID!=""{t.Fatal("multiple logical requests")}
   if err=l.get(l.api+"/api/v1/deliveries/"+s.ID,&root);err!=nil{t.Fatal(err)}
  }
 }
 if root.Target!="lead"||root.Text!=string(l.read("contract.md"))||root.Follow==nil||root.Follow.Resolution==nil||root.Follow.Resolution.Outcome!="done"||root.Follow.Options.NudgeSeconds!=0||len(root.Follow.Turns)<2||len(root.Result)==0{t.Fatal("missing native multi-turn resolution")}
 var first core.Delivery
 for i,id:=range root.Follow.Turns{
  var d core.Delivery
  if err=l.get(l.api+"/api/v1/deliveries/"+id,&d);err!=nil||d.ParentID!=root.ID||d.Target!="lead"||d.State!="completed"{t.Fatal("invalid activation",id,err)}
  if i==0{first=d}
 }
 var baseline,final struct{DraftDelivery,ReviewDelivery,Finding,Recommendation string;Accepted bool}
 var baselineState,finalState core.StateEntry
 if err=l.get(l.api+"/api/v1/state/discussion-contract/baseline",&baselineState);err!=nil||baselineState.Value==nil||json.Unmarshal([]byte(*baselineState.Value),&baseline)!=nil{t.Fatal("missing baseline checkpoint",err)}
 if err=l.get(l.api+"/api/v1/state/discussion-contract/final",&finalState);err!=nil||finalState.Value==nil||json.Unmarshal([]byte(*finalState.Value),&final)!=nil{t.Fatal("missing final evidence",err)}
 if baselineState.UpdatedAt.After(first.UpdatedAt)||!strings.Contains(strings.ToLower(baseline.Finding),"recovery")||!final.Accepted||!strings.Contains(strings.ToLower(final.Recommendation),"recovery"){t.Fatal("missing observed checkpoint or accepted recovery analysis")}
 used:=map[string]bool{}
 for i,item:=range []struct{id,target string}{{baseline.DraftDelivery,"expert"},{baseline.ReviewDelivery,"critic"},{final.DraftDelivery,"expert"},{final.ReviewDelivery,"critic"}}{
  var d core.Delivery
  if item.id==""||used[item.id]||l.get(l.api+"/api/v1/deliveries/"+item.id,&d)!=nil||d.Target!=item.target||d.State!="completed"||!(teamDelivery{Result:d.Result}).hasResult(){t.Fatal("invalid distinct worker evidence",item.target,item.id)}
  used[item.id]=true
  if d.UpdatedAt.Before(root.Follow.StartedAt)||(i<2&&d.UpdatedAt.After(first.UpdatedAt))||(i>=2&&!d.UpdatedAt.After(first.UpdatedAt)){t.Fatal("worker evidence outside expected checkpoint",item.id)}
 }
 t.Log("PASS: same three agents, native until-done without nudges, independent incomplete checkpoint and revised acceptance.")
}
