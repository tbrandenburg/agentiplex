//go:build linux

package examples_test

import (
	"bufio"
	"context"
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

// Same running gateways as the ordinary and until-done quickstarts. The
// observer never delegates, writes progress or supplies an actor's evidence.
func runDiscussionActors(l *liveTeam,dir string) {
	t:=l.t;t.Helper()
	lines:=discussionActorCommands(t)
	run:=func(command string,deadline time.Duration){
		t.Helper()
		out,err:=commandOutput(dir,deadline,l.env,"bash","-ec",command)
		t.Logf("Actor-owned README command: %.4000q",out)
		if err!=nil {t.Fatalf("native actor command: %v",err)}
	}
	var before []core.DeliverySummary
	if err:=l.get(l.api+"/api/v1/deliveries?limit=100",&before);err!=nil || len(before)>=80 {t.Fatal("cannot inspect prior deliveries",err)}
	prior:=map[string]bool{}
	for _,s:=range before{
		if s.State!="completed"{t.Fatal("prior work not settled",s.ID)}
		prior[s.ID]=true
	}
	run(lines[0],30*time.Second);run(lines[1],30*time.Second)
	read:=func(name string)string{
		b,err:=os.ReadFile(filepath.Join(dir,name));if err!=nil{t.Fatal(err)};return string(b)
	}
	var objective,initial core.StateEntry
	if err:=l.get(l.api+"/api/v1/state/discussion-actors/objective",&objective);err!=nil || objective.Value==nil || *objective.Value!=read("objective-actors.md") || objective.Revision==0{t.Fatal("wrong approved objective",err)}
	if err:=l.get(l.api+"/api/v1/state/discussion-actors/contract",&initial);err!=nil || initial.Value==nil || *initial.Value!=read("contract-actors.md"){t.Fatal("wrong approved contract",err)}
	current,err:=parseDiscussionActorMarkdown(*initial.Value)
	if err!=nil || !initialDiscussionActors(current){t.Fatal("invalid initial Markdown contract",err)}
	revision:=initial.Revision
	ctx,cancel:=context.WithTimeout(context.Background(),12*time.Minute);defer cancel()
	ready:=make(chan struct{},1)
	updates:=make(chan core.StateEntry,32)
	watchErr:=make(chan error,1)
	go func(){
		client:=&http.Client{Transport:&http.Transport{Proxy:nil}}
		defer client.CloseIdleConnections()
		for ctx.Err()==nil{
			req,err:=http.NewRequestWithContext(ctx,"GET",l.api+"/api/v1/state-watch/discussion-actors",nil)
			if err!=nil{watchErr<-err;return}
			resp,err:=client.Do(req)
			if err!=nil{if ctx.Err()==nil{watchErr<-err};return}
			if resp.StatusCode!=200{resp.Body.Close();watchErr<-fmt.Errorf("actor state watch HTTP %d",resp.StatusCode);return}
			sc:=bufio.NewScanner(resp.Body);sc.Buffer(make([]byte,4096),1<<20)
			for sc.Scan(){
				if !strings.HasPrefix(sc.Text(),"data: "){continue}
				var ev core.StateChange
				if err=json.Unmarshal([]byte(strings.TrimPrefix(sc.Text(),"data: ")),&ev);err!=nil{resp.Body.Close();watchErr<-err;return}
				if ev.Operation=="ready"{select{case ready<-struct{}{}:default:};continue}
				if ev.Entry==nil{continue}
				if ev.Entry.Key=="objective" && (ev.Entry.Value==nil || *ev.Entry.Value!=*objective.Value || ev.Entry.Revision!=objective.Revision){
					resp.Body.Close();watchErr<-fmt.Errorf("approved objective changed");return
				}
				if ev.Entry.Key!="contract"{continue}
				select{case updates<-*ev.Entry:case <-ctx.Done():resp.Body.Close();return}
			}
			resp.Body.Close()
			if sc.Err()!=nil && ctx.Err()==nil{watchErr<-sc.Err();return}
			// A reconnect yields a snapshot; never infer missed transitions.
		}
	}()
	select{case <-ready:case e:=<-watchErr:t.Fatal(e);case <-time.After(15*time.Second):t.Fatal("state watch not ready")}
	run(lines[2],4*time.Minute)
	for current.Status!="done"{
		select{
		case e:=<-watchErr:t.Fatal(e)
		case <-ctx.Done():t.Fatal("actor-owned contract stalled; ordinary wait observes only first turn")
		case entry:=<-updates:
			if entry.Value==nil{t.Fatal("contract deleted")}
			if entry.Revision==revision{continue}
			next,err:=parseDiscussionActorMarkdown(*entry.Value)
			if entry.Revision<revision || err!=nil{t.Fatal("invalid actor Markdown revision",err)}
			if err:=verifyDiscussionActorTransition(current,next);err!=nil{t.Fatal(err)}
			t.Logf("Actor contract revision %d: %s -> %s (%s, draft %d, review %s, checked %v)",entry.Revision,current.Owner,next.Owner,next.Status,next.Draft.Version,next.Review.Decision,next.Checked)
			current,revision=next,entry.Revision
		}
	}
	var deliveries []core.Delivery
	for{
		if ctx.Err()!=nil{t.Fatal("outstanding work at contract closure")}
		select{case e:=<-watchErr:t.Fatal(e);default:}
		var summaries []core.DeliverySummary
		if err:=l.get(l.api+"/api/v1/deliveries?limit=100",&summaries);err!=nil || len(summaries)>=100{t.Fatal("cannot inspect complete delivery set",err)}
		if len(summaries)>len(prior)+len(current.Handoffs)+1{t.Fatal("unexpected extra activation")}
		all:=len(summaries)==len(prior)+len(current.Handoffs)+1
		deliveries=nil
		for _,s:=range summaries{
			if s.State!="completed"{
				if terminalTeamState(s.State){t.Fatal("unsuccessful actor delivery",s.ID,s.State)}
				all=false
			}
			if prior[s.ID]{continue}
			var d core.Delivery
			if err:=l.get(l.api+"/api/v1/deliveries/"+s.ID,&d);err!=nil{t.Fatal(err)}
			if d.State!="completed"{all=false}
			deliveries=append(deliveries,d)
		}
		if all{break}
		time.Sleep(250*time.Millisecond)
	}
	if err:=verifyDiscussionActorReceipts(current,deliveries,*objective.Value);err!=nil{t.Fatal(err)}
	var final core.StateEntry
	if err:=l.get(l.api+"/api/v1/state/discussion-actors/contract",&final);err!=nil || final.Value==nil || final.Revision!=revision{t.Fatal("closed contract rewritten",err)}
	closed,err:=parseDiscussionActorMarkdown(*final.Value)
	if err!=nil || !reflect.DeepEqual(closed,current){t.Fatal("closed Markdown contract rewritten",err)}
	t.Logf("Final living Markdown contract:\n%s",*final.Value)
	var unchanged core.StateEntry
	if err:=l.get(l.api+"/api/v1/state/discussion-actors/objective",&unchanged);err!=nil || unchanged.Value==nil || *unchanged.Value!=*objective.Value || unchanged.Revision!=objective.Revision{t.Fatal("approved objective changed",err)}
	run(lines[2],30*time.Second)
	var after []core.DeliverySummary
	if err:=l.get(l.api+"/api/v1/deliveries?limit=100",&after);err!=nil || len(after)!=len(prior)+len(deliveries){t.Fatal("reattachment created additional work",err)}
	if err:=l.get(l.api+"/api/v1/state/discussion-actors/contract",&unchanged);err!=nil || unchanged.Revision!=revision || unchanged.Value==nil || *unchanged.Value!=*final.Value{t.Fatal("closed contract changed after reattachment",err)}
	t.Log("PASS: same three read-only actors, observed evidence-backed Markdown checkboxes, direct ownership handovers, missing recovery rejection, revised draft and independent acceptance; no orchestration or middleware continuation.")
}
