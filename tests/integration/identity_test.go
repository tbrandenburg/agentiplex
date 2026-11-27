package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tbrandenburg/agentiplex"
	"github.com/tbrandenburg/agentiplex/internal/api"
	"github.com/tbrandenburg/agentiplex/internal/dashboard"
	"github.com/tbrandenburg/agentiplex/internal/security"
)

type identityEnv struct {
	t *testing.T
	s *httptest.Server
	public string
}

func identityServer(t *testing.T,b *agentiplex.Bus,agents map[string]string,operator,viewer string)*identityEnv{
	t.Helper()
	dir:=t.TempDir()
	files:=map[string]string{}
	for id,tok:=range agents{files[id]=secretFile(t,dir,"agent-"+id,tok)}
	auth,err:=security.New(security.Config{OperatorFile:secretFile(t,dir,"operator",operator),ViewerFile:secretFile(t,dir,"viewer",viewer),AgentFiles:files})
	if err!=nil{t.Fatal(err)}
	ui,err:=dashboard.New(dashboard.DefaultConfig());if err!=nil{t.Fatal(err)}
	s:=httptest.NewUnstartedServer(nil)
	public:="https://"+s.Listener.Addr().String()
	s.Config.Handler=api.HandlerSecure(b,ui,public,true,auth)
	s.StartTLS();t.Cleanup(s.Close)
	return &identityEnv{t:t,s:s,public:public}
}

func (e *identityEnv) call(method,path,token,body string,want int)string{
	e.t.Helper()
	req,err:=http.NewRequest(method,e.s.URL+path,strings.NewReader(body));if err!=nil{e.t.Fatal(err)}
	req.Header.Set("Content-Type","application/json")
	if token!=""{req.Header.Set("Authorization","Bearer "+token)}
	resp,err:=e.s.Client().Do(req);if err!=nil{e.t.Fatal(err)};defer resp.Body.Close()
	raw,_:=io.ReadAll(resp.Body)
	if resp.StatusCode!=want{e.t.Fatalf("%s %s returned %d want %d: %s",method,path,resp.StatusCode,want,raw)}
	return string(raw)
}

func (e *identityEnv) send(token,key,text string)agentiplex.Delivery{
	e.t.Helper()
	body,_:=json.Marshal(map[string]string{"target":"tester-1","text":text,"idempotencyKey":key})
	var d agentiplex.Delivery
	if err:=json.Unmarshal([]byte(e.call("POST","/api/v1/messages",token,string(body),202)),&d);err!=nil{e.t.Fatal(err)}
	return d
}

func (e *identityEnv) a2a(token string)*a2aclient.Client{
	e.t.Helper()
	client:=&http.Client{Transport:authenticatedTransport{base:e.s.Client().Transport,token:token},Timeout:20*time.Second}
	c,err:=a2aclient.NewFromEndpoints(context.Background(),[]*a2a.AgentInterface{a2a.NewAgentInterface(e.public+"/a2a",a2a.TransportProtocolJSONRPC)},a2aclient.WithJSONRPCTransport(client))
	if err!=nil{e.t.Fatal(err)}
	e.t.Cleanup(func(){_ = c.Destroy()})
	return c
}

func (e *identityEnv) mcp(token string)*mcp.ClientSession{
	e.t.Helper()
	client:=&http.Client{Transport:authenticatedTransport{base:e.s.Client().Transport,token:token},Timeout:20*time.Second}
	ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second);defer cancel()
	c:=mcp.NewClient(&mcp.Implementation{Name:"claims-to-be-beta",Version:"1.0.0"},nil)
	session,err:=c.Connect(ctx,&mcp.StreamableClientTransport{Endpoint:e.public+"/mcp",HTTPClient:client,DisableStandaloneSSE:true},nil)
	if err!=nil{e.t.Fatal(err)}
	e.t.Cleanup(func(){_ = session.Close()})
	return session
}

var (
	opTok=strings.Repeat("o",48)
	viTok=strings.Repeat("v",48)
	alphaTok=strings.Repeat("a",48)
	betaTok=strings.Repeat("b",48)
)

