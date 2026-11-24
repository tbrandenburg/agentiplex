package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	catalog "github.com/Agent-Card/ai-catalog-go/catalog"
	"github.com/Agent-Card/ai-catalog-go/validate"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/tbrandenburg/agentiplex"
	"github.com/tbrandenburg/agentiplex/internal/api"
	"github.com/tbrandenburg/agentiplex/internal/simagent"
)

type agentService struct {
	addr string
	srv *http.Server
	calls atomic.Int32
}

func (a *agentService) start(t *testing.T, ambiguous bool) {
	t.Helper()
	addr:=a.addr
	if addr==""{addr="127.0.0.1:0"}
	ln,err:=net.Listen("tcp",addr)
	if err!=nil{t.Fatal(err)}
	a.addr=ln.Addr().String()
	h:=simagent.Handler("http://"+a.addr,"architect","architecture",func(){a.calls.Add(1)})
	if ambiguous{
		normal:=h
		h=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
			if r.URL.Path=="/invoke"{
				a.calls.Add(1)
				conn,_,err:=w.(http.Hijacker).Hijack()
				if err==nil{_ = conn.Close()}
				return
			}
			normal.ServeHTTP(w,r)
		})
	}
	a.srv=&http.Server{Handler:h,ReadHeaderTimeout:2*time.Second}
	s:=a.srv
	go func(){_ = s.Serve(ln)}()
}

func(a *agentService) stop(){if a.srv!=nil{_ = a.srv.Close();a.srv=nil}}
func(a *agentService) URL()string{return "http://"+a.addr}

func config(t *testing.T)agentiplex.Config{
	t.Helper()
	cfg:=agentiplex.DefaultConfig()
	cfg.Storage=t.TempDir()
	cfg.Timeout=4*time.Second
	cfg.RetryDelay=time.Second
	cfg.MaxAttempts=20
	return cfg
}

func open(t *testing.T,cfg agentiplex.Config)*agentiplex.Bus{
	t.Helper()
	b,err:=agentiplex.Open(context.Background(),cfg)
	if err!=nil{t.Fatal(err)}
	t.Cleanup(b.Close)
	return b
}

func wait(t *testing.T,b *agentiplex.Bus,id,state string)agentiplex.Delivery{
	t.Helper()
	deadline:=time.Now().Add(30*time.Second)
	for time.Now().Before(deadline){
		d,err:=b.Delivery(id)
		if err==nil&&d.State==state{return d}
		if err==nil&&(d.State=="failed"||d.State=="uncertain"||d.State=="completed")&&d.State!=state{t.Fatalf("delivery reached %s, wanted %s: %s",d.State,state,d.Detail)}
		time.Sleep(100*time.Millisecond)
	}
	d,err:=b.Delivery(id)
	t.Fatalf("delivery did not reach %s: state=%s detail=%s err=%v",state,d.State,d.Detail,err)
	return agentiplex.Delivery{}
}

