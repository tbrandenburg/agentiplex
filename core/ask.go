package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// Ask is Send plus a wait: it durably admits one text request under a required
// stable key, then blocks until the delivery is terminal, ctx ends, or the
// optional timeout elapses (done false, no error). The wait is only a view on
// the durable record and never cancels remote work: asking again with the same
// key re-attaches while the record is retained. A blocking ask to the caller's
// own agent is rejected because a one-turn gateway would deadlock on itself.
func(b *Bus) Ask(ctx context.Context,target,text,key string,timeout time.Duration)(Delivery,bool,error){
	if err:=b.rejectSelfAsk(ctx,target);err!=nil{return Delivery{},false,err}
	d,err:=b.Send(ctx,target,text,key)
	if err!=nil{return Delivery{},false,err}
	wctx:=ctx
	if timeout>0{var cancel context.CancelFunc;wctx,cancel=context.WithTimeout(ctx,timeout);defer cancel()}
	last,err:=b.WaitDelivery(wctx,d.ID)
	if err!=nil&&ctx.Err()==nil&&wctx.Err()!=nil{
		// Only the per-call timeout ended the wait: report the current record.
		if cur,e:=b.rawDelivery(d.ID);e==nil{last=cur}
		return last,false,nil
	}
	if err!=nil{return Delivery{},false,err}
	return last,true,nil
}

// WaitDelivery observes existing work, never submits or cancels it. The
// initial read checks caller scope before any watch is established. Broadcasts
// wait for the manifest to finish admission and then for its existing children.
func (b *Bus) WaitDelivery(ctx context.Context, id string) (Delivery, error) {
	d, err := b.DeliveryFor(ctx, id)
	if err != nil { return Delivery{}, err }
	if terminalOrPartial(d.State) { return d, nil }
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(b.ctx, cancel)
	defer stop()
	wait := func(id string, done func(string) bool) (Delivery, error) {
		return awaitTerminal(ctx, id, func() (Delivery, error) { return b.rawDelivery(id) }, b.watchDelivery, 500*time.Millisecond, done)
	}
	if d.Kind == "until-done" { if _,err:=wait(id,terminalState);err!=nil{return Delivery{},err};return b.DeliveryFor(ctx,id) }
	if d.Kind != "broadcast" { return wait(id, terminalState) }
	manifest, err := wait(id, func(state string) bool { return state == "broadcast-admitted" })
	if err != nil { return Delivery{}, err }
	for _, member := range manifest.Members {
		if _, err := wait(childID(id, member), terminalState); err != nil { return Delivery{}, err }
	}
	return b.DeliveryFor(ctx, id)
}

// ErrSelfAsk rejects a blocking ask whose target is the caller's own agent.
var ErrSelfAsk=fmt.Errorf("%w: blocking ask to your own agent would deadlock; use send_message and poll get_delivery",ErrInvalid)

// rejectSelfAsk compares the target with the authenticated caller's agent ID
// and its registered endpoint (an alias registered under another ID).
func(b *Bus) rejectSelfAsk(ctx context.Context,target string)error{
	caller:=callerID(ctx)
	if caller==""{return nil}
	if target==caller{return ErrSelfAsk}
	t,err:=b.Agent(target);if err!=nil{return nil}
	if me,err:=b.Agent(caller);err==nil&&me.Endpoint==t.Endpoint{return ErrSelfAsk}
	return nil
}

func(b *Bus) watchDelivery(ctx context.Context,id string)(nats.KeyWatcher,error){
	return b.store.Deliveries.Watch(id,nats.Context(ctx))
}

// awaitTerminal waits on a KV watch (current value, then updates). The record
// is re-read after every (re)established watch, so a missed update or a watch
// failure cannot hang the wait. Updates re-saved without a state change are
// ignored implicitly: only terminal states end the wait.
func awaitTerminal(ctx context.Context,id string,get func()(Delivery,error),watch func(context.Context,string)(nats.KeyWatcher,error),pause time.Duration,done func(string)bool)(Delivery,error){
	var last Delivery
	for{
		if w,err:=watch(ctx,id);err==nil{
			d,gerr:=get()
			switch{
			case gerr==nil:
				last=d
				if done(d.State){_ = w.Stop();return d,nil}
			case errors.Is(gerr,ErrNotFound):_ = w.Stop();return last,gerr
			default:_ = w.Stop();w=nil
			}
			var watchErrors <-chan error
			if w!=nil{watchErrors=w.Error()}
			for w!=nil{
				select{
				case <-ctx.Done():_ = w.Stop();return last,ctx.Err()
				case err,ok:=<-watchErrors:
					if !ok{watchErrors=nil}else if err!=nil{_ = w.Stop();w=nil}
				case e,ok:=<-w.Updates():
					if !ok{_ = w.Stop();w=nil;break}
					if e==nil{continue}
					if e.Operation()!=nats.KeyValuePut{_ = w.Stop();return last,ErrNotFound}
					var nd Delivery
					if json.Unmarshal(e.Value(),&nd)==nil{
						last=nd
						if done(nd.State){_ = w.Stop();return nd,nil}
					}
				}
			}
		}
		select{case <-ctx.Done():return last,ctx.Err();case <-time.After(pause):}
	}
}
