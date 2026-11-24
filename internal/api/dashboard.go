package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tbrandenburg/agentiplex"
)

// A bounded presentation-only data adapter. The existing domain/API
// contracts remain independent of the UI library.
func mountDashboardData(mux *http.ServeMux,b *agentiplex.Bus){
	mux.HandleFunc("GET /api/v1/dashboard",func(w http.ResponseWriter,r *http.Request){
		w.Header().Set("Cache-Control","no-store")
		agents,err:=b.Agents("","");if err!=nil{respond(w,200,nil,err);return}
		groups,err:=b.Collections("group");if err!=nil{respond(w,200,nil,err);return}
		pools,err:=b.Collections("pool");if err!=nil{respond(w,200,nil,err);return}
		deliveries,err:=b.RecentDeliveries(50);if err!=nil{respond(w,200,nil,err);return}
		events,err:=b.RecentEvents(80);if err!=nil{respond(w,200,nil,err);return}
		a:=[]map[string]any{}
		for _,agent:=range agents{
			kind:="External agent";skills:=[]string{}
			for _,s:=range agent.Card.Skills{skills=append(skills,s.Name);for _,t:=range s.Tags{if t=="simulated"{kind="Simulated fixture"}}}
			a=append(a,map[string]any{"id":agent.ID,"kind":kind,"skills":strings.Join(skills,", "),"endpoint":agent.Endpoint})
		}
		c:=[]map[string]any{}
		for _,item:=range append(groups,pools...){c=append(c,map[string]any{"id":item.ID,"kind":item.Kind,"members":strings.Join(item.Members,", ")})}
		type row struct{agentiplex.DeliverySummary;RecipientStatus string `json:"recipientStatus"`}
		ds:=[]row{}
		pending,completed,attention:=0,0,0
		for _,d:=range deliveries{
			switch d.State{case "preparing","queued","attempting","accepted":pending++;case "completed":completed++;default:attention++}
			recipients:=[]string{}
			for _,child:=range d.Children{recipients=append(recipients,child.Target+": "+child.State)}
			ds=append(ds,row{DeliverySummary:d,RecipientStatus:strings.Join(recipients,", ")})
		}
		write(w,200,map[string]any{"agents":a,"collections":c,"deliveries":ds,"events":events,"agentCount":len(a),"collectionCount":fmt.Sprintf("%d / %d",len(groups),len(pools)),"pendingCount":pending,"completedCount":completed,"attentionCount":attention})
	})
	mux.HandleFunc("GET /api/v1/dashboard/detail/{id}",func(w http.ResponseWriter,r *http.Request){
		w.Header().Set("Cache-Control","no-store")
		d,err:=b.Delivery(r.PathValue("id"));if err!=nil{respond(w,200,nil,err);return}
		raw,err:=json.MarshalIndent(d,"","  ");if err!=nil{respond(w,200,nil,err);return}
		write(w,200,map[string]any{"id":d.ID,"state":d.State,"detailJSON":string(raw)})
	})
}
