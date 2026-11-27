package agentiplex

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex/internal/simagent"
)

func TestRecoverPartiallyPublishedBroadcast(t *testing.T){
	ctx:=context.Background()
	cfg:=DefaultConfig();cfg.Storage=t.TempDir();cfg.Timeout=4*time.Second;cfg.RetryDelay=time.Second
	var firstCalls,secondCalls atomic.Int32
	start:=func(name string,calls *atomic.Int32)*httptest.Server{
		s:=httptest.NewUnstartedServer(nil)
		s.Config.Handler=simagent.Handler("http://"+s.Listener.Addr().String(),name,"testing",func(){calls.Add(1)})
		s.Start();t.Cleanup(s.Close);return s
	}
	first,second:=start("first",&firstCalls),start("second",&secondCalls)
	b,err:=Open(ctx,cfg);if err!=nil{t.Fatal(err)};defer b.Close()
	if _,err=b.Register(ctx,"first",first.URL);err!=nil{t.Fatal(err)}
	if _,err=b.Register(ctx,"second",second.URL);err!=nil{t.Fatal(err)}
	// Stop both workers while keeping the broker open to guarantee that
	// the missing child can only be recovered by the restarted daemon.
	b.cancel();b.wg.Wait()
	// Inject a durable parent and only one child, representing an
	// interrupted fan-out before publication of the second recipient.
	id:=scopedID("bg-","","interrupted")
	d:=Delivery{ID:id,Kind:"broadcast",Target:"engineering",Text:"review",Members:[]string{"first","second"},State:"fanout",UpdatedAt:time.Now().UTC()}
	b.mu.Lock()
	raw,_:=json.Marshal(d)
	_,err=b.store.Deliveries.Create(id,raw)
	b.mu.Unlock()
	if err!=nil{t.Fatal(err)}
	firstChild,err:=b.sendChild(ctx,"first",d.Text,childID(id,"first"),"",id,"")
	if err!=nil{t.Fatal(err)}
	if _,err=b.rawDelivery(childID(id,"second"));!errors.Is(err,ErrNotFound){t.Fatalf("second child unexpectedly admitted: %v",err)}
	b.Close()
	b2,err:=Open(ctx,cfg);if err!=nil{t.Fatal(err)};defer b2.Close()
	deadline:=time.Now().Add(30*time.Second)
	for time.Now().Before(deadline){
		got,err:=b2.Delivery(id);if err!=nil{t.Fatal(err)}
		if got.State=="completed"{
			if len(got.Children)!=2||got.Children[0].ID!=firstChild.ID||got.Children[1].ID!=childID(id,"second"){t.Fatalf("manifest changed %+v",got)}
			if firstCalls.Load()!=1||secondCalls.Load()!=1{t.Fatalf("recovery replayed a child: %d %d",firstCalls.Load(),secondCalls.Load())}
			return
		}
		time.Sleep(100*time.Millisecond)
	}
	t.Fatal("partially published broadcast did not recover")
}