func TestFoundationDirectOfflineAndRestart(t *testing.T){
	cfg:=config(t)
	b:=open(t,cfg)
	a:=&agentService{};a.start(t,false);defer a.stop()
	ctx:=context.Background()
	reg,err:=b.Register(ctx,"architect",a.URL());if err!=nil{t.Fatal(err)}
	if reg.Card.Name!="architect"{t.Fatalf("wrong card: %+v",reg.Card)}
	if _,err=b.Register(ctx,"architect",a.URL());!errors.Is(err,agentiplex.ErrConflict){t.Fatalf("duplicate: %v",err)}
	if _,err=b.Register(ctx,"invalid!",a.URL());!errors.Is(err,agentiplex.ErrInvalid){t.Fatalf("invalid ID: %v",err)}
	bad:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){_,_=io.WriteString(w,"{}")}))
	defer bad.Close()
	if _,err=b.Register(ctx,"bad",bad.URL);!errors.Is(err,agentiplex.ErrInvalid){t.Fatalf("invalid card: %v",err)}
	agents,err:=b.Agents("architecture","simulated")
	if err!=nil||len(agents)!=1{t.Fatalf("discovery: %d %v",len(agents),err)}
	agents,err=b.Agents("testing","")
	if err!=nil||len(agents)!=0{t.Fatalf("nonmatch: %d %v",len(agents),err)}

	// Independent HTTP consumer checks the catalog endpoint and resolves its card.
	srv:=httptest.NewServer(api.Handler(b))
	defer srv.Close()
	resp,err:=http.Get(srv.URL+catalog.WellKnownPath);if err!=nil{t.Fatal(err)}
	raw,err:=io.ReadAll(resp.Body);_ = resp.Body.Close()
	if err!=nil||resp.StatusCode!=200||resp.Header.Get("Content-Type")!=catalog.MediaTypeCatalog{t.Fatalf("catalog HTTP: %d %v",resp.StatusCode,err)}
	doc,err:=catalog.Parse(raw)
	if err!=nil||len(doc.Entries)!=1{t.Fatalf("catalog parse: %v",err)}
	if !validate.Validate(doc).IsValid||doc.Entries[0].Type!=catalog.MediaTypeA2AAgentCard||doc.Entries[0].URL!=reg.CardURL{t.Fatal("invalid catalog entry")}
	cardResp,err:=http.Get(doc.Entries[0].URL);if err!=nil{t.Fatal(err)}
	_ = cardResp.Body.Close()
	if cardResp.StatusCode!=200{t.Fatalf("card HTTP: %d",cardResp.StatusCode)}

	d,err:=b.Send(ctx,"architect","review requirement 42","first");if err!=nil||d.State!="queued"{t.Fatalf("send receipt: %+v %v",d,err)}
	done:=wait(t,b,d.ID,"completed")
	if !strings.Contains(string(done.Result),"SIMULATED architect"){t.Fatalf("missing response: %s",done.Result)}
	duplicate,err:=b.Send(ctx,"architect","review requirement 42","first")
	if err!=nil||duplicate.ID!=d.ID||a.calls.Load()!=1{t.Fatalf("idempotency: %v %d",err,a.calls.Load())}
	if _,err=b.Send(ctx,"architect","different prompt","first");!errors.Is(err,agentiplex.ErrConflict){t.Fatalf("conflicting idempotency key: %v",err)}

	a.stop()
	pending,err:=b.Send(ctx,"architect","offline handoff","second")
	if err!=nil{t.Fatal(err)}
	wait(t,b,pending.ID,"queued")
	b.Close()
	// Registry, intent, queued message and durable consumer must survive
	// stopping the daemon and its embedded broker before the agent returns.
	b2:=open(t,cfg)
	if _,err=b2.Agent("architect");err!=nil{t.Fatal(err)}
	if got,err:=b2.Delivery(pending.ID);err!=nil||got.State!="queued"{t.Fatalf("restart state: %s %v",got.State,err)}
	if a.calls.Load()!=1{t.Fatal("offline task was invoked")}
	a.start(t,false)
	wait(t,b2,pending.ID,"completed")
	if a.calls.Load()!=2{t.Fatalf("wrong number of target calls: %d",a.calls.Load())}
	events,err:=b2.Events(0)
	if err!=nil||len(events)<4{t.Fatalf("durable event log: %d %v",len(events),err)}
}

func TestAmbiguousSendDoesNotReplay(t *testing.T){
	b:=open(t,config(t))
	a:=&agentService{};a.start(t,true);defer a.stop()
	ctx:=context.Background()
	if _,err:=b.Register(ctx,"architect",a.URL());err!=nil{t.Fatal(err)}
	d,err:=b.Send(ctx,"architect","side effect","ambiguous");if err!=nil{t.Fatal(err)}
	wait(t,b,d.ID,"uncertain")
	time.Sleep(1500*time.Millisecond)
	if a.calls.Load()!=1{t.Fatalf("ambiguous request replayed: %d",a.calls.Load())}
}

func TestExternalBrokerAndHTTPSubmission(t *testing.T){
	n,err:=server.NewServer(&server.Options{Host:"127.0.0.1",Port:-1,JetStream:true,StoreDir:t.TempDir(),NoLog:true,NoSigs:true})
	if err!=nil{t.Fatal(err)}
	go n.Start()
	defer func(){n.Shutdown();n.WaitForShutdown()}()
	if !n.ReadyForConnections(10*time.Second){t.Fatal("NATS not ready")}
	cfg:=config(t);cfg.BrokerMode="external";cfg.BrokerURL=n.ClientURL()
	b:=open(t,cfg);defer b.Close()
	a:=&agentService{};a.start(t,false);defer a.stop()
	if _,err=b.Register(context.Background(),"architect",a.URL());err!=nil{t.Fatal(err)}
	h:=httptest.NewServer(api.Handler(b));defer h.Close()
	resp,err:=http.Post(h.URL+"/api/v1/messages","application/json",strings.NewReader(`{"target":"architect","text":"external broker","idempotencyKey":"external"}`))
	if err!=nil{t.Fatal(err)}
	var d agentiplex.Delivery
	err=json.NewDecoder(resp.Body).Decode(&d);_ = resp.Body.Close()
	if err!=nil||resp.StatusCode!=202||d.ID==""{t.Fatalf("HTTP submission: %d %v",resp.StatusCode,err)}
	wait(t,b,d.ID,"completed")
}
