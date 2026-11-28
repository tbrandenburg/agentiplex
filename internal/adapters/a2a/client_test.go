package a2a

import (
	"os"
	"path/filepath"
	"strings"
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

func TestClientCredentialOriginAliases(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	token := strings.Repeat("a", 48)
	for p, value := range map[string]string{a: token, b: strings.Repeat("b", 48)} {
		if err := os.WriteFile(p, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	allowed := []string{"https://Agent.example"}
	c, err := NewWithCredentials(allowed, time.Second, map[string]string{"https://Agent.example": a, "https://agent.example:443/": a}, "")
	if err != nil || len(c.tokens) != 1 || c.tokens["https://agent.example:443"] != token {
		t.Fatal("same-origin credential aliases failed")
	}
	for _, files := range []map[string]string{
		{"https://Agent.example": a, "https://agent.example:443": b},
		{"https://other.example": a},
		{"https://agent.example/card": a},
		{"http://localhost": a},
	} {
		if c, err := NewWithCredentials(allowed, time.Second, files, ""); err == nil || c != nil {
			t.Fatal("invalid or unapproved credential origin accepted")
		}
	}
}
