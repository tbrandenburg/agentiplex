package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func runObservationCLI(t *testing.T,server string,args ...string)(string,string,error){
	t.Helper()
	c:=Command()
	var out,diagnostic bytes.Buffer
	c.SetOut(&out);c.SetErr(&diagnostic)
	c.SetArgs(append([]string{"--server",server,"--token-file","","--ca-file",""},args...))
	err:=c.ExecuteContext(context.Background())
	return out.String(),diagnostic.String(),err
}

func TestWaitAndSendWait(t *testing.T){
	const id="receipt"
	var posts,gets atomic.Int32
	s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		switch {
		case r.Method=="POST"&&r.URL.Path=="/api/v1/messages":
			posts.Add(1)
			fmt.Fprint(w,"{\"id\":\"receipt\",\"state\":\"queued\"}")
		case r.Method=="GET"&&r.URL.Path=="/api/v1/deliveries/receipt/wait":
			gets.Add(1)
			fmt.Fprint(w,"{\"id\":\"receipt\",\"target\":\"lead\",\"state\":\"completed\",\"result\":{\"artifacts\":[{\"parts\":[{\"text\":\"A useful verdict\\u001b[0m\"}]}]}}")
		default:t.Errorf("unexpected request %s %s",r.Method,r.URL.Path);w.WriteHeader(404)
		}
	}))
	defer s.Close()
	out,diag,err:=runObservationCLI(t,s.URL,"send","lead","topic","--idempotency-key","one","--wait")
	if err!=nil||!strings.Contains(out,"A useful verdict")||strings.ContainsRune(out,27)||!strings.Contains(diag,id){t.Fatalf("send --wait: %q %q %v",out,diag,err)}
	out,_,err=runObservationCLI(t,s.URL,"messages","wait",id,"--json")
	if err!=nil||!strings.Contains(out,"\"artifacts\""){t.Fatalf("wait JSON: %q %v",out,err)}
	if posts.Load()!=1||gets.Load()!=2{t.Fatalf("posts=%d gets=%d",posts.Load(),gets.Load())}
}

func TestWaitReportsUncertainAndRejectsNonterminal(t *testing.T){
	for _,state:=range []string{"failed","uncertain","partial","auth-required","input-required","accepted"}{
		t.Run(state,func(t *testing.T){
			s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
				fmt.Fprintf(w,"{\"id\":\"receipt\",\"target\":\"lead\",\"state\":%q}",state)
			}))
			defer s.Close()
			out,_,err:=runObservationCLI(t,s.URL,"messages","wait","receipt")
			if err==nil{t.Fatal("non-success state returned success")}
			if state!="accepted"&&!strings.Contains(out,state){t.Fatalf("missing terminal state: %q",out)}
		})
	}
}

func TestListNotesReadableAndJSON(t *testing.T){
	s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if r.URL.Path!="/api/v1/notes/discussion"||r.URL.Query().Get("limit")!="5"{t.Errorf("unexpected request: %s",r.URL)}
		if topic:=r.URL.Query().Get("topic");topic!=""&&topic!="expert"{t.Errorf("unexpected topic: %s",topic)}
		fmt.Fprint(w,"[{\"key\":\"note-expert-1\",\"topic\":\"expert\",\"text\":\"Consider failure modes\",\"at\":\"2026-01-01T00:00:00Z\"}]")
	}))
	defer s.Close()
	out,_,err:=runObservationCLI(t,s.URL,"notes","list","discussion","--limit","5")
	if err!=nil||!strings.Contains(out,"[expert]")||!strings.Contains(out,"unverified local author")||!strings.Contains(out,"Consider failure modes"){t.Fatalf("%q %v",out,err)}
	out,_,err=runObservationCLI(t,s.URL,"notes","list","discussion","expert","--limit","5","--json")
	if err!=nil||!strings.HasPrefix(out,"[")||!strings.Contains(out,"\"topic\":\"expert\""){t.Fatalf("%q %v",out,err)}
}

func TestResultTextIgnoresHistoryAndUsesFinalMessage(t *testing.T){
	for _,tc:=range []struct{raw,want string}{
		{"{\"parts\":[{\"text\":\"direct\"}]}","direct"},
		{"{\"status\":{\"message\":{\"parts\":[{\"text\":\"status\"}]}},\"artifacts\":[{\"parts\":[{\"text\":\"artifact\"}]}]}","artifact"},
		{"{\"status\":{\"message\":{\"parts\":[{\"text\":\"status\"}]}},\"history\":[{\"parts\":[{\"text\":\"prompt\"}]}]}","status"},
		{"{\"parts\":[{\"data\":{\"text\":\"structured\"}}]}",""},
	}{
		if got:=resultText([]byte(tc.raw));got!=tc.want{t.Fatalf("got %q want %q",got,tc.want)}
	}
}
