package core

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

type Event struct {
	Sequence uint64 `json:"sequence"`
	Kind string `json:"kind"`
	ID string `json:"id"`
	State string `json:"state,omitempty"`
	Caller string `json:"caller,omitempty"`
	At time.Time `json:"at"`
}

func (b *Bus) emit(kind,id,state,caller string){
	data,err:=json.Marshal(Event{Kind:kind,ID:id,State:state,Caller:caller,At:time.Now().UTC()})
	if err==nil{_,err=b.store.JS.Publish(b.store.EventSubject,data)}
	if err!=nil{slog.Warn("event publication failed","kind",kind,"id",id)}
}

func (b *Bus) Events(after uint64)([]Event,error){
	out:=[]Event{}
	info,err:=b.store.JS.StreamInfo(b.store.EventStream)
	if err!=nil{return nil,err}
	if info.State.Msgs==0||after>=info.State.LastSeq{return out,nil}
	seq:=max(after+1,info.State.FirstSeq)
	for ;seq<=info.State.LastSeq&&len(out)<100;seq++{
		m,err:=b.store.JS.GetMsg(b.store.EventStream,seq)
		if errors.Is(err,nats.ErrMsgNotFound){continue}
		if err!=nil{return nil,err}
		var e Event
		if err=json.Unmarshal(m.Data,&e);err!=nil{return nil,err}
		e.Sequence=m.Sequence
		out=append(out,e)
	}
	return out,nil
}

// RecentEvents returns the newest retained events in reverse sequence order.
func (b *Bus) RecentEvents(limit int)([]Event,error){
	if limit<1||limit>100{return nil,ErrInvalid}
	info,err:=b.store.JS.StreamInfo(b.store.EventStream)
	if err!=nil{return nil,err}
	after:=uint64(0)
	if info.State.LastSeq>uint64(limit){after=info.State.LastSeq-uint64(limit)}
	events,err:=b.Events(after);if err!=nil{return nil,err}
	for i,j:=0,len(events)-1;i<j;i,j=i+1,j-1{events[i],events[j]=events[j],events[i]}
	return events,nil
}

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
