// Package api exposes the versioned trusted-local administration API.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"fmt"
	"time"

	"github.com/tbrandenburg/agentiplex/core"
	adapter "github.com/tbrandenburg/agentiplex/internal/adapters/a2a"
	cat "github.com/tbrandenburg/agentiplex/internal/catalog"
	"github.com/tbrandenburg/agentiplex/internal/dashboard"
	"github.com/tbrandenburg/agentiplex/internal/security"
	mcpadapter "github.com/tbrandenburg/agentiplex/internal/adapters/mcp"
)

// Handler exposes the administration API and native A2A ingress when configured.
func Handler(b *core.Bus, publicURL ...string) http.Handler {
	u,_:=dashboard.New(dashboard.DefaultConfig())
	return handler(b,u,publicURL...)
}

// HandlerWithDashboard allows operators to disable the static UI.
func HandlerWithDashboard(b *core.Bus, enabled bool, publicURL string) http.Handler {
	var u *dashboard.UI
	if enabled{u,_=dashboard.New(dashboard.DefaultConfig())}
	return handler(b,u,publicURL)
}

// HandlerWithProtocols enables optional protocol adapters explicitly.
func HandlerWithProtocols(b *core.Bus,u *dashboard.UI,publicURL string,mcpEnabled bool)http.Handler{
	return HandlerSecure(b,u,publicURL,mcpEnabled,nil)
}

func HandlerSecure(b *core.Bus,u *dashboard.UI,publicURL string,mcpEnabled bool,auth *security.Auth)http.Handler{
	mux:=http.NewServeMux()
	if mcpEnabled{mux.Handle(mcpadapter.Path,mcpadapter.Handler(b,auth!=nil))}
	mux.Handle("/",handlerMode(b,u,auth!=nil,publicURL))
	return auth.Wrap(mux)
}

func handler(b *core.Bus, u *dashboard.UI, publicURL ...string) http.Handler {return handlerMode(b,u,false,publicURL...)}

