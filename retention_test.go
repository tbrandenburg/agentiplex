package agentiplex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
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
		raw,_:=json.Marshal(Delivery{ID:fmt.Sprintf("old-%d",i),State:"completed",Result:big,UpdatedAt:old,ReturnedAt:&old})
		if _,err:=b.store.Deliveries.Create(fmt.Sprintf("old-%d",i),raw);err!=nil{t.Logf("full at %d: %v",i,err);break}
		full++
		if i>1000{t.Fatal("bucket never filled")}
	}
	if full<100{t.Fatalf("bucket filled after only %d records",full)}
	before,_:=b.Usage()
	cur,_,_:=read[Delivery](b.store.Deliveries,"running")
	n,err:=b.sweepRetention(ctx,time.Now(),7*24*time.Hour,30*24*time.Hour);if err!=nil{t.Fatal(err)}
	if n!=full{t.Fatalf("removed %d of %d",n,full)}
	after,_:=b.Usage()
	if after.Bytes>=before.Bytes/10{t.Fatalf("bytes not freed: %d -> %d",before.Bytes,after.Bytes)}
	if !exists(b,"running"){t.Fatal("nonterminal record removed")}
	cur.Detail="still polling";cur.UpdatedAt=time.Now().UTC();raw,_:=json.Marshal(cur)
	if _,err=b.store.Deliveries.Put("running",raw);err!=nil{t.Fatalf("running task update failed: %v",err)}
	putDelivery(t,b,Delivery{ID:"fresh",State:"queued",UpdatedAt:time.Now().UTC()})
}

func TestSweepRetentionReturnedMarkAndLongLimit(t *testing.T){
	b:=retentionBus(t)
	ctx:=context.Background()
	now:=time.Now().UTC()
	ago:=func(d time.Duration)time.Time{return now.Add(-d)}
	day:=24*time.Hour
	ret:=ago(10*day)
	putDelivery(t,b,Delivery{ID:"returned",State:"completed",UpdatedAt:ago(8*day),ReturnedAt:&ret})
	putDelivery(t,b,Delivery{ID:"unreturned",State:"completed",UpdatedAt:ago(8*day)})
	putDelivery(t,b,Delivery{ID:"unreturned-ancient",State:"failed",UpdatedAt:ago(31*day)})
	putDelivery(t,b,Delivery{ID:"recent",State:"completed",UpdatedAt:ago(day),ReturnedAt:&ret})
	for _,s:=range []string{"queued","attempting","accepted","preparing"}{putDelivery(t,b,Delivery{ID:"open-"+s,State:s,UpdatedAt:ago(100*day)})}
	if _,err:=b.sweepRetention(ctx,now,7*day,30*day);err!=nil{t.Fatal(err)}
	for id,want:=range map[string]bool{"returned":false,"unreturned":true,"unreturned-ancient":false,"recent":true,"open-queued":true,"open-attempting":true,"open-accepted":true,"open-preparing":true}{
		if exists(b,id)!=want{t.Fatalf("%s exists=%v want %v",id,!want,want)}
	}
	u,err:=b.Usage();if err!=nil{t.Fatal(err)}
	if u.Unreturned!=1{t.Fatalf("unreturned kept = %d, want 1",u.Unreturned)}
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
	if _,err:=b.sweepRetention(context.Background(),now,7*day,30*day);err!=nil{t.Fatal(err)}
	for id,want:=range map[string]bool{"bg-open":true,childID("bg-open","m1"):true,childID("bg-open","m2"):true,"bg-done":false,childID("bg-done","m1"):false,childID("bg-done","m2"):false,"bg-fresh":true,childID("bg-fresh","m1"):true,"bg-fanout":true}{
		if exists(b,id)!=want{t.Fatalf("%s exists=%v want %v",id,!want,want)}
	}
}

func TestAskIDSkipsPurgedHoleToRunningDelivery(t *testing.T){
	b:=retentionBus(t)
	id:=func(n int)string{h:=sha256.Sum256([]byte("ask\x00\x00a\x00x\x00"+strconv.Itoa(n)));return hex.EncodeToString(h[:])}
	now:=time.Now().UTC()
	putDelivery(t,b,Delivery{ID:id(1),State:"accepted",UpdatedAt:now})
	got,err:=b.askID("","a","x");if err!=nil{t.Fatal(err)}
	if got!=id(1){t.Fatal("ask must re-attach to the running delivery behind a purged id")}
}

func TestAskIDAfterReturnedHoleUsesNextFree(t *testing.T){
	b:=retentionBus(t)
	id:=func(n int)string{h:=sha256.Sum256([]byte("ask\x00\x00a\x00y\x00"+strconv.Itoa(n)));return hex.EncodeToString(h[:])}
	now:=time.Now().UTC()
	putDelivery(t,b,Delivery{ID:id(1),State:"completed",UpdatedAt:now,ReturnedAt:&now})
	got,err:=b.askID("","a","y");if err!=nil{t.Fatal(err)}
	if got!=id(2){t.Fatal("new ask must follow the highest existing id")}
}

func TestPutStateIgnoresNotesInKeyLimit(t *testing.T){
	b:=retentionBus(t)
	ctx:=context.Background()
	for i:=0;i<260;i++{if _,err:=b.Note(ctx,"team","t","n");err!=nil{t.Fatal(err)}}
	if _,err:=b.PutState(ctx,"team","plain","v",0);err!=nil{t.Fatalf("plain key refused after notes: %v",err)}
}
