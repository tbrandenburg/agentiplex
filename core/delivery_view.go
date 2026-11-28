package core

import (
	"sort"
	"time"
)

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
