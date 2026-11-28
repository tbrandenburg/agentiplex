package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

type Collection struct {
	ID string `json:"id"`
	Kind string `json:"kind"`
	Members []string `json:"members"`
	UpdatedAt time.Time `json:"updatedAt"`
}

const maxCollections = 32
const maxMembers = 32

// SetCollection creates or replaces a group or pool of registered logical IDs.
// Each submission snapshots membership; edits affect only future submissions.
func (b *Bus) SetCollection(ctx context.Context, kind,id string,members []string)(Collection,error){
	if (kind!="group"&&kind!="pool")||!validID.MatchString(id)||len(members)==0||len(members)>maxMembers{return Collection{},ErrInvalid}
	members=append([]string(nil),members...)
	sort.Strings(members)
	b.mu.Lock()
	defer b.mu.Unlock()
	for i,m:=range members{
		if i>0&&members[i-1]==m{return Collection{},fmt.Errorf("%w: duplicate member",ErrInvalid)}
		if _,err:=b.Agent(m);err!=nil{return Collection{},err}
	}
	_,err:=b.Collection(kind,id)
	if errors.Is(err,ErrNotFound){
		all,e:=b.Collections(kind);if e!=nil{return Collection{},e}
		if len(all)>=maxCollections{return Collection{},fmt.Errorf("%w: collection limit",ErrInvalid)}
	}else if err!=nil{return Collection{},err}
	if kind=="pool"{
		if err=b.ensurePool(id);err!=nil{return Collection{},err}
	}
	c:=Collection{ID:id,Kind:kind,Members:members,UpdatedAt:time.Now().UTC()}
	raw,err:=json.Marshal(c);if err!=nil{return Collection{},err}
	if _,err=b.store.Collections.Put(kind+"."+id,raw);err!=nil{return Collection{},err}
	b.emit(kind+".configured",id,"","")
	return c,nil
}

func (b *Bus) Collection(kind,id string)(Collection,error){
	if (kind!="group"&&kind!="pool")||!validID.MatchString(id){return Collection{},ErrInvalid}
	c,_,err:=read[Collection](b.store.Collections,kind+"."+id)
	return c,err
}

func (b *Bus) Collections(kind string)([]Collection,error){
	if kind!="group"&&kind!="pool"{return nil,ErrInvalid}
	ks,err:=keys(b.store.Collections);if err!=nil{return nil,err}
	sort.Strings(ks)
	out:=[]Collection{}
	prefix:=kind+"."
	for _,k:=range ks{
		if len(k)<=len(prefix)||k[:len(prefix)]!=prefix{continue}
		c,err:=b.Collection(kind,k[len(prefix):]);if err!=nil{return nil,err}
		out=append(out,c)
	}
	return out,nil
}

// ensurePool requires the admission lock (or startup before workers).
func (b *Bus) ensurePool(id string)error{
	if b.poolConsumers[id]!=nil{return nil}
	sub,err:=b.store.PoolConsumer(id);if err!=nil{return err}
	b.poolConsumers[id]=sub
	return nil
}

func (b *Bus) restorePools()error{
	pools,err:=b.Collections("pool");if err!=nil{return err}
	for _,p:=range pools{if err=b.ensurePool(p.ID);err!=nil{return err}}
	return nil
}
