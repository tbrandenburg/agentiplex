package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/tbrandenburg/agentiplex"
	"github.com/tbrandenburg/agentiplex/internal/api"
	"github.com/tbrandenburg/agentiplex/internal/cli"
)

func registerPair(t *testing.T,b *agentiplex.Bus)(*agentService,*agentService){
	t.Helper()
	first,second:=&agentService{},&agentService{}
	first.start(t,false);second.start(t,false)
	t.Cleanup(first.stop);t.Cleanup(second.stop)
	for id,s:=range map[string]*agentService{"tester-1":first,"tester-2":second}{
		if _,err:=b.Register(context.Background(),id,s.URL());err!=nil{t.Fatal(err)}
	}
	return first,second
}

func TestBroadcastSnapshotOfflineAndRestart(t *testing.T){
	cfg:=config(t)
	b:=open(t,cfg)
	first,second:=registerPair(t,b)
	ctx:=context.Background()
	if _,err:=b.SetCollection(ctx,"group","engineering",[]string{"tester-2","tester-1"});err!=nil{t.Fatal(err)}
	if _,err:=b.SetCollection(ctx,"group","bad",[]string{"tester-1","tester-1"});!errors.Is(err,agentiplex.ErrInvalid){t.Fatalf("duplicate member: %v",err)}
	if _,err:=b.SetCollection(ctx,"pool","bad",[]string{"missing"});!errors.Is(err,agentiplex.ErrNotFound){t.Fatalf("unknown member: %v",err)}
	second.stop()
	d,err:=b.Broadcast(ctx,"engineering","contract updated","broadcast-1")
	if err!=nil||len(d.Children)!=2{t.Fatalf("broadcast: %+v %v",d,err)}
	wait(t,b,d.Children[0].ID,"completed")
	if first.calls.Load()!=1||second.calls.Load()!=0{t.Fatal("wrong initial fan-out")}
	if _,err=b.SetCollection(ctx,"group","engineering",[]string{"tester-1"});err!=nil{t.Fatal(err)}
	found,err:=b.Discover("architecture","","engineering")
	if err!=nil||len(found)!=1||found[0].ID!="tester-1"{t.Fatalf("group discovery: %+v %v",found,err)}
	b.Close()
	b2:=open(t,cfg)
	second.start(t,false)
	done:=wait(t,b2,d.ID,"completed")
	if len(done.Children)!=2||first.calls.Load()!=1||second.calls.Load()!=1{t.Fatalf("snapshot or restart lost: %+v calls %d %d",done,first.calls.Load(),second.calls.Load())}
	duplicate,err:=b2.Broadcast(ctx,"engineering","contract updated","broadcast-1")
	if err!=nil||duplicate.ID!=d.ID||len(duplicate.Children)!=2{t.Fatalf("duplicate broadcast: %+v %v",duplicate,err)}
	if _,err=b2.Broadcast(ctx,"engineering","changed","broadcast-1");!errors.Is(err,agentiplex.ErrConflict){t.Fatalf("broadcast key conflict: %v",err)}
	next,err:=b2.Broadcast(ctx,"engineering","next contract","broadcast-2")
	if err!=nil||len(next.Children)!=1{t.Fatalf("new membership: %+v %v",next,err)}
	wait(t,b2,next.ID,"completed")
	if first.calls.Load()!=2||second.calls.Load()!=1{t.Fatal("new group membership not honored")}
}

