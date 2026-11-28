package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tbrandenburg/agentiplex/core"
)

func TestDaemonStaticRegistryReadinessAndFailure(t *testing.T){
	bin:=filepath.Join(t.TempDir(),"agentiplexd")
	if o,err:=exec.Command("go","build","-o",bin,"github.com/tbrandenburg/agentiplex/cmd/agentiplexd").CombinedOutput();err!=nil{t.Fatalf("build: %v\n%s",err,o)}
	dir:=t.TempDir()
	a,b:=&agentService{},&agentService{}
	a.start(t,false);b.start(t,false);defer a.stop();defer b.stop()
	conf:=func(extra,alpha string)string{
		p:=filepath.Join(dir,"config.yaml")
		y:=fmt.Sprintf("server:\n  listen: \"127.0.0.1:0\"\nbroker:\n  storage: %q\nobservability:\n  dashboard: false\nregistry:\n  health_interval: 0s\n  agents:\n    alpha: {url: %q}\n    beta: {url: %q}\n%s  groups: {team: [alpha, beta]}\n  pools: {rev: [alpha, beta]}\ndelivery:\n  timeout: 3s\n",filepath.Join(dir,"data"),alpha,b.URL(),extra)
		if err:=os.WriteFile(p,[]byte(y),0o600);err!=nil{t.Fatal(err)}
		return p
	}
	api,cmd:=startDaemon(t,bin,conf("",a.URL()))
	var agents []core.Agent
	if c:=call(t,"GET",api+"/api/v1/agents",nil,&agents);c!=200||len(agents)!=2{t.Fatalf("ready daemon without full roster: %d %d",c,len(agents))}
	var team core.Collection
	if c:=call(t,"GET",api+"/api/v1/groups/team",nil,&team);c!=200||len(team.Members)!=2{t.Fatalf("group: %d %+v",c,team)}
	_ = cmd.Process.Kill();_ = cmd.Wait()

	dead:=&agentService{};dead.start(t,false);dead.stop()
	for name,c:=range map[string][2]string{
		"unreachable new agent":{fmt.Sprintf("    gamma: {url: %q}\n",dead.URL()),a.URL()},
		"conflicting url":{"",b.URL()},
	}{
		out,err:=exec.Command(bin,"--config",conf(c[0],c[1])).CombinedOutput()
		want:=map[string]string{"unreachable new agent":"start the endpoint first","conflicting url":"refusing to retarget"}[name]
		if err==nil||strings.Contains(string(out),"agentiplexd ready")||!strings.Contains(string(out),want){t.Fatalf("%s: daemon outcome %v: %s",name,err,out)}
	}
}
