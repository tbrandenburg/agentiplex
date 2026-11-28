//go:build linux

package examples_test

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex/core"
	"go.yaml.in/yaml/v3"
)

// Explicit opt-in; ordinary go test ./... never starts real model runtimes.
var liveScenario = flag.String("live", "", "provision, docs, defect, integration, contract, autonomous or discussion")
var liveModel = flag.String("model", "", "OpenCode provider/model")
var liveSeconds = flag.Int("seconds", 0, "objective deadline in seconds (default: 900 hybrid, 1200 repository)")
var livePromptTimeout = flag.String("prompt-timeout", "", "gateway prompt deadline")
var liveStartupRetries = flag.Int("startup-retries", 4, "retry exited gateway startup probes")
var liveProvisionOnly = flag.Bool("provision-only", false, "start and register without inference")
var liveDiagnosticsDir = flag.String("diagnostics-dir", "", "optional export directory for synthetic live-test failure diagnostics")

type liveProcess struct {
	cmd *exec.Cmd
	done chan struct{}
	log string
	started time.Time
	exitErr error // read only after done is closed
}

type liveTeam struct {
	t *testing.T
	root, run, example, api string
	env []string
	client *http.Client
	daemon *liveProcess
	processes []*liveProcess
}

func newLiveRun(t *testing.T, example string) *liveTeam {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil { t.Fatal(err) }
	run, err := os.MkdirTemp("", "agentiplex-"+example+"-")
	if err != nil { t.Fatal(err) }
	t.Log("Private run directory (retained for evidence; review/delete):",run)
	home := filepath.Join(run,"home")
	if err := os.Mkdir(home,0700); err != nil { t.Fatal(err) }
	env := []string{"HOME="+home,"NO_PROXY=127.0.0.1,localhost","GIT_CONFIG_NOSYSTEM=1","GIT_CONFIG_GLOBAL=/dev/null","GIT_TERMINAL_PROMPT=0","GOTOOLCHAIN=local","GOWORK=off"}
	for _, key := range []string{"PATH","LANG","LC_ALL","TMPDIR","USER","LOGNAME","GOROOT"} {
		if v, ok := os.LookupEnv(key); ok { env=append(env,key+"="+v) }
	}
	for _, key := range []string{"GOCACHE","GOMODCACHE"} {
		out, err := commandOutput(root,30*time.Second,nil,"go","env",key)
		if err != nil { t.Fatal(err) }
		env=append(env,key+"="+strings.TrimSpace(string(out)))
	}
	l := &liveTeam{t:t,root:root,run:run,example:filepath.Join(root,"examples",example),env:env,client:&http.Client{Timeout:3*time.Second,Transport:&http.Transport{Proxy:nil}}}
	return l
}

func newLiveTeam(t *testing.T, example string) *liveTeam {
	t.Helper()
	l := newLiveRun(t,example)
	run := l.run
	var cfg struct{ Server struct{ Listen string } }
	if err := yaml.Unmarshal(l.read("team.yaml"),&cfg); err != nil { t.Fatal(err) }
	host, _, err := net.SplitHostPort(cfg.Server.Listen)
	if err != nil || host!="127.0.0.1" { t.Fatal("live example requires literal loopback listener") }
	l.api="http://"+cfg.Server.Listen
	for _, name := range []string{"agentiplexd","agentiplex"} { l.build(name,"./cmd/"+name) }
	if example=="autonomous-team" { l.build("simulated-agent","./examples/simulated-agent") }
	policies, err := os.ReadDir(filepath.Join(l.example,"policies"))
	if err != nil { t.Fatal(err) }
	for _, p := range policies {
		if !p.Type().IsRegular() { t.Fatal("unexpected policy file") }
		writeTeamFile(t,filepath.Join(run,"policies",p.Name()),string(l.read("policies",p.Name())))
	}
	return l
}

func (l *liveTeam) read(parts ...string) []byte {
	l.t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{l.example},parts...)...))
	if err != nil { l.t.Fatal(err) }
	return b
}

