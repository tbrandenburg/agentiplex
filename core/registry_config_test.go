package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRegistryRejectsInvalidDeclarations(t *testing.T){
	good:="registry:\n  namespace: x\n  agents:\n    pm: {url: \"https://pm.example.org\", token_file: /t/pm}\n    dev: {url: \"https://dev.example.org\"}\n  groups: {eng: [pm, dev]}\n  pools: {rev: [dev]}\n"
	r,err:=ParseRegistry([]byte(good))
	if err!=nil||len(r.Agents)!=2||len(r.Groups["eng"])!=2||r.Pools["rev"][0]!="dev"||r.Agents["pm"].TokenFile!="/t/pm"{t.Fatalf("good registry: %+v %v",r,err)}
	if r,err=ParseRegistry([]byte("server: {listen: x}\n"));err!=nil||len(r.Agents)!=0{t.Fatalf("absent block: %+v %v",r,err)}
	cases:=map[string]string{
		"unknown agent field":"registry:\n  agents:\n    pm: {url: \"https://a.example\", role: x}\n",
		"unknown registry field":"registry:\n  agent: {}\n",
		"duplicate agent key":"registry:\n  agents:\n    pm: {url: \"https://a.example\"}\n    pm: {url: \"https://b.example\"}\n",
		"duplicate field":"registry:\n  agents:\n    pm: {url: \"https://a.example\", url: \"https://b.example\"}\n",
		"invalid id":"registry:\n  agents:\n    Bad_ID: {url: \"https://a.example\"}\n",
		"missing url":"registry:\n  agents:\n    pm: {token_file: /t}\n",
		"bad url scheme":"registry:\n  agents:\n    pm: {url: \"ftp://a.example\"}\n",
		"url with query":"registry:\n  agents:\n    pm: {url: \"https://a.example/?x=1\"}\n",
		"repeated member":"registry:\n  agents:\n    pm: {url: \"https://a.example\"}\n  groups: {g: [pm, pm]}\n",
		"empty group":"registry:\n  groups: {g: []}\n",
		"invalid collection id":"registry:\n  pools: {\"B!\": [pm]}\n",
	}
	for name,in:=range cases{if _,err:=ParseRegistry([]byte(in));err==nil{t.Errorf("%s accepted",name)}}
}

func TestRegistryCredentialMerging(t *testing.T){
	dir:=t.TempDir()
	tok:=func(name,v string)string{p:=filepath.Join(dir,name);os.WriteFile(p,[]byte(strings.Repeat(v,48)),0o600);return p}
	a1,a2,same:=tok("a1","a"),tok("a2","b"),tok("a1copy","a")
	r:=Registry{Agents:map[string]AgentSpec{
		"pm":{URL:"https://shared.example",TokenFile:"/in/pm",OutboundTokenFile:a1},
		"dev":{URL:"https://SHARED.example:443/",TokenFile:"/in/dev",OutboundTokenFile:same},
		"qa":{URL:"https://qa.example",TokenFile:"/in/qa"},
	}}
	out,err:=r.OutboundTokenFiles(nil)
	if err!=nil||len(out)!=1||out["https://shared.example:443"]==""{t.Fatalf("shared origin with same secret: %v %v",out,err)}
	r.Agents["dev"]=AgentSpec{URL:"https://shared.example",TokenFile:"/in/dev",OutboundTokenFile:a2}
	if _,err=r.OutboundTokenFiles(nil);err==nil||!strings.Contains(err.Error(),"conflicting"){t.Fatalf("conflicting origin credentials accepted: %v",err)}
	if _,err=r.OutboundTokenFiles(map[string]string{"https://shared.example":a2});err==nil{t.Fatal("conflict with delivery.token_files accepted")}
	r.Agents["dev"]=AgentSpec{URL:"http://plain.example",OutboundTokenFile:a1}
	if _,err=r.OutboundTokenFiles(nil);err==nil{t.Fatal("outbound credential over plain HTTP accepted")}

	in,err:=r.AgentTokenFiles(map[string]string{"extra":"/in/extra"},false)
	if err!=nil||in["pm"]!="/in/pm"||in["extra"]!="/in/extra"||len(in)!=3{t.Fatalf("merge: %v %v",in,err)}
	if _,err=r.AgentTokenFiles(map[string]string{"pm":"/in/other"},false);err==nil{t.Fatal("agent credentialed twice accepted")}
	if _,err=r.AgentTokenFiles(nil,true);err==nil{t.Fatal("missing token_file accepted in secure mode")}
}
