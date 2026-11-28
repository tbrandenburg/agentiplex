//go:build linux

package examples_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/tbrandenburg/agentiplex/core"
)

// Read-only observer. The test never decides the next actor, admits a
// handoff, repairs a lost notification or supplies evidence.
func (l *liveTeam) actorContract(ws, base string, seconds int) {
	l.t.Helper()
	lines := actorCommands(l.t)
	env := append([]string(nil), l.env...)
	for i, v := range env {
		if strings.HasPrefix(v, "PATH=") {
			env[i] = "PATH="+l.run+string(os.PathListSeparator)+strings.TrimPrefix(v,"PATH=")
		}
	}
	env = append(env, "AGENTIPLEX_SERVER="+l.api)
	run := func(command string, deadline time.Duration) {
		l.t.Helper()
		out, err := commandOutput(l.example,deadline,env,"bash","-ec",command)
		l.t.Log(string(out))
		if err != nil { l.t.Fatalf("native actor command: %v",err) }
	}
	run(lines[0],30*time.Second)
	run(lines[1],30*time.Second)
	initial := teamCLI[teamState](l,"state","get","repository","objective")
	if initial.Value != string(l.read("objective.actors.md")) || initial.Revision == 0 { l.t.Fatal("wrong approved objective") }
	checklist := teamCLI[teamState](l,"state","get","repository","actors-contract")
	var current actorContractState
	if checklist.Value != string(l.read("contract.actors.json")) || json.Unmarshal([]byte(checklist.Value),&current)!=nil { l.t.Fatal("wrong approved actor checklist") }
	if err:=validateActorInitial(current);err!=nil { l.t.Fatal(err) }
	revision:=checklist.Revision
	ctx,cancel:=context.WithTimeout(context.Background(),time.Duration(seconds)*time.Second)
	defer cancel()
	ready:=make(chan struct{},1)
	updates:=make(chan core.StateEntry,128)
	watchErr:=make(chan error,1)
	go func(){
		client:=&http.Client{Transport:&http.Transport{Proxy:nil}}
		defer client.CloseIdleConnections()
		for ctx.Err()==nil {
			req,err:=http.NewRequestWithContext(ctx,"GET",l.api+"/api/v1/state-watch/repository",nil)
			if err!=nil { watchErr<-err;return }
			resp,err:=client.Do(req)
			if err!=nil { if ctx.Err()==nil {watchErr<-err};return }
			if resp.StatusCode!=200 { resp.Body.Close();watchErr<-fmt.Errorf("actor state watch HTTP %d",resp.StatusCode);return }
			sc:=bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte,4096),1<<20)
			for sc.Scan(){
				if !strings.HasPrefix(sc.Text(),"data: "){continue}
				var ev core.StateChange
				if err=json.Unmarshal([]byte(strings.TrimPrefix(sc.Text(),"data: ")),&ev);err!=nil {resp.Body.Close();watchErr<-err;return}
				if ev.Operation=="ready" { select{case ready<-struct{}{}:default:};continue }
				if ev.Entry==nil{continue}
				if ev.Entry.Key=="objective" && (ev.Entry.Value==nil || *ev.Entry.Value!=initial.Value || ev.Entry.Revision!=initial.Revision) {
					resp.Body.Close();watchErr<-fmt.Errorf("approved objective changed");return
				}
				if ev.Entry.Key!="actors-contract"{continue}
				select { case updates<-*ev.Entry:case <-ctx.Done():resp.Body.Close();return }
			}
			resp.Body.Close()
			if sc.Err()!=nil && ctx.Err()==nil {watchErr<-sc.Err();return}
			// A bounded SSE stream may reconnect to its snapshot. Any skipped
			// transition is rejected by verifyActorTransition, not inferred.
		}
	}()
	select{case <-ready:case e:=<-watchErr:l.t.Fatal(e);case <-time.After(15*time.Second):l.t.Fatal("actor state watch not ready")}
	run(lines[2],time.Duration(seconds)*time.Second)
	for current.Status!="done" {
		select{
		case e:=<-watchErr:l.t.Fatal(e)
		case <-ctx.Done():l.t.Fatal("contract stalled or objective deadline exceeded; no overall native waiter")
		case entry:=<-updates:
			if entry.Value==nil {l.t.Fatal("actor contract deleted")}
			if entry.Revision==revision {continue}
			var next actorContractState
			if entry.Revision<revision || json.Unmarshal([]byte(*entry.Value),&next)!=nil {l.t.Fatal("invalid actor state revision")}
			if err:=verifyActorTransition(current,next);err!=nil {l.t.Fatal(err)}
			l.t.Logf("Observed contract revision %d: %s -> %s (%s)",entry.Revision,current.Owner,next.Owner,next.Status)
			current,revision=next,entry.Revision
		}
	}
	// Closing application state isn't transport completion. Wait only as a
	// read-only observer until all expected ordinary deliveries settle.
	var deliveries []core.Delivery
	for {
		if ctx.Err()!=nil {l.t.Fatal("outstanding work at contract closure")}
		select{case e:=<-watchErr:l.t.Fatal(e);default:}
		var summaries []core.DeliverySummary
		if err:=l.get(l.api+"/api/v1/deliveries?limit=100",&summaries);err!=nil {l.t.Fatal(err)}
		if len(summaries)>len(current.Handoffs)+1 {l.t.Fatal("unexpected extra activation")}
		deliveries=nil
		all:=len(summaries)==len(current.Handoffs)+1
		for _,s:=range summaries{
			var d core.Delivery
			if err:=l.get(l.api+"/api/v1/deliveries/"+s.ID,&d);err!=nil {l.t.Fatal(err)}
			if d.State!="completed" {
				if terminalTeamState(d.State) {l.t.Fatal("unsuccessful actor delivery",d.ID,d.State)}
				all=false
			}
			deliveries=append(deliveries,d)
		}
		if all {break}
		time.Sleep(250*time.Millisecond)
	}
	if err:=verifyActorReceipts(current,deliveries,initial.Value);err!=nil {l.t.Fatal(err)}
	final:=teamCLI[teamState](l,"state","get","repository","objective")
	if final!=initial {l.t.Fatal("approved objective changed")}
	checklist=teamCLI[teamState](l,"state","get","repository","actors-contract")
	var finalContract actorContractState
	if checklist.Revision!=revision || json.Unmarshal([]byte(checklist.Value),&finalContract)!=nil || !reflect.DeepEqual(finalContract,current) {l.t.Fatal("closed contract rewritten")}
	source,err:=os.ReadFile(filepath.Join(ws,"calc.go"))
	if err!=nil {l.t.Fatal(err)}
	if fmt.Sprintf("%x",sha256.Sum256(source))!=current.Criteria[1].Candidate {l.t.Fatal("reviewed candidate is not the actual source")}
	if err=verifyTeamWorkspace(ws,base,"defect");err!=nil {l.t.Fatal(err)}
	// Reattach the initial ordinary send; closed application state must not
	// trigger another handoff or a new turn.
	run(lines[2],30*time.Second)
	var after []core.DeliverySummary
	if err=l.get(l.api+"/api/v1/deliveries?limit=100",&after);err!=nil || len(after)!=len(deliveries) {l.t.Fatal("idempotent initial submission created more work")}
	if got:=teamCLI[teamState](l,"state","get","repository","actors-contract");got!=checklist {l.t.Fatal("closed contract changed after reattachment")}
	l.t.Log("PASS: observed role-owned state changes, direct handoff receipts, actual candidate, independent tests and closed application contract without until-done.")
}
