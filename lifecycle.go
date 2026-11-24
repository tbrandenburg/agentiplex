package agentiplex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

type AgentHealth struct {
	ID string `json:"id"`
	Reachability string `json:"reachability"`
	ObservedAt *time.Time `json:"observedAt,omitempty"`
	Busy bool `json:"busy"`
}

// AgentHealths reports the last persisted card observation independently
// from the local delivery capacity reservation. Neither proves availability.
func (b *Bus) AgentHealths() ([]AgentHealth,error){
	agents,err:=b.Agents("","");if err!=nil{return nil,err}
	active,err:=b.activeEndpoints("");if err!=nil{return nil,err}
	out:=make([]AgentHealth,0,len(agents))
	for _,a:=range agents{out=append(out,AgentHealth{ID:a.ID,Reachability:a.Reachability,ObservedAt:a.ObservedAt,Busy:active[a.Endpoint]})}
	return out,nil
}

func (b *Bus) AgentHealth(id string)(AgentHealth,error){
	a,err:=b.Agent(id);if err!=nil{return AgentHealth{},err}
	active,err:=b.activeEndpoints("");if err!=nil{return AgentHealth{},err}
	return AgentHealth{ID:id,Reachability:a.Reachability,ObservedAt:a.ObservedAt,Busy:active[a.Endpoint]},nil
}

// Refresh updates only the card snapshot for the same registered endpoint.
// A changed endpoint requires explicit removal and a new registration.
func (b *Bus) Refresh(ctx context.Context,id string)(Agent,error){
	a,ok,err:=b.observe(ctx,id,true)
	if err!=nil{return Agent{},err}
	if !ok{return Agent{},fmt.Errorf("%w: registered endpoint or card unavailable; previous card retained",ErrConflict)}
	return a,nil
}

func (b *Bus) ProbeAgent(ctx context.Context,id string)(AgentHealth,error){
	if _,_,err:=b.observe(ctx,id,false);err!=nil{return AgentHealth{},err}
	return b.AgentHealth(id)
}

// Network I/O happens outside admission lock. CAS rejects an observation
// that races with another observation, removal or re-registration.
func (b *Bus) observe(ctx context.Context,id string,refresh bool)(Agent,bool,error){
	if !validID.MatchString(id){return Agent{},false,ErrInvalid}
	a,rev,err:=read[Agent](b.store.Registry,id);if err!=nil{return Agent{},false,err}
	probe,cancel:=context.WithTimeout(ctx,5*time.Second)
	defer cancel()
	card,url,endpoint,probeErr:=b.adapter.Resolve(probe,a.CardURL)
	if ctx.Err()!=nil{return Agent{},false,ctx.Err()}
	ok:=probeErr==nil&&url==a.CardURL&&endpoint==a.Endpoint
	old:=a.Reachability
	now:=time.Now().UTC()
	a.ObservedAt=&now
	a.Reachability="unreachable"
	if ok{
		a.Reachability="reachable"
		if refresh{a.Card=card;a.UpdatedAt=now}
	}
	raw,err:=json.Marshal(a);if err!=nil{return Agent{},false,err}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _,err=b.store.Registry.Update(id,raw,rev);err!=nil{
		if errors.Is(err,nats.ErrKeyRevisionMismatch){return Agent{},false,ErrConflict}
		return Agent{},false,err
	}
	if old!=a.Reachability{b.emit("agent.reachability",id,a.Reachability)}
	if refresh&&ok{b.emit("agent.refreshed",id,"")}
	return a,ok,nil
}

func (b *Bus) observeAgents(ctx context.Context){
	ticker:=time.NewTicker(b.cfg.HealthInterval)
	defer ticker.Stop()
	for{
		select{case<-ctx.Done():return;case<-ticker.C:}
		agents,err:=b.Agents("","");if err!=nil{continue}
		for _,a:=range agents{
			if ctx.Err()!=nil{return}
			_,_,_=b.observe(ctx,a.ID,false)
		}
	}
}

func pendingState(state string)bool{
	switch state{case "preparing","queued","attempting","accepted","fanout":return true}
	return false
}

// RemoveAgent is conservative: do not silently discard a recipient or
// change an admitted recipient snapshot. Historical terminal deliveries and
// application state remain inspectable. Collections must be edited first.
func (b *Bus) RemoveAgent(ctx context.Context,id string)error{
	if !validID.MatchString(id){return ErrInvalid}
	b.mu.Lock();defer b.mu.Unlock()
	_,rev,err:=read[Agent](b.store.Registry,id);if err!=nil{return err}
	for _,kind:=range []string{"group","pool"}{
		cs,err:=b.Collections(kind);if err!=nil{return err}
		for _,c:=range cs{for _,member:=range c.Members{if member==id{return fmt.Errorf("%w: agent belongs to %s %s",ErrConflict,kind,c.ID)}}}
	}
	ks,err:=keys(b.store.Deliveries);if err!=nil{return err}
	for _,key:=range ks{
		if ctx.Err()!=nil{return ctx.Err()}
		d,err:=b.rawDelivery(key);if err!=nil{return err}
		if d.Kind=="broadcast"{d,err=b.broadcastView(d);if err!=nil{return err}}
		if !pendingState(d.State){continue}
		if (d.Kind==""&&d.Target==id)||d.AssignedAgent==id{return fmt.Errorf("%w: pending delivery %s",ErrConflict,d.ID)}
		for _,member:=range d.Members{if member==id{return fmt.Errorf("%w: pending recipient snapshot %s",ErrConflict,d.ID)}}
	}
	if err=b.store.Registry.Delete(id,nats.LastRevision(rev));err!=nil{
		if errors.Is(err,nats.ErrKeyRevisionMismatch){return ErrConflict}
		return err
	}
	b.emit("agent.removed",id,"")
	return nil
}
