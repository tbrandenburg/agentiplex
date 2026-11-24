package agentiplex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/nats-io/nats.go"
)

func(b *Bus) Delivery(id string)(Delivery,error){
	if !validID.MatchString(id){return Delivery{},ErrInvalid}
	d,_,err:=read[Delivery](b.store.Deliveries,id);return d,err
}

func(b *Bus) save(d *Delivery,state,detail string)error{
	d.State=state;d.Detail=detail;d.UpdatedAt=time.Now().UTC()
	raw,err:=json.Marshal(d);if err!=nil{return err}
	_,err=b.store.Deliveries.Put(d.ID,raw)
	if err==nil{b.emit("delivery.state",d.ID,state)}
	return err
}

// Send accepts text in the foundation. The caller gets a receipt only after
// JetStream confirms publication. A caller retry can reuse its idempotency key.
func(b *Bus) Send(ctx context.Context,target,text,key string)(Delivery,error){
	if len(key)>256{return Delivery{},ErrInvalid}
	id:=a2a.NewMessageID()
	if key!=""{h:=sha256.Sum256([]byte(key));id=hex.EncodeToString(h[:])}
	return b.sendWithID(ctx,target,text,id,"")
}

// sendWithID shares the durable admission path with native A2A ingress.
func(b *Bus) sendWithID(ctx context.Context,target,text,id,ingressMessageID string)(Delivery,error){
	if strings.TrimSpace(text)==""||len(text)>64<<10||!validID.MatchString(id){return Delivery{},ErrInvalid}
	b.mu.Lock();defer b.mu.Unlock()
	old,err:=b.Delivery(id)
	if err==nil{
		if old.Target!=target||old.Text!=text||old.IngressMessageID!=ingressMessageID{return Delivery{},ErrConflict}
		if old.State=="preparing"{err=b.publish(ctx,&old)}
		return old,err
	}
	if !errors.Is(err,ErrNotFound){return Delivery{},err}
	a,err:=b.Agent(target);if err!=nil{return Delivery{},err}
	d:=Delivery{ID:id,IngressMessageID:ingressMessageID,Target:target,Text:text,Endpoint:a.Endpoint,CardURL:a.CardURL,State:"preparing",UpdatedAt:time.Now().UTC()}
	raw,err:=json.Marshal(d);if err!=nil{return Delivery{},err}
	if _,err=b.store.Deliveries.Create(id,raw);err!=nil{return Delivery{},err}
	err=b.publish(ctx,&d)
	return d,err
}

func(b *Bus) publish(ctx context.Context,d *Delivery)error{
	// Both the durable intent record and a broker publish ACK are required.
	// Recovery republishes preparing records with the same ID.
	_,err:=b.store.JS.Publish(b.store.WorkSubject,[]byte(d.ID),nats.MsgId(d.ID),nats.Context(ctx))
	if err!=nil{return err}
	return b.save(d,"queued","")
}

func(b *Bus) recoverPreparing(ctx context.Context)error{
	ks,err:=keys(b.store.Deliveries);if err!=nil{return err}
	for _,id:=range ks{
		d,err:=b.Delivery(id);if err!=nil{return err}
		if d.State=="preparing"{if err=b.publish(ctx,&d);err!=nil{return err}}
	}
	return nil
}

func(b *Bus) work(ctx context.Context){
	for ctx.Err()==nil{
		msgs,err:=b.store.Work.Fetch(1,nats.MaxWait(time.Second))
		if err!=nil{
			if !errors.Is(err,nats.ErrTimeout){slog.Warn("delivery fetch failed");select{case<-ctx.Done():return;case<-time.After(time.Second):}}
			continue
		}
		for _,m:=range msgs{b.processWithLease(ctx,m)}
	}
}

// processWithLease renews the broker lease during long A2A calls. The
// persisted delivery state remains authoritative if the process dies.
func(b *Bus) processWithLease(ctx context.Context,m *nats.Msg){
	done:=make(chan struct{})
	stopped:=make(chan struct{})
	go func(){
		defer close(stopped)
		ticker:=time.NewTicker(2*time.Second)
		defer ticker.Stop()
		for {
			select{
			case <-done:return
			case <-ticker.C:
				if err:=m.InProgress();err!=nil&&!errors.Is(err,nats.ErrMsgAlreadyAckd){
					slog.Warn("delivery lease renewal failed")
				}
			}
		}
	}()
	defer func(){close(done);<-stopped}()
	b.process(ctx,m)
}

func(b *Bus) settle(m *nats.Msg,d *Delivery,state,detail string){
	if err:=b.save(d,state,detail);err!=nil{_ = m.NakWithDelay(b.cfg.RetryDelay);return}
	if err:=m.AckSync();err!=nil{slog.Warn("delivery acknowledgment failed","id",d.ID)}
}

