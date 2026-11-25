package security

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoleCredentialValidationAndPathCanonicalization(t *testing.T){
	dir:=t.TempDir()
	write:=func(name,value string)string{t.Helper();p:=filepath.Join(dir,name);if err:=os.WriteFile(p,[]byte(value),0600);err!=nil{t.Fatal(err)};return p}
	op:=write("operator",strings.Repeat("o",48))
	co:=write("collaborator",strings.Repeat("c",48))
	if _,err:=New(Config{OperatorFile:op});err==nil{t.Fatal("partial role config accepted")}
	if _,err:=New(Config{OperatorFile:op,CollaboratorFile:op});err==nil{t.Fatal("duplicate role secret accepted")}
	if _,err:=ReadToken(write("short","secret"));err==nil{t.Fatal("short token accepted")}
	a,err:=New(Config{OperatorFile:op,CollaboratorFile:co});if err!=nil{t.Fatal(err)}
	h:=a.Wrap(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(204)}))
	for _,path:=range []string{"/api/v1/agents/../state/x/y","/api/v1/agents/%2e%2e/state/x/y","/api//v1/agents","/api/v1/agents%2fremove"}{
		r:=httptest.NewRequest("GET","https://localhost"+path,nil)
		r.Header.Set("Authorization","Bearer "+strings.Repeat("o",48))
		w:=httptest.NewRecorder();h.ServeHTTP(w,r);if w.Code!=400{t.Errorf("noncanonical path %q returned %d",path,w.Code)}
	}
}
