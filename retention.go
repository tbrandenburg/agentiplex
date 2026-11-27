package agentiplex

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	defaultRetention=7*24*time.Hour
	defaultUnreturnedRetention=30*24*time.Hour
)

// Usage reports the DELIVERIES bucket footprint. Unreturned counts terminal
// records kept by the last sweep only because no ask handed them back.
type Usage struct {
	Bytes uint64 `json:"bytes"`
	MaxBytes int64 `json:"maxBytes"`
	Records uint64 `json:"records"`
	Unreturned int64 `json:"unreturnedTerminal"`
}

// Usage returns current bucket usage from the broker.
func (b *Bus) Usage()(Usage,error){
	st,err:=b.store.Deliveries.Status();if err!=nil{return Usage{},err}
	u:=Usage{Bytes:st.Bytes(),Records:st.Values(),Unreturned:b.unreturned.Load()}
	if info,ok:=st.(*nats.KeyValueBucketStatus);ok{u.MaxBytes=info.StreamInfo().Config.MaxBytes}
	return u,nil
}


// sweepRetention removes terminal deliveries older than retention (measured
// from UpdatedAt). A terminal record never returned by an ask is kept until
// unreturned instead; this applies to every record without a mark, including
// ones that were never asked. A broadcast group (parent and children) is
// removed only when the derived parent state is terminal and the newest child
// update is older than retention; children of a live parent are never removed
// on their own. Nonterminal records are never touched. Deletion is a CAS
// purge, so a concurrent update wins and the record survives to the next sweep.
func (b *Bus) sweepRetention(ctx context.Context,now time.Time,retention,unreturned time.Duration)(int,error){
	ks,err:=keys(b.store.Deliveries);if err!=nil{return 0,err}
	kept,removed:=int64(0),0
	purge:=func(id string,rev uint64)bool{
		err:=b.store.Deliveries.Purge(id,nats.LastRevision(rev))
		if err!=nil{if !errors.Is(err,nats.ErrKeyExists){slog.Warn("retention purge failed","id",id)};return false}
		removed++;return true
	}
	for _,id:=range ks{
		if ctx.Err()!=nil{break}
		d,rev,e:=read[Delivery](b.store.Deliveries,id)
		if e!=nil{continue}
		if d.ParentID!=""{
			// Owned by a live parent; orphans (parent already removed) age out alone.
			if _,_,pe:=read[Delivery](b.store.Deliveries,d.ParentID);!errors.Is(pe,ErrNotFound){continue}
		}
		if d.Kind=="broadcast"{
			v,e:=b.broadcastView(d);if e!=nil||d.State=="fanout"||!terminalOrPartial(v.State){continue}
			if now.Sub(v.UpdatedAt)<=retention{continue}
			// Parent first: a crash leaves orphan children that age out alone, never a parent with missing children.
			if !purge(id,rev){continue}
			for _,c:=range v.Children{if _,cr,ce:=read[Delivery](b.store.Deliveries,c.ID);ce==nil{purge(c.ID,cr)}}
			continue
		}
		if !terminalState(d.State){continue}
		limit:=retention
		if d.ReturnedAt==nil{
			limit=unreturned
			if now.Sub(d.UpdatedAt)>retention{kept++}
		}
		if now.Sub(d.UpdatedAt)>limit&&purge(id,rev){if d.ReturnedAt==nil{kept--}}
	}
	b.unreturned.Store(kept)
	if removed>0{
		if err:=b.store.Deliveries.PurgeDeletes(nats.DeleteMarkersOlderThan(time.Hour));err!=nil{slog.Warn("retention marker cleanup failed")}
	}
	if removed>0||kept>0{slog.Info("retention sweep","removed",removed,"unreturnedTerminalKept",kept)}
	return removed,nil
}

func terminalOrPartial(s string)bool{return s=="partial"||terminalState(s)}
