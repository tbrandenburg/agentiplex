package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/tbrandenburg/agentiplex"
)

func mountLifecycle(mux *http.ServeMux,b *agentiplex.Bus){
	mux.HandleFunc("POST /api/v1/agents/{id}/refresh",func(w http.ResponseWriter,r *http.Request){
		a,err:=b.Refresh(r.Context(),r.PathValue("id"));respond(w,200,a,err)
	})
	mux.HandleFunc("DELETE /api/v1/agents/{id}",func(w http.ResponseWriter,r *http.Request){
		err:=b.RemoveAgent(r.Context(),r.PathValue("id"));respond(w,200,map[string]bool{"removed":true},err)
	})
	mux.HandleFunc("GET /api/v1/agents/{id}/health",func(w http.ResponseWriter,r *http.Request){
		h,err:=b.AgentHealth(r.PathValue("id"));respond(w,200,h,err)
	})
	mux.HandleFunc("POST /api/v1/agents/{id}/health",func(w http.ResponseWriter,r *http.Request){
		h,err:=b.ProbeAgent(r.Context(),r.PathValue("id"));respond(w,200,h,err)
	})
	mux.HandleFunc("GET /api/v1/state/{namespace}",func(w http.ResponseWriter,r *http.Request){
		w.Header().Set("Cache-Control","no-store")
		s,err:=b.ListState(r.PathValue("namespace"));respond(w,200,s,err)
	})
	mux.HandleFunc("GET /api/v1/state/{namespace}/{key}",func(w http.ResponseWriter,r *http.Request){
		w.Header().Set("Cache-Control","no-store")
		s,err:=b.GetState(r.PathValue("namespace"),r.PathValue("key"));respond(w,200,s,err)
	})
	mux.HandleFunc("PUT /api/v1/state/{namespace}/{key}",func(w http.ResponseWriter,r *http.Request){
		var in struct{Value *string `json:"value"`;Revision *uint64 `json:"revision"`}
		if !decode(w,r,&in){return}
		if in.Value==nil||in.Revision==nil{respond(w,200,nil,agentiplex.ErrInvalid);return}
		s,err:=b.PutState(r.Context(),r.PathValue("namespace"),r.PathValue("key"),*in.Value,*in.Revision);respond(w,200,s,err)
	})
	mux.HandleFunc("DELETE /api/v1/state/{namespace}/{key}",func(w http.ResponseWriter,r *http.Request){
		rev,err:=strconv.ParseUint(r.URL.Query().Get("revision"),10,64)
		if err!=nil||rev==0{respond(w,200,nil,agentiplex.ErrInvalid);return}
		err=b.DeleteState(r.Context(),r.PathValue("namespace"),r.PathValue("key"),rev);respond(w,200,map[string]bool{"deleted":true},err)
	})
	// Watch is a distinct route so the literal "watch" remains a valid key.
	mux.HandleFunc("GET /api/v1/state-watch/{namespace}",func(w http.ResponseWriter,r *http.Request){
		ns:=r.PathValue("namespace")
		if _,err:=b.ListState(ns);err!=nil{respond(w,200,nil,err);return}
		rc:=http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type","text/event-stream")
		w.Header().Set("Cache-Control","no-store")
		w.Header().Set("X-Accel-Buffering","no")
		ctx,cancel:=context.WithTimeout(r.Context(),5*time.Minute);defer cancel()
		_ = b.WatchState(ctx,ns,func(change agentiplex.StateChange)error{
			data,err:=json.Marshal(change);if err!=nil{return err}
			_ = rc.SetWriteDeadline(time.Now().Add(10*time.Second))
			if _,err=fmt.Fprintf(w,"data: %s\n\n",data);err!=nil{return err}
			if err=rc.Flush();err!=nil{return err}
			_ = rc.SetWriteDeadline(time.Time{})
			return nil
		})
	})
}
