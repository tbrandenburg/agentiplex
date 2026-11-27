package agentiplex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex/internal/simagent"
)

func retentionBus(t *testing.T)*Bus{
	t.Helper()
	cfg:=DefaultConfig();cfg.Storage=t.TempDir();cfg.BrokerSync=time.Minute
	b,err:=Open(context.Background(),cfg);if err!=nil{t.Fatal(err)}
	t.Cleanup(b.Close)
	return b
}

func putDelivery(t *testing.T,b *Bus,d Delivery){
	t.Helper()
	raw,_:=json.Marshal(d)
	if _,err:=b.store.Deliveries.Create(d.ID,raw);err!=nil{t.Fatalf("create %s: %v",d.ID,err)}
}

func exists(b *Bus,id string)bool{_,err:=b.rawDelivery(id);return err==nil}

func TestSweepRetentionFreesFullBucketAndKeepsWork(t *testing.T){
	b:=retentionBus(t)
	ctx:=context.Background()
	old:=time.Now().Add(-10*24*time.Hour).UTC()
	big:=json.RawMessage(`"`+strings.Repeat("x",500000)+`"`)
	running:=Delivery{ID:"running",Target:"a",State:"accepted",Endpoint:"http://127.0.0.1:1",UpdatedAt:old}
	putDelivery(t,b,running)
	full:=0
	for i:=0;;i++{
		raw,_:=json.Marshal(Delivery{ID:fmt.Sprintf("old-%d",i),State:"completed",Result:big,UpdatedAt:old})
		if _,err:=b.store.Deliveries.Create(fmt.Sprintf("old-%d",i),raw);err!=nil{t.Logf("full at %d: %v",i,err);break}
		full++
		if i>1000{t.Fatal("bucket never filled")}
	}
	if full<100{t.Fatalf("bucket filled after only %d records",full)}
	before,_:=b.Usage()
	cur,_,_:=read[Delivery](b.store.Deliveries,"running")
	// The background sweep may have removed some records already; only the end state matters.
	if _,err:=b.sweepRetention(ctx,time.Now(),7*24*time.Hour);err!=nil{t.Fatal(err)}
	after,_:=b.Usage()
	if ks,_:=keys(b.store.Deliveries);len(ks)!=1{t.Fatalf("keys left %v, want only the running one",ks)}
	if after.Bytes>=before.Bytes/10{t.Fatalf("bytes not freed: %d -> %d",before.Bytes,after.Bytes)}
	if !exists(b,"running"){t.Fatal("nonterminal record removed")}
	cur.Detail="still polling";cur.UpdatedAt=time.Now().UTC();raw,_:=json.Marshal(cur)
	if _,err:=b.store.Deliveries.Put("running",raw);err!=nil{t.Fatalf("running task update failed: %v",err)}
	putDelivery(t,b,Delivery{ID:"fresh",State:"queued",UpdatedAt:time.Now().UTC()})
}

func TestSweepRetentionSingleLimit(t *testing.T){
	b:=retentionBus(t)
	now:=time.Now().UTC()
	ago:=func(d time.Duration)time.Time{return now.Add(-d)}
	day:=24*time.Hour
	putDelivery(t,b,Delivery{ID:"expired",State:"completed",UpdatedAt:ago(8*day)})
	putDelivery(t,b,Delivery{ID:"expired-failed",State:"failed",UpdatedAt:ago(31*day)})
	putDelivery(t,b,Delivery{ID:"recent",State:"completed",UpdatedAt:ago(day)})
	for _,s:=range []string{"queued","attempting","accepted","preparing"}{putDelivery(t,b,Delivery{ID:"open-"+s,State:s,UpdatedAt:ago(100*day)})}
	if _,err:=b.sweepRetention(context.Background(),now,7*day);err!=nil{t.Fatal(err)}
	for id,want:=range map[string]bool{"expired":false,"expired-failed":false,"recent":true,"open-queued":true,"open-attempting":true,"open-accepted":true,"open-preparing":true}{
		if exists(b,id)!=want{t.Fatalf("%s exists=%v want %v",id,!want,want)}
	}
}

