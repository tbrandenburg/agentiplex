package core

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "log/slog"
 "strings"
 "time"

 "github.com/tbrandenburg/agentiplex/internal/security"
)

// FollowOptions bounds a durable logical request, independently of any waiter.
// Zero uses the documented finite defaults, except NudgeSeconds (disabled).
type FollowOptions struct {
 MaxTurns int `json:"maxTurns"`
 TimeoutSeconds int `json:"timeoutSeconds"`
 NudgeSeconds int `json:"nudgeSeconds"`
}

type FollowThrough struct {
 Options FollowOptions `json:"options"`
 StartedAt time.Time `json:"startedAt"`
 Deadline time.Time `json:"deadline"`
 Turns []string `json:"turns"`
 TurnReason string `json:"turnReason,omitempty"`
 Resolution *FollowResolution `json:"resolution,omitempty"`
}

type FollowResolution struct {
 ActivationID string `json:"activationId"`
 Outcome string `json:"outcome"`
 Text string `json:"text"`
}

func normalizeFollow(o FollowOptions) (FollowOptions,error) {
 if o.MaxTurns==0 { o.MaxTurns=20 }
 if o.TimeoutSeconds==0 { o.TimeoutSeconds=7200 }
 if o.MaxTurns<1 || o.MaxTurns>100 || o.TimeoutSeconds<1 || o.TimeoutSeconds>86400 || o.NudgeSeconds<0 || o.NudgeSeconds>86400 { return o,ErrInvalid }
 return o,nil
}

// SendUntilDone admits a logical request. It is intentionally limited to a
// dedicated, initially idle daemon: ordinary delegated calls do not yet carry
// a request correlation ID. One active request owns this conservative scope.
func (b *Bus) SendUntilDone(ctx context.Context,target,text,key string,o FollowOptions)(Delivery,error) {
 o,err:=normalizeFollow(o)
 if err!=nil || !validKey(key) || strings.TrimSpace(text)=="" || len(text)>60<<10 { return Delivery{},ErrInvalid }
 if p,ok:=security.PrincipalFrom(ctx);ok && p.Role!=security.RoleOperator { return Delivery{},ErrInvalid }
 id:=sendID(callerID(ctx),key)
 b.mu.Lock();defer b.mu.Unlock()
 old,err:=b.rawDelivery(id)
 if err==nil {
  if old.Follow==nil || old.Kind!="until-done" || old.Caller!=callerID(ctx) || old.Target!=target || old.Text!=text || old.Follow.Options!=o {return Delivery{},ErrConflict}
  b.wakeFollow()
  return old,nil
 }
 if !errors.Is(err,ErrNotFound){return Delivery{},err}
 ks,err:=keys(b.store.Deliveries);if err!=nil{return Delivery{},err}
 for _,k:=range ks {
  d,e:=b.rawDelivery(k);if e!=nil{return Delivery{},e}
  if d.Kind=="broadcast" {d,e=b.broadcastView(d);if e!=nil{return Delivery{},e}}
  if !terminalOrPartial(d.State) || d.State=="uncertain" {return Delivery{},fmt.Errorf("%w: until-done requires an idle dedicated daemon without uncertain deliveries",ErrConflict)}
 }
 a,err:=b.Agent(target);if err!=nil{return Delivery{},err}
 now:=time.Now().UTC()
 d:=Delivery{ID:id,Kind:"until-done",Caller:callerID(ctx),Target:target,Text:text,Endpoint:a.Endpoint,CardURL:a.CardURL,State:"continuing",UpdatedAt:now,Follow:&FollowThrough{Options:o,StartedAt:now,Deadline:now.Add(time.Duration(o.TimeoutSeconds)*time.Second),Turns:[]string{}}}
 raw,err:=json.Marshal(d);if err!=nil{return Delivery{},err}
 if _,err=b.store.Deliveries.Create(id,raw);err!=nil{return Delivery{},err}
 b.followID=id
 b.emit("delivery.state",id,d.State,d.Caller)
 b.wakeFollow()
 return d,nil
}

func (b *Bus) wakeFollow() {
 select {case b.followWake<-struct{}{}:default:}
}

// Called under admission lock. Direct messages to the reserved coordinator
// (including endpoint aliases) and collection fanout are not supported while
// a dedicated follow-through request is open. Other direct worker calls use
// the normal durable path.
func (b *Bus) guardFollow(target,endpoint string,collection bool)error {
 if b.followID==""{return nil}
 d,err:=b.rawDelivery(b.followID);if err!=nil{return err}
 if d.State=="continuing" && (collection || d.Target==target || d.Endpoint==endpoint) {
  return fmt.Errorf("%w: active until-done request reserves coordinator and dedicated scope",ErrConflict)
 }
 return nil
}

