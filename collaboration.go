package agentiplex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func scopedID(prefix,key string)string{
	if key==""{return prefix+a2a.NewMessageID()}
	h:=sha256.Sum256([]byte(key))
	return prefix+hex.EncodeToString(h[:30])
}

func childID(parent,member string)string{
	return scopedID("bc-",parent+"\x00"+member)
}

func (b *Bus) Broadcast(ctx context.Context,group,text,key string)(Delivery,error){
	if len(key)>256{return Delivery{},ErrInvalid}
	return b.submitCollection(ctx,"broadcast",group,text,scopedID("bg-",key),"")
}

func (b *Bus) Dispatch(ctx context.Context,pool,text,key string)(Delivery,error){
	if len(key)>256{return Delivery{},ErrInvalid}
	return b.submitCollection(ctx,"dispatch",pool,text,scopedID("dp-",key),"")
}

func (b *Bus) submitCollection(ctx context.Context,kind,target,text,id,messageID string)(Delivery,error){
	if !validID.MatchString(id)||!validID.MatchString(target)||strings.TrimSpace(text)==""||len(text)>64<<10{return Delivery{},ErrInvalid}
	if kind!="broadcast"&&kind!="dispatch"{return Delivery{},ErrInvalid}
	b.mu.Lock()
	d,err:=b.rawDelivery(id)
	if err==nil{
		if d.Kind!=kind||d.Target!=target||d.Text!=text||d.IngressMessageID!=messageID{b.mu.Unlock();return Delivery{},ErrConflict}
	}else if errors.Is(err,ErrNotFound){
		collectionKind:="group"
		if kind=="dispatch"{collectionKind="pool"}
		c,e:=b.Collection(collectionKind,target)
		if e!=nil{b.mu.Unlock();return Delivery{},e}
		d=Delivery{ID:id,Kind:kind,Target:target,Text:text,Members:c.Members,IngressMessageID:messageID,State:"preparing",UpdatedAt:time.Now().UTC()}
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
		_,err:=b.sendChild(ctx,member,d.Text,childID(d.ID,member),"",d.ID)
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
	for{
		if ctx.Err()!=nil{return}
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
			switch child.State{
			case "completed":complete++
			case "failed","uncertain","input-required","auth-required":failed++
			case "preparing","queued":pending++
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
// Only the single execution loop sends work. Persisted accepted records also
// reserve capacity across daemon restart.
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

// prepareRecipient is called exclusively by the execution loop.
func (b *Bus) prepareRecipient(ctx context.Context,d *Delivery)(ready,busy bool,err error){
	active,err:=b.activeEndpoints(d.ID);if err!=nil{return false,false,err}
	if d.Kind!="dispatch"{
		if active[d.Endpoint]{return false,true,nil}
		return b.probe(ctx,d.CardURL,d.Endpoint),false,nil
	}
	// Round-robin selection among snapshot members, skipping unavailable or
	// already occupied endpoints. Assignment becomes durable with attempting.
	if len(d.Members)==0{return false,false,nil}
	start:=b.poolCursor[d.Target]%len(d.Members)
	for i:=0;i<len(d.Members);i++{
		if ctx.Err()!=nil{return false,false,ctx.Err()}
		index:=(start+i)%len(d.Members)
		member:=d.Members[index]
		a,e:=b.Agent(member)
		if errors.Is(e,ErrNotFound){continue};if e!=nil{return false,false,e}
		if active[a.Endpoint]{busy=true;continue}
		if !b.probe(ctx,a.CardURL,a.Endpoint){continue}
		d.AssignedAgent=member
		d.Endpoint=a.Endpoint
		d.CardURL=a.CardURL
		b.poolCursor[d.Target]=(index+1)%len(d.Members)
		return true,false,nil
	}
	return false,busy,nil
}
