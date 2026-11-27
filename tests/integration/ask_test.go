package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tbrandenburg/agentiplex"
	"github.com/tbrandenburg/agentiplex/internal/api"
	"github.com/tbrandenburg/agentiplex/internal/daemon"
)

type askResult struct {
	ID string `json:"id"`
	State string `json:"state"`
	Result json.RawMessage `json:"result"`
	WaitedSeconds int `json:"waitedSeconds"`
	Done bool `json:"done"`
	StillRunning bool `json:"stillRunning"`
	LastObservedAt *time.Time `json:"lastObservedAt"`
	PollFailures int `json:"pollFailures"`
	Polls int `json:"polls"`
}

func askTool(c *mcp.ClientSession,args map[string]any)(askResult,error){
	var out askResult
	ctx,cancel:=context.WithTimeout(context.Background(),45*time.Second);defer cancel()
	res,err:=c.CallTool(ctx,&mcp.CallToolParams{Name:"ask",Arguments:args})
	if err!=nil{return out,err}
	if res.IsError{return out,context.Canceled}
	raw,err:=json.Marshal(res.StructuredContent);if err!=nil{return out,err}
	return out,json.Unmarshal(raw,&out)
}

func mustAsk(t *testing.T,c *mcp.ClientSession,args map[string]any)askResult{
	t.Helper()
	out,err:=askTool(c,args);if err!=nil{t.Fatalf("ask %v: %v",args,err)}
	return out
}

func askSetup(t *testing.T,poll time.Duration)(*agentiplex.Bus,*mcp.ClientSession,*taskAgent,agentiplex.Config){
	t.Helper()
	cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=poll
	b:=open(t,cfg);a:=newTaskAgent(t)
	if _,err:=b.Register(context.Background(),"worker",a.srv.URL);err!=nil{t.Fatal(err)}
	_,c:=mcpSession(t,b,false)
	return b,c,a,cfg
}

func TestAskWaitsPastDeliveryTimeoutWithoutPollingPerWaiter(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=time.Second;cfg.Timeout=time.Second
	b:=open(t,cfg);a:=newTaskAgent(t)
	var invokes atomic.Int32
	inner:=a.mux.Load();m:=http.NewServeMux()
	m.HandleFunc("/",func(w http.ResponseWriter,r *http.Request){if r.URL.Path=="/invoke"{invokes.Add(1)};inner.ServeHTTP(w,r)})
	a.mux.Store(m)
	if _,err:=b.Register(context.Background(),"worker",a.srv.URL);err!=nil{t.Fatal(err)}
	_,c:=mcpSession(t,b,false)
	const waiters=8
	res:=make([]askResult,waiters);var wg sync.WaitGroup
	for i:=range waiters{wg.Add(1);go func(){defer wg.Done();res[i],_=askTool(c,map[string]any{"target":"worker","text":"long task","idempotencyKey":"waiters"})}()}
	time.Sleep(3*time.Second)
	if a.sends.Load()!=1{t.Fatalf("sends %d",a.sends.Load())}
	close(a.release);wg.Wait()
	for i,r:=range res{
		if !r.Done||r.State!="completed"||r.ID!=res[0].ID||r.WaitedSeconds<2{t.Fatalf("waiter %d: %+v",i,r)}
	}
	d,err:=b.Delivery(res[0].ID);if err!=nil{t.Fatal(err)}
	// Every /invoke request is either the one send or a daemon status poll; waiters add none.
	if a.sends.Load()!=1||int(invokes.Load())>d.Polls+2{t.Fatalf("waiters polled: invokes=%d polls=%d sends=%d",invokes.Load(),d.Polls,a.sends.Load())}
}

func TestAskTimeoutReattachAndRetention(t *testing.T){
	b,c,a,_:=askSetup(t,time.Second)
	args:=map[string]any{"target":"worker","text":"long task","timeoutSeconds":3,"idempotencyKey":"k1"}
	first:=mustAsk(t,c,args)
	if first.Done||!first.StillRunning||first.LastObservedAt==nil||first.Polls<1||first.State!="accepted"{t.Fatalf("stillRunning shape: %+v",first)}
	again:=mustAsk(t,c,args)
	if again.ID!=first.ID||again.Done||a.sends.Load()!=1{t.Fatalf("re-attach: %+v sends=%d",again,a.sends.Load())}
	close(a.release)
	wait(t,b,first.ID,"completed")
	got:=mustAsk(t,c,args)
	if got.ID!=first.ID||!got.Done||got.State!="completed"||len(got.Result)==0||a.sends.Load()!=1{t.Fatalf("collect: %+v",got)}
	again=mustAsk(t,c,args)
	if again.ID!=first.ID||a.sends.Load()!=1{t.Fatalf("same key stays the same operation while retained: %+v sends=%d",again,a.sends.Load())}
	args["idempotencyKey"]="k2"
	next:=mustAsk(t,c,args)
	if next.ID==first.ID||!next.Done||a.sends.Load()!=2{t.Fatalf("new key must start new work: %+v sends=%d",next,a.sends.Load())}
}

