package core

import (
	"context"

	"github.com/tbrandenburg/agentiplex/internal/security"
)

// callerID is the authenticated agent ID, or "" for operator and local callers.
func callerID(ctx context.Context)string{
	if p,ok:=security.PrincipalFrom(ctx);ok&&p.Role==security.RoleAgent{return p.AgentID}
	return ""
}

// authorAs is the identity recorded on notes: the agent ID, "operator", or "" locally.
func authorAs(ctx context.Context)string{
	p,ok:=security.PrincipalFrom(ctx)
	if !ok{return ""}
	if p.Role==security.RoleAgent{return p.AgentID}
	return p.Role
}

// scopedKey separates request identities by caller. IDs never contain NUL.
func scopedKey(caller,key string)string{return caller+"\x00"+key}

// DeliveryFor returns a delivery only if the principal may read it: named
// agents see only their own submissions (and broadcast children); everyone
// else is trusted. Foreign records are reported as not found.
func (b *Bus) DeliveryFor(ctx context.Context,id string)(Delivery,error){
	d,err:=b.Delivery(id)
	if err!=nil{return Delivery{},err}
	if c:=callerID(ctx);c!=""&&d.Caller!=c{return Delivery{},ErrNotFound}
	return d,nil
}
