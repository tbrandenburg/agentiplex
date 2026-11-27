package agentiplex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
)

func terminalState(s string)bool{
	switch s{case "completed","failed","uncertain","input-required","auth-required":return true}
	return false
}

// Ask durably admits one text request like Send, then blocks until the delivery
// is terminal, ctx ends, or the optional timeout elapses (done false, no error).
// The wait is only a view on the durable record: asking again re-attaches.
// An empty key derives one from target and text; it re-attaches to a delivery
// that is nonterminal or whose result was not yet returned, otherwise it starts
// a new delivery. An explicit key re-attaches forever.
func(b *Bus) Ask(ctx context.Context,target,text,key string,timeout time.Duration)(Delivery,bool,error){
	var d Delivery
	var err error
	if key!=""{
		d,err=b.Send(ctx,target,text,key)
	}else{
		var id string
		if id,err=b.askID(target,text);err!=nil{return Delivery{},false,err}
		d,err=b.sendWithID(ctx,target,text,id,"")
	}
	if err!=nil{return Delivery{},false,err}
	wctx:=ctx
	if timeout>0{var cancel context.CancelFunc;wctx,cancel=context.WithTimeout(ctx,timeout);defer cancel()}
	last,err:=awaitTerminal(wctx,d.ID,func()(Delivery,error){return b.rawDelivery(d.ID)},b.watchDelivery,500*time.Millisecond)
	if err!=nil&&ctx.Err()==nil&&wctx.Err()!=nil{
		// Only the per-call timeout ended the wait: report the current record.
		if cur,e:=b.rawDelivery(d.ID);e==nil{last=cur}
		return last,false,nil
	}
	if err!=nil{return Delivery{},false,err}
	if last.ReturnedAt==nil{
		if last,err=b.markReturned(d.ID);err!=nil{return Delivery{},false,err}
	}
	return last,true,nil
}

// askID picks the first derived delivery that does not exist, is nonterminal,
// or whose result was not returned yet.
func(b *Bus) askID(target,text string)(string,error){
	const gap=8
	next,missing:="",0
	for n:=0;missing<gap;n++{
		h:=sha256.Sum256([]byte("ask\x00"+target+"\x00"+text+"\x00"+strconv.Itoa(n)))
		id:=hex.EncodeToString(h[:])
		d,err:=b.rawDelivery(id)
		if errors.Is(err,ErrNotFound){
			if next==""{next=id}
			missing++;continue
		}
		if err!=nil{return "",err}
		missing,next=0,""
		if !terminalState(d.State)||d.ReturnedAt==nil{return id,nil}
	}
	return next,nil
}

// markReturned sets the durable returned mark by compare-and-swap so it never
// clobbers a concurrent save. It adds no event and leaves UpdatedAt untouched.
func(b *Bus) markReturned(id string)(Delivery,error){
	var err error
	for range 5{
		d,rev,e:=read[Delivery](b.store.Deliveries,id)
		if e!=nil{return Delivery{},e}
		if d.ReturnedAt!=nil{return d,nil}
		now:=time.Now().UTC();d.ReturnedAt=&now
		raw,e:=json.Marshal(d);if e!=nil{return Delivery{},e}
		if _,err=b.store.Deliveries.Update(id,raw,rev);err==nil{return d,nil}
	}
	return Delivery{},err
}

func(b *Bus) watchDelivery(ctx context.Context,id string)(nats.KeyWatcher,error){
	return b.store.Deliveries.Watch(id,nats.Context(ctx))
}

// awaitTerminal waits on a KV watch (current value, then updates). The record
// is re-read after every (re)established watch, so a missed update or a watch
// failure cannot hang the wait. Updates re-saved without a state change are
// ignored implicitly: only terminal states end the wait.
func awaitTerminal(ctx context.Context,id string,get func()(Delivery,error),watch func(context.Context,string)(nats.KeyWatcher,error),pause time.Duration)(Delivery,error){
	var last Delivery
	for{
		if w,err:=watch(ctx,id);err==nil{
			d,gerr:=get()
			switch{
			case gerr==nil:
				last=d
				if terminalState(d.State){_ = w.Stop();return d,nil}
			case errors.Is(gerr,ErrNotFound):_ = w.Stop();return last,gerr
			default:_ = w.Stop();w=nil
			}
			for w!=nil{
				select{
				case <-ctx.Done():_ = w.Stop();return last,ctx.Err()
				case e,ok:=<-w.Updates():
					if !ok{w=nil;break}
					if e==nil{continue}
					if e.Operation()!=nats.KeyValuePut{_ = w.Stop();return last,ErrNotFound}
					var nd Delivery
					if json.Unmarshal(e.Value(),&nd)==nil{
						last=nd
						if terminalState(nd.State){_ = w.Stop();return nd,nil}
					}
				}
			}
		}
		select{case <-ctx.Done():return last,ctx.Err();case <-time.After(pause):}
	}
}
