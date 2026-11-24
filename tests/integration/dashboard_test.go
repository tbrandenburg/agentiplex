package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tbrandenburg/agentiplex"
	"github.com/tbrandenburg/agentiplex/internal/api"
)

func TestDashboardUsesStoredDeliveryState(t *testing.T) {
	b:=open(t,config(t))
	registerPair(t,b)
	ctx:=context.Background()
	if _,err:=b.SetCollection(ctx,"group","engineering",[]string{"tester-1","tester-2"});err!=nil{t.Fatal(err)}
	d,err:=b.Broadcast(ctx,"engineering","private fixture prompt","dashboard-broadcast")
	if err!=nil{t.Fatal(err)}
	wait(t,b,d.ID,"completed")
	s:=httptest.NewServer(api.Handler(b))
	defer s.Close()
	get:=func(path string)(int,http.Header,string){
		t.Helper()
		res,err:=http.Get(s.URL+path);if err!=nil{t.Fatal(err)}
		defer res.Body.Close()
		raw,err:=io.ReadAll(res.Body);if err!=nil{t.Fatal(err)}
		return res.StatusCode,res.Header,string(raw)
	}
	code,h,html:=get("/ui/")
	if code!=200||!strings.Contains(html,"Agent collaboration console")||!strings.Contains(h.Get("Content-Security-Policy"),"script-src 'self'"){t.Fatalf("invalid embedded UI %d",code)}
	for _,path:=range []string{"/ui/bootstrap.js","/ui/vendor/sdk.js","/ui/vendor/cxd.css","/ui/vendor/LICENSE"}{
		if code,_,body:=get(path);code!=200||len(body)<100{t.Fatalf("asset missing %s %d",path,code)}
	}
	code,_,configJSON:=get("/ui/config.json")
	if code!=200||!strings.Contains(configJSON,`"theme":"cxd"`){t.Fatalf("config missing %d",code)}
	code,_,snapshot:=get("/api/v1/dashboard")
	if code!=200||!strings.Contains(snapshot,`"completedCount":1`)||strings.Contains(snapshot,"private fixture prompt"){t.Fatalf("snapshot invalid %d %s",code,snapshot)}
	code,_,raw:=get("/api/v1/deliveries?limit=1")
	if code!=200||strings.Contains(raw,"private fixture prompt")||strings.Contains(raw,"SIMULATED"){t.Fatalf("summary must omit payloads: %d %s",code,raw)}
	var summaries []agentiplex.DeliverySummary
	if err:=json.Unmarshal([]byte(raw),&summaries);err!=nil||len(summaries)!=1{t.Fatalf("summary: %v %s",err,raw)}
	if summaries[0].ID!=d.ID||summaries[0].State!="completed"||len(summaries[0].Children)!=2{t.Fatalf("aggregate summary: %+v",summaries)}
	if code,_,raw=get("/api/v1/deliveries/"+d.Children[0].ID);code!=200||!strings.Contains(raw,"private fixture prompt")||!strings.Contains(raw,"SIMULATED"){t.Fatalf("detail did not preserve actual result: %d %s",code,raw)}
	if code,_,_:=get("/api/v1/deliveries?limit=101");code!=400{t.Fatalf("invalid limit: %d",code)}
	disabled:=httptest.NewServer(api.HandlerWithDashboard(b,false,""))
	defer disabled.Close()
	res,err:=http.Get(disabled.URL+"/ui/");if err!=nil{t.Fatal(err)}
	res.Body.Close()
	if res.StatusCode!=404{t.Fatalf("dashboard disable: %d",res.StatusCode)}
}
