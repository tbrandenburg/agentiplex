package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex/core"
	"github.com/tbrandenburg/agentiplex/internal/api"
)

func TestKeylessSubmissionsRejectedEverywhere(t *testing.T){
	b,c,a,_:=askSetup(t,time.Second);close(a.release)
	if _,err:=b.Register(context.Background(),"other",a.srv.URL+"/");err!=nil{t.Log(err)}
	for _,tool:=range []string{"send_message","broadcast","dispatch","ask"}{
		for _,args:=range []map[string]any{{"target":"worker","text":"x"},{"target":"worker","text":"x","idempotencyKey":""},{"target":"worker","text":"x","idempotencyKey":"  "}}{
			mcpCall(t,c,tool,args,true)
		}
	}
	s:=httptest.NewServer(api.HandlerWithProtocols(b,nil,"",true));t.Cleanup(s.Close)
	for _,path:=range []string{"/api/v1/messages","/api/v1/broadcasts","/api/v1/dispatches"}{
		for _,body:=range []string{`{"target":"worker","text":"x"}`,`{"target":"worker","text":"x","idempotencyKey":""}`}{
			if code:=call(t,"POST",s.URL+path,json.RawMessage(body),nil);code!=400{t.Fatalf("%s %s returned %d",path,body,code)}
		}
	}
	if n,_:=b.RecentDeliveries(10);len(n)!=0||a.sends.Load()!=0{t.Fatalf("keyless calls created work: %d sends=%d",len(n),a.sends.Load())}
}

func TestConcurrentDuplicateSubmissionIsOneOperation(t *testing.T){
	b,_,a,_:=askSetup(t,time.Second)
	s:=httptest.NewServer(api.HandlerWithProtocols(b,nil,"",true));t.Cleanup(s.Close)
	ids:=make([]string,12);var wg sync.WaitGroup
	for i:=range ids{wg.Add(1);go func(){defer wg.Done()
		var d core.Delivery
		if code:=call(t,"POST",s.URL+"/api/v1/messages",map[string]string{"target":"worker","text":"dup","idempotencyKey":"same"},&d);code!=202{t.Errorf("status %d",code)}
		ids[i]=d.ID
	}()}
	wg.Wait()
	for _,id:=range ids{if id==""||id!=ids[0]{t.Fatalf("ids differ: %v",ids)}}
	wait(t,b,ids[0],"accepted");time.Sleep(1500*time.Millisecond)
	if a.sends.Load()!=1{t.Fatalf("sends %d, want 1",a.sends.Load())}
	if code:=call(t,"POST",s.URL+"/api/v1/messages",map[string]string{"target":"worker","text":"changed","idempotencyKey":"same"},nil);code!=409{t.Fatalf("changed payload status %d, want 409",code)}
	if code:=call(t,"POST",s.URL+"/api/v1/messages",map[string]string{"target":"nobody","text":"dup","idempotencyKey":"same"},nil);code==202{t.Fatal("changed target accepted")}
	if a.sends.Load()!=1{t.Fatalf("sends %d after rejected reuse",a.sends.Load())}
}

// The proxy forwards the request, lets the daemon finish, then drops the response.
func TestLostResponseAfterTerminalPersistenceRetriesSameOperation(t *testing.T){
	b,_,a,_:=askSetup(t,time.Second);close(a.release)
	up,_:=url.Parse(httptest.NewServer(api.HandlerWithProtocols(b,nil,"",true)).URL)
	var mu sync.Mutex;drop:=true
	rp:=httputil.NewSingleHostReverseProxy(up)
	proxy:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		mu.Lock();d:=drop;mu.Unlock()
		if !d{rp.ServeHTTP(w,r);return}
		rec:=httptest.NewRecorder();rp.ServeHTTP(rec,r)
		var got core.Delivery;_ = json.Unmarshal(rec.Body.Bytes(),&got)
		wait(t,b,got.ID,"completed")
		conn,_,_:=w.(http.Hijacker).Hijack();_ = conn.Close()
	}));t.Cleanup(proxy.Close)
	body:=map[string]string{"target":"worker","text":"once","idempotencyKey":"lost-1"}
	probe:=&http.Client{Timeout:10*time.Second}
	raw,_:=json.Marshal(body)
	if resp,err:=probe.Post(proxy.URL+"/api/v1/messages","application/json",strings.NewReader(string(raw)));err==nil{resp.Body.Close();t.Fatal("response was not dropped")}
	mu.Lock();drop=false;mu.Unlock()
	var d core.Delivery
	if code:=call(t,"POST",proxy.URL+"/api/v1/messages",body,&d);code!=202||d.State!="completed"||a.sends.Load()!=1{t.Fatalf("retry %d %+v sends=%d",code,d,a.sends.Load())}
	if n,_:=b.RecentDeliveries(10);len(n)!=1{t.Fatalf("%d deliveries",len(n))}
}

