package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
)

func scopedID(prefix,caller,key string)string{
	h:=sha256.Sum256([]byte(scopedKey(caller,key)))
	return prefix+hex.EncodeToString(h[:30])
}

func childID(parent,member string)string{
	return scopedID("bc-","",parent+"\x00"+member)
}

func (b *Bus) Broadcast(ctx context.Context,group,text,key string)(Delivery,error){
	if !validKey(key){return Delivery{},ErrInvalid}
	return b.submitCollection(ctx,"broadcast",group,text,scopedID("bg-",callerID(ctx),key),"")
}

func (b *Bus) Dispatch(ctx context.Context,pool,text,key string)(Delivery,error){
	if !validKey(key){return Delivery{},ErrInvalid}
	return b.submitCollection(ctx,"dispatch",pool,text,scopedID("dp-",callerID(ctx),key),"")
}

func (b *Bus) submitCollection(ctx context.Context,kind,target,text,id,messageID string)(Delivery,error){
	if !validID.MatchString(id)||!validID.MatchString(target)||strings.TrimSpace(text)==""||len(text)>64<<10{return Delivery{},ErrInvalid}
	if kind!="broadcast"&&kind!="dispatch"{return Delivery{},ErrInvalid}
	b.mu.Lock()
	d,err:=b.rawDelivery(id)
	if err==nil{
		if d.Kind!=kind||d.Caller!=callerID(ctx)||d.Target!=target||d.Text!=text||d.IngressMessageID!=messageID{b.mu.Unlock();return Delivery{},ErrConflict}
	}else if errors.Is(err,ErrNotFound){
		collectionKind:="group"
		if kind=="dispatch"{collectionKind="pool"}
		c,e:=b.Collection(collectionKind,target)
		if e!=nil{b.mu.Unlock();return Delivery{},e}
		d=Delivery{ID:id,Caller:callerID(ctx),Kind:kind,Target:target,Text:text,Members:c.Members,IngressMessageID:messageID,State:"preparing",UpdatedAt:time.Now().UTC()}
		if kind=="broadcast"{d.State="fanout"}
		raw,e:=json.Marshal(d);if e!=nil{b.mu.Unlock();return Delivery{},e}
		_,err=b.store.Deliveries.Create(id,raw)
		if err!=nil{b.mu.Unlock();return Delivery{},err}
	}else{b.mu.Unlock();return Delivery{},err}
	if kind=="dispatch"{
		if d.State=="preparing"{err=b.publish(ctx,&d)}
		b.mu.Unlock()
		return d,err
	}
	b.mu.Unlock()
	// The persisted manifest is the durable acceptance of a broadcast.
	// If some child publishes fail, recovery completes the fan-out without
	// changing membership or creating duplicate logical child deliveries.
	b.reconcileBroadcast(ctx,d)
	return b.Delivery(id)
}

func (b *Bus) reconcileBroadcast(ctx context.Context,d Delivery){
	if d.Kind!="broadcast"||d.State!="fanout"{return}
	all:=true
	for _,member:=range d.Members{
		if ctx.Err()!=nil{return}
		_,err:=b.sendChild(ctx,member,d.Text,childID(d.ID,member),"",d.ID,d.Caller)
		if err!=nil{all=false;slog.Warn("broadcast child admission pending","broadcast",d.ID,"member",member)}
	}
	if !all{return}
	b.mu.Lock()
	defer b.mu.Unlock()
	current,err:=b.rawDelivery(d.ID)
	if err==nil&&current.State=="fanout"{
		if err=b.save(&current,"broadcast-admitted","");err!=nil{slog.Warn("broadcast manifest update failed","id",d.ID)}
	}
}

func (b *Bus) recoverFanouts(ctx context.Context){
	ticker:=time.NewTicker(2*time.Second)
	defer ticker.Stop()
	var swept time.Time
	for{
		if ctx.Err()!=nil{return}
		if now:=time.Now();now.Sub(swept)>=time.Minute{
			swept=now
			if _,err:=b.sweepRetention(ctx,now,b.cfg.Retention);err!=nil{slog.Warn("retention sweep failed")}
		}
		ks,err:=keys(b.store.Deliveries)
		if err==nil{
			for _,id:=range ks{
				if ctx.Err()!=nil{return}
				d,e:=b.rawDelivery(id)
				if e==nil&&d.Kind=="broadcast"&&d.State=="fanout"{b.reconcileBroadcast(ctx,d)}
			}
		}
		select{case<-ctx.Done():return;case<-ticker.C:}
	}
}

