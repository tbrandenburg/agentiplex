package integration_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex/core"
)

func kinds(t *testing.T,b *core.Bus,kind string)int{
	t.Helper()
	ev,err:=b.Events(0);if err!=nil{t.Fatal(err)}
	n:=0;for _,e:=range ev{if e.Kind==kind{n++}}
	return n
}

func TestStaticRegistryStartupSemantics(t *testing.T){
	ctx:=context.Background()
	cfg:=config(t);cfg.HealthInterval=0
	a,b2,c:=&agentService{},&agentService{},&agentService{}
	a.start(t,false);b2.start(t,false);defer a.stop();defer b2.stop();defer c.stop()
	reg:=core.Registry{
		Agents:map[string]core.AgentSpec{"alpha":{URL:a.URL()},"beta":{URL:b2.URL()}},
		Groups:map[string][]string{"team":{"alpha","beta"}},Pools:map[string][]string{"rev":{"beta","alpha"}}}

	// Legacy path: no registry means nothing is applied.
	bus:=open(t,cfg)
	if err:=bus.ApplyRegistry(ctx,core.Registry{});err!=nil{t.Fatal(err)}
	if ag,_:=bus.Agents("","");len(ag)!=0{t.Fatal("empty registry registered agents")}

	if err:=bus.ApplyRegistry(ctx,reg);err!=nil{t.Fatal(err)}
	ag,_:=bus.Agents("","")
	if len(ag)!=2||ag[0].Card.Name!="architect"{t.Fatalf("agents: %+v",ag)}
	if g,err:=bus.Collection("group","team");err!=nil||!slices.Equal(g.Members,[]string{"alpha","beta"}){t.Fatalf("group: %+v %v",g,err)}
	if p,err:=bus.Collection("pool","rev");err!=nil||!slices.Equal(p.Members,[]string{"alpha","beta"}){t.Fatalf("pool: %+v %v",p,err)}
	old,err:=bus.Broadcast(ctx,"team","hello","k1");if err!=nil{t.Fatal(err)}
	wait(t,bus,old.ID,"completed")

	// Repeated apply and restart: no duplicate records or events.
	reg1,reg2,reg3:=kinds(t,bus,"agent.registered"),kinds(t,bus,"group.configured"),kinds(t,bus,"pool.configured")
	if reg1!=2||reg2!=1||reg3!=1{t.Fatalf("events %d %d %d",reg1,reg2,reg3)}
	bus.Close()
	bus=open(t,cfg)
	if err=bus.ApplyRegistry(ctx,reg);err!=nil{t.Fatal(err)}
	if kinds(t,bus,"agent.registered")!=2||kinds(t,bus,"group.configured")!=1||kinds(t,bus,"pool.configured")!=1{t.Fatal("restart duplicated records/events")}

	// Existing agent temporarily offline: retained, card reused.
	bus.Close();a.stop()
	bus=open(t,cfg)
	if err=bus.ApplyRegistry(ctx,reg);err!=nil{t.Fatalf("offline existing agent: %v",err)}
	if got,err:=bus.Agent("alpha");err!=nil||got.Card.Name!="architect"{t.Fatalf("snapshot lost: %+v %v",got,err)}

	// Queued work for the offline agent survives restart and runs once on return.
	q,err:=bus.Send(ctx,"alpha","queued while offline","kq");if err!=nil{t.Fatal(err)}
	bus.Close()
	bus=open(t,cfg)
	if err=bus.ApplyRegistry(ctx,reg);err!=nil{t.Fatal(err)}
	if d,_:=bus.Delivery(q.ID);d.State!="queued"{t.Fatalf("queued work state %s",d.State)}
	a.start(t,false)
	wait(t,bus,q.ID,"completed")
	if n:=a.calls.Load();n!=2{t.Fatalf("alpha invoked %d times, want 2 (broadcast + queued)",n)}

	// Failures validate before any write and never overwrite.
	bad:=core.Registry{Agents:map[string]core.AgentSpec{"alpha":{URL:b2.URL()},"gamma":{URL:b2.URL()}}}
	if err=bus.ApplyRegistry(ctx,bad);err==nil||!strings.Contains(err.Error(),"retarget"){t.Fatalf("url conflict: %v",err)}
	if _,err=bus.Agent("gamma");!errors.Is(err,core.ErrNotFound){t.Fatal("conflicting apply wrote records")}
	if got,_:=bus.Agent("alpha");got.CardURL!=a.URL()+"/.well-known/agent-card.json"{t.Fatalf("alpha retargeted: %s",got.CardURL)}
	unk:=core.Registry{Groups:map[string][]string{"x":{"ghost"}}}
	if err=bus.ApplyRegistry(ctx,unk);err==nil||!strings.Contains(err.Error(),"unknown member"){t.Fatalf("unknown member: %v",err)}
	c.start(t,false);c.stop() // port freed: nothing listens
	start:=time.Now()
	down:=core.Registry{Agents:map[string]core.AgentSpec{"gamma":{URL:c.URL()}}}
	if err=bus.ApplyRegistry(ctx,down);err==nil||!strings.Contains(err.Error(),"start the endpoint first")||time.Since(start)>cfg.Timeout+time.Second{t.Fatalf("unavailable new card: %v after %s",err,time.Since(start))}
	if _,err=bus.Agent("gamma");!errors.Is(err,core.ErrNotFound){t.Fatal("placeholder record created")}

	// Membership change and omission: old snapshot unchanged, new admissions follow, nothing deleted.
	shrink:=core.Registry{Groups:map[string][]string{"team":{"beta"}}}
	if err=bus.ApplyRegistry(ctx,shrink);err!=nil{t.Fatal(err)}
	if kinds(t,bus,"group.configured")!=2{t.Fatal("changed membership not recorded once")}
	if err=bus.ApplyRegistry(ctx,shrink);err!=nil||kinds(t,bus,"group.configured")!=2{t.Fatalf("unchanged collection not a no-op: %v",err)}
	again,_:=bus.Delivery(old.ID)
	if !slices.Equal(again.Members,old.Members)||len(again.Members)!=2{t.Fatalf("old snapshot changed: %v -> %v",old.Members,again.Members)}
	nw,err:=bus.Broadcast(ctx,"team","after","k2");if err!=nil{t.Fatal(err)}
	if !slices.Equal(nw.Members,[]string{"beta"}){t.Fatalf("new admission members %v",nw.Members)}
	if ag,_:=bus.Agents("","");len(ag)!=2{t.Fatal("omitted agent was removed")}
	if p,_:=bus.Collection("pool","rev");len(p.Members)!=2{t.Fatal("omitted pool changed")}

	// Dynamic registration still works alongside (endpoint back up).
	c.start(t,false)
	if _,err=bus.Register(ctx,"dyn",c.URL());err!=nil{t.Fatalf("dynamic register: %v",err)}
}
