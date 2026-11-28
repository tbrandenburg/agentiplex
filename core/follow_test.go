package core

import (
 "context"
 "fmt"
 "net/http/httptest"
 "sync/atomic"
 "testing"
 "time"

 "github.com/tbrandenburg/agentiplex/internal/simagent"
)

// Inject the durable boundaries on either side of child creation. Neither
// record has been published: startup must recover exactly one activation.
func TestFollowIntentRecovery(t *testing.T){
 for _,created:=range []bool{false,true}{
  t.Run(fmt.Sprintf("child-created-%t",created),func(t *testing.T){
   b:=retentionBus(t);ctx:=context.Background();cfg:=b.cfg
   var calls atomic.Int32
   srv:=httptest.NewServer(nil)
   srv.Config.Handler=simagent.Handler(srv.URL,"lead","planning",func(){calls.Add(1)})
   defer srv.Close()
   a,err:=b.Register(ctx,"lead",srv.URL);if err!=nil{t.Fatal(err)}
   now:=time.Now().UTC();id:=sendID("","crash")
   childID:=scopedID("ft-","",id+":1")
   parent:=Delivery{ID:id,Kind:"until-done",Target:"lead",Text:"fixture",Endpoint:a.Endpoint,CardURL:a.CardURL,State:"continuing",UpdatedAt:now,Follow:&FollowThrough{Options:FollowOptions{MaxTurns:1,TimeoutSeconds:60},StartedAt:now,Deadline:now.Add(time.Minute),Turns:[]string{childID},TurnReason:"initial activation"}}
   putDelivery(t,b,parent)
   if created{putDelivery(t,b,Delivery{ID:childID,ParentID:id,Target:"lead",Text:"fixture activation",Endpoint:a.Endpoint,CardURL:a.CardURL,State:"preparing",UpdatedAt:now})}
   b.Close()
   recovered,err:=Open(ctx,cfg);if err!=nil{t.Fatal(err)};defer recovered.Close()
   wctx,cancel:=context.WithTimeout(ctx,25*time.Second);defer cancel()
   d,err:=recovered.WaitDelivery(wctx,id)
   if err!=nil||d.State!="input-required"||d.Follow==nil||len(d.Follow.Turns)!=1||d.Follow.Turns[0]!=childID||calls.Load()!=1{t.Fatalf("recovered %+v calls=%d err=%v",d,calls.Load(),err)}
   child,err:=recovered.Delivery(childID)
   if err!=nil||child.State!="completed"||child.ParentID!=id{t.Fatalf("recovered child %+v %v",child,err)}
  })
 }
}
