package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex"
	"github.com/tbrandenburg/agentiplex/internal/api"
	"github.com/tbrandenburg/agentiplex/internal/cli"
	"github.com/tbrandenburg/agentiplex/internal/simagent"
)

func TestRegistrationRefreshAndRemove(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);ctx:=context.Background()
	var mode atomic.Int32
	s:=httptest.NewUnstartedServer(nil)
	base:="http://"+s.Listener.Addr().String()
	old:=simagent.Handler(base,"original","architecture",nil)
	updated:=simagent.Handler(base,"updated","testing",nil)
	changed:=simagent.Handler(base+"/different","wrong","quality",nil)
	s.Config.Handler=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		switch mode.Load(){case 0:old.ServeHTTP(w,r);case 1:updated.ServeHTTP(w,r);case 2:changed.ServeHTTP(w,r);default:http.Error(w,"unavailable",503)}
	})
	s.Start();defer s.Close()
	reg,err:=b.Register(ctx,"architect",s.URL);if err!=nil||reg.Reachability!="reachable"||reg.ObservedAt==nil{t.Fatalf("registration: %+v %v",reg,err)}
	mode.Store(1)
	h,err:=b.ProbeAgent(ctx,"architect");if err!=nil||h.Reachability!="reachable"||h.Busy{t.Fatalf("probe: %+v %v",h,err)}
	got,err:=b.Agent("architect");if err!=nil||got.Card.Name!="original"{t.Fatal("probe changed card")}
	refreshed,err:=b.Refresh(ctx,"architect");if err!=nil||refreshed.Card.Name!="updated"||refreshed.Endpoint!=reg.Endpoint||!refreshed.UpdatedAt.After(reg.UpdatedAt){t.Fatalf("refresh: %+v %v",refreshed,err)}
	mode.Store(2)
	if _,err=b.Refresh(ctx,"architect");!errors.Is(err,agentiplex.ErrConflict){t.Fatalf("endpoint change: %v",err)}
	got,err=b.Agent("architect");if err!=nil||got.Card.Name!="updated"||got.Endpoint!=reg.Endpoint||got.Reachability!="unreachable"||!got.UpdatedAt.Equal(refreshed.UpdatedAt){t.Fatalf("invalid card replaced valid snapshot: %+v %v",got,err)}
	mode.Store(3)
	if h,err=b.ProbeAgent(ctx,"architect");err!=nil||h.Reachability!="unreachable"{t.Fatalf("offline probe: %+v %v",h,err)}
	if agents,err:=b.Discover("testing","","");err!=nil||len(agents)!=1{t.Fatalf("offline discovery: %+v %v",agents,err)}
	if raw,err:=b.Catalog();err!=nil||!strings.Contains(string(raw),"updated"){t.Fatalf("offline catalog: %s %v",raw,err)}
	b.Close()
	b2:=open(t,cfg)
	if h,err=b2.AgentHealth("architect");err!=nil||h.Reachability!="unreachable"||h.ObservedAt==nil{t.Fatalf("observation lost: %+v %v",h,err)}
	if _,err=b2.PutState(ctx,"architect","context","retain me",0);err!=nil{t.Fatal(err)}
	if err=b2.RemoveAgent(ctx,"architect");err!=nil{t.Fatal(err)}
	if _,err=b2.Agent("architect");!errors.Is(err,agentiplex.ErrNotFound){t.Fatalf("removed record: %v",err)}
	if _,err=b2.Send(ctx,"architect","new work","after-removal");!errors.Is(err,agentiplex.ErrNotFound){t.Fatalf("send to removed agent: %v",err)}
	if raw,err:=b2.Catalog();err!=nil||strings.Contains(string(raw),"updated"){t.Fatalf("removed catalog: %s %v",raw,err)}
	if st,err:=b2.GetState("architect","context");err!=nil||st.Value==nil||*st.Value!="retain me"{t.Fatalf("state removed implicitly: %+v %v",st,err)}
	mode.Store(1)
	if _,err=b2.Register(ctx,"architect",s.URL);err!=nil{t.Fatalf("re-register: %v",err)}
}

