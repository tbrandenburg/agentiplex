// Package cli implements a remote client, not a second broker instance.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type remote struct{base string;http *http.Client}

func(c *remote) call(ctx context.Context,method,path string,in any)([]byte,error){
	var body io.Reader
	if in!=nil{data,err:=json.Marshal(in);if err!=nil{return nil,err};body=bytes.NewReader(data)}
	req,err:=http.NewRequestWithContext(ctx,method,strings.TrimRight(c.base,"/")+path,body)
	if err!=nil{return nil,err}
	if in!=nil{req.Header.Set("Content-Type","application/json")}
	resp,err:=c.http.Do(req);if err!=nil{return nil,err};defer resp.Body.Close()
	data,err:=io.ReadAll(http.MaxBytesReader(nil,resp.Body,32<<20));if err!=nil{return nil,err}
	if resp.StatusCode<200||resp.StatusCode>=300{return nil,fmt.Errorf("HTTP %d: %s",resp.StatusCode,strings.TrimSpace(string(data)))}
	return data,nil
}

func Command()*cobra.Command{
	base:=os.Getenv("AGENTIPLEX_SERVER")
	if base==""{base="http://127.0.0.1:8080"}
	r:=&remote{base:base,http:&http.Client{Timeout:150*time.Second}}
	root:=&cobra.Command{Use:"agentiplex",Short:"Remote Agentiplex administration",SilenceUsage:true}
	root.PersistentFlags().StringVar(&r.base,"server",base,"daemon HTTP URL")
	print:=func(cmd *cobra.Command,method,path string,in any)error{
		data,err:=r.call(cmd.Context(),method,path,in);if err!=nil{return err}
		_,err=fmt.Fprintln(cmd.OutOrStdout(),strings.TrimSpace(string(data)));return err
	}
	root.AddCommand(&cobra.Command{Use:"register ID URL",Args:cobra.ExactArgs(2),RunE:func(c *cobra.Command,a []string)error{return print(c,"POST","/api/v1/agents",map[string]string{"id":a[0],"url":a[1]})}})
	agents:=&cobra.Command{Use:"agents"}
	agents.AddCommand(&cobra.Command{Use:"list",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/api/v1/agents",nil)}},&cobra.Command{Use:"get ID",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/api/v1/agents/"+url.PathEscape(a[0]),nil)}})
	root.AddCommand(agents)
	var skill,tag string
	discover:=&cobra.Command{Use:"discover",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{
		q:=url.Values{"skill":{skill},"tag":{tag}}
		return print(c,"GET","/api/v1/agents?"+q.Encode(),nil)
	}}
	discover.Flags().StringVar(&skill,"skill","","advertised skill ID or name")
	discover.Flags().StringVar(&tag,"tag","","advertised skill tag")
	root.AddCommand(discover)
	catalog:=&cobra.Command{Use:"catalog"}
	catalog.AddCommand(&cobra.Command{Use:"get",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/.well-known/ai-catalog.json",nil)}})
	root.AddCommand(catalog)
	var key string
	send:=&cobra.Command{Use:"send TARGET TEXT",Args:cobra.ExactArgs(2),RunE:func(c *cobra.Command,a []string)error{
		return print(c,"POST","/api/v1/messages",map[string]string{"target":a[0],"text":a[1],"idempotencyKey":key})
	}}
	send.Flags().StringVar(&key,"idempotency-key","","stable caller-generated key for safe resubmission")
	root.AddCommand(send)
	messages:=&cobra.Command{Use:"messages"}
	messages.AddCommand(&cobra.Command{Use:"status ID",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/api/v1/deliveries/"+url.PathEscape(a[0]),nil)}})
	root.AddCommand(messages)
	root.AddCommand(&cobra.Command{Use:"status",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/readyz",nil)}})
	root.AddCommand(&cobra.Command{Use:"health",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/healthz",nil)}})
	var follow bool
	events:=&cobra.Command{Use:"events",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{
		var after uint64
		for{
			data,err:=r.call(c.Context(),"GET","/api/v1/events?after="+strconv.FormatUint(after,10),nil);if err!=nil{return err}
			var items []struct{Sequence uint64 `json:"sequence"`}
			var raw []json.RawMessage
			if err=json.Unmarshal(data,&items);err!=nil{return err}
			if err=json.Unmarshal(data,&raw);err!=nil{return err}
			for i,item:=range items{after=item.Sequence;if _,err=fmt.Fprintln(c.OutOrStdout(),string(raw[i]));err!=nil{return err}}
			if len(items)==100{continue}
			if !follow{return nil}
			select{case<-c.Context().Done():return nil;case<-time.After(time.Second):}
		}
	}}
	events.Flags().BoolVar(&follow,"follow",false,"poll durable event log continuously")
	root.AddCommand(events)
	return root
}