func handlerMode(b *core.Bus,u *dashboard.UI,secured bool,publicURL ...string)http.Handler{
	mux:=http.NewServeMux()
	if u!=nil{u.Mount(mux);mountDashboardData(mux,b)}
	if len(publicURL)>0 && publicURL[0]!="" { adapter.Mount(mux,b,publicURL[0],secured) }
	mux.HandleFunc("GET /healthz",func(w http.ResponseWriter,r *http.Request){write(w,200,map[string]bool{"alive":true})})
	mux.HandleFunc("GET /readyz",func(w http.ResponseWriter,r *http.Request){
		if !b.Ready(){write(w,503,map[string]bool{"ready":false});return}
		write(w,200,map[string]bool{"ready":true})
	})
	mux.HandleFunc("GET /api/v1/usage",func(w http.ResponseWriter,r *http.Request){
		u,err:=b.Usage();respond(w,200,map[string]any{"deliveries":u},err)
	})
	mux.HandleFunc("POST /api/v1/agents",func(w http.ResponseWriter,r *http.Request){
		var in struct{ID string `json:"id"`; URL string `json:"url"`}
		if !decode(w,r,&in){return}
		a,err:=b.Register(r.Context(),in.ID,in.URL);respond(w,201,a,err)
	})
	mux.HandleFunc("GET /api/v1/agents",func(w http.ResponseWriter,r *http.Request){
		a,err:=b.Discover(r.URL.Query().Get("skill"),r.URL.Query().Get("tag"),r.URL.Query().Get("group"));respond(w,200,a,err)
	})
	mux.HandleFunc("GET /api/v1/agents/{id}",func(w http.ResponseWriter,r *http.Request){
		a,err:=b.Agent(r.PathValue("id"));respond(w,200,a,err)
	})
	mountLifecycle(mux,b)
	mux.HandleFunc("GET "+cat.Path,func(w http.ResponseWriter,r *http.Request){
		data,err:=b.Catalog();if err!=nil{respond(w,200,nil,err);return}
		w.Header().Set("Content-Type",cat.MediaType);w.WriteHeader(200);_,_=w.Write(data)
	})
	mux.HandleFunc("GET /api/v1/deliveries",func(w http.ResponseWriter,r *http.Request){
		limit:=50
		if raw:=r.URL.Query().Get("limit");raw!=""{n,e:=strconv.Atoi(raw);if e!=nil{respond(w,200,nil,core.ErrInvalid);return};limit=n}
		d,err:=b.RecentDeliveries(limit);respond(w,200,d,err)
	})
	mux.HandleFunc("GET /api/v1/deliveries/{id}",func(w http.ResponseWriter,r *http.Request){
		d,err:=b.DeliveryFor(r.Context(),r.PathValue("id"));respond(w,200,d,err)
	})
	mux.HandleFunc("GET /api/v1/deliveries/{id}/wait",func(w http.ResponseWriter,r *http.Request){
		w.Header().Set("Cache-Control","no-store")
		rc:=http.NewResponseController(w)
		// This is an observation of durable work, not a long execution
		// request. Clear the normal request deadline only on this route.
		_ = rc.SetReadDeadline(time.Time{})
		_ = rc.SetWriteDeadline(time.Time{})
		d,err:=b.WaitDelivery(r.Context(),r.PathValue("id"))
		if r.Context().Err()!=nil{return}
		_ = rc.SetWriteDeadline(time.Now().Add(10*time.Second))
		respond(w,200,d,err)
	})
	mux.HandleFunc("POST /api/v1/deliveries/{id}/abandon",func(w http.ResponseWriter,r *http.Request){
		var in struct{TaskID string `json:"taskId"`;RemoteStopped bool `json:"remoteStopped"`}
		if !decode(w,r,&in){return}
		d,err:=b.AbandonTask(r.Context(),r.PathValue("id"),in.TaskID,in.RemoteStopped);respond(w,200,d,err)
	})
	mux.HandleFunc("GET /api/v1/events",func(w http.ResponseWriter,r *http.Request){
		var after uint64
		if raw:=r.URL.Query().Get("after");raw!=""{v,err:=strconv.ParseUint(raw,10,64);if err!=nil{respond(w,200,nil,core.ErrInvalid);return};after=v}
		e,err:=b.Events(after);respond(w,200,e,err)
	})
	for _,kind:=range []string{"group","pool"}{
		path:="/api/v1/"+kind+"s"
		mux.HandleFunc("GET "+path,func(w http.ResponseWriter,r *http.Request){
			c,err:=b.Collections(kind);respond(w,200,c,err)
		})
		mux.HandleFunc("GET "+path+"/{id}",func(w http.ResponseWriter,r *http.Request){
			c,err:=b.Collection(kind,r.PathValue("id"));respond(w,200,c,err)
		})
		mux.HandleFunc("PUT "+path+"/{id}",func(w http.ResponseWriter,r *http.Request){
			var in struct{Members []string `json:"members"`}
			if !decode(w,r,&in){return}
			c,err:=b.SetCollection(r.Context(),kind,r.PathValue("id"),in.Members);respond(w,200,c,err)
		})
	}
	mux.HandleFunc("POST /api/v1/messages",func(w http.ResponseWriter,r *http.Request){
  var in struct {
   Target string `json:"target"`
   Text string `json:"text"`
   Key string `json:"idempotencyKey"`
   Follow *core.FollowOptions `json:"follow,omitempty"`
  }
  if !decode(w,r,&in){return}
  var d core.Delivery
  var err error
  if in.Follow!=nil{d,err=b.SendUntilDone(r.Context(),in.Target,in.Text,in.Key,*in.Follow)}else{d,err=b.Send(r.Context(),in.Target,in.Text,in.Key)}
  respond(w,202,d,err)
 })
 mux.HandleFunc("POST /api/v1/deliveries/{id}/nudge",func(w http.ResponseWriter,r *http.Request){
  d,err:=b.NudgeFollow(r.Context(),r.PathValue("id"));respond(w,200,d,err)
 })
 for _,route:=range []struct{
		path string
		submit func(context.Context,string,string,string)(core.Delivery,error)
	}{
		{"/api/v1/broadcasts",b.Broadcast},
		{"/api/v1/dispatches",b.Dispatch},
	}{
		mux.HandleFunc("POST "+route.path,func(w http.ResponseWriter,r *http.Request){
			var in struct{Target string `json:"target"`;Text string `json:"text"`;Key string `json:"idempotencyKey"`}
			if !decode(w,r,&in){return}
			d,err:=route.submit(r.Context(),in.Target,in.Text,in.Key);respond(w,202,d,err)
		})
	}
	mux.HandleFunc("GET /api/v1/events/stream",func(w http.ResponseWriter,r *http.Request){
		streamEvents(b,w,r)
	})
	return mux
}

