package core

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// terminalState classifies individual deliveries, including outcomes requiring
// operator intervention. A partial broadcast is a derived aggregate, not a
// terminal state of an individual delivery.
func terminalState(s string)bool{
	switch s{case "completed","failed","uncertain","input-required","auth-required":return true}
	return false
}

// pendingState includes unfinished broadcast admission. Unknown states are
// deliberately not treated as pending or terminal.
func pendingState(state string)bool{
	switch state{case "preparing","queued","attempting","accepted","fanout":return true}
	return false
}

func terminalOrPartial(s string)bool{return s=="partial"||terminalState(s)}

type Delivery struct {
	Kind string `json:"kind,omitempty"`
	ParentID string `json:"parentId,omitempty"`
	Members []string `json:"members,omitempty"`
	AssignedAgent string `json:"assignedAgent,omitempty"`
	Children []ChildDelivery `json:"children,omitempty"`
	ID string `json:"id"`
	Caller string `json:"caller,omitempty"` // immutable authenticated agent ID; empty for operator/local submissions
	IngressMessageID string `json:"ingressMessageId,omitempty"`
	Target string `json:"target"`
	Text string `json:"text"`
	Endpoint string `json:"endpoint"`
	CardURL string `json:"cardUrl"`
	State string `json:"state"`
	Attempts int `json:"attempts"`
	Polls int `json:"polls"` // cumulative status lookup attempts, never a lifetime budget
	PollFailures int `json:"pollFailures,omitempty"` // consecutive failed lookups
	LastObservedAt *time.Time `json:"lastObservedAt,omitempty"` // last valid downstream task snapshot
	TaskID a2a.TaskID `json:"taskId,omitempty"`
	ContextID string `json:"contextId,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Detail string `json:"detail,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ChildDelivery struct {
	ID string `json:"id"`
	Target string `json:"target"`
	State string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

func(b *Bus) Delivery(id string)(Delivery,error){
	if !validID.MatchString(id){return Delivery{},ErrInvalid}
	d,err:=b.rawDelivery(id)
	if err==nil&&d.Kind=="broadcast"{return b.broadcastView(d)}
	return d,err
}

func(b *Bus) rawDelivery(id string)(Delivery,error){
	d,_,err:=read[Delivery](b.store.Deliveries,id);return d,err
}

func(b *Bus) save(d *Delivery,state,detail string)error{
	prev:=d.State
	d.State=state;d.Detail=detail;d.UpdatedAt=time.Now().UTC()
	raw,err:=json.Marshal(d);if err!=nil{return err}
	_,err=b.store.Deliveries.Put(d.ID,raw)
	if err==nil&&state!=prev{b.emit("delivery.state",d.ID,state,d.Caller)}
	return err
}

// DeliverySummary is the metadata-only view used by the live dashboard.
// Prompt and response bodies are returned only by explicit detail lookup.
type DeliverySummary struct {
	ID string `json:"id"`
	Kind string `json:"kind"`
	Target string `json:"target"`
	State string `json:"state"`
	ParentID string `json:"parentId,omitempty"`
	AssignedAgent string `json:"assignedAgent,omitempty"`
	Children []ChildDelivery `json:"children,omitempty"`
	Attempts int `json:"attempts"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// RecentDeliveries returns at most 100 recent parent/direct/pool records.
// Broadcast children are represented within their parent; use Delivery for
// the original child record. This demo uses a KV scan, not a search index.
func (b *Bus) RecentDeliveries(limit int) ([]DeliverySummary,error){
	if limit<1||limit>100{return nil,ErrInvalid}
	ks,err:=keys(b.store.Deliveries);if err!=nil{return nil,err}
	out:=make([]DeliverySummary,0,len(ks))
	for _,id:=range ks{
		d,err:=b.rawDelivery(id);if err!=nil{return nil,err}
		if d.ParentID!=""{continue}
		if d.Kind=="broadcast"{d,err=b.broadcastView(d);if err!=nil{return nil,err}}
		kind:=d.Kind;if kind==""{kind="send"}
		children:=make([]ChildDelivery,len(d.Children))
		for i,c:=range d.Children{children[i]=ChildDelivery{ID:c.ID,Target:c.Target,State:c.State}}
		out=append(out,DeliverySummary{ID:d.ID,Kind:kind,Target:d.Target,State:d.State,AssignedAgent:d.AssignedAgent,Children:children,Attempts:d.Attempts,UpdatedAt:d.UpdatedAt})
	}
	sort.Slice(out,func(i,j int)bool{
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt){return out[i].ID<out[j].ID}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out)>limit{out=out[:limit]}
	return out,nil
}
