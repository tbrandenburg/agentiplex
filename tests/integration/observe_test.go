package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex/core"
	"github.com/tbrandenburg/agentiplex/internal/api"
	"github.com/tbrandenburg/agentiplex/internal/cli"
	"github.com/tbrandenburg/agentiplex/internal/daemon"
	"github.com/tbrandenburg/agentiplex/internal/security"
)

func observeCLI(ctx context.Context,server string,args ...string)(string,error){
	cmd:=cli.Command()
	var out bytes.Buffer
	cmd.SetOut(&out);cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--server",server,"--token-file","","--ca-file",""},args...))
	err:=cmd.ExecuteContext(ctx)
	return out.String(),err
}

func TestWaitCLIReattachAfterDisconnectAndSIGKILL(t *testing.T){
	start,a:=askDaemon(t)
	api,cmd:=start()
	if c:=call(t,"POST",api+"/api/v1/agents",map[string]string{"id":"worker","url":a.srv.URL},nil);c!=201{t.Fatalf("register: %d",c)}
	var receipt core.Delivery
	if c:=call(t,"POST",api+"/api/v1/messages",map[string]string{"target":"worker","text":"long task","idempotencyKey":"wait-key"},&receipt);c!=202{t.Fatalf("send: %d",c)}
	waitSends(t,a,1)
	ctx,cancel:=context.WithTimeout(context.Background(),300*time.Millisecond)
	_,err:=observeCLI(ctx,api,"messages","wait",receipt.ID)
	cancel()
	if err==nil{t.Fatal("detached wait returned success")}
	blocked:=make(chan error,1)
	go func(){ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel();_,err:=observeCLI(ctx,api,"messages","wait",receipt.ID);blocked<-err}()
	time.Sleep(500*time.Millisecond)
	sigkill(t,cmd)
	select{case err:=<-blocked:if err==nil{t.Fatal("wait succeeded after SIGKILL")};case<-time.After(5*time.Second):t.Fatal("wait did not detach")}
	api2,_:=start()
	close(a.release)
	ctx,cancel=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
	out,err:=observeCLI(ctx,api2,"messages","wait",receipt.ID,"--json")
	var got core.Delivery
	if err!=nil||json.Unmarshal([]byte(out),&got)!=nil||got.ID!=receipt.ID||got.State!="completed"||a.sends.Load()!=1{
		t.Fatalf("reattach %s %v sends=%d",out,err,a.sends.Load())
	}
}

func TestWaitHTTPOutlivesOrdinaryDeadlineAndNotesCLI(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=time.Second
	b:=open(t,cfg);a:=newTaskAgent(t)
	if _,err:=b.Register(context.Background(),"worker",a.srv.URL);err!=nil{t.Fatal(err)}
	hs:=daemon.NewServer(api.HandlerWithProtocols(b,nil,"",true),nil,cfg.Timeout)
	hs.ReadTimeout=time.Second;hs.WriteTimeout=time.Second
	s:=httptest.NewUnstartedServer(nil);s.Config=hs;s.Start();t.Cleanup(s.Close)
	go func(){time.Sleep(3*time.Second);close(a.release)}()
	ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second);defer cancel()
	out,err:=observeCLI(ctx,s.URL,"send","worker","topic","--idempotency-key","long-wait","--wait")
	if err!=nil||!strings.Contains(out,"completed")||a.sends.Load()!=1{t.Fatalf("wait result %s %v",out,err)}
	for _,topic:=range []string{"z","a","z"}{
		if _,err:=b.Note(ctx,"discussion",topic,"finding "+topic);err!=nil{t.Fatal(err)}
	}
	if _,err:=b.PutState(ctx,"discussion","ordinary","not a note",0);err!=nil{t.Fatal(err)}
	out,err=observeCLI(ctx,s.URL,"notes","list","discussion","--json")
	var notes []core.Note
	if err!=nil||json.Unmarshal([]byte(out),&notes)!=nil||len(notes)!=3||notes[0].Topic!="z"||notes[1].Topic!="a"||notes[2].Topic!="z"{t.Fatalf("notes %s %v",out,err)}
	out,err=observeCLI(ctx,s.URL,"notes","list","discussion","a","--limit","1")
	if err!=nil||!strings.Contains(out,"[a]")||strings.Contains(out,"[z]"){t.Fatalf("filtered notes %s %v",out,err)}
	if c:=call(t,"GET",s.URL+"/api/v1/notes/discussion?limit=101",nil,nil);c!=400{t.Fatalf("invalid limit %d",c)}
}

func TestWaitAndNotesRespectPrincipal(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);registerPair(t,b)
	e:=identityServer(t,b,map[string]string{"alpha":alphaTok,"beta":betaTok},opTok,viTok)
	d:=e.send(alphaTok,"wait","test")
	wait(t,b,d.ID,"completed")
	path:="/api/v1/deliveries/"+d.ID+"/wait"
	e.call("GET",path,alphaTok,"",200)
	e.call("GET",path,betaTok,"",404)
	e.call("GET",path,opTok,"",200)
	e.call("GET",path,viTok,"",403)
	e.call("GET","/api/v1/deliveries/missing/wait",opTok,"",404)
	if _,err:=b.Note(security.WithPrincipal(context.Background(),security.Principal{Role:security.RoleAgent,AgentID:"alpha"}),"discussion","expert","finding");err!=nil{t.Fatal(err)}
	var notes []core.Note
	raw:=e.call("GET","/api/v1/notes/discussion",betaTok,"",200)
	if json.Unmarshal([]byte(raw),&notes)!=nil||len(notes)!=1||notes[0].Author!="alpha"||notes[0].Topic!="expert"{t.Fatalf("notes %s",raw)}
	e.call("GET","/api/v1/notes/discussion",viTok,"",403)
	e.call("POST","/api/v1/notes/discussion",betaTok,"{}",403)
}

func TestWaitBroadcastAndDispatch(t *testing.T){
	b:=open(t,config(t));registerPair(t,b)
	ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
	if _,err:=b.SetCollection(ctx,"group","team",[]string{"tester-1","tester-2"});err!=nil{t.Fatal(err)}
	if _,err:=b.SetCollection(ctx,"pool","workers",[]string{"tester-1","tester-2"});err!=nil{t.Fatal(err)}
	d,err:=b.Broadcast(ctx,"team","topic","broadcast-wait")
	if err!=nil{t.Fatal(err)}
	got,err:=b.WaitDelivery(ctx,d.ID)
	if err!=nil||got.State!="completed"||len(got.Children)!=2{t.Fatalf("broadcast wait %+v %v",got,err)}
	d,err=b.Dispatch(ctx,"workers","topic","dispatch-wait")
	if err!=nil{t.Fatal(err)}
	got,err=b.WaitDelivery(ctx,d.ID)
	if err!=nil||got.State!="completed"||got.AssignedAgent==""{t.Fatalf("dispatch wait %+v %v",got,err)}
	if _,err=b.WaitDelivery(ctx,"missing");!errors.Is(err,core.ErrNotFound){t.Fatalf("missing: %v",err)}
}
