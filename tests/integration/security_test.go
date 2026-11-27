package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tbrandenburg/agentiplex"
	"github.com/tbrandenburg/agentiplex/internal/api"
	"github.com/tbrandenburg/agentiplex/internal/cli"
	"github.com/tbrandenburg/agentiplex/internal/dashboard"
	"github.com/tbrandenburg/agentiplex/internal/security"
	"github.com/tbrandenburg/agentiplex/internal/simagent"
)

type authenticatedTransport struct{base http.RoundTripper;token string}
func (t authenticatedTransport) RoundTrip(r *http.Request)(*http.Response,error){
	r=r.Clone(r.Context());r.Header.Set("Authorization","Bearer "+t.token)
	return t.base.RoundTrip(r)
}
func secretFile(t *testing.T,dir,name,value string)string{
	t.Helper();p:=filepath.Join(dir,name)
	if err:=os.WriteFile(p,[]byte(value),0600);err!=nil{t.Fatal(err)}
	return p
}
func TestSecureHTTPRolesA2AAndMCP(t *testing.T){
	b:=open(t,config(t));a,_:=registerPair(t,b)
	dir:=t.TempDir()
	operator,collaborator,viewer:=strings.Repeat("o",48),strings.Repeat("c",48),strings.Repeat("v",48)
	opFile:=secretFile(t,dir,"operator",operator)
	coFile:=secretFile(t,dir,"collaborator",collaborator)
	viFile:=secretFile(t,dir,"viewer",viewer)
	auth,err:=security.New(security.Config{OperatorFile:opFile,CollaboratorFile:coFile,ViewerFile:viFile})
	if err!=nil{t.Fatal(err)}
	ui,err:=dashboard.New(dashboard.DefaultConfig());if err!=nil{t.Fatal(err)}
	s:=httptest.NewUnstartedServer(nil)
	public:="https://"+s.Listener.Addr().String()
	s.Config.Handler=api.HandlerSecure(b,ui,public,true,auth)
	s.StartTLS();defer s.Close()
	call:=func(method,path,token,body,origin string,want int)string{
		t.Helper()
		req,err:=http.NewRequest(method,s.URL+path,strings.NewReader(body));if err!=nil{t.Fatal(err)}
		req.Header.Set("Content-Type","application/json")
		if token!=""{req.Header.Set("Authorization","Bearer "+token)}
		if origin!=""{req.Header.Set("Origin",origin)}
		resp,err:=s.Client().Do(req);if err!=nil{t.Fatal(err)};defer resp.Body.Close()
		raw,err:=io.ReadAll(resp.Body);if err!=nil{t.Fatal(err)}
		if resp.StatusCode!=want{t.Fatalf("%s %s returned %d want %d: %s",method,path,resp.StatusCode,want,raw)}
		for _,secret:=range []string{operator,collaborator,viewer}{if strings.Contains(string(raw),secret){t.Fatal("credential disclosed in response")}}
		return string(raw)
	}
	if body:=call("GET","/readyz","","","",200);strings.TrimSpace(body)!=`{"ready":true}`{t.Fatalf("public readyz body %q",body)}
	call("GET","/api/v1/usage","","","",401)
	call("GET","/api/v1/usage",viewer,"","",200)
	card:=call("GET","/.well-known/agent-card.json","","","",200)
	if !strings.Contains(card,"httpAuthSecurityScheme")||!strings.Contains(card,"securityRequirements"){t.Fatal("missing advertised security")}
	for _,path:=range []string{"/api/v1/agents","/api/v1/dashboard","/api/v1/state/engineering","/.well-known/ai-catalog.json","/ui/"}{call("GET",path,"","","",401)}
	call("POST","/a2a","","{}","",401)
	call("POST","/mcp","","{}","",401)
	call("GET","/api/v1/agents",collaborator,"","",200)
	call("POST","/api/v1/agents",collaborator,"{}","",403)
	call("DELETE","/api/v1/agents/tester-1",collaborator,"","",403)
	call("PUT","/api/v1/groups/team",collaborator,`{"members":["tester-1"]}`,"",403)
	call("PUT","/api/v1/groups/team",operator,`{"members":["tester-1"]}`,"",200)
	call("PUT","/api/v1/state/team/key",collaborator,`{"value":"context","revision":0}`,"",200)
	call("DELETE","/api/v1/state/team/key?revision=1",collaborator,"","",403)
	call("GET","/api/v1/dashboard",viewer,"","",200)
	call("GET","/api/v1/state/team/key",viewer,"","",403)
	call("POST","/api/v1/messages",viewer,"{}","",403)
	call("POST","/api/v1/messages",operator,"{}","https://evil.example",403)
	call("POST","/mcp",collaborator,"{}","https://evil.example",403)
	req,_:=http.NewRequest("GET",s.URL+"/ui/",nil);req.SetBasicAuth("viewer",viewer)
	resp,err:=s.Client().Do(req);if err!=nil{t.Fatal(err)};resp.Body.Close()
	if resp.StatusCode!=200{t.Fatalf("viewer browser authentication: %d",resp.StatusCode)}
	// No TLS means no protected access, even with a valid credential.
	rec:=httptest.NewRecorder();plain:=httptest.NewRequest("GET","http://localhost/api/v1/agents",nil);plain.Header.Set("Authorization","Bearer "+operator)
	s.Config.Handler.ServeHTTP(rec,plain);if rec.Code!=403{t.Fatal("plaintext auth accepted")}
	client:=&http.Client{Transport:authenticatedTransport{base:s.Client().Transport,token:collaborator},Timeout:20*time.Second}
	ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
	ac,err:=a2aclient.NewFromEndpoints(ctx,[]*a2a.AgentInterface{a2a.NewAgentInterface(public+"/a2a",a2a.TransportProtocolJSONRPC)},a2aclient.WithJSONRPCTransport(client))
	if err!=nil{t.Fatal(err)};defer ac.Destroy()
	task:=nativeSend(t,ac,sendRequest("secure-a2a","tester-1","secure task",true))
	wait(t,b,string(task.ID),"completed")
	mc:=mcp.NewClient(&mcp.Implementation{Name:"secure-test",Version:"1.0.0"},nil)
	session,err:=mc.Connect(ctx,&mcp.StreamableClientTransport{Endpoint:public+"/mcp",HTTPClient:client,DisableStandaloneSSE:true},nil)
	if err!=nil{t.Fatal(err)};defer session.Close()
	d:=decodeMCP[agentiplex.Delivery](t,mcpCall(t,session,"send_message",map[string]any{"target":"tester-1","text":"secure MCP","idempotencyKey":"secure-mcp"},false))
	wait(t,b,d.ID,"completed")
	if a.calls.Load()!=2{t.Fatal("secure SDK requests did not execute exactly twice")}
	ca:=secretFile(t,dir,"ca.crt",string(pem.EncodeToMemory(&pem.Block{Type:"CERTIFICATE",Bytes:s.Certificate().Raw})))
	cmd:=cli.Command();var output bytes.Buffer;cmd.SetOut(&output);cmd.SetErr(&output)
	cmd.SetArgs([]string{"--server",s.URL,"--ca-file",ca,"--token-file",coFile,"state","get","team","key"})
	if err=cmd.Execute();err!=nil||!strings.Contains(output.String(),"context"){t.Fatalf("secured CLI: %v %s",err,output.String())}
}