// ResolveFollow records a proposal from the currently executing coordinator.
// The proposal is not final until that activation and all known work finish.
// Trusted-local mode cannot attest actor identity; secure mode requires the
// registered recipient's own credential, not an operator or another agent.
func (b *Bus) ResolveFollow(ctx context.Context,id,activation,outcome,text string)(Delivery,error) {
 if !validID.MatchString(id)||!validID.MatchString(activation)||(outcome!="done"&&outcome!="blocked")||strings.TrimSpace(text)==""||len(text)>16<<10{return Delivery{},ErrInvalid}
 b.mu.Lock();defer b.mu.Unlock()
 d,err:=b.rawDelivery(id);if err!=nil{return Delivery{},err}
 if d.Follow==nil{return Delivery{},ErrNotFound}
 if p,ok:=security.PrincipalFrom(ctx);ok&&(p.Role!=security.RoleAgent||p.AgentID!=d.Target){return Delivery{},ErrNotFound}
 proposal:=FollowResolution{ActivationID:activation,Outcome:outcome,Text:text}
 if d.Follow.Resolution!=nil {
  if *d.Follow.Resolution!=proposal{return Delivery{},ErrConflict}
  return d,nil
 }
 turns:=d.Follow.Turns
 if d.State!="continuing"||len(turns)==0||turns[len(turns)-1]!=activation{return Delivery{},ErrConflict}
 a,err:=b.rawDelivery(activation);if err!=nil{return Delivery{},err}
 if a.State!="attempting"&&a.State!="accepted"{return Delivery{},ErrConflict}
 d.Follow.Resolution=&proposal
 if err=b.save(&d,d.State,"resolution recorded; awaiting execution completion");err!=nil{return Delivery{},err}
 b.wakeFollow()
 return d,nil
}

// NudgeFollow is a safe reconsideration, not a bypass of active/uncertain
// work, the turn budget or the deadline. The normal event path uses the same
// admission function; repeated nudges cannot create competing turns.
func (b *Bus) NudgeFollow(ctx context.Context,id string)(Delivery,error) {
 if _,err:=b.DeliveryFor(ctx,id);err!=nil{return Delivery{},err}
 b.mu.Lock();defer b.mu.Unlock()
 d,err:=b.rawDelivery(id);if err!=nil{return Delivery{},err}
 if d.Follow==nil{return Delivery{},ErrInvalid}
 if err=b.advanceFollow(ctx,&d,time.Now().UTC(),"manual nudge");err!=nil{return Delivery{},err}
 return d,nil
}

// The only in-memory index is reconstructed on startup. A wake is a hint;
// periodic reconciliation recovers missed events and enforces deadlines.
func (b *Bus) restoreFollow()error {
 ks,err:=keys(b.store.Deliveries);if err!=nil{return err}
 for _,id:=range ks {
  d,e:=b.rawDelivery(id);if e!=nil{return e}
  if d.Kind=="until-done"&&d.State=="continuing" {
   if b.followID!=""||d.Follow==nil{return fmt.Errorf("invalid persisted follow-through scope")}
   b.followID=id
  }
 }
 return nil
}

func (b *Bus) followLoop(ctx context.Context) {
 ticker:=time.NewTicker(500*time.Millisecond)
 defer ticker.Stop()
 for ctx.Err()==nil {
  b.reconcileFollows(ctx)
  select {case <-ctx.Done():return;case <-ticker.C:case <-b.followWake:}
 }
}

func (b *Bus) reconcileFollows(ctx context.Context) {
 b.mu.Lock();defer b.mu.Unlock()
 if b.followID==""{return}
 d,err:=b.rawDelivery(b.followID)
 if err!=nil{slog.Warn("follow-through record unavailable");return}
 if d.State!="continuing"{b.followID="";return}
 if err=b.advanceFollow(ctx,&d,time.Now().UTC(),"continuation");err!=nil{slog.Warn("follow-through reconciliation pending","id",d.ID)}
 if terminalState(d.State){ // confirm persistence before releasing the cached reservation
  if current,e:=b.rawDelivery(d.ID);e==nil&&terminalState(current.State){b.followID=""}
 }
}

