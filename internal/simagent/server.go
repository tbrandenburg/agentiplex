// Package simagent exposes a provider-free, real SDK A2A service.
package simagent

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

func Handler(base,name,skill string,onCall func())http.Handler{
	card:=&a2a.AgentCard{Name:name,Description:"Deterministic simulated agent: "+skill,Version:"0.1.0",
		SupportedInterfaces:[]*a2a.AgentInterface{a2a.NewAgentInterface(base+"/invoke",a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:[]string{"text/plain"},DefaultOutputModes:[]string{"text/plain"},
		Skills:[]a2a.AgentSkill{{ID:skill,Name:skill,Description:"Simulated "+skill,Tags:[]string{skill,"simulated"}}}}
	executor:=a2asrv.AgentExecutorFunc(func(ctx context.Context,ec *a2asrv.ExecutorContext)iter.Seq2[a2a.Event,error]{
		return func(yield func(a2a.Event,error)bool){
			if onCall!=nil{onCall()}
			yield(a2a.NewMessage(a2a.MessageRoleAgent,a2a.NewTextPart("SIMULATED "+name+": request received")),nil)
		}
	})
	mux:=http.NewServeMux()
	mux.Handle("/invoke",a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))
	mux.Handle(a2asrv.WellKnownAgentCardPath,a2asrv.NewStaticAgentCardHandler(card))
	return mux
}