func TestOutboundA2AOriginScopedCredential(t *testing.T){
	token:=strings.Repeat("s",48)
	target:=httptest.NewUnstartedServer(nil)
	base:="https://"+target.Listener.Addr().String()
	sim:=simagent.Handler(base,"secure-target","architecture",nil)
	target.Config.Handler=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if r.Header.Get("Authorization")!="Bearer "+token{http.Error(w,"unauthorized",401);return}
		sim.ServeHTTP(w,r)
	})
	target.StartTLS();defer target.Close()
	dir:=t.TempDir()
	ca:=secretFile(t,dir,"ca.crt",string(pem.EncodeToMemory(&pem.Block{Type:"CERTIFICATE",Bytes:target.Certificate().Raw})))
	cred:=secretFile(t,dir,"outbound",token)
	cfg:=config(t);cfg.HealthInterval=0;cfg.OutboundCAFile=ca;cfg.OutboundTokenFiles=map[string]string{base:cred}
	b:=open(t,cfg)
	ctx:=context.Background()
	if _,err:=b.Register(ctx,"secure",base);err!=nil{t.Fatal(err)}
	d,err:=b.Send(ctx,"secure","authenticated outbound","secure-outbound");if err!=nil{t.Fatal(err)}
	done:=wait(t,b,d.ID,"completed")
	raw,_:=json.Marshal(done)
	if strings.Contains(string(raw),token)||!strings.Contains(string(raw),"SIMULATED secure-target"){t.Fatalf("invalid result: %s",raw)}
	other:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if r.Header.Get("Authorization")!=""{t.Error("credential sent to another origin")}
		http.Error(w,"no card",404)
	}));defer other.Close()
	if _,err=b.Register(ctx,"other",other.URL);err==nil{t.Fatal("invalid card accepted")}
	b.Close()
	cfg.Storage=t.TempDir();cfg.OutboundTokenFiles=map[string]string{"http://127.0.0.1:1234":cred}
	if invalid,err:=agentiplex.Open(ctx,cfg);err==nil{invalid.Close();t.Fatal("plaintext outbound credential accepted")}
}