func TestPerAgentIdentityIsolation(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);first,_:=registerPair(t,b)
	if _,err:=b.SetCollection(context.Background(),"group","team",[]string{"tester-1","tester-2"});err!=nil{t.Fatal(err)}
	e:=identityServer(t,b,map[string]string{"alpha":alphaTok,"beta":betaTok},opTok,viTok)

	// Missing, unknown and retired shared-role credentials never authenticate.
	for _,tok:=range []string{"",strings.Repeat("x",48),strings.Repeat("c",48)}{
		e.call("POST","/api/v1/messages",tok,`{}`,401)
		e.call("POST","/a2a",tok,`{}`,401)
		e.call("POST","/mcp",tok,`{}`,401)
	}

	// Same key and text from two agents: independent deliveries; same-principal retry is idempotent.
	da:=e.send(alphaTok,"k1","same text");db:=e.send(betaTok,"k1","same text")
	if da.ID==db.ID{t.Fatal("two principals collided on one delivery")}
	wait(t,b,da.ID,"completed");wait(t,b,db.ID,"completed")
	if retry:=e.send(alphaTok,"k1","same text");retry.ID!=da.ID{t.Fatalf("retry created %s, want %s",retry.ID,da.ID)}
	if first.calls.Load()!=2{t.Fatalf("expected two executions, got %d",first.calls.Load())}
	ra,_:=b.Delivery(da.ID);rb,_:=b.Delivery(db.ID)
	if ra.Caller!="alpha"||rb.Caller!="beta"{t.Fatalf("callers %q %q",ra.Caller,rb.Caller)}

	// Spoofed identity fields are rejected; read access is limited to own records.
	e.call("POST","/api/v1/messages",betaTok,`{"target":"tester-1","text":"x","idempotencyKey":"s","sender":"alpha"}`,400)
	e.call("GET","/api/v1/deliveries/"+da.ID,alphaTok,"",200)
	e.call("GET","/api/v1/deliveries/"+da.ID,betaTok,"",404)
	e.call("GET","/api/v1/deliveries/"+da.ID,opTok,"",200)
	for _,path:=range []string{"/api/v1/deliveries","/api/v1/events","/api/v1/events/stream"}{e.call("GET",path,alphaTok,"",403)}
	e.call("GET","/api/v1/deliveries",opTok,"",200)
	e.call("POST","/api/v1/deliveries/"+da.ID+"/abandon",alphaTok,`{}`,403)

	// Broadcast children inherit the caller.
	body:=`{"target":"team","text":"fan","idempotencyKey":"bc"}`
	var parent agentiplex.Delivery
	if err:=json.Unmarshal([]byte(e.call("POST","/api/v1/broadcasts",alphaTok,body,202)),&parent);err!=nil{t.Fatal(err)}
	view:=wait(t,b,parent.ID,"completed")
	for _,c:=range view.Children{
		e.call("GET","/api/v1/deliveries/"+c.ID,alphaTok,"",200)
		e.call("GET","/api/v1/deliveries/"+c.ID,betaTok,"",404)
		if child,_:=b.Delivery(c.ID);child.Caller!="alpha"{t.Fatalf("child caller %q",child.Caller)}
	}
	var other agentiplex.Delivery
	if err:=json.Unmarshal([]byte(e.call("POST","/api/v1/broadcasts",betaTok,body,202)),&other);err!=nil||other.ID==parent.ID{t.Fatalf("broadcast key shared across principals: %v",err)}
	e.call("GET","/api/v1/deliveries/"+parent.ID,betaTok,"",404)

	// Events record the immutable caller.
	evs,err:=b.Events(0);if err!=nil{t.Fatal(err)}
	sawAlpha:=false
	for _,ev:=range evs{if ev.ID==da.ID&&ev.Kind=="delivery.state"{if ev.Caller!="alpha"{t.Fatalf("event caller %q",ev.Caller)};sawAlpha=true}}
	if !sawAlpha{t.Fatal("no delivery event recorded")}

	// A2A: same messageId from two principals, no cross-reads, retry returns the same task.
	ca,cb:=e.a2a(alphaTok),e.a2a(betaTok)
	ta:=nativeSend(t,ca,sendRequest("m1","tester-1","a2a text",true))
	tb:=nativeSend(t,cb,sendRequest("m1","tester-1","a2a text",true))
	if ta.ID==tb.ID{t.Fatal("A2A messageId shared across principals")}
	if again:=nativeSend(t,ca,sendRequest("m1","tester-1","a2a text",true));again.ID!=ta.ID{t.Fatal("A2A retry created a second task")}
	wait(t,b,string(ta.ID),"completed");wait(t,b,string(tb.ID),"completed")
	if first.calls.Load()!=6{t.Fatalf("expected 6 executions, got %d",first.calls.Load())}
	if _,err:=cb.GetTask(context.Background(),&a2a.GetTaskRequest{ID:ta.ID});err==nil{t.Fatal("beta read alpha's A2A task")}
	if got,err:=ca.GetTask(context.Background(),&a2a.GetTaskRequest{ID:ta.ID});err!=nil||got.ID!=ta.ID{t.Fatalf("own task: %v",err)}

	// MCP: scoped keys, own-result reads only, server-set note author regardless of claims.
	ma,mb:=e.mcp(alphaTok),e.mcp(betaTok)
	args:=map[string]any{"target":"tester-1","text":"mcp text","idempotencyKey":"mk"}
	ra1:=decodeMCP[agentiplex.Delivery](t,mcpCall(t,ma,"send_message",args,false))
	rb1:=decodeMCP[agentiplex.Delivery](t,mcpCall(t,mb,"send_message",args,false))
	if ra1.ID==rb1.ID{t.Fatal("MCP key shared across principals")}
	wait(t,b,ra1.ID,"completed");wait(t,b,rb1.ID,"completed")
	mcpCall(t,mb,"get_delivery",map[string]any{"id":ra1.ID},true)
	mcpCall(t,ma,"get_delivery",map[string]any{"id":ra1.ID},false)
	mcpCall(t,mb,"note",map[string]any{"namespace":"team","topic":"t","text":"hello","author":"alpha"},true)
	mcpCall(t,mb,"note",map[string]any{"namespace":"team","topic":"t","text":"hello"},false)
	notes,_:=b.ReadNotes("team","t",10)
	if len(notes)!=1||notes[0].Author!="beta"{t.Fatalf("note author %+v",notes)}

	// Agents cannot manage the registry or collections.
	e.call("POST","/api/v1/agents",alphaTok,`{"id":"x","url":"http://127.0.0.1:1"}`,403)
	e.call("PUT","/api/v1/groups/team",alphaTok,`{"members":["tester-1"]}`,403)
}

