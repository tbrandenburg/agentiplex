package agentiplex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"sort"
	"strings"

	"github.com/tbrandenburg/agentiplex/internal/security"
	"go.yaml.in/yaml/v3"
)

// AgentSpec declares one static agent: where its native A2A card lives and the
// credential files bound to its ID. Descriptions and skills come from the card.
type AgentSpec struct {
	URL string `yaml:"url"`
	TokenFile string `yaml:"token_file"`
	OutboundTokenFile string `yaml:"outbound_token_file"`
}

// Registry is the optional declarative startup provisioning (additive, never a deletion list).
type Registry struct {
	Namespace any `yaml:"namespace"`
	HealthInterval any `yaml:"health_interval"`
	Agents map[string]AgentSpec `yaml:"agents"`
	Groups map[string][]string `yaml:"groups"`
	Pools map[string][]string `yaml:"pools"`
}

// ParseRegistry strictly decodes the registry block of a config file: unknown fields
// and duplicate keys are errors. An absent block yields an empty Registry.
func ParseRegistry(raw []byte)(Registry,error){
	var top struct{Registry yaml.Node `yaml:"registry"`}
	if err:=yaml.Unmarshal(raw,&top);err!=nil{return Registry{},fmt.Errorf("registry: %w",err)}
	if top.Registry.Kind==0{return Registry{},nil}
	block,err:=yaml.Marshal(&top.Registry);if err!=nil{return Registry{},err}
	dec:=yaml.NewDecoder(bytes.NewReader(block));dec.KnownFields(true)
	var r Registry
	if err=dec.Decode(&r);err!=nil{return Registry{},fmt.Errorf("registry: %w",err)}
	return r,r.Validate()
}

func (r Registry) ids()[]string{
	ids:=make([]string,0,len(r.Agents))
	for id:=range r.Agents{ids=append(ids,id)}
	sort.Strings(ids)
	return ids
}

func httpOrigin(raw string)(*url.URL,string,error){
	u,err:=url.Parse(raw)
	if err!=nil||(u.Scheme!="http"&&u.Scheme!="https")||u.Hostname()==""||u.User!=nil||u.RawQuery!=""||u.Fragment!=""{return nil,"",errors.New("expected HTTP(S) URL without credentials, query or fragment")}
	port:=u.Port()
	if port==""{port="80";if u.Scheme=="https"{port="443"}}
	return u,u.Scheme+"://"+net.JoinHostPort(strings.ToLower(u.Hostname()),port),nil
}

// Validate checks the declaration itself, without touching files or stored state.
func (r Registry) Validate()error{
	for _,id:=range r.ids(){
		s:=r.Agents[id]
		if !validID.MatchString(id){return fmt.Errorf("registry.agents.%q: invalid agent ID",id)}
		if s.URL==""{return fmt.Errorf("registry.agents.%s: url is required",id)}
		if _,_,err:=httpOrigin(s.URL);err!=nil{return fmt.Errorf("registry.agents.%s.url: %v",id,err)}
	}
	for kind,m:=range map[string]map[string][]string{"groups":r.Groups,"pools":r.Pools}{
		if len(m)>maxCollections{return fmt.Errorf("registry.%s: more than %d collections",kind,maxCollections)}
		for id,members:=range m{
			if !validID.MatchString(id){return fmt.Errorf("registry.%s.%q: invalid ID",kind,id)}
			if len(members)==0||len(members)>maxMembers{return fmt.Errorf("registry.%s.%s: 1–%d members required",kind,id,maxMembers)}
			seen:=map[string]bool{}
			for _,mem:=range members{
				if !validID.MatchString(mem){return fmt.Errorf("registry.%s.%s: invalid member %q",kind,id,mem)}
				if seen[mem]{return fmt.Errorf("registry.%s.%s: repeated member %q",kind,id,mem)}
				seen[mem]=true
			}
		}
	}
	return nil
}

