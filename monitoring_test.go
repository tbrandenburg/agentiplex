package agentiplex

import (
	"testing"
	"time"
)

func TestTaskPollDelay(t *testing.T){
	cfg:=DefaultConfig()
	for _,tc:=range []struct{failures int;want time.Duration}{{0,5*time.Second},{1,10*time.Second},{2,20*time.Second},{3,40*time.Second},{4,time.Minute},{63,time.Minute}}{
		if got:=cfg.pollDelay(tc.failures);got!=tc.want{t.Fatalf("failures %d: %s want %s",tc.failures,got,tc.want)}
	}
	cfg.TaskPollInterval=time.Second
	if got:=cfg.pollDelay(0);got!=time.Second{t.Fatal(got)}
	if got:=cfg.pollDelay(63);got!=time.Minute{t.Fatal(got)}
	cfg.TaskPollInterval=-time.Second
	if err:=cfg.validate();err==nil{t.Fatal("negative task poll interval accepted")}
}