// advanceFollow runs only under the single-daemon admission mutex. Persist
// each deterministic child intent in the parent before creating/publishing it.
// A crash anywhere in that sequence recovers the SAME child, never a new turn.
func (b *Bus) advanceFollow(ctx context.Context,d *Delivery,now time.Time,reason string)error {
 if d.State!="continuing"{return nil}
 f:=d.Follow
 if f==nil{return ErrInvalid}
 if !now.Before(f.Deadline){return b.save(d,"input-required","continuation deadline exhausted; inspect existing work")}
 a,err:=b.Agent(d.Target)
 if errors.Is(err,ErrNotFound)||(err==nil&&(a.Endpoint!=d.Endpoint||a.CardURL!=d.CardURL)){return b.save(d,"input-required","coordinator registration changed")}
 if err!=nil{return err}
 var last Delivery
 if n:=len(f.Turns);n>0 {
  last,err=b.rawDelivery(f.Turns[n-1])
  if errors.Is(err,ErrNotFound){return b.admitFollowTurn(ctx,d,n)}
  if err!=nil{return err}
  if last.ParentID!=d.ID{return b.save(d,"uncertain","invalid continuation child")}
  if last.State=="preparing"{return b.publish(ctx,&last)}
  if pendingState(last.State){return nil}
  if last.State!="completed" {
   state:=last.State
   if !terminalState(state){state="uncertain"}
   return b.save(d,state,"coordinator activation did not complete: "+last.ID)
  }
 }
 // The scope is deliberately the entire dedicated daemon since admission.
 // Check actual records, not the truncated dashboard view. All admissions
 // serialize with this check. Do not equate transport completion with
 // application acceptance.
 ks,err:=keys(b.store.Deliveries);if err!=nil{return err}
 pending:=false
 owned:=map[string]bool{}
 for _,id:=range f.Turns{owned[id]=true}
 for _,id:=range ks {
  if id==d.ID||owned[id]{continue}
  v,e:=b.rawDelivery(id);if e!=nil{return e}
  if v.UpdatedAt.Before(f.StartedAt){continue}
  if v.Target==d.Target||v.Endpoint==d.Endpoint{return b.save(d,"input-required","unexpected coordinator activation: "+id)}
  if v.Kind=="broadcast"{v,e=b.broadcastView(v);if e!=nil{return e}}
  if pendingState(v.State){pending=true;continue}
  if v.State!="completed"{return b.save(d,"input-required","unresolved delivery in dedicated scope: "+id)}
 }
 if pending{return nil}
 if p:=f.Resolution;p!=nil {
  d.Result,_=json.Marshal(map[string]any{"parts":[]map[string]string{{"text":p.Text}}})
  if p.Outcome=="blocked"{return b.save(d,"input-required",p.Text)}
  return b.save(d,"completed","explicit resolution; dedicated scope quiescent")
 }
 if len(f.Turns)>=f.Options.MaxTurns{return b.save(d,"input-required","continuation turn budget exhausted")}
 // A small fixed floor avoids a hot loop on instant responses. No inactivity
 // timer is required for normal continuation; the periodic reconciliation
 // also covers missed events and durable deadlines. Optional watchdog interval
 // is reflected in the activation reason if the last turn has stalled that long.
 if !last.UpdatedAt.IsZero()&&now.Sub(last.UpdatedAt)<250*time.Millisecond{return nil}
 n:=len(f.Turns)+1
 if n==1{reason="initial activation"}else if reason=="continuation"&&f.Options.NudgeSeconds>0&&now.Sub(last.UpdatedAt)>=time.Duration(f.Options.NudgeSeconds)*time.Second{reason="inactivity watchdog"}
 f.TurnReason=reason
 f.Turns=append(f.Turns,scopedID("ft-","",fmt.Sprintf("%s:%d",d.ID,n)))
 if err=b.save(d,"continuing","activation intent recorded");err!=nil{return err}
 return b.admitFollowTurn(ctx,d,n)
}

func (b *Bus) admitFollowTurn(ctx context.Context,d *Delivery,n int)error {
 id:=d.Follow.Turns[n-1]
 text:=fmt.Sprintf("Agentiplex until-done request %s; activation %s; turn %d (%s). This is a continuation of the SAME objective, not permission to replay completed or uncertain work. Read persisted progress before acting. When the objective is fulfilled or blocked, call agentiplex_resolve_request with requestId, activationId, outcome (done or blocked), and result; otherwise return after a bounded turn. Do not schedule yourself.\n\nOriginal objective:\n%s",d.ID,id,n,d.Follow.TurnReason,d.Text)
 child:=Delivery{ID:id,ParentID:d.ID,Caller:d.Caller,Target:d.Target,Text:text,Endpoint:d.Endpoint,CardURL:d.CardURL,State:"preparing",UpdatedAt:time.Now().UTC()}
 raw,err:=json.Marshal(child);if err!=nil{return err}
 if _,err=b.store.Deliveries.Create(id,raw);err!=nil{return err}
 return b.publish(ctx,&child)
}

func (b *Bus) followView(d Delivery)(Delivery,error) {
 if d.Follow==nil{return d,ErrInvalid}
 d.Children=make([]ChildDelivery,0,len(d.Follow.Turns))
 for _,id:=range d.Follow.Turns {
  c,e:=b.rawDelivery(id)
  if errors.Is(e,ErrNotFound){d.Children=append(d.Children,ChildDelivery{ID:id,Target:d.Target,State:"preparing"});continue}
  if e!=nil{return Delivery{},e}
  d.Children=append(d.Children,ChildDelivery{ID:id,Target:c.Target,State:c.State,Detail:c.Detail})
 }
 return d,nil
}
