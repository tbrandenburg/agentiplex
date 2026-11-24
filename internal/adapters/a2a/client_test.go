package a2a

import (
	"testing"
	"time"
)

func TestOutboundOriginPolicy(t *testing.T){
	c,err:=New([]string{"https://agent.example:8443"},time.Second)
	if err!=nil{t.Fatal(err)}
	for _,u:=range []string{"http://127.0.0.1:8001","http://localhost:8001","https://agent.example:8443/invoke"}{
		if _,err=c.check(u);err!=nil{t.Errorf("rejected %s: %v",u,err)}
	}
	for _,u:=range []string{"file:///etc/passwd","http://169.254.169.254/","http://agent.example:8443","https://agent.example:443","http://user:secret@localhost:8001","http://localhost:8001/?q=x"}{
		if _,err=c.check(u);err==nil{t.Errorf("accepted %s",u)}
	}
}
