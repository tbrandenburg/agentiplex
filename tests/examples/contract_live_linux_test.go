//go:build linux

package examples_test

import (
 "bufio"
 "context"
 "encoding/json"
 "fmt"
 "net/http"
 "os"
 "strings"
 "time"

 "github.com/tbrandenburg/agentiplex/core"
)

type liveContract struct {
 Blocker string
 Criteria []struct{ ID, Description, Owner, Status, Delivery, Evidence string }
}

func sameContractShape(a,b liveContract)bool {
 if len(a.Criteria)!=len(b.Criteria){return false}
 for i,x:=range a.Criteria{y:=b.Criteria[i];if x.ID!=y.ID||x.Description!=y.Description||x.Owner!=y.Owner{return false}}
 return true
}

func (l *liveTeam) contract(ws,base string,seconds int) {
 l.t.Helper()
 // Execute the actual README approval and submission commands, not a second
 // orchestration implementation. Only transport/environment are test-owned.
 doc:=string(l.read("README.md"))
 _,block,ok:=strings.Cut(doc,"<!-- contract-quickstart:start -->")
 block,_,end:=strings.Cut(block,"<!-- contract-quickstart:end -->")
 if !ok||!end{l.t.Fatal("missing contract quickstart")}
 block=strings.TrimSpace(block)
 block=strings.TrimPrefix(block,"```sh\n");block=strings.TrimSuffix(block,"\n```")
 lines:=strings.Split(block,"\n")
 if len(lines)!=3||!strings.Contains(lines[2],"--until-done")||!strings.Contains(lines[2],"--wait")||strings.Contains(lines[2],"--nudge-after"){l.t.Fatal("unexpected contract quickstart commands")}
 env:=append([]string(nil),l.env...)
 for i,v:=range env{if strings.HasPrefix(v,"PATH="){env[i]="PATH="+l.run+string(os.PathListSeparator)+strings.TrimPrefix(v,"PATH=")}}
 env=append(env,"AGENTIPLEX_SERVER="+l.api)
 run:=func(command string,deadline time.Duration){
  l.t.Helper()
  out,err:=commandOutput(l.example,deadline,env,"bash","-ec",command)
  l.t.Log(string(out))
  if err!=nil{l.t.Fatalf("native contract command failed: %v",err)}
 }
 run(lines[0],30*time.Second);run(lines[1],30*time.Second)
 initial:=teamCLI[teamState](l,"state","get","repository","objective")
 if initial.Value!=string(l.read("objective.contract.md"))||initial.Revision==0{l.t.Fatal("wrong approved objective")}
 checklist:=teamCLI[teamState](l,"state","get","repository","contract")
 var approved liveContract
 if json.Unmarshal([]byte(checklist.Value),&approved)!=nil||len(approved.Criteria)!=4{l.t.Fatal("invalid approved checklist")}
 // Observe the actual intermediate checkpoint and detect contract shape or
 // objective changes. This is observation only; it never schedules work.
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 ready:=make(chan struct{},1)
 checkpoints:=make(chan time.Time,1)
 watchErr:=make(chan error,1)
 go func(){
  client:=&http.Client{Transport:&http.Transport{Proxy:nil}}
  defer client.CloseIdleConnections()
  for ctx.Err()==nil{
   req,err:=http.NewRequestWithContext(ctx,"GET",l.api+"/api/v1/state-watch/repository",nil)
   if err!=nil{watchErr<-err;return}
   resp,err:=client.Do(req)
   if err!=nil{if ctx.Err()==nil{watchErr<-err};return}
   if resp.StatusCode!=200{resp.Body.Close();watchErr<-fmt.Errorf("state watch HTTP %d",resp.StatusCode);return}
   sc:=bufio.NewScanner(resp.Body);sc.Buffer(make([]byte,4096),1<<20)
   for sc.Scan(){
    if !strings.HasPrefix(sc.Text(),"data: "){continue}
    var ev core.StateChange
    if err=json.Unmarshal([]byte(strings.TrimPrefix(sc.Text(),"data: ")),&ev);err!=nil{resp.Body.Close();watchErr<-err;return}
    if ev.Operation=="ready"{select{case ready<-struct{}{}:default:};continue}
    if ev.Entry==nil{continue}
    if ev.Entry.Key=="objective"&&(ev.Entry.Value==nil||*ev.Entry.Value!=initial.Value||ev.Entry.Revision!=initial.Revision){resp.Body.Close();watchErr<-fmt.Errorf("approved objective changed");return}
    if ev.Entry.Key!="contract"{continue}
    var progress liveContract
    if ev.Entry.Value==nil||json.Unmarshal([]byte(*ev.Entry.Value),&progress)!=nil||!sameContractShape(approved,progress){resp.Body.Close();watchErr<-fmt.Errorf("approved checklist changed");return}
    p:=progress.Criteria
    if p[0].Status=="verified"&&p[0].Delivery!=""&&p[0].Evidence!=""&&p[1].Status=="pending"&&p[2].Status=="rework"&&p[2].Evidence!=""&&p[3].Status=="pending"{
     select{case checkpoints<-ev.Entry.UpdatedAt:default:}
    }
   }
   resp.Body.Close()
   if sc.Err()!=nil&&ctx.Err()==nil{watchErr<-sc.Err();return}
   // The existing watch endpoint has a bounded stream lifetime; reconnect
   // to the current snapshot. Never infer a missed checkpoint.
  }
 }()
 select{case <-ready:case e:=<-watchErr:l.t.Fatal(e);case <-time.After(15*time.Second):l.t.Fatal("state watch not ready")}
 run(lines[2],time.Duration(seconds)*time.Second+30*time.Second)
 select{case e:=<-watchErr:l.t.Fatal(e);default:}
 var checkpoint time.Time
 select{case checkpoint=<-checkpoints:case e:=<-watchErr:l.t.Fatal(e);case <-time.After(5*time.Second):l.t.Fatal("missing observed failed-check checkpoint")}
 final:=teamCLI[teamState](l,"state","get","repository","objective")
 if initial!=final{l.t.Fatal("approved objective changed")}
 checklist=teamCLI[teamState](l,"state","get","repository","contract")
 var contract liveContract
 if json.Unmarshal([]byte(checklist.Value),&contract)!=nil||!sameContractShape(approved,contract)||contract.Blocker!=""{l.t.Fatal("invalid final contract")}
 ids:=map[string]bool{}
 evidence:=map[string]core.Delivery{}
 for _,item:=range contract.Criteria{
  if item.Status!="verified"||item.Evidence==""||ids[item.Delivery]{l.t.Fatal("missing distinct criterion evidence",item.ID)}
  ids[item.Delivery]=true
  d:=teamCLI[core.Delivery](l,"messages","status",item.Delivery)
  if d.Target!=item.Owner||d.State!="completed"||len(d.Result)==0||string(d.Result)=="null"{l.t.Fatal("invalid independent delivery",item.ID)}
  evidence[item.ID]=d
 }
 var records []core.DeliverySummary
 if err:=l.get(l.api+"/api/v1/deliveries?limit=100",&records);err!=nil||len(records)>=100{l.t.Fatal("cannot inspect complete delivery set",err)}
 var root core.Delivery
 for _,summary:=range records{
  d:=teamCLI[core.Delivery](l,"messages","status",summary.ID)
  if d.State!="completed"{l.t.Fatal("unfinished or unsuccessful delivery",d.ID)}
  if d.Kind=="until-done"{if root.ID!=""{l.t.Fatal("multiple logical requests")};root=d;continue}
  if d.Target=="pm"{l.t.Fatal("unexpected PM activation",d.ID)}
 }
 if root.Target!="pm"||root.Text!=initial.Value||root.Follow==nil||len(root.Follow.Turns)<2||root.Follow.Options.NudgeSeconds!=0||root.Follow.Resolution==nil||root.Follow.Resolution.Outcome!="done"||len(root.Result)==0{l.t.Fatal("native continuation did not explicitly resolve across turns")}
 turns:=map[string]bool{}
 var first core.Delivery
 for i,id:=range root.Follow.Turns{
  d:=teamCLI[core.Delivery](l,"messages","status",id)
  if turns[id]||d.ParentID!=root.ID||d.Target!="pm"||d.State!="completed"||len(d.Result)==0||!strings.Contains(d.Text,fmt.Sprintf("turn %d (",i+1)){l.t.Fatal("invalid or duplicate PM activation",id)}
  turns[id]=true
  if i==0{first=d}
 }
 if checkpoint.After(first.UpdatedAt)||evidence["baseline"].UpdatedAt.After(first.UpdatedAt)||!evidence["implementation"].UpdatedAt.After(first.UpdatedAt){l.t.Fatal("repair occurred before initial verification checkpoint")}
 if err:=verifyTeamWorkspace(ws,base,"defect");err!=nil{l.t.Fatal(err)}
 // Reattach by repeating exactly the same submission, including the limits.
 run(lines[2],30*time.Second)
 var after []core.DeliverySummary
 if err:=l.get(l.api+"/api/v1/deliveries?limit=100",&after);err!=nil||len(after)!=len(records){l.t.Fatal("fulfilled contract scheduled additional work")}
 got:=teamCLI[core.Delivery](l,"messages","status",root.ID)
 if got.Follow==nil||len(got.Follow.Turns)!=len(turns){l.t.Fatal("fulfilled contract gained activations")}
 l.t.Log("PASS: actual README commands, observed failed-check checkpoint, native continuation without nudging, independent owner evidence, repaired workspace and closed reattachment.")
}
