package core

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
)

// SubscribeEvents replays the retained JetStream event log, then follows
// it. Core NATS notifications only wake the reader; the durable stream
// remains authoritative. A slow subscriber never stalls delivery.
func (b *Bus) SubscribeEvents(ctx context.Context,after uint64,kind string,yield func(Event)error)error{
	wake:=make(chan struct{},1)
	sub,err:=b.store.Conn.Subscribe(b.store.EventSubject,func(_ *nats.Msg){
		select{case wake<-struct{}{}:default:}
	})
	if err!=nil{return err}
	defer sub.Unsubscribe()
	if err=b.store.Conn.FlushTimeout(5*time.Second);err!=nil{return err}
	ticker:=time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err()!=nil{return ctx.Err()}
		if b.ctx.Err()!=nil{return b.ctx.Err()}
		events,err:=b.Events(after)
		if err!=nil{return err}
		for _,e:=range events{
			after=e.Sequence
			if kind==""||kind==e.Kind{
				if err=yield(e);err!=nil{return err}
			}
		}
		if len(events)==100{continue}
		select{
		case <-ctx.Done():return ctx.Err()
		case <-b.ctx.Done():return b.ctx.Err()
		case <-wake:
		case <-ticker.C:
		}
	}
}