func TestAgentIdentitySurvivesRestartAndRotation(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);first,_:=registerPair(t,b)
	e:=identityServer(t,b,map[string]string{"alpha":alphaTok,"beta":betaTok},opTok,viTok)
	d:=e.send(alphaTok,"k","persist me");wait(t,b,d.ID,"completed")
	b.Close();e.s.Close()

	// Restart: alpha's token is rotated, beta is revoked.
	b2:=open(t,cfg)
	rotated:=strings.Repeat("r",48)
	e2:=identityServer(t,b2,map[string]string{"alpha":rotated},opTok,viTok)
	e2.call("GET","/api/v1/deliveries/"+d.ID,alphaTok,"",401)
	e2.call("GET","/api/v1/deliveries/"+d.ID,betaTok,"",401)
	var got agentiplex.Delivery
	if err:=json.Unmarshal([]byte(e2.call("GET","/api/v1/deliveries/"+d.ID,rotated,"",200)),&got);err!=nil||got.Caller!="alpha"||got.State!="completed"{t.Fatalf("record after restart: %+v %v",got,err)}
	if retry:=e2.send(rotated,"k","persist me");retry.ID!=d.ID||first.calls.Load()!=1{t.Fatalf("retry after restart re-executed: %s calls=%d",retry.ID,first.calls.Load())}
	// A rotated credential for a different identity does not inherit records.
	e3:=identityServer(t,b2,map[string]string{"beta":rotated},opTok,viTok)
	e3.call("GET","/api/v1/deliveries/"+d.ID,rotated,"",404)
}

func TestCredentialsNeverLeakInRecordsOrCard(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);registerPair(t,b)
	e:=identityServer(t,b,map[string]string{"alpha":alphaTok},opTok,viTok)
	d:=e.send(alphaTok,"k","x");wait(t,b,d.ID,"completed")
	evs,_:=b.Events(0)
	dump,_:=json.Marshal(map[string]any{"events":evs,"delivery":d,"card":e.call("GET","/.well-known/agent-card.json","","",200),"catalog":e.call("GET","/.well-known/ai-catalog.json",opTok,"",200),"agents":e.call("GET","/api/v1/agents",opTok,"",200)})
	for _,tok:=range []string{opTok,viTok,alphaTok}{if strings.Contains(string(dump),tok){t.Fatal("credential exposed")}}
}
