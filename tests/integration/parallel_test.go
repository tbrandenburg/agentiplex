package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/tbrandenburg/agentiplex"
	"github.com/tbrandenburg/agentiplex/internal/api"
	"github.com/tbrandenburg/agentiplex/internal/cli"
	"github.com/tbrandenburg/agentiplex/internal/simagent"
)

func TestParallelDeliveryAndEndpointExclusion(t *testing.T){
	cfg:=config(t);cfg.Workers=4;cfg.Timeout=15*time.Second;cfg.HealthInterval=0
	b:=open(t,cfg)
	ctx:=context.Background()
	slow:=httptest.NewUnstartedServer(nil)
	entered:=make(chan struct{})
	release:=make(chan struct{})
	var once sync.Once
	unblock:=func(){once.Do(func(){close(release)})}
	var slowCalls atomic.Int32
	normal:=simagent.Handler("http://"+slow.Listener.Addr().String(),"slow","testing",nil)
	slow.Config.Handler=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if r.URL.Path=="/invoke"{
			if slowCalls.Add(1)==1{close(entered)}
			select{case<-release:case<-r.Context().Done():return}
		}
		normal.ServeHTTP(w,r)
	})
	slow.Start()
	t.Cleanup(func(){unblock();slow.Close()})
	fast:=&agentService{};fast.start(t,false);t.Cleanup(fast.stop)
	for id,u:=range map[string]string{"slow":slow.URL,"alias":slow.URL,"fast":fast.URL()}{
		if _,err:=b.Register(ctx,id,u);err!=nil{t.Fatal(err)}
	}
	if _,err:=b.SetCollection(ctx,"pool","workers",[]string{"alias","fast"});err!=nil{t.Fatal(err)}
	first,err:=b.Send(ctx,"slow","blocking send","slow-first");if err!=nil{t.Fatal(err)}
	select{case<-entered:case<-time.After(10*time.Second):t.Fatal("slow call not entered")}
	defer unblock()
	second,err:=b.Send(ctx,"alias","must not overlap","same-endpoint");if err!=nil{t.Fatal(err)}
	other,err:=b.Dispatch(ctx,"workers","unrelated work","parallel-pool");if err!=nil{t.Fatal(err)}
	// Slow invocation is still blocked when the independent pool member finishes.
	done:=wait(t,b,other.ID,"completed")
	if done.AssignedAgent!="fast"||fast.calls.Load()!=1||slowCalls.Load()!=1{t.Fatalf("overlap or wrong pool assignment: %+v",done)}
	current,err:=b.Delivery(first.ID);if err!=nil||current.State!="attempting"{t.Fatalf("slow call was not concurrent: %+v %v",current,err)}
	queued,err:=b.Delivery(second.ID);if err!=nil||queued.State!="queued"{t.Fatalf("same endpoint not queued: %+v %v",queued,err)}
	unblock()
	wait(t,b,first.ID,"completed")
	wait(t,b,second.ID,"completed")
	if slowCalls.Load()!=2{t.Fatalf("wrong slow calls: %d",slowCalls.Load())}
}

