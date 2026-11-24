// A provider-free independent MCP client. No Agentiplex library imports.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main(){
	if err:=run();err!=nil{log.Print(err);os.Exit(1)}
}

func run()error{
	endpoint:=flag.String("url","http://127.0.0.1:8080/mcp","MCP Streamable HTTP URL")
	target:=flag.String("target","","optional registered recipient")
	prompt:=flag.String("text","","explicit text request")
	key:=flag.String("key","","stable idempotency key for delegation")
	flag.Parse()
	if (*target==""&&(*prompt!=""||*key!=""))||(*target!=""&&(*prompt==""||*key=="")){return fmt.Errorf("provide target, text and key together")}
	ctx,cancel:=context.WithTimeout(context.Background(),2*time.Minute);defer cancel()
	client:=mcp.NewClient(&mcp.Implementation{Name:"agentiplex-example",Version:"0.1.0"},nil)
	session,err:=client.Connect(ctx,&mcp.StreamableClientTransport{Endpoint:*endpoint,DisableStandaloneSSE:true},nil)
	if err!=nil{return err};defer session.Close()
	tools,err:=session.ListTools(ctx,&mcp.ListToolsParams{});if err!=nil{return err}
	for _,tool:=range tools.Tools{fmt.Println("Tool:",tool.Name)}
	call:=func(name string,args map[string]any)(json.RawMessage,error){
		res,err:=session.CallTool(ctx,&mcp.CallToolParams{Name:name,Arguments:args})
		if err!=nil{return nil,err}
		if res.IsError{return nil,fmt.Errorf("%s failed: %v",name,res.Content)}
		return json.Marshal(res.StructuredContent)
	}
	discovered,err:=call("discover_agents",map[string]any{});if err!=nil{return err}
	fmt.Println("Discovered:",string(discovered))
	if *target==""{return nil}
	raw,err:=call("send_message",map[string]any{"target":*target,"text":*prompt,"idempotencyKey":*key});if err!=nil{return err}
	var receipt struct{ID string `json:"id"`}
	if err=json.Unmarshal(raw,&receipt);err!=nil{return err}
	if receipt.ID==""{return fmt.Errorf("missing delivery ID")}
	fmt.Println("Durable receipt:",string(raw))
	ticker:=time.NewTicker(time.Second);defer ticker.Stop()
	for{
		raw,err=call("get_delivery",map[string]any{"id":receipt.ID});if err!=nil{return err}
		var d struct{State string `json:"state"`}
		if err=json.Unmarshal(raw,&d);err!=nil{return err}
		switch d.State{
		case "completed":fmt.Println(string(raw));return nil
		case "failed","partial","uncertain","input-required","auth-required":fmt.Println(string(raw));return fmt.Errorf("delivery ended in %s; do not replay with a new key",d.State)
		}
		select{case<-ctx.Done():return fmt.Errorf("wait ended; delivery %s remains queryable: %w",receipt.ID,ctx.Err());case<-ticker.C:}
	}
}
