// Package simagent exposes a provider-free, real SDK A2A service.
package simagent

import (
	"context"
	"iter"
	"net/http"
	"strings"

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
			yield(a2a.NewMessage(a2a.MessageRoleAgent,a2a.NewTextPart(response(name,skill,ec.Message))),nil)
		}
	})
	mux:=http.NewServeMux()
	mux.Handle("/invoke",a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))
	mux.Handle(a2asrv.WellKnownAgentCardPath,a2asrv.NewStaticAgentCardHandler(card))
	return mux
}

// Deterministic, role-specific fixtures. These are not model outputs,
// executed tests, repository edits, or safety evidence.
func response(name,skill string,message *a2a.Message)string{
	prompt:=""
	if message!=nil{
		for _,part:=range message.Parts{if part!=nil{prompt+=part.Text()+" "}}
	}
	prompt=strings.TrimSpace(prompt)
	runes:=[]rune(prompt)
	if len(runes)>240{prompt=string(runes[:240])+"…"}
	role:="Acknowledged the request."
	switch skill{
	case "planning":role="Fixture plan: assign architecture review, implementation review, verification and quality review."
	case "architecture":role="Fixture review: separate communication-loss detection from the motor stop action; trace the 100 ms requirement and prohibit automatic restart."
	case "development":role="Fixture implementation proposal: latch the communication-loss state and require explicit reset. No files were changed."
	case "testing":role="Fixture test plan: inject communication loss, measure stop latency and check restart inhibition. No test was executed."
	case "quality":role="Fixture quality review: require measured timing evidence, traceability and an independent approval. No release approval was issued."
	}
	return "SIMULATED "+name+": request received. "+role+"\nInput: "+prompt
}
