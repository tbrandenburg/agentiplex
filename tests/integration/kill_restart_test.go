package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/tbrandenburg/agentiplex/core"
)

var listenRe=regexp.MustCompile(`listen=(\S+)`)

// startDaemon runs the real agentiplexd binary and returns its API base and process.
func startDaemon(t *testing.T,bin,cfg string)(string,*exec.Cmd){
	t.Helper()
	cmd:=exec.Command(bin,"--config",cfg)
	out,err:=cmd.StderrPipe();if err!=nil{t.Fatal(err)}
	if err=cmd.Start();err!=nil{t.Fatal(err)}
	addr:=make(chan string,1)
	go func(){
		sc:=bufio.NewScanner(out);sent:=false
		for sc.Scan(){if m:=listenRe.FindStringSubmatch(sc.Text());!sent&&strings.Contains(sc.Text(),"agentiplexd ready")&&m!=nil{sent=true;addr<-m[1]}}
	}()
	select{
	case a:=<-addr:return "http://"+a,cmd
	case <-time.After(20*time.Second):_ = cmd.Process.Kill();t.Fatal("daemon did not become ready");return "",nil
	}
}

func call(t *testing.T,method,url string,in,out any)int{
	t.Helper()
	var body bytes.Buffer
	if in!=nil{_ = json.NewEncoder(&body).Encode(in)}
	req,_:=http.NewRequest(method,url,&body);req.Header.Set("Content-Type","application/json")
	resp,err:=http.DefaultClient.Do(req);if err!=nil{t.Fatal(err)}
	defer resp.Body.Close()
	if out!=nil{_ = json.NewDecoder(resp.Body).Decode(out)}
	return resp.StatusCode
}