func streamEvents(b *core.Bus,w http.ResponseWriter,r *http.Request){
	raw:=r.URL.Query().Get("after")
	if raw==""{raw=r.Header.Get("Last-Event-ID")}
	var after uint64
	if raw!=""{n,err:=strconv.ParseUint(raw,10,64);if err!=nil{respond(w,200,nil,core.ErrInvalid);return};after=n}
	rc:=http.NewResponseController(w)
	// SSE outlives the server's normal request write timeout. Individual
	// writes still have a deadline, preventing a slow reader from hanging.
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type","text/event-stream")
	w.Header().Set("Cache-Control","no-cache")
	w.Header().Set("X-Accel-Buffering","no")
	writeEvent:=func(data string)error{
		_ = rc.SetWriteDeadline(time.Now().Add(10*time.Second))
		if _,err:=fmt.Fprint(w,data);err!=nil{return err}
		if err:=rc.Flush();err!=nil{return err}
		_ = rc.SetWriteDeadline(time.Time{})
		return nil
	}
	if err:=writeEvent(": connected\n\n");err!=nil{return}
	// A bounded connection lifetime allows periodic reconnect even behind
	// proxies which cannot keep an idle stream open. Replay by sequence.
	ctx,cancel:=context.WithTimeout(r.Context(),5*time.Minute)
	defer cancel()
	_ = b.SubscribeEvents(ctx,after,r.URL.Query().Get("kind"),func(e core.Event)error{
		data,err:=json.Marshal(e);if err!=nil{return err}
		return writeEvent(fmt.Sprintf("id: %d\ndata: %s\n\n",e.Sequence,data))
	})
}

func decode(w http.ResponseWriter,r *http.Request,dst any)bool{
	r.Body=http.MaxBytesReader(w,r.Body,128<<10)
	dec:=json.NewDecoder(r.Body);dec.DisallowUnknownFields()
	if err:=dec.Decode(dst);err!=nil{write(w,400,map[string]string{"error":"invalid request body"});return false}
	var extra any
	if err:=dec.Decode(&extra);err!=io.EOF{write(w,400,map[string]string{"error":"expected one JSON object"});return false}
	return true
}

func respond(w http.ResponseWriter,code int,v any,err error){
	if err==nil{write(w,code,v);return}
	switch{
	case errors.Is(err,core.ErrInvalid):code=400
	case errors.Is(err,core.ErrNotFound):code=404
	case errors.Is(err,core.ErrConflict):code=409
	default:code=503
	}
	message:="operation failed"
	if code!=503{message=err.Error()}
	write(w,code,map[string]string{"error":message})
}

func write(w http.ResponseWriter,code int,v any){
	w.Header().Set("Content-Type","application/json");w.WriteHeader(code);_ = json.NewEncoder(w).Encode(v)
}
