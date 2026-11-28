package core

import (
	"context"
	"fmt"
)

// AbandonTask stops monitoring a known accepted task and releases its local
// reservation. The operator must independently establish that the remote
// execution has stopped; Agentiplex cannot verify or cancel it here.
// Keep the task handle and last result, report uncertainty, never replay.
func (b *Bus) AbandonTask(ctx context.Context,id,taskID string,remoteStopped bool)(Delivery,error){
	if !validID.MatchString(id)||taskID==""||!remoteStopped{return Delivery{},ErrInvalid}
	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx.Err()!=nil{return Delivery{},ctx.Err()}
	d,err:=b.rawDelivery(id);if err!=nil{return Delivery{},err}
	if b.inflight[id]{return Delivery{},fmt.Errorf("%w: delivery currently being observed; retry after the current operation",ErrConflict)}
	if d.State!="accepted"||d.Kind=="broadcast"||string(d.TaskID)!=taskID{return Delivery{},fmt.Errorf("%w: expected accepted downstream task",ErrConflict)}
	if err=b.save(&d,"uncertain","operator abandoned monitoring after confirming remote stopped; outcome unknown; no automatic replay");err!=nil{return Delivery{},err}
	// The next broker redelivery sees the terminal record and acknowledges it.
	b.emit("delivery.abandoned",id,d.State,d.Caller)
	return d,nil
}
