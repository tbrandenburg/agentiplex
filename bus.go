// Package agentiplex implements the Agentiplex collaboration service.
package agentiplex

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	adapter "github.com/tbrandenburg/agentiplex/internal/adapters/a2a"
	store "github.com/tbrandenburg/agentiplex/internal/nats"
	"github.com/nats-io/nats.go"
)

type Bus struct {
	cfg Config
	store *store.Store
	adapter *adapter.Client
	mu sync.Mutex // Single-daemon admission and delivery state transitions.
	cancel context.CancelFunc
	wg sync.WaitGroup
	closeOnce sync.Once
}

// Open starts the worker. One active daemon per namespace is supported in v0.1.
func Open(ctx context.Context,cfg Config)(*Bus,error){
	if err:=cfg.validate();err!=nil{return nil,err}
	a,err:=adapter.New(cfg.AllowedOrigins,cfg.Timeout)
	if err!=nil{return nil,err}
	s,err:=store.Open(cfg.BrokerMode,cfg.BrokerURL,cfg.Storage,cfg.Namespace,cfg.Replicas,cfg.Timeout)
	if err!=nil{return nil,err}
	ctx,cancel:=context.WithCancel(ctx)
	b:=&Bus{cfg:cfg,store:s,adapter:a,cancel:cancel}
	if err=b.recoverPreparing(ctx);err!=nil{b.Close();return nil,err}
	b.wg.Add(1)
	go func(){defer b.wg.Done();b.work(ctx)}()
	return b,nil
}

func (b *Bus) Close(){
	b.closeOnce.Do(func(){b.cancel();b.wg.Wait();b.store.Close()})
}

func (b *Bus) Ready()bool{
	if !b.store.Conn.IsConnected(){return false}
	_,err:=b.store.JS.AccountInfo()
	return err==nil
}

func read[T any](kv nats.KeyValue,id string)(T,uint64,error){
	var v T
	e,err:=kv.Get(id)
	if errors.Is(err,nats.ErrKeyNotFound){return v,0,ErrNotFound}
	if err!=nil{return v,0,err}
	err=json.Unmarshal(e.Value(),&v)
	return v,e.Revision(),err
}

func keys(kv nats.KeyValue)([]string,error){
	k,err:=kv.Keys()
	if errors.Is(err,nats.ErrNoKeysFound){return nil,nil}
	return k,err
}

func (b *Bus) emit(kind,id,state string){
	data,err:=json.Marshal(Event{Kind:kind,ID:id,State:state,At:time.Now().UTC()})
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