func (l *liveTeam) build(name,pkg string) {
	l.t.Helper()
	out, err := commandOutput(l.root,5*time.Minute,l.env,"go","build","-mod=readonly","-o",filepath.Join(l.run,name),pkg)
	if err != nil { l.t.Fatalf("build %s: %v\n%s",name,err,out) }
}

func (l *liveTeam) spawn(name,dir string, env []string, args ...string) *liveProcess {
	l.t.Helper()
	path := filepath.Join(l.run,name+".log")
	f, err := os.OpenFile(path,os.O_CREATE|os.O_WRONLY|os.O_TRUNC,0600)
	if err != nil { l.t.Fatal(err) }
	cmd := exec.Command(args[0],args[1:]...)
	cmd.Dir,cmd.Env,cmd.Stdout,cmd.Stderr=dir,env,f,f
	cmd.SysProcAttr=&syscall.SysProcAttr{Setsid:true}
	err=cmd.Start()
	f.Close()
	if err != nil { l.t.Fatal(err) }
	p:=&liveProcess{cmd:cmd,done:make(chan struct{}),log:path,started:time.Now().UTC()}
	l.processes=append(l.processes,p)
	go func(){ p.exitErr=cmd.Wait(); close(p.done) }()
	l.t.Cleanup(func(){
		select { case <-p.done: return; default: }
		_ = syscall.Kill(-cmd.Process.Pid,syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(15*time.Second):
			_ = syscall.Kill(-cmd.Process.Pid,syscall.SIGKILL)
			<-p.done
		}
	})
	return p
}

func (l *liveTeam) get(endpoint string,out any) error {
	resp,err:=l.client.Get(endpoint)
	if err!=nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode!=http.StatusOK { return fmt.Errorf("HTTP %d",resp.StatusCode) }
	return json.NewDecoder(resp.Body).Decode(out)
}

var errLiveProcessExited=errors.New("process exited before readiness")

func (l *liveTeam) waitReady(p *liveProcess, endpoint string, deadline time.Duration) error {
	end:=time.Now().Add(deadline)
	for time.Now().Before(end) {
		select { case <-p.done: return fmt.Errorf("%w; see %s",errLiveProcessExited,p.log); default: }
		var obj json.RawMessage
		if l.get(endpoint,&obj)==nil { return nil }
		time.Sleep(300*time.Millisecond)
	}
	return fmt.Errorf("readiness deadline exceeded; see %s",p.log)
}

func assertFreeTeamPort(t *testing.T,addr string) {
	t.Helper()
	s,err:=net.Listen("tcp",addr)
	if err!=nil { t.Fatalf("required port %s unavailable: %v",addr,err) }
	s.Close()
}

func (l *liveTeam) startDaemon() {
	l.t.Helper()
	u,err:=url.Parse(l.api)
	if err!=nil { l.t.Fatal(err) }
	assertFreeTeamPort(l.t,u.Host)
	l.daemon=l.spawn("daemon",l.root,l.env,filepath.Join(l.run,"agentiplexd"),"--config",filepath.Join(l.example,"team.yaml"),"--storage",filepath.Join(l.run,"jetstream"))
	if err:=l.waitReady(l.daemon,l.api+"/readyz",120*time.Second); err!=nil { l.t.Fatal(err) }
}

func (l *liveTeam) configureModel(path string) {
	l.t.Helper()
	if *liveModel=="" { return }
	provider,model,ok:=strings.Cut(*liveModel,"/")
	if !ok || provider=="" || model=="" { l.t.Fatal("model must be provider/model") }
	raw,err:=os.ReadFile(path)
	if err!=nil { l.t.Fatal(err) }
	var cfg map[string]any
	if err=json.Unmarshal(raw,&cfg); err!=nil { l.t.Fatal(err) }
	cfg["model"]=map[string]string{"providerID":provider,"model":model}
	raw,err=json.MarshalIndent(cfg,"","  ")
	if err!=nil { l.t.Fatal(err) }
	writeTeamFile(l.t,path,string(raw)+"\n")
}