func TestAskRequiresKeyAndRejectsChangedPayload(t *testing.T){
	b,c,a,_:=askSetup(t,time.Second)
	close(a.release)
	if _,err:=askTool(c,map[string]any{"target":"worker","text":"quick"});err==nil{t.Fatal("ask without key must be rejected")}
	if _,err:=askTool(c,map[string]any{"target":"worker","text":"quick","idempotencyKey":" "});err==nil{t.Fatal("blank key must be rejected")}
	args:=map[string]any{"target":"worker","text":"quick","idempotencyKey":"ask-key"}
	one:=mustAsk(t,c,args)
	if !one.Done||one.State!="completed"{t.Fatalf("%+v",one)}
	two:=mustAsk(t,c,args)
	if two.ID!=one.ID||!two.Done||a.sends.Load()!=1{t.Fatalf("key re-attach: %+v sends=%d",two,a.sends.Load())}
	if _,err:=askTool(c,map[string]any{"target":"worker","text":"different","idempotencyKey":"ask-key"});err==nil{t.Fatal("changed text under a used key must be an error")}
	if _,err:=askTool(c,map[string]any{"target":"nobody","text":"x","idempotencyKey":"u"});err==nil{t.Fatal("unknown target must be an error")}
	if _,err:=askTool(c,map[string]any{"target":"worker","text":" ","idempotencyKey":"e"});err==nil{t.Fatal("empty text must be an error")}
	if n,_:=b.RecentDeliveries(10);len(n)!=1||a.sends.Load()!=1{t.Fatalf("rejected calls created work: %d sends=%d",len(n),a.sends.Load())}
}

func TestAskReattachesAfterRestart(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=time.Second
	b:=open(t,cfg);a:=newTaskAgent(t)
	if _,err:=b.Register(context.Background(),"worker",a.srv.URL);err!=nil{t.Fatal(err)}
	s:=httptest.NewServer(api.HandlerWithProtocols(b,nil,"",true))
	c1:=mcpClient(t,s.URL)
	args:=map[string]any{"target":"worker","text":"long task","timeoutSeconds":1,"idempotencyKey":"restart-key"}
	first:=mustAsk(t,c1,args)
	if first.Done{t.Fatalf("%+v",first)}
	_ = c1.Close();s.Close();b.Close()
	b2:=open(t,cfg);_,c2:=mcpSession(t,b2,false)
	go func(){time.Sleep(2*time.Second);close(a.release)}()
	got:=mustAsk(t,c2,map[string]any{"target":"worker","text":"long task","idempotencyKey":"restart-key"})
	if got.ID!=first.ID||!got.Done||got.State!="completed"||a.sends.Load()!=1{t.Fatalf("after restart: %+v sends=%d",got,a.sends.Load())}
}

func TestAskReturnsFailedAndUncertainAsDone(t *testing.T){
	b,c,a,_:=askSetup(t,time.Second)
	_ = b
	args:=map[string]any{"target":"worker","text":"long task","idempotencyKey":"uncertain-key"}
	go func(){time.Sleep(time.Second);a.restart(a.card())}()
	u:=mustAsk(t,c,args)
	if !u.Done||u.State!="uncertain"||u.StillRunning{t.Fatalf("uncertain: %+v",u)}

	cfg:=config(t);cfg.HealthInterval=0;cfg.MaxAttempts=1
	b2:=open(t,cfg);dead:=newTaskAgent(t)
	if _,err:=b2.Register(context.Background(),"gone",dead.srv.URL);err!=nil{t.Fatal(err)}
	dead.srv.Close()
	_,c2:=mcpSession(t,b2,false)
	f:=mustAsk(t,c2,map[string]any{"target":"gone","text":"anything","idempotencyKey":"gone-key"})
	if !f.Done||f.State!="failed"{t.Fatalf("failed: %+v",f)}
}

// The real server construction, with its deadlines shortened, must not cut off a blocking /mcp call.
func TestAskSurvivesHTTPWriteTimeout(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=time.Second
	b:=open(t,cfg);a:=newTaskAgent(t)
	if _,err:=b.Register(context.Background(),"worker",a.srv.URL);err!=nil{t.Fatal(err)}
	hs:=daemon.NewServer(api.HandlerWithProtocols(b,nil,"",true),nil,cfg.Timeout)
	hs.ReadTimeout=time.Second;hs.WriteTimeout=time.Second
	s:=httptest.NewUnstartedServer(nil);s.Config=hs;s.Start();t.Cleanup(s.Close)
	go func(){time.Sleep(3*time.Second);close(a.release)}()
	got:=mustAsk(t,mcpClient(t,s.URL),map[string]any{"target":"worker","text":"long task","idempotencyKey":"timeout-key"})
	if !got.Done||got.State!="completed"||got.WaitedSeconds<2{t.Fatalf("%+v",got)}
}

func mcpClient(t *testing.T,url string)*mcp.ClientSession{
	t.Helper()
	ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel()
	c,err:=mcp.NewClient(&mcp.Implementation{Name:"ask-test",Version:"1"},nil).Connect(ctx,&mcp.StreamableClientTransport{Endpoint:url+"/mcp",DisableStandaloneSSE:true},nil)
	if err!=nil{t.Fatal(err)}
	t.Cleanup(func(){_ = c.Close()})
	return c
}
