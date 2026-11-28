package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tbrandenburg/agentiplex/core"
	"github.com/tbrandenburg/agentiplex/internal/api"
	"github.com/tbrandenburg/agentiplex/internal/daemon"
)

func mcpSession(t *testing.T,b *core.Bus,legacy bool)(*httptest.Server,*mcp.ClientSession){
	t.Helper()
	s:=httptest.NewServer(api.HandlerWithProtocols(b,nil,"",true))
	t.Cleanup(s.Close)
	opts:=&mcp.ClientSessionOptions{}
	if legacy{opts.ProtocolVersion="2025-11-25"}
	client:=mcp.NewClient(&mcp.Implementation{Name:"independent-test",Version:"1.0.0"},nil)
	ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel()
	session,err:=client.Connect(ctx,&mcp.StreamableClientTransport{Endpoint:s.URL+"/mcp",DisableStandaloneSSE:true},opts)
	if err!=nil{t.Fatal(err)}
	t.Cleanup(func(){_ = session.Close()})
	return s,session
}

func mcpCall(t *testing.T,c *mcp.ClientSession,name string,args map[string]any,wantError bool)[]byte{
	t.Helper()
	ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel()
	res,err:=c.CallTool(ctx,&mcp.CallToolParams{Name:name,Arguments:args})
	if err!=nil{t.Fatalf("%s: %v",name,err)}
	if res.IsError!=wantError{t.Fatalf("%s: isError=%v expected %v; %+v",name,res.IsError,wantError,res.Content)}
	if len(res.Content)==0{t.Fatalf("%s: missing text fallback",name)}
	if wantError{return nil}
	raw,err:=json.Marshal(res.StructuredContent);if err!=nil||string(raw)=="null"{t.Fatalf("%s: structured output %s %v",name,raw,err)}
	return raw
}

func decodeMCP[T any](t *testing.T,raw []byte)T{
	t.Helper();var v T
	if err:=json.Unmarshal(raw,&v);err!=nil{t.Fatal(err)}
	return v
}

