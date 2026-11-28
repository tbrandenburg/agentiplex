package core

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/nats-io/nats.go"
)

func (b *Bus) workSubscriptions() []*nats.Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids:=make([]string,0,len(b.poolConsumers))
	for id:=range b.poolConsumers{ids=append(ids,id)}
	sort.Strings(ids)
	out:=[]*nats.Subscription{b.store.Work}
	for _,id:=range ids{out=append(out,b.poolConsumers[id])}
	return out
}

func(b *Bus) work(ctx context.Context){
	// One fair-ish fetch loop, bounded concurrent HTTP operations. Reserve a
	// slot before fetching so no unleased work waits in a local queue.
	slots:=make(chan struct{},b.cfg.Workers)
	var running sync.WaitGroup
	defer running.Wait()
	for ctx.Err()==nil{
		subs:=b.workSubscriptions()
		wait:=time.Second
		if len(subs)>1{wait=100*time.Millisecond}
		for _,sub:=range subs{
			select{case<-ctx.Done():return;case slots<-struct{}{}:}
			if ctx.Err()!=nil{<-slots;return}
			msgs,err:=sub.Fetch(1,nats.MaxWait(wait))
			if err!=nil||len(msgs)==0{
				<-slots
				if err!=nil&&!errors.Is(err,nats.ErrTimeout){slog.Warn("delivery fetch failed");select{case<-ctx.Done():return;case<-time.After(time.Second):}}
				continue
			}
			m:=msgs[0]
			id:=string(m.Data)
			b.mu.Lock()
			if b.inflight[id]{
				// A redelivery can arrive during a long call or broker reconnect.
				// Leave settlement to its current local owner.
				b.mu.Unlock();<-slots;continue
			}
			b.inflight[id]=true
			b.mu.Unlock()
			running.Add(1)
			go func(){
				defer running.Done()
				defer func(){b.mu.Lock();delete(b.inflight,id);b.mu.Unlock();<-slots}()
				b.processWithLease(ctx,m)
			}()
		}
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
	b.laterAfter(m,d,state,detail,b.cfg.RetryDelay)
}

func(b *Bus) laterAfter(m *nats.Msg,d *Delivery,state,detail string,delay time.Duration){
	if err:=b.save(d,state,detail);err!=nil{slog.Warn("delivery state persistence failed","id",d.ID)}
	_ = m.NakWithDelay(delay)
}

// Polling is a read-only observation, not a new execution attempt.
// Back off consecutive failures up to a minute without imposing a task lifetime.
func(c Config) pollDelay(failures int)time.Duration{
	delay:=c.TaskPollInterval
	if delay==0{delay=c.RetryDelay}
	for i:=0;i<failures&&delay<time.Minute;i++{
		delay*=2
		if delay>time.Minute{delay=time.Minute}
	}
	return delay
}

func(b *Bus) process(ctx context.Context,m *nats.Msg){
	b.mu.Lock()
	d,err:=b.rawDelivery(string(m.Data))
	b.mu.Unlock()
	if errors.Is(err,ErrNotFound){_ = m.Term();return}
	if err!=nil{_ = m.NakWithDelay(b.cfg.RetryDelay);return}
	if terminalState(d.State){_ = m.AckSync();return}
	switch d.State{
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
	// Probe only before submission. A dispatch may choose another member
	// until attempt intent is persisted; never reassign an accepted or
	// ambiguous request.
	ready,busy,err:=b.prepareRecipient(ctx,&d)
	if ctx.Err()!=nil{_ = m.Nak();return}
	if err!=nil{_ = m.NakWithDelay(b.cfg.RetryDelay);return}
	if !ready{
		if busy{_ = m.NakWithDelay(b.cfg.RetryDelay);return}
		d.Attempts++
		if d.Attempts>=b.cfg.MaxAttempts{b.settle(m,&d,"failed","no reachable eligible target before send; retry limit reached")}else{b.later(m,&d,"queued","no reachable eligible target before send")}
		return
	}
	// prepareRecipient has already atomically checked capacity and persisted
	// attempting under mu. Never send before durable attempt intent.
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
	d.PollFailures=0
	now:=time.Now().UTC();d.LastObservedAt=&now
	d.Result,_=json.Marshal(t)
	switch t.Status.State{
	case a2a.TaskStateCompleted:b.settle(m,d,"completed","")
	case a2a.TaskStateFailed,a2a.TaskStateCanceled,a2a.TaskStateRejected:b.settle(m,d,"failed",string(t.Status.State))
	case a2a.TaskStateInputRequired:b.settle(m,d,"input-required","operator action required at target")
	case a2a.TaskStateAuthRequired:b.settle(m,d,"auth-required","target authentication required")
	case a2a.TaskStateSubmitted,a2a.TaskStateWorking:
		b.laterAfter(m,d,"accepted","downstream task active",b.cfg.pollDelay(0))
	default:b.settle(m,d,"uncertain","unknown downstream task state")
	}
}

func(b *Bus) poll(ctx context.Context,m *nats.Msg,d *Delivery){
	if ctx.Err()!=nil{_ = m.Nak();return}
	// Each lookup has a transport deadline; accepted execution does not.
	call,cancel:=context.WithTimeout(ctx,b.cfg.Timeout)
	t,err:=b.adapter.Task(call,d.Endpoint,d.TaskID);cancel()
	if ctx.Err()!=nil{_ = m.Nak();return}
	d.Polls++
	if errors.Is(err,a2a.ErrTaskNotFound)||(err==nil&&t!=nil&&t.ID!=d.TaskID){
		b.settle(m,d,"uncertain","remote task lost; outcome unknown; no automatic replay");return
	}
	if err!=nil||t==nil{
		// Never discard the known task handle or release its endpoint merely
		// because observation failed. Retain the last valid snapshot, and
		// retry only GetTask (never SendMessage or pool reassignment).
		if d.PollFailures<63{d.PollFailures++}
		detail:="task status unavailable; monitoring continues"
		if err==nil{detail="task status invalid; monitoring continues"}
		b.laterAfter(m,d,"accepted",detail,b.cfg.pollDelay(d.PollFailures))
		return
	}
	b.recordTask(m,d,t)
}