func TestPoolDispatchDistributionOfflineAndRestart(t *testing.T){
	cfg:=config(t);b:=open(t,cfg)
	first,second:=registerPair(t,b)
	ctx:=context.Background()
	if _,err:=b.SetCollection(ctx,"pool","testers",[]string{"tester-1","tester-2"});err!=nil{t.Fatal(err)}
	for i:=0;i<4;i++{
		key:=fmt.Sprintf("pool-%d",i)
		d,err:=b.Dispatch(ctx,"testers","verify",key);if err!=nil{t.Fatal(err)}
		done:=wait(t,b,d.ID,"completed")
		want:=fmt.Sprintf("tester-%d",i%2+1)
		if done.AssignedAgent!=want||len(done.Children)!=0{t.Fatalf("expected one recipient %s, got %+v",want,done)}
		again,err:=b.Dispatch(ctx,"testers","verify",key)
		if err!=nil||again.ID!=d.ID||again.AssignedAgent!=want{t.Fatalf("duplicate dispatch: %+v %v",again,err)}
	}
	if first.calls.Load()!=2||second.calls.Load()!=2{t.Fatalf("distribution: %d %d",first.calls.Load(),second.calls.Load())}
	first.stop()
	d,err:=b.Dispatch(ctx,"testers","offline fallback","fallback");if err!=nil{t.Fatal(err)}
	if done:=wait(t,b,d.ID,"completed");done.AssignedAgent!="tester-2"{t.Fatalf("offline member not skipped: %+v",done)}
	second.stop()
	pending,err:=b.Dispatch(ctx,"testers","both offline","pool-restart");if err!=nil{t.Fatal(err)}
	b.Close()
	b2:=open(t,cfg)
	if c,err:=b2.Collection("pool","testers");err!=nil||len(c.Members)!=2{t.Fatalf("pool persistence: %+v %v",c,err)}
	first.start(t,false)
	done:=wait(t,b2,pending.ID,"completed")
	if done.AssignedAgent!="tester-1"||first.calls.Load()!=3||second.calls.Load()!=3{t.Fatalf("pool recovery: %+v calls %d %d",done,first.calls.Load(),second.calls.Load())}
}

func TestPoolAmbiguousNeverReassigns(t *testing.T){
	b:=open(t,config(t))
	first,second:=registerPair(t,b)
	first.stop();first.start(t,true)
	ctx:=context.Background()
	if _,err:=b.SetCollection(ctx,"pool","testers",[]string{"tester-1","tester-2"});err!=nil{t.Fatal(err)}
	d,err:=b.Dispatch(ctx,"testers","side effect","pool-ambiguous");if err!=nil{t.Fatal(err)}
	done:=wait(t,b,d.ID,"uncertain")
	if done.AssignedAgent!="tester-1"{t.Fatalf("assignment: %+v",done)}
	if _,err=b.Dispatch(ctx,"testers","side effect","pool-ambiguous");err!=nil{t.Fatal(err)}
	time.Sleep(1500*time.Millisecond)
	if first.calls.Load()!=1||second.calls.Load()!=0{t.Fatal("ambiguous dispatch replayed or reassigned")}
}

func TestBroadcastPartialFailure(t *testing.T){
	b:=open(t,config(t))
	first,second:=registerPair(t,b)
	second.stop();second.start(t,true)
	ctx:=context.Background()
	if _,err:=b.SetCollection(ctx,"group","engineering",[]string{"tester-1","tester-2"});err!=nil{t.Fatal(err)}
	d,err:=b.Broadcast(ctx,"engineering","review","partial");if err!=nil{t.Fatal(err)}
	deadline:=time.Now().Add(20*time.Second)
	for time.Now().Before(deadline){
		got,err:=b.Delivery(d.ID);if err!=nil{t.Fatal(err)}
		if got.State=="partial"{
			if got.Children[0].State!="completed"||got.Children[1].State!="uncertain"||first.calls.Load()!=1||second.calls.Load()!=1{t.Fatalf("wrong child outcomes %+v",got)}
			return
		}
		time.Sleep(100*time.Millisecond)
	}
	t.Fatal("partial broadcast not reported")
}