func (l *liveTeam) gateway(name,desc,workspace,access,policy,port,prompt string,extra ...string) {
	l.t.Helper()
	assertFreeTeamPort(l.t,"127.0.0.1:"+port)
	gw,err:=exec.LookPath("opencode-a2a-lite")
	if err!=nil { l.t.Fatal("install the pinned gateway; see examples/README.md") }
	args:=append([]string{gw,"--name",name,"--description",desc,"--workspace",workspace,"--port",port,"--access",access,"--policy-file",filepath.Join(l.run,"policies",policy+".json"),"--prompt-timeout",prompt,"--no-env"},extra...)
	for attempt:=0;attempt<=*liveStartupRetries;attempt++ {
		p:=l.spawn(name+"-"+strconv.Itoa(attempt),workspace,l.env,args...)
		err=l.waitReady(p,"http://127.0.0.1:"+port+"/.well-known/agent-card.json",10*time.Minute)
		if err==nil { return }
		if !errors.Is(err,errLiveProcessExited) || attempt==*liveStartupRetries { l.t.Fatal(err) }
		l.t.Log(name,"startup exited; retrying:",err)
		time.Sleep(2*time.Second)
		assertFreeTeamPort(l.t,"127.0.0.1:"+port)
	}
}

func teamCLI[T any](l *liveTeam,args ...string) T {
	l.t.Helper()
	var result T
	out,err:=commandOutput(l.root,30*time.Second,l.env,filepath.Join(l.run,"agentiplex"),append([]string{"--server",l.api},args...)...)
	if err!=nil { l.t.Fatalf("native CLI %s failed: %v",args[0],err) }
	if err=json.Unmarshal(out,&result);err!=nil { l.t.Fatal("invalid CLI JSON:",err) }
	return result
}

func (l *liveTeam) checkRoster(want map[string]bool) {
	l.t.Helper()
	got:=map[string]bool{}
	for _,a:=range teamCLI[[]struct{ID string}](l,"agents","list") { got[a.ID]=true }
	if strings.Join(sortedTeamIDs(got),",")!=strings.Join(sortedTeamIDs(want),",") { l.t.Fatalf("registry mismatch: %v",sortedTeamIDs(got)) }
	l.t.Log("Team ready:",sortedTeamIDs(got))
}

func (l *liveTeam) awaitEvidence(id,namespace,objective string,revision uint64,scenario string,seconds int) {
	l.t.Helper()
	end:=time.Now().Add(time.Duration(seconds)*time.Second)
	var records []teamDelivery
	for {
		if time.Now().After(end) { l.t.Fatal("team objective deadline exceeded") }
		select { case <-l.daemon.done: l.t.Fatal("daemon exited"); default: }
		records=nil
		if err:=l.get(l.api+"/api/v1/deliveries?limit=100",&records);err!=nil { l.t.Fatal(err) }
		if len(records)>=100 { l.t.Fatal("delivery evidence exceeds bounded acceptance window") }
		terminal,found:=true,false
		for _,d:=range records {
			terminal=terminal && terminalTeamState(d.State)
			found=found || d.ID==id
		}
		if terminal && found { break }
		time.Sleep(time.Second)
	}
	ids:=map[string]bool{}
	for _,d:=range records {
		ids[d.ID]=true
		for _,child:=range d.Children { ids[child.ID]=true }
	}
	var root teamDelivery
	var deliveries []teamDelivery
	for _,key:=range sortedTeamIDs(ids) {
		d:=teamCLI[teamDelivery](l,"messages","status",key)
		deliveries=append(deliveries,d)
		if d.ID==id { root=d }
		l.t.Logf("Delivery %s target=%s state=%s has_result=%t",d.ID,d.Target,d.State,d.hasResult())
	}
	var state []teamState
	for _,s:=range teamCLI[[]teamState](l,"state","list",namespace) {
		state=append(state,teamCLI[teamState](l,"state","get",namespace,s.Key))
		l.t.Log("Shared state key:",s.Key)
	}
	raw,err:=json.MarshalIndent(map[string]any{"root":root,"deliveries":deliveries,"state":state,"objective_revision":revision},"","  ")
	if err!=nil { l.t.Fatal(err) }
	writeTeamFile(l.t,filepath.Join(l.run,"evidence.json"),string(raw))
	if err=verifyTeamEvidence(root,deliveries,state,objective,revision,scenario);err!=nil { l.t.Fatal(err) }
	// Exercise the documented result commands against the already verified
	// durable work. Reattaching must not create another submission.
	waited:=teamCLI[teamDelivery](l,"messages","wait",id,"--json")
	if waited.ID!=id || waited.State!="completed" || !waited.hasResult() { l.t.Fatal("reattached CLI wait did not return the completed PM result") }
	notes:=teamCLI[[]core.Note](l,"notes","list",namespace,"--json")
	if scenario!="autonomous" && len(notes)==0 { l.t.Fatal("CLI notes list lost the repository notes") }
	l.t.Logf("CLI wait and notes list verified: %d recent notes",len(notes))
	l.t.Log("Verified durable evidence; trusted-local note topics are not authenticated authors or tool-call attestations.")
}