// AgentTokenFiles merges declared inbound credentials into the existing ID->file map.
// An ID may be credentialed once; in secure mode every declared agent needs a credential.
func (r Registry) AgentTokenFiles(existing map[string]string,secure bool)(map[string]string,error){
	out:=map[string]string{}
	for id,f:=range existing{out[id]=f}
	for _,id:=range r.ids(){
		f:=r.Agents[id].TokenFile
		if f==""{
			if secure&&out[id]==""{return nil,fmt.Errorf("registry.agents.%s: token_file is required in secure mode",id)}
			continue
		}
		if _,dup:=out[id];dup{return nil,fmt.Errorf("registry.agents.%s: credential also set in security.agent_token_files",id)}
		out[id]=f
	}
	return out,nil
}

// OutboundTokenFiles merges declared outbound credentials by approved HTTPS origin.
// Two different secrets for one origin are a conflict; the same secret is shared.
func (r Registry) OutboundTokenFiles(existing map[string]string)(map[string]string,error){
	out:=map[string]string{}
	for raw,f:=range existing{
		_,o,err:=httpOrigin(raw);if err!=nil{out[raw]=f;continue}
		out[o]=f
	}
	for _,id:=range r.ids(){
		f:=r.Agents[id].OutboundTokenFile
		if f==""{continue}
		u,o,_:=httpOrigin(r.Agents[id].URL)
		if u.Scheme!="https"{return nil,fmt.Errorf("registry.agents.%s: outbound_token_file requires an HTTPS url",id)}
		prev,ok:=out[o]
		if !ok{out[o]=f;continue}
		a,err:=security.ReadToken(prev);if err!=nil{return nil,err}
		b,err:=security.ReadToken(f);if err!=nil{return nil,err}
		if a!=b{return nil,fmt.Errorf("registry.agents.%s: conflicting outbound credential for origin %s",id,o)}
	}
	return out,nil
}

func sameCard(configured,stored string)bool{
	u,err:=url.Parse(configured);if err!=nil{return false}
	if u.Path==""||u.Path=="/"{u.Path="/.well-known/agent-card.json"}
	return u.String()==stored
}

// ApplyRegistry provisions declared agents, groups and pools additively. Everything
// checkable is validated before the first write; identical state is a no-op, a
// different URL for a persisted ID fails, and an unresolvable new card fails.
// Collection edits affect new admissions only. Call before accepting requests.
func (b *Bus) ApplyRegistry(ctx context.Context,r Registry)error{
	if err:=r.Validate();err!=nil{return err}
	existing:=map[string]bool{}
	for _,id:=range r.ids(){
		a,err:=b.Agent(id)
		if errors.Is(err,ErrNotFound){continue}
		if err!=nil{return err}
		if !sameCard(r.Agents[id].URL,a.CardURL){return fmt.Errorf("registry.agents.%s: configured url %s differs from persisted registration %s; refusing to retarget",id,r.Agents[id].URL,a.CardURL)}
		existing[id]=true
	}
	for _,kind:=range []string{"group","pool"}{
		m:=r.Groups;if kind=="pool"{m=r.Pools}
		for cid,members:=range m{
			for _,mem:=range members{
				if _,declared:=r.Agents[mem];declared{continue}
				if _,err:=b.Agent(mem);err!=nil{return fmt.Errorf("registry.%ss.%s: unknown member %q",kind,cid,mem)}
			}
		}
	}
	for _,id:=range r.ids(){
		if existing[id]{continue}
		c,cancel:=context.WithTimeout(ctx,b.cfg.Timeout)
		_,err:=b.Register(c,id,r.Agents[id].URL);cancel()
		if err!=nil{return fmt.Errorf("registry.agents.%s: cannot register %s (start the endpoint first, then restart): %w",id,r.Agents[id].URL,err)}
	}
	for _,kind:=range []string{"group","pool"}{
		m:=r.Groups;if kind=="pool"{m=r.Pools}
		cids:=make([]string,0,len(m))
		for cid:=range m{cids=append(cids,cid)}
		sort.Strings(cids)
		for _,cid:=range cids{
			want:=append([]string(nil),m[cid]...);sort.Strings(want)
			if cur,err:=b.Collection(kind,cid);err==nil&&reflect.DeepEqual(cur.Members,want){continue}else if err!=nil&&!errors.Is(err,ErrNotFound){return err}
			if _,err:=b.SetCollection(ctx,kind,cid,want);err!=nil{return fmt.Errorf("registry.%ss.%s: %w",kind,cid,err)}
		}
	}
	return nil
}
