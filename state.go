package agentiplex

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nats-io/nats.go"
)

const MaxStateValueBytes=16<<10
const MaxStateKeys=256

type StateEntry struct {
	Namespace string `json:"namespace"`
	Key string `json:"key"`
	Value *string `json:"value,omitempty"`
	Revision uint64 `json:"revision"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type StateChange struct {
	Operation string `json:"operation"`
	Entry *StateEntry `json:"entry,omitempty"`
}

func stateKey(namespace,key string)(string,error){
	if !validID.MatchString(namespace)||!validID.MatchString(key){return "",ErrInvalid}
	return namespace+"."+key,nil
}

func stateEntry(e nats.KeyValueEntry,includeValue bool)StateEntry{
	ns,key,_:=strings.Cut(e.Key(),".")
	s:=StateEntry{Namespace:ns,Key:key,Revision:e.Revision(),UpdatedAt:e.Created()}
	if includeValue{v:=string(e.Value());s.Value=&v}
	return s
}

func stateError(err error)error{
	switch{
	case errors.Is(err,nats.ErrKeyNotFound),errors.Is(err,nats.ErrKeyDeleted):return ErrNotFound
	case errors.Is(err,nats.ErrKeyExists),errors.Is(err,nats.ErrKeyRevisionMismatch):return ErrConflict
	default:return err
	}
}

func (b *Bus) GetState(namespace,key string)(StateEntry,error){
	k,err:=stateKey(namespace,key);if err!=nil{return StateEntry{},err}
	e,err:=b.store.State.Get(k);if err!=nil{return StateEntry{},stateError(err)}
	return stateEntry(e,true),nil
}

// PutState uses native KV Create/Update, never unconditional Put.
// Revision zero is create-only; updates require the observed revision.
func (b *Bus) PutState(ctx context.Context,namespace,key,value string,revision uint64)(StateEntry,error){
	k,err:=stateKey(namespace,key);if err!=nil{return StateEntry{},err}
	if len(value)>MaxStateValueBytes||!utf8.ValidString(value){return StateEntry{},ErrInvalid}
	b.mu.Lock();defer b.mu.Unlock()
	if ctx.Err()!=nil{return StateEntry{},ctx.Err()}
	var next uint64
	if revision==0{
		_,e:=b.store.State.Get(k)
		if e==nil{return StateEntry{},ErrConflict}
		if !errors.Is(stateError(e),ErrNotFound){return StateEntry{},e}
		entries,e:=b.ListState(namespace);if e!=nil{return StateEntry{},e}
		if len(entries)>=MaxStateKeys{return StateEntry{},fmt.Errorf("%w: namespace key limit",ErrInvalid)}
		next,err=b.store.State.Create(k,[]byte(value))
	}else{
		e,eerr:=b.store.State.Get(k);if eerr!=nil{return StateEntry{},stateError(eerr)}
		if e.Revision()!=revision{return StateEntry{},ErrConflict}
		next,err=b.store.State.Update(k,[]byte(value),revision)
	}
	if err!=nil{return StateEntry{},stateError(err)}
	e,err:=b.store.State.GetRevision(k,next)
	if err!=nil{return StateEntry{},stateError(err)}
	b.emit("state.updated",k,"")
	return stateEntry(e,true),nil
}

func (b *Bus) DeleteState(ctx context.Context,namespace,key string,revision uint64)error{
	k,err:=stateKey(namespace,key);if err!=nil{return err}
	if revision==0{return ErrInvalid}
	b.mu.Lock();defer b.mu.Unlock()
	if ctx.Err()!=nil{return ctx.Err()}
	e,err:=b.store.State.Get(k);if err!=nil{return stateError(err)}
	if e.Revision()!=revision{return ErrConflict}
	if err=b.store.State.Delete(k,nats.LastRevision(revision));err!=nil{return stateError(err)}
	b.emit("state.deleted",k,"")
	return nil
}

// ListState returns metadata only. A namespace is an application label,
// not an authenticated principal or permission boundary.
func (b *Bus) ListState(namespace string)([]StateEntry,error){
	if !validID.MatchString(namespace){return nil,ErrInvalid}
	ks,err:=keys(b.store.State);if err!=nil{return nil,err}
	sort.Strings(ks)
	out:=[]StateEntry{}
	for _,k:=range ks{
		if !strings.HasPrefix(k,namespace+"."){continue}
		e,err:=b.store.State.Get(k)
		if errors.Is(stateError(err),ErrNotFound){continue}
		if err!=nil{return nil,err}
		out=append(out,stateEntry(e,false))
	}
	return out,nil
}

// WatchState uses JetStream KV's initial snapshot and live updates.
// "ready" marks the end of the initial snapshot; reconnect starts a new
// snapshot, not an exactly-once historical event replay.
func (b *Bus) WatchState(ctx context.Context,namespace string,yield func(StateChange)error)error{
	if !validID.MatchString(namespace)||yield==nil{return ErrInvalid}
	watcher,err:=b.store.State.Watch(namespace+".*")
	if err!=nil{return err}
	defer watcher.Stop()
	for{
		select{
		case<-ctx.Done():return ctx.Err()
		case<-b.ctx.Done():return b.ctx.Err()
		case e,ok:=<-watcher.Updates():
			if !ok{return errors.New("state watcher closed")}
			change:=StateChange{Operation:"ready"}
			if e!=nil{
				op:="put"
				if e.Operation()!=nats.KeyValuePut{op="delete"}
				entry:=stateEntry(e,op=="put")
				change=StateChange{Operation:op,Entry:&entry}
			}
			if err=yield(change);err!=nil{return err}
		}
	}
}