func TestSIGKILLRestartCompletesWithoutDuplicates(t *testing.T){
	bin:=filepath.Join(t.TempDir(),"agentiplexd")
	if o,err:=exec.Command("go","build","-o",bin,"github.com/tbrandenburg/agentiplex/cmd/agentiplexd").CombinedOutput();err!=nil{t.Fatalf("build: %v\n%s",err,o)}
	dir:=t.TempDir()
	cfg:=filepath.Join(dir,"config.yaml")
	yaml:=fmt.Sprintf("server:\n  listen: \"127.0.0.1:0\"\nbroker:\n  storage: %q\nobservability:\n  dashboard: false\nregistry:\n  health_interval: 0s\ndelivery:\n  timeout: 4s\n  retry_delay: 1s\n  workers: 2\n  task_poll_interval: 1s\n",filepath.Join(dir,"data"))
	if err:=os.WriteFile(cfg,[]byte(yaml),0o600);err!=nil{t.Fatal(err)}

	// Downstream A2A agent: "accepted" work stays in working until released; while
	// down, new submissions are refused so the delivery stays queued for retry.
	var down atomic.Bool;down.Store(true)
	release:=make(chan struct{})
	var mu sync.Mutex;runs:=map[string]int{}
	srv:=httptest.NewUnstartedServer(nil)
	base:="http://"+srv.Listener.Addr().String()
	card:=&a2a.AgentCard{Name:"slow",Description:"slow fixture",Version:"1",
		SupportedInterfaces:[]*a2a.AgentInterface{a2a.NewAgentInterface(base+"/invoke",a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:[]string{"text/plain"},DefaultOutputModes:[]string{"text/plain"},
		Skills:[]a2a.AgentSkill{{ID:"slow",Name:"slow",Description:"slow",Tags:[]string{"slow"}}}}
	executor:=a2asrv.AgentExecutorFunc(func(ctx context.Context,ec *a2asrv.ExecutorContext)iter.Seq2[a2a.Event,error]{
		return func(yield func(a2a.Event,error)bool){
			text:=ec.Message.Parts[0].Text()
			mu.Lock();runs[text]++;mu.Unlock()
			if !yield(&a2a.Task{ID:ec.TaskID,ContextID:ec.ContextID,Status:a2a.TaskStatus{State:a2a.TaskStateWorking}},nil){return}
			select{case <-release:case <-ctx.Done():return}
			if !yield(a2a.NewArtifactEvent(ec,a2a.NewTextPart("done "+text)),nil){return}
			yield(a2a.NewStatusUpdateEvent(ec,a2a.TaskStateCompleted,nil),nil)
		}
	})
	rpc:=a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
	mux:=http.NewServeMux()
	mux.HandleFunc("/invoke",func(w http.ResponseWriter,r *http.Request){
		if down.Load(){
			// Refuse only new submissions; allow task polling once up.
			http.Error(w,"unavailable",503);return
		}
		rpc.ServeHTTP(w,r)
	})
	mux.Handle(a2asrv.WellKnownAgentCardPath,a2asrv.NewStaticAgentCardHandler(card))
	srv.Config.Handler=mux;srv.Start();defer srv.Close()

	api,cmd:=startDaemon(t,bin,cfg)
	pid:=cmd.Process.Pid
	defer func(){_ = cmd.Process.Kill()}()
	if c:=call(t,"POST",api+"/api/v1/agents",map[string]string{"id":"slow","url":srv.URL},nil);c!=201{t.Fatalf("register: %d",c)}

	// Accepted delivery: agent accepts it and keeps working.
	down.Store(false)
	var acc,queued core.Delivery
	if c:=call(t,"POST",api+"/api/v1/messages",map[string]string{"target":"slow","text":"accepted-one","idempotencyKey":"k-acc"},&acc);c!=202{t.Fatalf("send accepted: %d",c)}
	deadline:=time.Now().Add(15*time.Second)
	for time.Now().Before(deadline){
		mu.Lock();n:=runs["accepted-one"];mu.Unlock()
		if n==1{break};time.Sleep(50*time.Millisecond)
	}
	for time.Now().Before(deadline){
		var d core.Delivery;call(t,"GET",api+"/api/v1/deliveries/"+acc.ID,nil,&d)
		if d.State=="working"||d.State=="accepted"||d.TaskID!=""{break};time.Sleep(50*time.Millisecond)
	}
	// Queued delivery: target refuses submissions, so it stays queued.
	down.Store(true)
	if c:=call(t,"POST",api+"/api/v1/messages",map[string]string{"target":"slow","text":"queued-one","idempotencyKey":"k-q"},&queued);c!=202{t.Fatalf("send queued: %d",c)}
	time.Sleep(1500*time.Millisecond)
	mu.Lock();if runs["queued-one"]!=0{t.Fatalf("queued delivery already executed")};mu.Unlock()

	var pre,preQ core.Delivery
	call(t,"GET",api+"/api/v1/deliveries/"+acc.ID,nil,&pre);call(t,"GET",api+"/api/v1/deliveries/"+queued.ID,nil,&preQ)
	t.Logf("before SIGKILL: accepted=%s queued=%s",pre.State,preQ.State)
	if pre.State=="completed"||pre.State=="failed"||preQ.State=="completed"{t.Fatalf("fixture not mid-flight: %s/%s",pre.State,preQ.State)}
	if err:=syscallKill(pid);err!=nil{t.Fatal(err)}
	_ = cmd.Wait()

	down.Store(false);close(release)
	api2,cmd2:=startDaemon(t,bin,cfg)
	defer func(){_ = cmd2.Process.Kill();_ = cmd2.Wait()}()
	for _,id:=range []string{acc.ID,queued.ID}{
		deadline=time.Now().Add(25*time.Second);state:=""
		for time.Now().Before(deadline){
			var d core.Delivery;call(t,"GET",api2+"/api/v1/deliveries/"+id,nil,&d);state=d.State
			if state=="completed"{break};time.Sleep(200*time.Millisecond)
		}
		if state!="completed"{t.Fatalf("delivery %s ended %q after SIGKILL restart",id,state)}
	}
	mu.Lock();defer mu.Unlock()
	if runs["accepted-one"]!=1||runs["queued-one"]!=1{t.Fatalf("duplicate or missing downstream execution: %v",runs)}
}