func TestMCPDelegationAndStateAcrossRestart(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);first,second:=registerPair(t,b)
	ctx:=context.Background()
	if _,err:=b.SetCollection(ctx,"group","engineering",[]string{"tester-1","tester-2"});err!=nil{t.Fatal(err)}
	if _,err:=b.SetCollection(ctx,"pool","testers",[]string{"tester-1","tester-2"});err!=nil{t.Fatal(err)}
	s,c:=mcpSession(t,b,false)
	list,err:=c.ListTools(ctx,&mcp.ListToolsParams{});if err!=nil{t.Fatal(err)}
	want:=map[string]bool{"discover_agents":true,"list_collections":true,"get_agent_health":true,"send_message":false,"broadcast":false,"dispatch":false,"get_delivery":true,"list_state":true,"get_state":true,"put_state":false,"ask":false,"note":false,"read_notes":true,"resolve_request":false}
	if len(list.Tools)!=len(want){t.Fatalf("unexpected tool count %d",len(list.Tools))}
	for _,tool:=range list.Tools{
		read,ok:=want[tool.Name]
		if !ok||tool.InputSchema==nil||tool.Annotations==nil||tool.Annotations.ReadOnlyHint!=read{t.Fatalf("unexpected tool: %+v",tool)}
	}
	agents:=decodeMCP[struct{Agents []core.Agent}](t,mcpCall(t,c,"discover_agents",map[string]any{"skill":"architecture","group":"engineering"},false))
	if len(agents.Agents)!=2{t.Fatalf("discovery %+v",agents)}
	none:=decodeMCP[struct{Agents []core.Agent;Registered int;Hint string}](t,mcpCall(t,c,"discover_agents",map[string]any{"skill":"nonexistent"},false))
	if len(none.Agents)!=0||none.Registered<2||!strings.Contains(none.Hint,"without filters"){t.Fatalf("empty filtered discovery lacks hint %+v",none)}
	h:=decodeMCP[core.AgentHealth](t,mcpCall(t,c,"get_agent_health",map[string]any{"id":"tester-1"},false))
	if h.Reachability!="reachable"||h.Busy{t.Fatalf("health %+v",h)}
	pools:=decodeMCP[struct{Collections []core.Collection}](t,mcpCall(t,c,"list_collections",map[string]any{"kind":"pool"},false))
	if len(pools.Collections)!=1||len(pools.Collections[0].Members)!=2{t.Fatalf("pools %+v",pools)}
	mcpCall(t,c,"list_collections",map[string]any{"kind":"bad"},true)
	send:=map[string]any{"target":"tester-1","text":"review fixture","idempotencyKey":"mcp-send"}
	d:=decodeMCP[core.Delivery](t,mcpCall(t,c,"send_message",send,false))
	wait(t,b,d.ID,"completed")
	result:=mcpCall(t,c,"get_delivery",map[string]any{"id":d.ID},false)
	if !strings.Contains(string(result),"SIMULATED"){t.Fatalf("missing downstream result %s",result)}
	dup:=decodeMCP[core.Delivery](t,mcpCall(t,c,"send_message",send,false))
	if dup.ID!=d.ID||first.calls.Load()!=1{t.Fatal("duplicate re-executed")}
	mcpCall(t,c,"send_message",map[string]any{"target":"tester-1","text":"changed","idempotencyKey":"mcp-send"},true)
	mcpCall(t,c,"send_message",map[string]any{"target":"tester-1","text":"no key"},true)
	mcpCall(t,c,"send_message",map[string]any{"target":"missing","text":"bad","idempotencyKey":"bad-target"},true)
	mcpCall(t,c,"get_delivery",map[string]any{"id":"missing"},true)
	bc:=decodeMCP[core.Delivery](t,mcpCall(t,c,"broadcast",map[string]any{"target":"engineering","text":"update","idempotencyKey":"mcp-bc"},false))
	wait(t,b,bc.ID,"completed")
	bc=decodeMCP[core.Delivery](t,mcpCall(t,c,"get_delivery",map[string]any{"id":bc.ID},false))
	if len(bc.Children)!=2||first.calls.Load()!=2||second.calls.Load()!=1{t.Fatalf("broadcast %+v",bc)}
	dp:=decodeMCP[core.Delivery](t,mcpCall(t,c,"dispatch",map[string]any{"target":"testers","text":"verify","idempotencyKey":"mcp-dp"},false))
	wait(t,b,dp.ID,"completed")
	dp=decodeMCP[core.Delivery](t,mcpCall(t,c,"get_delivery",map[string]any{"id":dp.ID},false))
	if dp.AssignedAgent==""||first.calls.Load()+second.calls.Load()!=4{t.Fatalf("dispatch %+v",dp)}
	create:=map[string]any{"namespace":"engineering","key":"context","value":"pending","revision":0}
	st:=decodeMCP[core.StateEntry](t,mcpCall(t,c,"put_state",create,false))
	if st.Revision==0{t.Fatal("missing state revision")}
	mcpCall(t,c,"put_state",create,true)
	mcpCall(t,c,"put_state",map[string]any{"namespace":"engineering","key":"context","value":"missing revision"},true)
	updated:=decodeMCP[core.StateEntry](t,mcpCall(t,c,"put_state",map[string]any{"namespace":"engineering","key":"context","value":"complete","revision":st.Revision},false))
	if updated.Revision<=st.Revision{t.Fatal("state revision did not advance")}
	metadata:=mcpCall(t,c,"list_state",map[string]any{"namespace":"engineering"},false)
	if bytes.Contains(metadata,[]byte("\"value\"")){t.Fatalf("state values in list %s",metadata)}
	_ = c.Close();s.Close();b.Close()
	b2:=open(t,cfg);_,c2:=mcpSession(t,b2,true)
	got:=decodeMCP[core.StateEntry](t,mcpCall(t,c2,"get_state",map[string]any{"namespace":"engineering","key":"context"},false))
	if got.Revision!=updated.Revision||got.Value==nil||*got.Value!="complete"{t.Fatalf("restart state %+v",got)}
	replay:=decodeMCP[core.Delivery](t,mcpCall(t,c2,"send_message",send,false))
	if replay.ID!=d.ID||first.calls.Load()+second.calls.Load()!=4{t.Fatal("restart replayed work")}
}

func TestMCPBoundaryAndDisabled(t *testing.T){
	b:=open(t,config(t))
	disabled:=api.HandlerWithProtocols(b,nil,"",false)
	enabled:=api.HandlerWithProtocols(b,nil,"",true)
	for _,tc:=range []struct{name,peer,host,origin string;handler http.Handler;status int}{
		{"disabled","127.0.0.1:1234","localhost:8080","",disabled,404},
		{"remote","192.0.2.1:1234","localhost:8080","",enabled,403},
		{"host","127.0.0.1:1234","evil.example","",enabled,403},
		{"origin","127.0.0.1:1234","localhost:8080","https://evil.example",enabled,403},
	}{
		t.Run(tc.name,func(t *testing.T){
			req:=httptest.NewRequest("POST","http://localhost:8080/mcp",strings.NewReader("{}"))
			req.RemoteAddr=tc.peer;req.Host=tc.host;if tc.origin!=""{req.Header.Set("Origin",tc.origin)}
			rec:=httptest.NewRecorder();tc.handler.ServeHTTP(rec,req)
			if rec.Code!=tc.status{t.Fatalf("status %d want %d",rec.Code,tc.status)}
		})
	}
	cmd:=daemon.Command();cmd.SetArgs([]string{"--mcp","--dashboard=false","--listen","0.0.0.0:0","--trusted-network"})
	if err:=cmd.Execute();err==nil||!strings.Contains(err.Error(),"MCP requires"){t.Fatalf("non-loopback daemon should fail closed: %v",err)}
}