func TestSweepRetentionBroadcastGroups(t *testing.T){
	b:=retentionBus(t)
	now:=time.Now().UTC();day:=24*time.Hour;old:=now.Add(-20*day)
	members:=[]string{"m1","m2"}
	// open: one child still accepted, even though the other is ancient and terminal
	putDelivery(t,b,Delivery{ID:"bg-open",Kind:"broadcast",Members:members,State:"broadcast-admitted",UpdatedAt:old})
	putDelivery(t,b,Delivery{ID:childID("bg-open","m1"),ParentID:"bg-open",State:"completed",UpdatedAt:old})
	putDelivery(t,b,Delivery{ID:childID("bg-open","m2"),ParentID:"bg-open",State:"accepted",UpdatedAt:old})
	// done: all terminal and old
	putDelivery(t,b,Delivery{ID:"bg-done",Kind:"broadcast",Members:members,State:"broadcast-admitted",UpdatedAt:old})
	for _,m:=range members{putDelivery(t,b,Delivery{ID:childID("bg-done",m),ParentID:"bg-done",State:"completed",UpdatedAt:old})}
	// fresh: terminal but recent child
	putDelivery(t,b,Delivery{ID:"bg-fresh",Kind:"broadcast",Members:members,State:"broadcast-admitted",UpdatedAt:old})
	putDelivery(t,b,Delivery{ID:childID("bg-fresh","m1"),ParentID:"bg-fresh",State:"completed",UpdatedAt:old})
	putDelivery(t,b,Delivery{ID:childID("bg-fresh","m2"),ParentID:"bg-fresh",State:"completed",UpdatedAt:now})
	// fanout still in progress
	putDelivery(t,b,Delivery{ID:"bg-fanout",Kind:"broadcast",Members:members,State:"fanout",UpdatedAt:old})
	if _,err:=b.sweepRetention(context.Background(),now,7*day);err!=nil{t.Fatal(err)}
	for id,want:=range map[string]bool{"bg-open":true,childID("bg-open","m1"):true,childID("bg-open","m2"):true,"bg-done":false,childID("bg-done","m1"):false,childID("bg-done","m2"):false,"bg-fresh":true,childID("bg-fresh","m1"):true,"bg-fanout":true}{
		if exists(b,id)!=want{t.Fatalf("%s exists=%v want %v",id,!want,want)}
	}
}

func TestPutStateIgnoresNotesInKeyLimit(t *testing.T){
	b:=retentionBus(t)
	ctx:=context.Background()
	for i:=0;i<260;i++{if _,err:=b.Note(ctx,"team","t","n");err!=nil{t.Fatal(err)}}
	if _,err:=b.PutState(ctx,"team","plain","v",0);err!=nil{t.Fatalf("plain key refused after notes: %v",err)}
}

// After retention expiry the same key starts a new operation (new execution).
func TestExpiredKeyStartsNewOperation(t *testing.T){
	b:=retentionBus(t);ctx:=context.Background()
	var calls atomic.Int32
	srv:=httptest.NewUnstartedServer(nil);srv.Start();t.Cleanup(srv.Close)
	srv.Config.Handler=simagent.Handler(srv.URL,"w","testing",func(){calls.Add(1)})
	if _,err:=b.Register(ctx,"w",srv.URL);err!=nil{t.Fatal(err)}
	d,done,err:=b.Ask(ctx,"w","job","exp-1",30*time.Second);if err!=nil||!done{t.Fatalf("%+v %v",d,err)}
	again,err:=b.Send(ctx,"w","job","exp-1");if err!=nil||again.ID!=d.ID||calls.Load()!=1{t.Fatalf("%+v %v calls=%d",again,err,calls.Load())}
	if _,err:=b.sweepRetention(ctx,time.Now().Add(8*24*time.Hour),7*24*time.Hour);err!=nil{t.Fatal(err)}
	if exists(b,d.ID){t.Fatal("expired record survived")}
	// The broker's publish de-duplication window (2m) is shorter than the minimum
	// retention, so a real expiry re-executes; here only the new record is checked.
	fresh,err:=b.Send(ctx,"w","job","exp-1");if err!=nil||fresh.ID!=d.ID||fresh.State=="completed"||len(fresh.Result)!=0{t.Fatalf("expired key must be a new operation: %+v %v",fresh,err)}
}
