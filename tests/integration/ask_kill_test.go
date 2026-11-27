package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex"
)

// askDaemon builds and starts the real daemon with MCP enabled on shared storage.
func askDaemon(t *testing.T)(start func()(string,*exec.Cmd),a *taskAgent){
	t.Helper()
	bin:=filepath.Join(t.TempDir(),"agentiplexd")
	if o,err:=exec.Command("go","build","-o",bin,"github.com/tbrandenburg/agentiplex/cmd/agentiplexd").CombinedOutput();err!=nil{t.Fatalf("build: %v\n%s",err,o)}
	dir:=t.TempDir();cfg:=filepath.Join(dir,"config.yaml")
	yaml:=fmt.Sprintf("server:\n  listen: \"127.0.0.1:0\"\nprotocols:\n  mcp:\n    enabled: true\nbroker:\n  storage: %q\nobservability:\n  dashboard: false\nregistry:\n  health_interval: 0s\ndelivery:\n  timeout: 4s\n  retry_delay: 1s\n  workers: 2\n  task_poll_interval: 1s\n",filepath.Join(dir,"data"))
	if err:=os.WriteFile(cfg,[]byte(yaml),0o600);err!=nil{t.Fatal(err)}
	a=newTaskAgent(t)
	return func()(string,*exec.Cmd){
		api,cmd:=startDaemon(t,bin,cfg)
		t.Cleanup(func(){_ = cmd.Process.Kill();_ = cmd.Wait()})
		return api,cmd
	},a
}

func sigkill(t *testing.T,cmd *exec.Cmd){
	t.Helper()
	if err:=syscallKill(cmd.Process.Pid);err!=nil{t.Fatal(err)}
	_ = cmd.Wait()
}

func waitSends(t *testing.T,a *taskAgent,n int32){
	t.Helper()
	for end:=time.Now().Add(15*time.Second);time.Now().Before(end);time.Sleep(50*time.Millisecond){if a.sends.Load()>=n{return}}
	t.Fatalf("agent never received %d sends", n)
}

// Kill during a blocked ask on a running task; after restart the repeated ask
// re-attaches to the same delivery and the task still ran exactly once.
func TestAskSIGKILLDuringBlockedAskThenReattach(t *testing.T){
	start,a:=askDaemon(t)
	api,cmd:=start()
	if c:=call(t,"POST",api+"/api/v1/agents",map[string]string{"id":"worker","url":a.srv.URL},nil);c!=201{t.Fatalf("register: %d",c)}
	args:=map[string]any{"target":"worker","text":"long task","idempotencyKey":"kill-key"}
	blocked:=make(chan error,1)
	go func(){_,err:=askTool(mcpClient(t,api),args);blocked<-err}()
	waitSends(t,a,1);time.Sleep(1500*time.Millisecond)
	sigkill(t,cmd)
	if err:=<-blocked;err==nil{t.Fatal("blocked ask must fail when the daemon is killed")}

	api2,_:=start()
	go func(){time.Sleep(2*time.Second);close(a.release)}()
	got:=mustAsk(t,mcpClient(t,api2),args)
	if !got.Done||got.State!="completed"||a.sends.Load()!=1{t.Fatalf("re-attach: %+v sends=%d",got,a.sends.Load())}
	var d agentiplex.Delivery
	call(t,"GET",api2+"/api/v1/deliveries/"+got.ID,nil,&d)
	if d.ID!=got.ID||d.State!="completed"{t.Fatalf("delivery %+v",d)}
}

// The task finishes while the daemon is down: the repeated ask with
// the same key collects the finished result; a new key is new work.
func TestAskFinishedWhileDownThenNewKeyIsNewWork(t *testing.T){
	start,a:=askDaemon(t)
	api,cmd:=start()
	if c:=call(t,"POST",api+"/api/v1/agents",map[string]string{"id":"worker","url":a.srv.URL},nil);c!=201{t.Fatalf("register: %d",c)}
	args:=map[string]any{"target":"worker","text":"long task","idempotencyKey":"kill-key"}
	blocked:=make(chan error,1)
	go func(){_,err:=askTool(mcpClient(t,api),args);blocked<-err}()
	waitSends(t,a,1);time.Sleep(1500*time.Millisecond)
	sigkill(t,cmd)
	<-blocked
	close(a.release)

	api2,_:=start()
	got:=mustAsk(t,mcpClient(t,api2),args)
	if !got.Done||got.State!="completed"||a.sends.Load()!=1{t.Fatalf("collect: %+v sends=%d",got,a.sends.Load())}
	args["idempotencyKey"]="kill-key-2"
	next:=mustAsk(t,mcpClient(t,api2),args)
	if next.ID==got.ID||!next.Done||a.sends.Load()!=2{t.Fatalf("new delivery expected: %+v sends=%d",next,a.sends.Load())}
}
