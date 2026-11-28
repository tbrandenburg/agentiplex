// Package core implements the Agentiplex collaboration service.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	adapter "github.com/tbrandenburg/agentiplex/internal/adapters/a2a"
	store "github.com/tbrandenburg/agentiplex/internal/nats"
	"github.com/nats-io/nats.go"
)

type Bus struct {
	cfg Config
	store *store.Store
	adapter *adapter.Client
	mu sync.Mutex // Single-daemon admission and delivery state transitions.
	ctx context.Context
	cancel context.CancelFunc
	poolConsumers map[string]*nats.Subscription
	poolCursor map[string]int
	inflight map[string]bool // guarded by mu; includes status polls and probes
	wg sync.WaitGroup
	closeOnce sync.Once
}

// Open starts the worker. One active daemon per namespace is supported in v0.1.
func Open(ctx context.Context,cfg Config)(*Bus,error){
	if err:=cfg.validate();err!=nil{return nil,err}
	if cfg.Workers==0{cfg.Workers=DefaultConfig().Workers}
	if cfg.Retention==0{cfg.Retention=defaultRetention}
	a,err:=adapter.NewWithCredentials(cfg.AllowedOrigins,cfg.Timeout,cfg.OutboundTokenFiles,cfg.OutboundCAFile)
	if err!=nil{return nil,err}
	s,err:=store.Open(cfg.BrokerMode,cfg.BrokerURL,cfg.Storage,cfg.Namespace,cfg.Replicas,cfg.Timeout,cfg.BrokerSync,store.AuthConfig{TokenFile:cfg.BrokerTokenFile,CredentialsFile:cfg.BrokerCredentialsFile,CAFile:cfg.BrokerCAFile})
	if err!=nil{return nil,err}
	ctx,cancel:=context.WithCancel(ctx)
	b:=&Bus{cfg:cfg,store:s,adapter:a,ctx:ctx,cancel:cancel,poolConsumers:map[string]*nats.Subscription{},poolCursor:map[string]int{},inflight:map[string]bool{}}
	if err=b.restorePools();err!=nil{b.Close();return nil,err}
	if err=b.recoverPreparing(ctx);err!=nil{b.Close();return nil,err}
	b.wg.Add(2)
	go func(){defer b.wg.Done();b.work(ctx)}()
	go func(){defer b.wg.Done();b.recoverFanouts(ctx)}()
	if cfg.HealthInterval>0{b.wg.Add(1);go func(){defer b.wg.Done();b.observeAgents(ctx)}()}
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