func TestCollaborationA2AAndCLI(t *testing.T){
	b:=open(t,config(t));first,second:=registerPair(t,b)
	s,c:=ingressClient(t,b)
	runCLI:=func(args ...string)agentiplex.Delivery{
		t.Helper()
		cmd:=cli.Command();var out bytes.Buffer;cmd.SetOut(&out);cmd.SetErr(&out)
		cmd.SetArgs(append([]string{"--server",s.URL},args...))
		if err:=cmd.Execute();err!=nil{t.Fatalf("CLI %v: %v %s",args,err,out.String())}
		var d agentiplex.Delivery
		if err:=json.Unmarshal(out.Bytes(),&d);err!=nil{t.Fatalf("CLI JSON: %s %v",out.String(),err)}
		return d
	}
	runCLI("groups","set","engineering","tester-1","tester-2")
	runCLI("pools","set","testers","tester-1","tester-2")
	d:=runCLI("broadcast","engineering","CLI broadcast","--idempotency-key","cli-bc")
	wait(t,b,d.ID,"completed")
	d=runCLI("dispatch","testers","CLI dispatch","--idempotency-key","cli-dp")
	wait(t,b,d.ID,"completed")
	request:=func(id,op,target string)*a2a.SendMessageRequest{
		m:=a2a.NewMessage(a2a.MessageRoleUser,a2a.NewDataPart(map[string]any{"operation":op,"target":target,"text":"native collaboration"}));m.ID=id
		return &a2a.SendMessageRequest{Message:m,Config:&a2a.SendMessageConfig{ReturnImmediately:true}}
	}
	bc:=nativeSend(t,c,request("native-bc","broadcast","engineering"))
	bc=waitNative(t,c,bc.ID,a2a.TaskStateCompleted)
	if len(bc.Artifacts)!=1||bc.Artifacts[0].Name!="broadcast-recipients"{t.Fatalf("broadcast artifact: %+v",bc)}
	dp:=nativeSend(t,c,request("native-dp","dispatch","testers"))
	dp=waitNative(t,c,dp.ID,a2a.TaskStateCompleted)
	meta,ok:=dp.Metadata["https://github.com/tbrandenburg/agentiplex"].(map[string]any)
	if !ok||meta["assignedAgent"]!="tester-2"{t.Fatalf("dispatch assignment: %+v",dp)}
	if first.calls.Load()!=3||second.calls.Load()!=3{t.Fatalf("wrong recipient counts: %d %d",first.calls.Load(),second.calls.Load())}
	if _,err:=c.SendMessage(context.Background(),request("native-bc","dispatch","testers"));!errors.Is(err,a2a.ErrInvalidParams){t.Fatalf("cross-operation identity collision: %v",err)}
}

func TestEventSubscriptionReplayAndLive(t *testing.T){
	b:=open(t,config(t))
	registerPair(t,b)
	s:=httptest.NewServer(api.Handler(b));defer s.Close()
	ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second);defer cancel()
	connect:=func(after uint64)(*http.Response,*bufio.Scanner){
		t.Helper()
		req,err:=http.NewRequestWithContext(ctx,"GET",s.URL+"/api/v1/events/stream?kind=group.configured",nil);if err!=nil{t.Fatal(err)}
		if after>0{req.Header.Set("Last-Event-ID",fmt.Sprint(after))}
		res,err:=http.DefaultClient.Do(req);if err!=nil{t.Fatal(err)}
		if res.StatusCode!=200||res.Header.Get("Content-Type")!="text/event-stream"{t.Fatalf("SSE response %d",res.StatusCode)}
		return res,bufio.NewScanner(res.Body)
	}
	readEvent:=func(scanner *bufio.Scanner)agentiplex.Event{
		t.Helper()
		for scanner.Scan(){
			line:=scanner.Text()
			if !strings.HasPrefix(line,"data: "){continue}
			var event agentiplex.Event
			if err:=json.Unmarshal([]byte(strings.TrimPrefix(line,"data: ")),&event);err!=nil{t.Fatal(err)}
			return event
		}
		t.Fatalf("SSE stream ended: %v",scanner.Err());return agentiplex.Event{}
	}
	if _,err:=b.SetCollection(ctx,"group","first",[]string{"tester-1"});err!=nil{t.Fatal(err)}
	res,scanner:=connect(0);defer res.Body.Close()
	first:=readEvent(scanner)
	if first.Kind!="group.configured"||first.ID!="first"||first.Sequence==0{t.Fatalf("replay: %+v",first)}
	if _,err:=b.SetCollection(ctx,"group","second",[]string{"tester-2"});err!=nil{t.Fatal(err)}
	second:=readEvent(scanner)
	if second.ID!="second"||second.Sequence<=first.Sequence{t.Fatalf("live: %+v",second)}
	res.Body.Close()
	if _,err:=b.SetCollection(ctx,"group","third",[]string{"tester-1"});err!=nil{t.Fatal(err)}
	res2,scanner2:=connect(second.Sequence);defer res2.Body.Close()
	third:=readEvent(scanner2)
	if third.ID!="third"||third.Sequence<=second.Sequence{t.Fatalf("resume: %+v",third)}
}