func (l *liveTeam) repository(scenario,prompt string,seconds int) {
	defer l.reportFailure("repository",scenario)
	ws:=filepath.Join(l.run,"workspace")
	writeTeamFile(l.t,filepath.Join(ws,"opencode.json"),string(l.read("opencode.json")))
	l.configureModel(filepath.Join(ws,"opencode.json"))
	base:=initTeamFixture(l.t,ws)
	// Explicit native-role startup opens an ACP session before readiness.
	// MCP must exist first; then register the five live endpoints.
	l.startDaemon()
	roles:=[]struct{name,desc,access,policy,port string}{
		{"pm","Engineering coordinator","read","coordinator","19081"},
		{"architect","Architecture reviewer","read","reader","19082"},
		{"developer","Repository implementation and repair","danger-full-access","writer","19083"},
		{"tester","Independent executable verification","danger-full-access","writer","19084"},
		{"quality","Independent acceptance review","read","reader","19085"},
	}
	want:=map[string]bool{}
	for _,r:=range roles {
		rolePath:=filepath.Join(l.example,"roles",r.name+".md")
		policy:=r.policy
		if scenario=="contract" {
			extra:=""
			switch r.name {
			case "pm": extra="contract-kernel.md"
			case "developer","tester": extra="contract-worker.md"; policy="contract-worker"
			}
			if extra!="" {
				rolePath=filepath.Join(l.run,r.name+".md")
				writeTeamFile(l.t,rolePath,string(l.read("roles",r.name+".md"))+"\n"+string(l.read("roles",extra)))
			}
		}
		l.gateway(r.name,r.desc,ws,r.access,policy,r.port,prompt,"--system-prompt-file",rolePath)
		teamCLI[json.RawMessage](l,"register",r.name,"http://127.0.0.1:"+r.port)
		want[r.name]=true
	}
	l.checkRoster(want)
	if mustTeamGit(l.t,ws,"status","--porcelain")!="" { l.t.Fatal("provisioning changed workspace") }
	if scenario=="provision" || *liveProvisionOnly { l.t.Log("PASS: provisioned only; no model task submitted");return }
	objective:=strings.TrimRight(string(l.read("objective.md")),"\n")
	switch scenario {
	case "defect":
		objective="Fix Clamp(value, lower, upper int) int in calc.go for lower <= upper so it returns the value bounded by the inclusive limits. Preserve its signature and leave unrelated files unchanged. The check go test -count=1 ./... must pass. Leave the patch uncommitted; no branch or history changes."
	case "integration":
		objective=strings.TrimRight(string(l.read("objective.integration.md")),"\n")
	}
	if scenario!="docs" {
		out,err:=commandOutput(ws,90*time.Second,l.env,"go","test","-count=1","./...")
		var exit *exec.ExitError
		if !errors.As(err,&exit) || exit.ExitCode()!=1 || !strings.Contains(string(out),"--- FAIL: TestClamp") { l.t.Fatalf("fixture must start with the expected failing check: %v\n%s",err,out) }
		l.t.Log("Baseline local go test failed as expected.")
	}
	if scenario=="contract" {
		l.contract(ws,base,seconds)
		return
	}
	initial:=teamCLI[teamState](l,"state","put","repository","objective",objective,"--revision","0")
	if initial.Value!=objective || initial.Revision==0 { l.t.Fatal("operator objective publication failed") }
	root:=teamCLI[teamDelivery](l,"send","pm",objective,"--idempotency-key","repository-objective")
	l.awaitEvidence(root.ID,"repository",objective,initial.Revision,scenario,seconds)
	if err:=verifyTeamWorkspace(ws,base,scenario);err!=nil { l.t.Fatal(err) }
	l.t.Log("PASS:",scenario,"objective; independent workspace checks passed.")
}

