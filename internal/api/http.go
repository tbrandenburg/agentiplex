// Package api exposes the versioned trusted-local administration API.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/tbrandenburg/agentiplex"
	adapter "github.com/tbrandenburg/agentiplex/internal/adapters/a2a"
	cat "github.com/tbrandenburg/agentiplex/internal/catalog"
)

// Handler exposes the administration API and native A2A ingress when configured.
func Handler(b *agentiplex.Bus, publicURL ...string) http.Handler {
	mux:=http.NewServeMux()
	if len(publicURL)>0 && publicURL[0]!="" { adapter.Mount(mux,b,publicURL[0]) }
	mux.HandleFunc("GET /healthz",func(w http.ResponseWriter,r *http.Request){write(w,200,map[string]bool{"alive":true})})
	mux.HandleFunc("GET /readyz",func(w http.ResponseWriter,r *http.Request){
		if !b.Ready(){write(w,503,map[string]bool{"ready":false});return}
		write(w,200,map[string]bool{"ready":true})
	})
	mux.HandleFunc("POST /api/v1/agents",func(w http.ResponseWriter,r *http.Request){
		var in struct{ID string `json:"id"`; URL string `json:"url"`}
		if !decode(w,r,&in){return}
		a,err:=b.Register(r.Context(),in.ID,in.URL);respond(w,201,a,err)
	})
	mux.HandleFunc("GET /api/v1/agents",func(w http.ResponseWriter,r *http.Request){
		a,err:=b.Agents(r.URL.Query().Get("skill"),r.URL.Query().Get("tag"));respond(w,200,a,err)
	})
	mux.HandleFunc("GET /api/v1/agents/{id}",func(w http.ResponseWriter,r *http.Request){
		a,err:=b.Agent(r.PathValue("id"));respond(w,200,a,err)
	})
	mux.HandleFunc("GET "+cat.Path,func(w http.ResponseWriter,r *http.Request){
		data,err:=b.Catalog();if err!=nil{respond(w,200,nil,err);return}
		w.Header().Set("Content-Type",cat.MediaType);w.WriteHeader(200);_,_=w.Write(data)
	})
	mux.HandleFunc("POST /api/v1/messages",func(w http.ResponseWriter,r *http.Request){
		var in struct{Target string `json:"target"`;Text string `json:"text"`;Key string `json:"idempotencyKey"`}
		if !decode(w,r,&in){return}
		d,err:=b.Send(r.Context(),in.Target,in.Text,in.Key);respond(w,202,d,err)
	})
	mux.HandleFunc("GET /api/v1/deliveries/{id}",func(w http.ResponseWriter,r *http.Request){
		d,err:=b.Delivery(r.PathValue("id"));respond(w,200,d,err)
	})
	mux.HandleFunc("GET /api/v1/events",func(w http.ResponseWriter,r *http.Request){
		var after uint64
		if raw:=r.URL.Query().Get("after");raw!=""{v,err:=strconv.ParseUint(raw,10,64);if err!=nil{respond(w,200,nil,agentiplex.ErrInvalid);return};after=v}
		e,err:=b.Events(after);respond(w,200,e,err)
	})
	return mux
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
	case errors.Is(err,agentiplex.ErrInvalid):code=400
	case errors.Is(err,agentiplex.ErrNotFound):code=404
	case errors.Is(err,agentiplex.ErrConflict):code=409
	default:code=503
	}
	message:="operation failed"
	if code!=503{message=err.Error()}
	write(w,code,map[string]string{"error":message})
}

func write(w http.ResponseWriter,code int,v any){
	w.Header().Set("Content-Type","application/json");w.WriteHeader(code);_ = json.NewEncoder(w).Encode(v)
}