func TestRestartWhileQueuedThenSameKeyRunsOnce(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);a:=newTaskAgent(t);close(a.release)
	ctx:=context.Background()
	if _,err:=b.Register(ctx,"worker",a.srv.URL);err!=nil{t.Fatal(err)}
	a.offline.Store(true)
	a.srv.Config.Handler=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){http.Error(w,"down",503)})
	first,err:=b.Send(ctx,"worker","queued work","q-1");if err!=nil{t.Fatal(err)}
	b.Close()
	a.srv.Config.Handler=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){a.mux.Load().ServeHTTP(w,r)})
	b2:=open(t,cfg)
	again,err:=b2.Send(ctx,"worker","queued work","q-1");if err!=nil||again.ID!=first.ID{t.Fatalf("%+v %v",again,err)}
	wait(t,b2,first.ID,"completed")
	if a.sends.Load()!=1{t.Fatalf("sends %d",a.sends.Load())}
}

func TestAmbiguousSendIsNeverReplayedOnRetry(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);a:=&agentService{};a.start(t,true);defer a.stop()
	ctx:=context.Background()
	if _,err:=b.Register(ctx,"architect",a.URL());err!=nil{t.Fatal(err)}
	d,_:=b.Send(ctx,"architect","side effect","amb-1");wait(t,b,d.ID,"uncertain")
	b.Close()
	b2:=open(t,cfg)
	again,err:=b2.Send(ctx,"architect","side effect","amb-1")
	time.Sleep(2*time.Second)
	if err!=nil||again.ID!=d.ID||again.State!="uncertain"||a.calls.Load()!=1{t.Fatalf("%+v %v calls=%d",again,err,a.calls.Load())}
}

func TestOfflineRecipientDoesNotStarveHealthyOne(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0;cfg.Workers=2;cfg.RetryDelay=time.Second
	b:=open(t,cfg);ctx:=context.Background()
	down,up:=newTaskAgent(t),newTaskAgent(t);close(up.release)
	if _,err:=b.Register(ctx,"down",down.srv.URL);err!=nil{t.Fatal(err)}
	if _,err:=b.Register(ctx,"up",up.srv.URL);err!=nil{t.Fatal(err)}
	down.offline.Store(true)
	down.srv.Config.Handler=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){http.Error(w,"down",503)})
	for i:=range 8{if _,err:=b.Send(ctx,"down","stuck","down-"+string(rune('a'+i)));err!=nil{t.Fatal(err)}}
	d,err:=b.Send(ctx,"up","healthy","up-1");if err!=nil{t.Fatal(err)}
	start:=time.Now();wait(t,b,d.ID,"completed")
	if time.Since(start)>15*time.Second||up.sends.Load()!=1||down.sends.Load()!=0{t.Fatalf("healthy %v sends up=%d down=%d",time.Since(start),up.sends.Load(),down.sends.Load())}
}

func TestSelfAskRejectedButOthersAllowed(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);ctx:=context.Background()
	me,other:=newTaskAgent(t),newTaskAgent(t);close(me.release);close(other.release)
	if _,err:=b.Register(ctx,"alpha",me.srv.URL);err!=nil{t.Fatal(err)}
	if _,err:=b.Register(ctx,"alpha-alias",me.srv.URL);err!=nil{t.Fatal(err)}
	if _,err:=b.Register(ctx,"beta",other.srv.URL);err!=nil{t.Fatal(err)}
	e:=identityServer(t,b,map[string]string{"alpha":alphaTok,"beta":betaTok},opTok,viTok)
	c:=e.mcp(alphaTok)
	for _,target:=range []string{"alpha","alpha-alias"}{
		mcpCall(t,c,"ask",map[string]any{"target":target,"text":"loop","idempotencyKey":"self-"+target},true)
	}
	if me.sends.Load()!=0{t.Fatalf("self ask reached the agent: %d",me.sends.Load())}
	if got:=decodeMCP[askResult](t,mcpCall(t,c,"ask",map[string]any{"target":"beta","text":"fine","idempotencyKey":"peer"},false));!got.Done||got.State!="completed"{t.Fatalf("%+v",got)}
	if d:=decodeMCP[map[string]any](t,mcpCall(t,e.mcp(alphaTok),"send_message",map[string]any{"target":"alpha","text":"async ok","idempotencyKey":"async-self"},false));d["id"]==""{t.Fatal("async self send must stay allowed")}
}
