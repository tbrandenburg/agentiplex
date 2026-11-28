package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// Send accepts text in the foundation. The caller gets a receipt only after
// JetStream confirms publication. The idempotency key is required: it names one
// logical operation per caller, and the same key with the same target and text
// returns the same delivery while the record is retained.
func(b *Bus) Send(ctx context.Context,target,text,key string)(Delivery,error){
	if !validKey(key){return Delivery{},ErrInvalid}
	return b.sendWithID(ctx,target,text,sendID(callerID(ctx),key),"")
}

func sendID(caller,key string)string {
 h:=sha256.Sum256([]byte(scopedKey(caller,key)))
 return hex.EncodeToString(h[:])
}

func validKey(key string)bool{return strings.TrimSpace(key)!=""&&len(key)<=256}

// sendWithID shares the durable admission path with native A2A ingress.
func(b *Bus) sendWithID(ctx context.Context,target,text,id,ingressMessageID string)(Delivery,error){
	return b.sendChild(ctx,target,text,id,ingressMessageID,"",callerID(ctx))
}

func(b *Bus) sendChild(ctx context.Context,target,text,id,ingressMessageID,parent,caller string)(Delivery,error){
	if strings.TrimSpace(text)==""||len(text)>64<<10||!validID.MatchString(id){return Delivery{},ErrInvalid}
	b.mu.Lock();defer b.mu.Unlock()
	old,err:=b.rawDelivery(id)
	if err==nil{
		if old.Kind!=""||old.ParentID!=parent||old.Caller!=caller||old.Target!=target||old.Text!=text||old.IngressMessageID!=ingressMessageID{return Delivery{},ErrConflict}
		if old.State=="preparing"{err=b.publish(ctx,&old)}
		return old,err
	}
	if !errors.Is(err,ErrNotFound){return Delivery{},err}
	a,err:=b.Agent(target);if err!=nil{return Delivery{},err}
	if err=b.guardFollow(target,a.Endpoint,false);err!=nil{return Delivery{},err}
	d:=Delivery{ID:id,Caller:caller,ParentID:parent,IngressMessageID:ingressMessageID,Target:target,Text:text,Endpoint:a.Endpoint,CardURL:a.CardURL,State:"preparing",UpdatedAt:time.Now().UTC()}
	raw,err:=json.Marshal(d);if err!=nil{return Delivery{},err}
	if _,err=b.store.Deliveries.Create(id,raw);err!=nil{return Delivery{},err}
	err=b.publish(ctx,&d)
	return d,err
}

func(b *Bus) publish(ctx context.Context,d *Delivery)error{
	// Both the durable intent record and a broker publish ACK are required.
	// Recovery republishes preparing records with the same ID.
	subject:=b.store.WorkSubject
	if d.Kind=="dispatch"{subject=b.store.PoolPrefix+d.Target}
	_,err:=b.store.JS.Publish(subject,[]byte(d.ID),nats.MsgId(d.ID),nats.Context(ctx))
	if err!=nil{return err}
	return b.save(d,"queued","")
}

func(b *Bus) recoverPreparing(ctx context.Context)error{
	ks,err:=keys(b.store.Deliveries);if err!=nil{return err}
	for _,id:=range ks{
		d,err:=b.rawDelivery(id);if err!=nil{return err}
		if d.State=="preparing"{if err=b.publish(ctx,&d);err!=nil{return err}}
	}
	return nil
}
