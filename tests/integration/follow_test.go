package integration_test

import (
 "context"
 "encoding/json"
 "errors"
 "iter"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "github.com/a2aproject/a2a-go/v2/a2a"
 "github.com/a2aproject/a2a-go/v2/a2asrv"
 "github.com/tbrandenburg/agentiplex/core"
 "github.com/tbrandenburg/agentiplex/internal/api"
 "github.com/tbrandenburg/agentiplex/internal/security"
)

// A controlled real A2A SDK agent; each turn has its own release signal.
// No model, fake CLI, fake broker or inferred acceptance is involved.
func followAgent(t *testing.T, turns int)(*taskAgent,[]chan struct{}){
 t.Helper()
 a:=newTaskAgent(t)
 release:=make([]chan struct{},turns)
 for i:=range release{release[i]=make(chan struct{})}
 t.Cleanup(func(){for _,ch:=range release{releaseFollow(ch)}})
 executor:=a2asrv.AgentExecutorFunc(func(ctx context.Context,ec *a2asrv.ExecutorContext)iter.Seq2[a2a.Event,error]{
  return func(yield func(a2a.Event,error)bool){
   n:=int(a.sends.Add(1))-1
   if n>=len(release){yield(a2a.NewStatusUpdateEvent(ec,a2a.TaskStateFailed,nil),nil);return}
   if !yield(&a2a.Task{ID:ec.TaskID,ContextID:ec.ContextID,Status:a2a.TaskStatus{State:a2a.TaskStateWorking}},nil){return}
   select{case <-release[n]:case <-ctx.Done():return}
   yield(a2a.NewStatusUpdateEvent(ec,a2a.TaskStateCompleted,nil),nil)
  }
 })
 mux:=http.NewServeMux()
 mux.Handle("/invoke",a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))
 mux.Handle(a2asrv.WellKnownAgentCardPath,a2asrv.NewStaticAgentCardHandler(a.card()))
 a.mux.Store(mux)
 return a,release
}

func releaseFollow(ch chan struct{}){select{case <-ch:default:close(ch)}}

func awaitFollowTurn(t *testing.T,b *core.Bus,id string,n int)core.Delivery{
 t.Helper()
 deadline:=time.Now().Add(25*time.Second)
 for time.Now().Before(deadline){
  d,e:=b.Delivery(id)
  if e==nil&&d.Follow!=nil&&len(d.Follow.Turns)>=n{
   return wait(t,b,d.Follow.Turns[n-1],"accepted")
  }
  if e==nil&&d.State!="continuing"{t.Fatalf("request ended before turn %d: %+v",n,d)}
  time.Sleep(50*time.Millisecond)
 }
 t.Fatalf("no activation %d",n);return core.Delivery{}
}

