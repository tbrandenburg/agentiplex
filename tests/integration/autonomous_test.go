package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tbrandenburg/agentiplex/core"
	"github.com/tbrandenburg/agentiplex/internal/api"
)

// A provider-free protocol fixture: an independently hosted, accepted A2A
// task acts as an MCP caller, delegates work and returns observed evidence.
// This tests the necessary reentrant transport path, not model reasoning.
func TestActiveA2AAgentDelegatesThroughMCP(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg)
	busHTTP:=httptest.NewServer(api.HandlerWithProtocols(b,nil,"",true));defer busHTTP.Close()
	worker:=&agentService{};worker.start(t,false);defer worker.stop()
	ctx:=context.Background()
	if _,err:=b.Register(ctx,"architect",worker.URL());err!=nil{t.Fatal(err)}
	coordinator:=httptest.NewUnstartedServer(nil)
	base:="http://"+coordinator.Listener.Addr().String()
	card:=&a2a.AgentCard{Name:"Fixture coordinator",Description:"Provider-free protocol fixture",Version:"1",
		SupportedInterfaces:[]*a2a.AgentInterface{a2a.NewAgentInterface(base+"/invoke",a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:[]string{"text/plain"},DefaultOutputModes:[]string{"text/plain"},
		Skills:[]a2a.AgentSkill{{ID:"planning",Name:"Planning",Description:"Delegation fixture",Tags:[]string{"simulated"}}}}
	delegate:=func(ctx context.Context)(string,error){
		ctx,cancel:=context.WithTimeout(ctx,20*time.Second);defer cancel()
		cl:=mcp.NewClient(&mcp.Implementation{Name:"fixture-coordinator",Version:"1"},nil)
		session,err:=cl.Connect(ctx,&mcp.StreamableClientTransport{Endpoint:busHTTP.URL+"/mcp",DisableStandaloneSSE:true},nil)
		if err!=nil{return "",err};defer session.Close()
		call:=func(name string,args map[string]any,out any)error{
			res,err:=session.CallTool(ctx,&mcp.CallToolParams{Name:name,Arguments:args})
			if err!=nil{return err};if res.IsError{return fmt.Errorf("%s returned tool error",name)}
			raw,err:=json.Marshal(res.StructuredContent);if err!=nil{return err};return json.Unmarshal(raw,out)
		}
		var found struct{Agents []core.Agent}
		if err=call("discover_agents",map[string]any{"skill":"architecture"},&found);err!=nil||len(found.Agents)!=1{return "",fmt.Errorf("discover worker: %v",err)}
		var d core.Delivery
		if err=call("send_message",map[string]any{"target":found.Agents[0].ID,"text":"fixture review","idempotencyKey":"nested-fixture"},&d);err!=nil{return "",err}
		id:=d.ID
		for{
			if err=call("get_delivery",map[string]any{"id":id},&d);err!=nil{return "",err}
			if d.State=="completed"{break}
			if d.State=="failed"||d.State=="uncertain"||d.State=="input-required"||d.State=="auth-required"{return "",fmt.Errorf("nested delivery %s",d.State)}
			select{case<-ctx.Done():return "",ctx.Err();case<-time.After(100*time.Millisecond):}
		}
		if !strings.Contains(string(d.Result),"SIMULATED architect"){return "",fmt.Errorf("missing real transport fixture response")}
		var entry core.StateEntry
		if err=call("put_state",map[string]any{"namespace":"review","key":"pm","value":id,"revision":0},&entry);err!=nil{return "",err}
		return id,nil
	}
	executor:=a2asrv.AgentExecutorFunc(func(ctx context.Context,ec *a2asrv.ExecutorContext)iter.Seq2[a2a.Event,error]{
		return func(yield func(a2a.Event,error)bool){
			if !yield(&a2a.Task{ID:ec.TaskID,ContextID:ec.ContextID,Status:a2a.TaskStatus{State:a2a.TaskStateWorking}},nil){return}
			id,err:=delegate(ctx)
			if err!=nil{yield(a2a.NewStatusUpdateEvent(ec,a2a.TaskStateFailed,a2a.NewMessage(a2a.MessageRoleAgent,a2a.NewTextPart(err.Error()))),nil);return}
			if !yield(a2a.NewArtifactEvent(ec,a2a.NewTextPart("provider-free evidence "+id)),nil){return}
			yield(a2a.NewStatusUpdateEvent(ec,a2a.TaskStateCompleted,nil),nil)
		}
	})
	mux:=http.NewServeMux()
	mux.Handle("/invoke",a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))
	mux.Handle(a2asrv.WellKnownAgentCardPath,a2asrv.NewStaticAgentCardHandler(card))
	coordinator.Config.Handler=mux;coordinator.Start();defer coordinator.Close()
	if _,err:=b.Register(ctx,"pm",coordinator.URL);err!=nil{t.Fatal(err)}
	root,err:=b.Send(ctx,"pm","one objective","nested-root");if err!=nil{t.Fatal(err)}
	done:=wait(t,b,root.ID,"completed")
	state,err:=b.GetState("review","pm")
	if err!=nil||state.Value==nil||!strings.Contains(string(done.Result),*state.Value){t.Fatalf("missing nested evidence: %+v %v",state,err)}
	child,err:=b.Delivery(*state.Value)
	if err!=nil||child.State!="completed"||child.Target!="architect"||worker.calls.Load()!=1{t.Fatalf("nested work: %+v %v",child,err)}
}