func (l *liveTeam) autonomous(prompt string,seconds int) {
	reg,err:=core.ParseRegistry(l.read("team.yaml"))
	if err!=nil { l.t.Fatal(err) }
	port:=func(name string)string{
		u,err:=url.Parse(reg.Agents[name].URL)
		if err!=nil || u.Hostname()!="127.0.0.1" || u.Port()=="" { l.t.Fatalf("invalid endpoint for %s",name) }
		return u.Port()
	}
	for _,r:=range []struct{name,skill string}{{"developer","development"},{"tester-1","testing"},{"tester-2","testing"},{"quality","quality"}} {
		p:=port(r.name)
		assertFreeTeamPort(l.t,"127.0.0.1:"+p)
		proc:=l.spawn(r.name,l.root,l.env,filepath.Join(l.run,"simulated-agent"),"--name",r.name,"--skill",r.skill,"--port",p)
		if err=l.waitReady(proc,"http://127.0.0.1:"+p+"/.well-known/agent-card.json",30*time.Second);err!=nil { l.t.Fatal(err) }
	}
	for _,r:=range []struct{name,skill,policy string}{{"pm","planning","coordinator"},{"architect","architecture","worker"}} {
		ws:=filepath.Join(l.run,r.name)
		if err=os.CopyFS(ws,os.DirFS(filepath.Join(l.example,"profiles",r.name)));err!=nil { l.t.Fatal(err) }
		if err=os.CopyFS(filepath.Join(ws,"fixture"),os.DirFS(filepath.Join(l.example,"fixture")));err!=nil { l.t.Fatal(err) }
		l.configureModel(filepath.Join(ws,"opencode.json"))
		l.gateway(r.name,"Read-only engineering "+r.skill,ws,"read",r.policy,port(r.name),prompt)
	}
	l.startDaemon()
	want:=map[string]bool{}
	for name:=range reg.Agents { want[name]=true }
	l.checkRoster(want)
	if *liveProvisionOnly { l.t.Log("PASS: provisioned only; no model task submitted");return }
	objective:="Review the fictional controller fixture for architectural, implementation and verification risks. Collaborate with the team and produce a consolidated evidence-based assessment."
	root:=teamCLI[teamDelivery](l,"send","pm",objective,"--idempotency-key","autonomous-review")
	l.awaitEvidence(root.ID,"review",objective,0,"autonomous",seconds)
	l.t.Log("PASS: real PM, independent real worker and persisted review state; manual evidence review still required.")
}

func TestLiveTeam(t *testing.T) {
	if *liveScenario=="" { t.Skip("opt in with -args -live SCENARIO") }
	if *liveStartupRetries<0 || *liveStartupRetries>4 || *liveSeconds<0 || *liveSeconds>2400 { t.Fatal("invalid bounded live test arguments") }
	prompt,seconds:=*livePromptTimeout,*liveSeconds
	switch *liveScenario {
	case "discussion":
		runDiscussionQuickstart(t)
	case "autonomous":
		if prompt=="" { prompt="10m" }; if seconds==0 { seconds=900 }
		newLiveTeam(t,"autonomous-team").autonomous(prompt,seconds)
	case "provision","docs","defect","integration","contract":
		if prompt=="" { prompt="45m" }; if seconds==0 { seconds=1200 }
		newLiveTeam(t,"repository-team").repository(*liveScenario,prompt,seconds)
	default: t.Fatal("unknown live scenario")
	}
}
