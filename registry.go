package agentiplex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	cat "github.com/tbrandenburg/agentiplex/internal/catalog"
)

func(b *Bus) Register(ctx context.Context,id,url string)(Agent,error){
	if !validID.MatchString(id){return Agent{},fmt.Errorf("%w: invalid agent ID",ErrInvalid)}
	card,cardURL,endpoint,err:=b.adapter.Resolve(ctx,url)
	if err!=nil{return Agent{},fmt.Errorf("%w: card resolution or validation failed: %v",ErrInvalid,err)}
	now:=time.Now().UTC()
	a:=Agent{ID:id,CardURL:cardURL,Endpoint:endpoint,Card:card,UpdatedAt:now,Reachability:"reachable",ObservedAt:&now}
	raw,err:=json.Marshal(a);if err!=nil{return Agent{},err}
	b.mu.Lock();defer b.mu.Unlock()
	_,err=b.store.Registry.Create(id,raw)
	if errors.Is(err,nats.ErrKeyExists){return Agent{},ErrConflict}
	if err!=nil{return Agent{},err}
	b.emit("agent.registered",id,"","")
	return a,nil
}

func(b *Bus) Agent(id string)(Agent,error){
	if !validID.MatchString(id){return Agent{},ErrInvalid}
	a,_,err:=read[Agent](b.store.Registry,id)
	if a.Reachability==""{a.Reachability="unknown"}
	return a,err
}

func(b *Bus) Agents(skill,tag string)([]Agent,error){return b.Discover(skill,tag,"")}

func(b *Bus) Discover(skill,tag,group string)([]Agent,error){
	members:=map[string]bool{}
	if group!=""{c,err:=b.Collection("group",group);if err!=nil{return nil,err};for _,id:=range c.Members{members[id]=true}}
	ks,err:=keys(b.store.Registry);if err!=nil{return nil,err}
	sort.Strings(ks)
	out:=[]Agent{}
	for _,id:=range ks{
		if group!=""&&!members[id]{continue}
		a,err:=b.Agent(id)
		if errors.Is(err,ErrNotFound){continue}
		if err!=nil{return nil,err}
		if skill!=""||tag!=""{
			found:=false
			for _,s:=range a.Card.Skills{
				if skill!=""&&!strings.EqualFold(s.ID,skill)&&!strings.EqualFold(s.Name,skill){continue}
				if tag!=""{
					ok:=false;for _,t:=range s.Tags{if strings.EqualFold(t,tag){ok=true;break}}
					if !ok{continue}
				}
				found=true;break
			}
			if !found{continue}
		}
		out=append(out,a)
	}
	return out,nil
}

func(b *Bus) Catalog()([]byte,error){
	agents,err:=b.Agents("","");if err!=nil{return nil,err}
	entries:=make([]cat.Entry,0,len(agents))
	for _,a:=range agents{
		tags:=[]string{};seen:=map[string]bool{}
		for _,s:=range a.Card.Skills{for _,t:=range s.Tags{if !seen[t]{seen[t]=true;tags=append(tags,t)}}}
		entries=append(entries,cat.Entry{ID:a.ID,Name:a.Card.Name,Description:a.Card.Description,URL:a.CardURL,UpdatedAt:a.UpdatedAt.Format(time.RFC3339),Tags:tags})
	}
	return cat.Build(b.cfg.Namespace,entries)
}