func TestRemovalGuardsPendingSnapshots(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);first,second:=registerPair(t,b);ctx:=context.Background()
	if _,err:=b.SetCollection(ctx,"pool","testers",[]string{"tester-1","tester-2"});err!=nil{t.Fatal(err)}
	if err:=b.RemoveAgent(ctx,"tester-1");!errors.Is(err,agentiplex.ErrConflict){t.Fatalf("membership guard: %v",err)}
	first.stop();second.stop()
	pending,err:=b.Dispatch(ctx,"testers","pending snapshot","guard-pool");if err!=nil{t.Fatal(err)}
	if _,err=b.SetCollection(ctx,"pool","testers",[]string{"tester-2"});err!=nil{t.Fatal(err)}
	if err=b.RemoveAgent(ctx,"tester-1");!errors.Is(err,agentiplex.ErrConflict){t.Fatalf("pool snapshot guard: %v",err)}
	first.start(t,false)
	wait(t,b,pending.ID,"completed")
	first.stop()
	d,err:=b.Send(ctx,"tester-1","pending direct","guard-direct");if err!=nil{t.Fatal(err)}
	if err=b.RemoveAgent(ctx,"tester-1");!errors.Is(err,agentiplex.ErrConflict){t.Fatalf("direct guard: %v",err)}
	first.start(t,false);wait(t,b,d.ID,"completed")
	if err=b.RemoveAgent(ctx,"tester-1");err!=nil{t.Fatal(err)}
	if old,err:=b.Delivery(d.ID);err!=nil||old.State!="completed"{t.Fatalf("history lost: %+v %v",old,err)}
}

func TestPeriodicReachability(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=time.Second
	b:=open(t,cfg);a:=&agentService{};a.start(t,false);defer a.stop()
	if _,err:=b.Register(context.Background(),"architect",a.URL());err!=nil{t.Fatal(err)}
	observe:=func(want string){
		t.Helper()
		deadline:=time.Now().Add(12*time.Second)
		for time.Now().Before(deadline){h,err:=b.AgentHealth("architect");if err==nil&&h.Reachability==want&&h.ObservedAt!=nil{return};time.Sleep(100*time.Millisecond)}
		t.Fatalf("periodic observer did not reach %s",want)
	}
	a.stop();observe("unreachable")
	a.start(t,false);observe("reachable")
}

func TestStateCASWatchRestartAndLimits(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
	initial,err:=b.PutState(ctx,"architect","context","pending",0);if err!=nil||initial.Revision==0{t.Fatalf("create: %+v %v",initial,err)}
	if _,err=b.PutState(ctx,"architect","context","overwrite",0);!errors.Is(err,agentiplex.ErrConflict){t.Fatalf("create collision: %v",err)}
	other,err:=b.PutState(ctx,"engineering","context","separate",0);if err!=nil{t.Fatal(err)}
	changes:=make(chan agentiplex.StateChange,32);watchDone:=make(chan error,1)
	watchCtx,stop:=context.WithCancel(ctx)
	defer stop()
	go func(){watchDone<-b.WatchState(watchCtx,"architect",func(c agentiplex.StateChange)error{select{case changes<-c:return nil;case<-watchCtx.Done():return watchCtx.Err()}})}()
	read:=func()agentiplex.StateChange{t.Helper();select{case c:=<-changes:return c;case e:=<-watchDone:t.Fatalf("watch exited: %v",e);case<-time.After(10*time.Second):t.Fatal("watch timeout")};return agentiplex.StateChange{}}
	if c:=read();c.Operation!="put"||c.Entry==nil||c.Entry.Revision!=initial.Revision{t.Fatalf("initial snapshot: %+v",c)}
	if c:=read();c.Operation!="ready"{t.Fatalf("ready marker: %+v",c)}
	errs:=make(chan error,2)
	for _,value:=range []string{"first","second"}{go func(){_,err:=b.PutState(ctx,"architect","context",value,initial.Revision);errs<-err}()}
	success,conflict:=0,0
	for i:=0;i<2;i++{err:=<-errs;if err==nil{success++}else if errors.Is(err,agentiplex.ErrConflict){conflict++}else{t.Fatal(err)}}
	if success!=1||conflict!=1{t.Fatalf("CAS: %d %d",success,conflict)}
	current,err:=b.GetState("architect","context");if err!=nil||current.Revision<=initial.Revision{t.Fatalf("current: %+v %v",current,err)}
	if c:=read();c.Operation!="put"||c.Entry==nil||c.Entry.Revision!=current.Revision{t.Fatalf("update watch: %+v",c)}
	if err=b.DeleteState(ctx,"architect","context",initial.Revision);!errors.Is(err,agentiplex.ErrConflict){t.Fatalf("stale delete: %v",err)}
	if err=b.DeleteState(ctx,"architect","context",current.Revision);err!=nil{t.Fatal(err)}
	if c:=read();c.Operation!="delete"||c.Entry==nil||c.Entry.Value!=nil||c.Entry.Revision<=current.Revision{t.Fatalf("delete watch: %+v",c)}
	if _,err=b.GetState("architect","context");!errors.Is(err,agentiplex.ErrNotFound){t.Fatalf("deleted get: %v",err)}
	recreated,err:=b.PutState(ctx,"architect","context","",0);if err!=nil||recreated.Value==nil||*recreated.Value!=""||recreated.Revision<=current.Revision{t.Fatalf("recreate: %+v %v",recreated,err)}
	if _,err=b.PutState(ctx,"architect","large",strings.Repeat("x",agentiplex.MaxStateValueBytes),0);err!=nil{t.Fatalf("max value: %v",err)}
	if _,err=b.PutState(ctx,"architect","oversize",strings.Repeat("x",agentiplex.MaxStateValueBytes+1),0);!errors.Is(err,agentiplex.ErrInvalid){t.Fatalf("oversize: %v",err)}
	if _,err=b.PutState(ctx,"architect.*","context","bad",0);!errors.Is(err,agentiplex.ErrInvalid){t.Fatalf("invalid namespace: %v",err)}
	list,err:=b.ListState("engineering");if err!=nil||len(list)!=1||list[0].Value!=nil||list[0].Revision!=other.Revision{t.Fatalf("metadata isolation: %+v %v",list,err)}
	stop();select{case<-watchDone:case<-time.After(5*time.Second):t.Fatal("watch cancellation blocked")}
	b.Close()
	b2:=open(t,cfg)
	got,err:=b2.GetState("architect","context");if err!=nil||got.Revision!=recreated.Revision||got.Value==nil||*got.Value!=""{t.Fatalf("restart: %+v %v",got,err)}
	got,err=b2.GetState("engineering","context");if err!=nil||got.Value==nil||*got.Value!="separate"{t.Fatalf("namespace restart: %+v %v",got,err)}
	events,err:=b2.Events(0);if err!=nil{t.Fatal(err)}
	encoded,_:=json.Marshal(events);if strings.Contains(string(encoded),"separate")||!strings.Contains(string(encoded),"state.updated"){t.Fatal("event metadata invalid")}
}