func TestUntilDoneContinuationRestartAndResolution(t *testing.T){
 cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=time.Second
 b:=open(t,cfg)
 ctx:=context.Background()
 lead,release:=followAgent(t,2)
 worker:=newTaskAgent(t)
 if _,err:=b.Register(ctx,"lead",lead.srv.URL);err!=nil{t.Fatal(err)}
 if _,err:=b.Register(ctx,"expert",worker.srv.URL);err!=nil{t.Fatal(err)}
 srv:=httptest.NewServer(api.Handler(b));defer srv.Close()
 path:=filepath.Join(t.TempDir(),"objective.md")
 const objective="Review the fixture and resolve all findings.\n"
 if err:=os.WriteFile(path,[]byte(objective),0600);err!=nil{t.Fatal(err)}
 out,err:=observeCLI(ctx,srv.URL,"send","lead","--file",path,"--idempotency-key","follow","--until-done","--max-turns","3","--timeout","90s")
 var root core.Delivery
 if err!=nil||json.Unmarshal([]byte(out),&root)!=nil||root.ID==""||root.Text!=objective||root.State!="continuing"{t.Fatalf("native submit: %s %v",out,err)}
 first:=awaitFollowTurn(t,b,root.ID,1)
 for i:=0;i<3;i++{if _,err=b.NudgeFollow(ctx,root.ID);err!=nil{t.Fatal(err)}}
 if _,err=b.Send(ctx,"lead","competing turn","bad");!errors.Is(err,core.ErrConflict){t.Fatalf("competing activation: %v",err)}
 if _,err=b.SendUntilDone(ctx,"lead","other","bad2",core.FollowOptions{});!errors.Is(err,core.ErrConflict){t.Fatalf("parallel request: %v",err)}
 wctx,cancel:=context.WithTimeout(ctx,150*time.Millisecond)
 if _,err=b.WaitDelivery(wctx,root.ID);err==nil{t.Fatal("unresolved request returned from wait")}
 cancel()
 srv.Close();b.Close()
 b2:=open(t,cfg)
 options:=core.FollowOptions{MaxTurns:3,TimeoutSeconds:90}
 attached,err:=b2.SendUntilDone(ctx,"lead",objective,"follow",options)
 if err!=nil||attached.ID!=root.ID||len(attached.Follow.Turns)!=1{t.Fatalf("reattach %+v %v",attached,err)}
 if _,err=b2.SendUntilDone(ctx,"lead",objective,"follow",core.FollowOptions{MaxTurns:4,TimeoutSeconds:90});!errors.Is(err,core.ErrConflict){t.Fatalf("changed options: %v",err)}
 if _,err=b2.Send(ctx,"lead",objective,"follow");!errors.Is(err,core.ErrConflict){t.Fatalf("changed lifecycle: %v",err)}
 releaseFollow(release[0])
 second:=awaitFollowTurn(t,b2,root.ID,2)
 if first.ID==second.ID||lead.sends.Load()!=2{t.Fatal("activation replay or missing continuation")}
 if got,_:=b2.Delivery(first.ID);got.State!="completed"{t.Fatal("first activation not completed")}
 wrong:=security.WithPrincipal(ctx,security.Principal{Role:security.RoleAgent,AgentID:"expert"})
 if _,err=b2.ResolveFollow(wrong,root.ID,second.ID,"done","wrong owner");!errors.Is(err,core.ErrNotFound){t.Fatalf("foreign resolution: %v",err)}
 if _,err=b2.ResolveFollow(ctx,root.ID,first.ID,"done","stale activation");!errors.Is(err,core.ErrConflict){t.Fatalf("stale resolution: %v",err)}
 work,err:=b2.Send(ctx,"expert","independent verification","worker");if err!=nil{t.Fatal(err)}
 wait(t,b2,work.ID,"accepted")
 _,m:=mcpSession(t,b2,false)
 args:=map[string]any{"requestId":root.ID,"activationId":second.ID,"outcome":"done","result":"Independent finding accepted"}
 mcpCall(t,m,"resolve_request",args,false)
 mcpCall(t,m,"resolve_request",args,false) // idempotent proposal
 releaseFollow(release[1])
 wait(t,b2,second.ID,"completed")
 got,err:=b2.Delivery(root.ID)
 if err!=nil||got.State!="continuing"||got.Follow.Resolution==nil{t.Fatalf("premature closure %+v %v",got,err)}
 releaseFollow(worker.release)
 done:=wait(t,b2,root.ID,"completed")
 if len(done.Follow.Turns)!=2||!strings.Contains(string(done.Result),"Independent finding accepted"){t.Fatalf("resolution %+v",done)}
 again,err:=b2.SendUntilDone(ctx,"lead",objective,"follow",options)
 if err!=nil||again.ID!=done.ID||again.State!="completed"{t.Fatalf("closed reattach %+v %v",again,err)}
 if _,err=b2.NudgeFollow(ctx,root.ID);err!=nil{t.Fatal(err)}
 srv2:=httptest.NewServer(api.Handler(b2));defer srv2.Close()
 out,err=observeCLI(ctx,srv2.URL,"messages","wait",root.ID)
 if err!=nil||!strings.Contains(out,"Independent finding accepted")||lead.sends.Load()!=2{t.Fatalf("final wait %s %v sends=%d",out,err,lead.sends.Load())}
}

