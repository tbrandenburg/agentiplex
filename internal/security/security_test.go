package security

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialValidationAndPathCanonicalization(t *testing.T){
	dir:=t.TempDir()
	write:=func(name,value string)string{t.Helper();p:=filepath.Join(dir,name);if err:=os.WriteFile(p,[]byte(value),0600);err!=nil{t.Fatal(err)};return p}
	op:=write("operator",strings.Repeat("o",48))
	al:=write("alpha",strings.Repeat("a",48))
	if _,err:=New(Config{AgentFiles:map[string]string{"alpha":al}});err==nil{t.Fatal("config without operator accepted")}
	if _,err:=New(Config{OperatorFile:op,AgentFiles:map[string]string{"alpha":op}});err==nil{t.Fatal("agent reusing operator secret accepted")}
	if _,err:=New(Config{OperatorFile:op,AgentFiles:map[string]string{"alpha":al,"beta":al}});err==nil{t.Fatal("two agents sharing a secret accepted")}
	for _,id:=range []string{"Bad ID","operator","viewer",""}{
		if _,err:=New(Config{OperatorFile:op,AgentFiles:map[string]string{id:al}});err==nil{t.Fatalf("agent ID %q accepted",id)}
	}
	if _,err:=New(Config{OperatorFile:op,AgentFiles:map[string]string{"alpha":write("short","secret")}});err==nil{t.Fatal("short token accepted")}
	if _,err:=New(Config{OperatorFile:op,AgentFiles:map[string]string{"alpha":filepath.Join(dir,"missing")}});err==nil{t.Fatal("missing token file accepted")}
	a,err:=New(Config{OperatorFile:op,AgentFiles:map[string]string{"alpha":al}});if err!=nil{t.Fatal(err)}
	if err=a.AddAgent("beta",write("beta",strings.Repeat("b",48)));err!=nil{t.Fatal(err)}
	if err=a.AddAgent("gamma",write("dup",strings.Repeat("b",48)));err==nil{t.Fatal("late duplicate accepted")}
	if p,ok:=a.resolve(strings.Repeat("b",48));!ok||p.Role!=RoleAgent||p.AgentID!="beta"{t.Fatalf("resolve: %+v %v",p,ok)}
	if _,ok:=a.resolve(strings.Repeat("c",48));ok{t.Fatal("unknown token resolved")}
	if roleAllowed(RoleAgent,"GET","/api/v1/deliveries")||roleAllowed(RoleAgent,"GET","/api/v1/events")||!roleAllowed(RoleAgent,"GET","/api/v1/deliveries/x"){t.Fatal("agent delivery matrix wrong")}
	h:=a.Wrap(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(204)}))
	for _,path:=range []string{"/api/v1/agents/../state/x/y","/api/v1/agents/%2e%2e/state/x/y","/api//v1/agents","/api/v1/agents%2fremove"}{
		r:=httptest.NewRequest("GET","https://localhost"+path,nil)
		r.Header.Set("Authorization","Bearer "+strings.Repeat("o",48))
		w:=httptest.NewRecorder();h.ServeHTTP(w,r);if w.Code!=400{t.Errorf("noncanonical path %q returned %d",path,w.Code)}
	}
}