func(b *Bus) later(m *nats.Msg,d *Delivery,state,detail string){
	if err:=b.save(d,state,detail);err!=nil{slog.Warn("delivery state persistence failed","id",d.ID)}
	_ = m.NakWithDelay(b.cfg.RetryDelay)
}

func(b *Bus) process(ctx context.Context,m *nats.Msg){
	b.mu.Lock()
	d,err:=b.Delivery(string(m.Data))
	b.mu.Unlock()
	if err!=nil{_ = m.NakWithDelay(b.cfg.RetryDelay);return}
	switch d.State{
	case "completed","failed","uncertain","input-required","auth-required":
		_ = m.AckSync();return
	case "attempting":
		// A previous process might have sent a prompt before crashing.
		b.settle(m,&d,"uncertain","previous send outcome unknown; no automatic replay");return
	case "accepted":
		b.poll(ctx,m,&d);return
	case "preparing","queued":
	default:
		b.settle(m,&d,"uncertain","unrecognized delivery state");return
	}
	if ctx.Err()!=nil{_ = m.Nak();return}
	// Read-only card resolution is a safe reachability probe. Never retry
	// SendMessage itself after an ambiguous HTTP or SDK failure.
	d.Attempts++
	probe,stop:=context.WithTimeout(ctx,5*time.Second)
	_,_,endpoint,err:=b.adapter.Resolve(probe,d.CardURL);stop()
	if ctx.Err()!=nil{_ = m.Nak();return}
	if err!=nil{
		if d.Attempts>=b.cfg.MaxAttempts{b.settle(m,&d,"failed","target unavailable before send; retry limit reached")}else{b.later(m,&d,"queued","target unavailable before send")}
		return
	}
	if endpoint!=d.Endpoint{b.settle(m,&d,"failed","registered interface changed; register again");return}
	if err=b.save(&d,"attempting","");err!=nil{_ = m.NakWithDelay(b.cfg.RetryDelay);return}
	call,cancel:=context.WithTimeout(ctx,b.cfg.Timeout)
	result,err:=b.adapter.Send(call,d.Endpoint,d.ID,d.Text);cancel()
	if err!=nil{b.settle(m,&d,"uncertain","A2A send outcome unknown; no automatic replay");return}
	switch r:=result.(type){
	case *a2a.Message:
		if r==nil{b.settle(m,&d,"uncertain","empty A2A message");return}
		d.ContextID=r.ContextID
		d.Result,_=json.Marshal(r)
		b.settle(m,&d,"completed","")
	case *a2a.Task:
		b.recordTask(m,&d,r)
	default:
		b.settle(m,&d,"uncertain","unsupported A2A response")
	}
}

func(b *Bus) recordTask(m *nats.Msg,d *Delivery,t *a2a.Task){
	if t==nil||t.ID==""{b.settle(m,d,"uncertain","missing downstream task ID");return}
	d.TaskID=t.ID;d.ContextID=t.ContextID
	d.Result,_=json.Marshal(t)
	switch t.Status.State{
	case a2a.TaskStateCompleted:b.settle(m,d,"completed","")
	case a2a.TaskStateFailed,a2a.TaskStateCanceled,a2a.TaskStateRejected:b.settle(m,d,"failed",string(t.Status.State))
	case a2a.TaskStateInputRequired:b.settle(m,d,"input-required","operator action required at target")
	case a2a.TaskStateAuthRequired:b.settle(m,d,"auth-required","target authentication required")
	case a2a.TaskStateSubmitted,a2a.TaskStateWorking:
		b.later(m,d,"accepted","downstream task active")
	default:b.settle(m,d,"uncertain","unknown downstream task state")
	}
}

func(b *Bus) poll(ctx context.Context,m *nats.Msg,d *Delivery){
	if ctx.Err()!=nil{_ = m.Nak();return}
	d.Polls++
	// Bounded status polling; never send another prompt to recover a task.
	call,cancel:=context.WithTimeout(ctx,b.cfg.Timeout)
	t,err:=b.adapter.Task(call,d.Endpoint,d.TaskID);cancel()
	if err!=nil{
		if d.Polls>=b.cfg.MaxAttempts*6{b.settle(m,d,"uncertain","downstream task status unavailable")}else{b.later(m,d,"accepted","task status lookup failed")}
		return
	}
	if t==nil||t.ID!=d.TaskID{b.settle(m,d,"uncertain","task ID mismatch");return}
	if d.Polls>=b.cfg.MaxAttempts*6&&(t.Status.State==a2a.TaskStateWorking||t.Status.State==a2a.TaskStateSubmitted){
		b.settle(m,d,"uncertain",fmt.Sprintf("task %s still active at polling limit",d.TaskID));return
	}
	b.recordTask(m,d,t)
}
