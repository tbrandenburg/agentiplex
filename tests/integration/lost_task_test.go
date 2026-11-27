package integration_test

import (
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/tbrandenburg/agentiplex"
)

// taskAgent runs a task until release is closed. Swapping the handler simulates a gateway restart.
type taskAgent struct {
	srv *httptest.Server
	sends atomic.Int32
	offline atomic.Bool
	release chan struct{}
	mux atomic.Pointer[http.ServeMux]
}

func newTaskAgent(t *testing.T)*taskAgent{
	a:=&taskAgent{release:make(chan struct{})}
	a.srv=httptest.NewUnstartedServer(nil)
	a.restart(a.card())
	a.srv.Config.Handler=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if a.offline.Load()&&r.URL.Path=="/invoke"{http.Error(w,"offline",503);return}
		a.mux.Load().ServeHTTP(w,r)
	})
	a.srv.Start()
	t.Cleanup(func(){select{case <-a.release:default:close(a.release)};a.srv.Close()})
	return a
}

// restart installs a fresh handler with an empty task store.
func(a *taskAgent) restart(card *a2a.AgentCard){
	executor:=a2asrv.AgentExecutorFunc(func(ctx context.Context,ec *a2asrv.ExecutorContext)iter.Seq2[a2a.Event,error]{
		return func(yield func(a2a.Event,error)bool){
			a.sends.Add(1)
			if !yield(&a2a.Task{ID:ec.TaskID,ContextID:ec.ContextID,Status:a2a.TaskStatus{State:a2a.TaskStateWorking}},nil){return}
			select{case<-a.release:case<-ctx.Done():return}
			yield(a2a.NewStatusUpdateEvent(ec,a2a.TaskStateCompleted,nil),nil)
		}
	})
	m:=http.NewServeMux()
	m.Handle("/invoke",a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))
	m.Handle(a2asrv.WellKnownAgentCardPath,a2asrv.NewStaticAgentCardHandler(card))
	a.mux.Store(m)
}

func (a *taskAgent) card() *a2a.AgentCard{
	base:="http://"+a.srv.Listener.Addr().String()
	return &a2a.AgentCard{Name:"tasker",Description:"Controlled test agent",Version:"0.1.0",
		SupportedInterfaces:[]*a2a.AgentInterface{a2a.NewAgentInterface(base+"/invoke",a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:[]string{"text/plain"},DefaultOutputModes:[]string{"text/plain"},
		Skills:[]a2a.AgentSkill{{ID:"testing",Name:"Testing",Description:"Verification",Tags:[]string{"testing"}}}}
}

func TestLostRemoteTaskSettlesUncertainWithoutReplay(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=time.Second
	b:=open(t,cfg);ctx:=context.Background()
	a:=newTaskAgent(t)
	if _,err:=b.Register(ctx,"worker",a.srv.URL);err!=nil{t.Fatal(err)}
	d,err:=b.Send(ctx,"worker","long task","lost-key");if err!=nil{t.Fatal(err)}
	wait(t,b,d.ID,"accepted")
	a.restart(a.card())
	deadline:=time.Now().Add(20*time.Second)
	var cur agentiplex.Delivery
	for cur.State!="uncertain"{
		if time.Now().After(deadline){t.Fatalf("not settled: %+v",cur)}
		time.Sleep(100*time.Millisecond)
		cur,_=b.Delivery(d.ID)
	}
	if cur.Detail!="remote task lost; outcome unknown; no automatic replay"||cur.Polls<1||cur.LastObservedAt==nil{t.Fatalf("wrong settlement: %+v",cur)}
	if h,err:=b.AgentHealth("worker");err!=nil||h.Busy{t.Fatalf("reservation not released: %+v %v",h,err)}
	next,err:=b.Send(ctx,"worker","next task","after-lost");if err!=nil{t.Fatal(err)}
	wait(t,b,next.ID,"accepted")
	if a.sends.Load()!=2{t.Fatalf("expected exactly one send per delivery, got %d",a.sends.Load())}
}

func TestUnreachableAgentStaysAcceptedThenCompletes(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=time.Second
	b:=open(t,cfg);ctx:=context.Background()
	a:=newTaskAgent(t)
	if _,err:=b.Register(ctx,"worker",a.srv.URL);err!=nil{t.Fatal(err)}
	d,err:=b.Send(ctx,"worker","long task","outage-key");if err!=nil{t.Fatal(err)}
	wait(t,b,d.ID,"accepted")
	a.offline.Store(true)
	deadline:=time.Now().Add(20*time.Second)
	for {
		cur,_:=b.Delivery(d.ID)
		if cur.State!="accepted"{t.Fatalf("left accepted during outage: %+v",cur)}
		if cur.PollFailures>=2{break}
		if time.Now().After(deadline){t.Fatal("outage not observed")}
		time.Sleep(100*time.Millisecond)
	}
	a.offline.Store(false);close(a.release)
	done:=wait(t,b,d.ID,"completed")
	if a.sends.Load()!=1||done.PollFailures!=0{t.Fatalf("replay or stale failures: sends=%d %+v",a.sends.Load(),done)}
}

func TestStateEventsOnlyOnTransitions(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=time.Second
	b:=open(t,cfg);ctx:=context.Background()
	a:=newTaskAgent(t)
	if _,err:=b.Register(ctx,"worker",a.srv.URL);err!=nil{t.Fatal(err)}
	d,err:=b.Send(ctx,"worker","long task","events-key");if err!=nil{t.Fatal(err)}
	wait(t,b,d.ID,"accepted")
	for {
		cur,_:=b.Delivery(d.ID)
		if cur.Polls>=3{break}
		time.Sleep(100*time.Millisecond)
	}
	close(a.release)
	wait(t,b,d.ID,"completed")
	evs,err:=b.Events(0);if err!=nil{t.Fatal(err)}
	seen:=map[string]int{}
	for _,e:=range evs{if e.Kind=="delivery.state"&&e.ID==d.ID{seen[e.State]++}}
	if seen["accepted"]!=1||seen["completed"]!=1{t.Fatalf("want one event per transition: %v",seen)}
}