func TestUntilDoneBudgetAndUncertain(t *testing.T){
 t.Run("turn budget",func(t *testing.T){
  b:=open(t,config(t));a:=&agentService{};a.start(t,false);defer a.stop()
  ctx:=context.Background()
  if _,err:=b.Register(ctx,"lead",a.URL());err!=nil{t.Fatal(err)}
  d,err:=b.SendUntilDone(ctx,"lead","never resolves","limit",core.FollowOptions{MaxTurns:2,TimeoutSeconds:60});if err!=nil{t.Fatal(err)}
  done:=wait(t,b,d.ID,"input-required")
  if len(done.Follow.Turns)!=2||a.calls.Load()!=2||!strings.Contains(done.Detail,"turn budget"){t.Fatalf("wrong budget result %+v",done)}
 })
 t.Run("deadline does not cancel admitted work",func(t *testing.T){
  cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=time.Second
  b:=open(t,cfg);a:=newTaskAgent(t);ctx:=context.Background()
  if _,err:=b.Register(ctx,"lead",a.srv.URL);err!=nil{t.Fatal(err)}
  d,err:=b.SendUntilDone(ctx,"lead","wait","deadline",core.FollowOptions{MaxTurns:5,TimeoutSeconds:5});if err!=nil{t.Fatal(err)}
  first:=awaitFollowTurn(t,b,d.ID,1)
  done:=wait(t,b,d.ID,"input-required")
  if len(done.Follow.Turns)!=1||!strings.Contains(done.Detail,"deadline"){t.Fatalf("wrong deadline %+v",done)}
  if cur,_:=b.Delivery(first.ID);cur.State!="accepted"{t.Fatal("deadline canceled admitted work")}
  releaseFollow(a.release);wait(t,b,first.ID,"completed")
  if cur,_:=b.NudgeFollow(ctx,d.ID);cur.State!="input-required"||a.sends.Load()!=1{t.Fatal("paused work was replayed")}
 })
 t.Run("uncertain",func(t *testing.T){
  b:=open(t,config(t));a:=&agentService{};a.start(t,true);defer a.stop()
  ctx:=context.Background()
  if _,err:=b.Register(ctx,"lead",a.URL());err!=nil{t.Fatal(err)}
  d,err:=b.SendUntilDone(ctx,"lead","side effect","uncertain",core.FollowOptions{MaxTurns:5,TimeoutSeconds:60,NudgeSeconds:1});if err!=nil{t.Fatal(err)}
  done:=wait(t,b,d.ID,"uncertain")
  if _,err=b.NudgeFollow(ctx,d.ID);err!=nil{t.Fatal(err)}
  time.Sleep(1200*time.Millisecond)
  if len(done.Follow.Turns)!=1||a.calls.Load()!=1{t.Fatal("uncertain work replayed")}
 })
}

func TestUntilDoneWatchdogCoalescesWithContinuation(t *testing.T){
 cfg:=config(t);cfg.HealthInterval=0;cfg.TaskPollInterval=time.Second
 b:=open(t,cfg);ctx:=context.Background()
 lead,release:=followAgent(t,2);worker:=newTaskAgent(t)
 if _,err:=b.Register(ctx,"lead",lead.srv.URL);err!=nil{t.Fatal(err)}
 if _,err:=b.Register(ctx,"expert",worker.srv.URL);err!=nil{t.Fatal(err)}
 root,err:=b.SendUntilDone(ctx,"lead","review","watchdog",core.FollowOptions{MaxTurns:3,TimeoutSeconds:60,NudgeSeconds:1});if err!=nil{t.Fatal(err)}
 first:=awaitFollowTurn(t,b,root.ID,1)
 work,err:=b.Send(ctx,"expert","verify","watchdog-worker");if err!=nil{t.Fatal(err)}
 wait(t,b,work.ID,"accepted")
 releaseFollow(release[0]);wait(t,b,first.ID,"completed")
 time.Sleep(1500*time.Millisecond)
 if d,e:=b.NudgeFollow(ctx,root.ID);e!=nil||len(d.Follow.Turns)!=1||d.State!="continuing"{t.Fatalf("watchdog/manual nudge ignored outstanding work %+v %v",d,e)}
 releaseFollow(worker.release)
 second:=awaitFollowTurn(t,b,root.ID,2)
 if !strings.Contains(second.Text,"inactivity watchdog"){t.Fatalf("expected overdue watchdog activation: %s",second.Text)}
 for i:=0;i<5;i++{if _,err=b.NudgeFollow(ctx,root.ID);err!=nil{t.Fatal(err)}}
 if _,err=b.ResolveFollow(ctx,root.ID,second.ID,"blocked","Needs operator decision");err!=nil{t.Fatal(err)}
 releaseFollow(release[1])
 done:=wait(t,b,root.ID,"input-required")
 if len(done.Follow.Turns)!=2||lead.sends.Load()!=2||!strings.Contains(done.Detail,"Needs operator decision"){t.Fatalf("watchdog duplicated work %+v",done)}
}