func TestStateCLIAndHTTPWatch(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);s:=httptest.NewServer(api.Handler(b));defer s.Close()
	run:=func(args ...string)(string,error){cmd:=cli.Command();var out bytes.Buffer;cmd.SetOut(&out);cmd.SetErr(&out);cmd.SetArgs(append([]string{"--server",s.URL},args...));err:=cmd.Execute();return out.String(),err}
	raw,err:=run("state","put","team","requirement","REQ-42");if err!=nil{t.Fatalf("CLI put: %v %s",err,raw)}
	var entry agentiplex.StateEntry;if err=json.Unmarshal([]byte(raw),&entry);err!=nil||entry.Revision==0{t.Fatalf("CLI receipt: %v %s",err,raw)}
	if _,err=run("state","put","team","requirement","conflict");err==nil{t.Fatal("CLI overwrote state")}
	ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel()
	req,err:=http.NewRequestWithContext(ctx,"GET",s.URL+"/api/v1/state-watch/team",nil);if err!=nil{t.Fatal(err)}
	resp,err:=http.DefaultClient.Do(req);if err!=nil{t.Fatal(err)};defer resp.Body.Close()
	if resp.StatusCode!=200||resp.Header.Get("Content-Type")!="text/event-stream"{t.Fatalf("watch HTTP %d",resp.StatusCode)}
	scan:=bufio.NewScanner(resp.Body)
	next:=func()agentiplex.StateChange{t.Helper();for scan.Scan(){if strings.HasPrefix(scan.Text(),"data: "){var c agentiplex.StateChange;if err:=json.Unmarshal([]byte(strings.TrimPrefix(scan.Text(),"data: ")),&c);err!=nil{t.Fatal(err)};return c}};t.Fatalf("watch ended: %v",scan.Err());return agentiplex.StateChange{}}
	if c:=next();c.Operation!="put"||c.Entry==nil||c.Entry.Revision!=entry.Revision{t.Fatalf("SSE snapshot: %+v",c)}
	if c:=next();c.Operation!="ready"{t.Fatalf("SSE ready: %+v",c)}
	if _,err=run("state","delete","team","requirement","--revision",fmt.Sprint(entry.Revision));err!=nil{t.Fatal(err)}
	if c:=next();c.Operation!="delete"{t.Fatalf("SSE deletion: %+v",c)}
	if _,err=run("state","get","team","requirement");err==nil{t.Fatal("deleted CLI value found")}
}
