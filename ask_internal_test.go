package agentiplex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type fakeEntry struct{nats.KeyValueEntry;raw []byte}
func(e fakeEntry) Value()[]byte{return e.raw}
func(e fakeEntry) Operation()nats.KeyValueOp{return nats.KeyValuePut}

type fakeWatcher struct{ch chan nats.KeyValueEntry}
func(w fakeWatcher) Context()context.Context{return context.Background()}
func(w fakeWatcher) Updates()<-chan nats.KeyValueEntry{return w.ch}
func(w fakeWatcher) Stop()error{return nil}
func(w fakeWatcher) Error()<-chan error{return nil}

// A failing watch, a closed watch and a missed update must all end in the
// terminal record via re-reads, not hang.
func TestAwaitTerminalRecoversFromWatchInterruption(t *testing.T){
	watches,gets:=0,0
	watch:=func(context.Context,string)(nats.KeyWatcher,error){
		watches++
		switch watches{
		case 1:return nil,errors.New("watch unavailable")
		case 2:ch:=make(chan nats.KeyValueEntry);close(ch);return fakeWatcher{ch},nil
		}
		return fakeWatcher{make(chan nats.KeyValueEntry)},nil
	}
	get:=func()(Delivery,error){
		gets++
		if gets<2{return Delivery{ID:"x",State:"accepted"},nil}
		return Delivery{ID:"x",State:"completed"},nil // the terminal update was never delivered
	}
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
	d,err:=awaitTerminal(ctx,"x",get,watch,10*time.Millisecond)
	if err!=nil||d.State!="completed"||watches!=3{t.Fatalf("%+v %v watches=%d",d,err,watches)}
}

func TestAwaitTerminalIgnoresNonTerminalUpdatesAndHonoursContext(t *testing.T){
	ch:=make(chan nats.KeyValueEntry,2)
	for _,s:=range []string{"accepted","accepted"}{raw,_:=json.Marshal(Delivery{ID:"x",State:s});ch<-fakeEntry{raw:raw}}
	watch:=func(context.Context,string)(nats.KeyWatcher,error){return fakeWatcher{ch},nil}
	get:=func()(Delivery,error){return Delivery{ID:"x",State:"accepted"},nil}
	ctx,cancel:=context.WithTimeout(context.Background(),300*time.Millisecond);defer cancel()
	d,err:=awaitTerminal(ctx,"x",get,watch,10*time.Millisecond)
	if !errors.Is(err,context.DeadlineExceeded)||d.State!="accepted"{t.Fatalf("%+v %v",d,err)}
}