func TestOperatorAbandonsKnownStoppedTaskWithoutReplay(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=time.Second
	b:=open(t,cfg)
	ctx:=context.Background()
	target:=httptest.NewUnstartedServer(nil)
	base:="http://"+target.Listener.Addr().String()
	card:=&a2a.AgentCard{Name:"stoppable",Description:"Controlled test agent",Version:"0.1.0",
		SupportedInterfaces:[]*a2a.AgentInterface{a2a.NewAgentInterface(base+"/invoke",a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:[]string{"text/plain"},DefaultOutputModes:[]string{"text/plain"},
		Skills:[]a2a.AgentSkill{{ID:"testing",Name:"Testing",Description:"Verification",Tags:[]string{"testing"}}}}
	var calls atomic.Int32
	var offline atomic.Bool
	stop:=make(chan struct{})
	stopped:=make(chan struct{})
	var once sync.Once
	stopRemote:=func(){once.Do(func(){close(stop)})}
	executor:=a2asrv.AgentExecutorFunc(func(ctx context.Context,ec *a2asrv.ExecutorContext)iter.Seq2[a2a.Event,error]{
		return func(yield func(a2a.Event,error)bool){
			if calls.Add(1)>1{yield(a2a.NewMessage(a2a.MessageRoleAgent,a2a.NewTextPart("new work")),nil);return}
			defer close(stopped)
			if !yield(&a2a.Task{ID:ec.TaskID,ContextID:ec.ContextID,Status:a2a.TaskStatus{State:a2a.TaskStateWorking}},nil){return}
			select{case<-stop:case<-ctx.Done():return}
			yield(a2a.NewStatusUpdateEvent(ec,a2a.TaskStateCanceled,nil),nil)
		}
	})
	mux:=http.NewServeMux()
	mux.Handle("/invoke",a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))
	mux.Handle(a2asrv.WellKnownAgentCardPath,a2asrv.NewStaticAgentCardHandler(card))
	target.Config.Handler=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if offline.Load()&&r.URL.Path=="/invoke"{http.Error(w,"offline",503);return}
		mux.ServeHTTP(w,r)
	})
	target.Start()
	t.Cleanup(func(){stopRemote();target.Close()})
	if _,err:=b.Register(ctx,"worker",target.URL);err!=nil{t.Fatal(err)}
	d,err:=b.Send(ctx,"worker","first task","abandon-key");if err!=nil{t.Fatal(err)}
	accepted:=wait(t,b,d.ID,"accepted")
	if _,err=b.AbandonTask(ctx,d.ID,string(accepted.TaskID),false);!errors.Is(err,agentiplex.ErrInvalid){t.Fatalf("confirmation bypass: %v",err)}
	// The test operator knows execution stopped, although GetTask is unavailable.
	offline.Store(true);stopRemote()
	select{case<-stopped:case<-time.After(10*time.Second):t.Fatal("remote did not stop")}
	deadline:=time.Now().Add(20*time.Second)
	for {
		cur,e:=b.Delivery(d.ID);if e!=nil||cur.State!="accepted"{t.Fatalf("unexpected state: %+v %v",cur,e)}
		if cur.PollFailures>0{break}
		if time.Now().After(deadline){t.Fatal("outage not observed")}
		time.Sleep(50*time.Millisecond)
	}
	if _,err=b.AbandonTask(ctx,d.ID,"wrong-id",true);!errors.Is(err,agentiplex.ErrConflict){t.Fatalf("wrong task handle: %v",err)}
	s:=httptest.NewServer(api.Handler(b));defer s.Close()
	var abandoned agentiplex.Delivery
	for {
		cmd:=cli.Command();var output bytes.Buffer;cmd.SetOut(&output);cmd.SetErr(&output)
		cmd.SetArgs([]string{"--server",s.URL,"messages","abandon",d.ID,string(accepted.TaskID),"--confirm-remote-stopped"})
		err=cmd.Execute()
		if err==nil{if err=json.Unmarshal(output.Bytes(),&abandoned);err!=nil{t.Fatal(err)};break}
		if time.Now().After(deadline){t.Fatalf("abandon: %v %s",err,output.String())}
		time.Sleep(50*time.Millisecond)
	}
	if abandoned.State!="uncertain"||abandoned.TaskID!=accepted.TaskID||len(abandoned.Result)==0{t.Fatalf("lost outcome or correlation: %+v",abandoned)}
	health,err:=b.AgentHealth("worker");if err!=nil||health.Busy{t.Fatalf("reservation not released: %+v %v",health,err)}
	offline.Store(false)
	again,err:=b.Send(ctx,"worker","first task","abandon-key");if err!=nil||again.ID!=d.ID||again.State!="uncertain"{t.Fatalf("replay: %+v %v",again,err)}
	next,err:=b.Send(ctx,"worker","new task","new-key");if err!=nil{t.Fatal(err)}
	wait(t,b,next.ID,"completed")
	if calls.Load()!=2{t.Fatalf("wrong invocation count: %d",calls.Load())}
}