func (b *Bus) broadcastView(d Delivery)(Delivery,error){
	d.Children=make([]ChildDelivery,0,len(d.Members))
	complete,failed,pending,active:=0,0,0,0
	for _,member:=range d.Members{
		id:=childID(d.ID,member)
		child,err:=b.rawDelivery(id)
		view:=ChildDelivery{ID:id,Target:member,State:"preparing"}
		if errors.Is(err,ErrNotFound){pending++}else if err!=nil{return Delivery{},err}else{
			view.State=child.State
			view.Detail=child.Detail
			if child.UpdatedAt.After(d.UpdatedAt){d.UpdatedAt=child.UpdatedAt}
			switch {
			case child.State=="completed":complete++
			case terminalState(child.State):failed++
			case child.State=="preparing"||child.State=="queued":pending++
			default:active++
			}
		}
		d.Children=append(d.Children,view)
	}
	switch {
	case pending+active>0:
		d.State="queued"
		if active+complete+failed>0{d.State="accepted"}
		d.Detail="one or more recipient deliveries remain pending"
	case complete==len(d.Members):
		d.State="completed";d.Detail=""
	case failed==len(d.Members):
		d.State="failed";d.Detail="no recipient completed successfully"
	default:
		d.State="partial";d.Detail="some recipients failed or require intervention"
	}
	return d,nil
}

// activeEndpoints protects capacity across the independent direct/pool queues.
// Persisted attempting and accepted records reserve capacity across restart.
// New reservations are checked and persisted atomically under b.mu.
func (b *Bus) activeEndpoints(exclude string)(map[string]bool,error){
	active:=map[string]bool{}
	ks,err:=keys(b.store.Deliveries);if err!=nil{return nil,err}
	for _,id:=range ks{
		if id==exclude{continue}
		d,e:=b.rawDelivery(id);if e!=nil{return nil,e}
		if d.State=="accepted"||d.State=="attempting"{active[d.Endpoint]=true}
	}
	return active,nil
}

func (b *Bus) probe(ctx context.Context,cardURL,endpoint string)bool{
	probe,stop:=context.WithTimeout(ctx,5*time.Second)
	defer stop()
	_,_,resolved,err:=b.adapter.Resolve(probe,cardURL)
	return err==nil&&resolved==endpoint
}

// claimRecipient serializes capacity check and durable attempt intent only.
// The network probe runs outside mu, so an unavailable endpoint cannot block
// admission or unrelated workers. A second worker must recheck after its probe.
func (b *Bus) claimRecipient(ctx context.Context,d *Delivery,index int)(ready,busy bool,err error){
	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx.Err()!=nil{return false,false,ctx.Err()}
	active,err:=b.activeEndpoints(d.ID);if err!=nil{return false,false,err}
	if active[d.Endpoint]{return false,true,nil}
	d.Attempts++
	if err=b.save(d,"attempting","");err!=nil{return false,false,err}
	if d.Kind=="dispatch"{b.poolCursor[d.Target]=(index+1)%len(d.Members)}
	return true,false,nil
}

func (b *Bus) prepareRecipient(ctx context.Context,d *Delivery)(ready,busy bool,err error){
	b.mu.Lock()
	active,err:=b.activeEndpoints(d.ID)
	start:=0
	if len(d.Members)>0{start=b.poolCursor[d.Target]%len(d.Members)}
	b.mu.Unlock()
	if err!=nil{return false,false,err}
	if d.Kind!="dispatch"{
		if active[d.Endpoint]{return false,true,nil}
		if !b.probe(ctx,d.CardURL,d.Endpoint){return false,false,nil}
		return b.claimRecipient(ctx,d,0)
	}
	if len(d.Members)==0{return false,false,nil}
	for i:=0;i<len(d.Members);i++{
		if ctx.Err()!=nil{return false,false,ctx.Err()}
		index:=(start+i)%len(d.Members)
		member:=d.Members[index]
		a,e:=b.Agent(member)
		if errors.Is(e,ErrNotFound){continue};if e!=nil{return false,false,e}
		if active[a.Endpoint]{busy=true;continue}
		if !b.probe(ctx,a.CardURL,a.Endpoint){continue}
		candidate:=*d
		candidate.AssignedAgent=member
		candidate.Endpoint=a.Endpoint
		candidate.CardURL=a.CardURL
		ok,occupied,e:=b.claimRecipient(ctx,&candidate,index)
		if e!=nil{return false,false,e}
		if ok{*d=candidate;return true,false,nil}
		busy=busy||occupied
	}
	return false,busy,nil
}
