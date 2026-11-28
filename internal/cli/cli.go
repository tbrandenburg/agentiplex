// Package cli implements a remote client, not a second broker instance.
package cli

import (
	"bufio"
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
	"github.com/tbrandenburg/agentiplex/internal/security"
)

type remote struct{base string;http *http.Client;token string}

func(c *remote) authorize(req *http.Request){if c.token!=""{req.Header.Set("Authorization","Bearer "+c.token)}}

func(c *remote) call(ctx context.Context,method,path string,in any)([]byte,error){
	var body io.Reader
	if in!=nil{data,err:=json.Marshal(in);if err!=nil{return nil,err};body=bytes.NewReader(data)}
	req,err:=http.NewRequestWithContext(ctx,method,strings.TrimRight(c.base,"/")+path,body)
	if err!=nil{return nil,err}
	c.authorize(req)
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
	var tokenFile,caFile string
	root.PersistentFlags().StringVar(&tokenFile,"token-file",os.Getenv("AGENTIPLEX_TOKEN_FILE"),"bearer credential file")
	root.PersistentFlags().StringVar(&caFile,"ca-file",os.Getenv("AGENTIPLEX_CA_FILE"),"additional trusted CA certificate")
	root.PersistentPreRunE=func(*cobra.Command,[]string)error{
		u,err:=url.Parse(r.base);if err!=nil||u.Hostname()==""||u.User!=nil||u.RawQuery!=""||u.Fragment!=""||(u.Scheme!="http"&&u.Scheme!="https"){return fmt.Errorf("invalid daemon origin")}
		if tokenFile!=""{if u.Scheme!="https"{return fmt.Errorf("credential requires HTTPS")};r.token,err=security.ReadToken(tokenFile);if err!=nil{return err}}
		tlsConfig,err:=security.TLSConfig(caFile);if err!=nil{return err}
		transport:=http.DefaultTransport.(*http.Transport).Clone();transport.TLSClientConfig=tlsConfig
		r.http.Transport=transport;r.http.CheckRedirect=func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse};return nil
	}
	print:=func(cmd *cobra.Command,method,path string,in any)error{
		data,err:=r.call(cmd.Context(),method,path,in);if err!=nil{return err}
		_,err=fmt.Fprintln(cmd.OutOrStdout(),strings.TrimSpace(string(data)));return err
	}
	root.AddCommand(&cobra.Command{Use:"register ID URL",Args:cobra.ExactArgs(2),RunE:func(c *cobra.Command,a []string)error{return print(c,"POST","/api/v1/agents",map[string]string{"id":a[0],"url":a[1]})}})
	agents:=&cobra.Command{Use:"agents"}
	agents.AddCommand(&cobra.Command{Use:"list",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/api/v1/agents",nil)}},&cobra.Command{Use:"get ID",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/api/v1/agents/"+url.PathEscape(a[0]),nil)}})
	agents.AddCommand(&cobra.Command{Use:"refresh ID",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{return print(c,"POST","/api/v1/agents/"+url.PathEscape(a[0])+"/refresh",nil)}},
	&cobra.Command{Use:"remove ID",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{return print(c,"DELETE","/api/v1/agents/"+url.PathEscape(a[0]),nil)}},
	&cobra.Command{Use:"health ID",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{return print(c,"POST","/api/v1/agents/"+url.PathEscape(a[0])+"/health",nil)}})
	root.AddCommand(agents)
	root.AddCommand(stateCommand(r,print),notesCommand(r))
	var skill,tag,group string
	discover:=&cobra.Command{Use:"discover",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{
		q:=url.Values{"skill":{skill},"tag":{tag},"group":{group}}
		return print(c,"GET","/api/v1/agents?"+q.Encode(),nil)
	}}
	discover.Flags().StringVar(&skill,"skill","","advertised skill ID or name")
	discover.Flags().StringVar(&tag,"tag","","advertised skill tag")
	discover.Flags().StringVar(&group,"group","","configured group ID")
	root.AddCommand(discover)
	catalog:=&cobra.Command{Use:"catalog"}
	catalog.AddCommand(&cobra.Command{Use:"get",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/.well-known/ai-catalog.json",nil)}})
	root.AddCommand(catalog)
	var key string
	var sendWait,untilDone bool
 var file string
 var maxTurns int
 var nudgeAfter,deadline time.Duration
 send:=&cobra.Command{Use:"send TARGET [TEXT]",Args:cobra.RangeArgs(1,2),RunE:func(c *cobra.Command,a []string)error{
  text,err:=messageInput(c,a[1:],file);if err!=nil{return err}
  in:=map[string]any{"target":a[0],"text":text,"idempotencyKey":key}
  if untilDone {
   if maxTurns<1||maxTurns>100||deadline<time.Second||deadline>24*time.Hour||deadline%time.Second!=0||nudgeAfter<0||nudgeAfter>24*time.Hour||nudgeAfter%time.Second!=0{return fmt.Errorf("invalid continuation limits (1–100 turns, whole seconds, deadline 1s–24h)")}
   in["follow"]=map[string]int{"maxTurns":maxTurns,"timeoutSeconds":int(deadline/time.Second),"nudgeSeconds":int(nudgeAfter/time.Second)}
  }else if c.Flags().Changed("max-turns")||c.Flags().Changed("timeout")||c.Flags().Changed("nudge-after"){return fmt.Errorf("continuation limits require --until-done")}
		if !sendWait{return print(c,"POST","/api/v1/messages",in)}
		raw,err:=r.call(c.Context(),"POST","/api/v1/messages",in);if err!=nil{return err}
		var receipt struct{ID string}
		if err=json.Unmarshal(raw,&receipt);err!=nil{return err}
		if receipt.ID==""{return fmt.Errorf("invalid delivery receipt")}
		fmt.Fprintf(c.ErrOrStderr(),"Delivery %s admitted; waiting (Ctrl-C detaches, not cancels).\n",receipt.ID)
		return waitAndPrint(c,r,receipt.ID,false)
	}}
	send.Flags().StringVar(&key,"idempotency-key","","required stable caller-generated key, created once per intended operation and reused only to retry")
	send.Flags().BoolVar(&sendWait,"wait",false,"wait for the admitted message's selected completion lifecycle")
 send.Flags().StringVar(&file,"file","","read message content from a file, or - for stdin")
 send.Flags().BoolVar(&untilDone,"until-done",false,"continue across turns until explicit resolution or intervention (dedicated daemon)")
 send.Flags().IntVar(&maxTurns,"max-turns",20,"maximum coordinator activations, including initial turn")
 send.Flags().DurationVar(&deadline,"timeout",2*time.Hour,"durable continuation deadline, not a wait timeout")
 send.Flags().DurationVar(&nudgeAfter,"nudge-after",0,"optional inactivity watchdog interval (0 disables)")
	_ = send.MarkFlagRequired("idempotency-key")
	root.AddCommand(send)
	for _,kind:=range []string{"group","pool"}{
		path:="/api/v1/"+kind+"s"
		cmd:=&cobra.Command{Use:kind+"s"}
		cmd.AddCommand(&cobra.Command{Use:"set ID MEMBER [MEMBER...]",Args:cobra.MinimumNArgs(2),RunE:func(c *cobra.Command,a []string)error{
			return print(c,"PUT",path+"/"+url.PathEscape(a[0]),map[string][]string{"members":a[1:]})
		}},&cobra.Command{Use:"list",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{return print(c,"GET",path,nil)}},
		&cobra.Command{Use:"get ID",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{return print(c,"GET",path+"/"+url.PathEscape(a[0]),nil)}})
		root.AddCommand(cmd)
	}
	for _,kind:=range []string{"broadcast","dispatch"}{
		path:="/api/v1/"+kind+"s"
		if kind=="dispatch"{path="/api/v1/dispatches"}
		var key string
		cmd:=&cobra.Command{Use:kind+" TARGET TEXT",Args:cobra.ExactArgs(2),RunE:func(c *cobra.Command,a []string)error{
			return print(c,"POST",path,map[string]string{"target":a[0],"text":a[1],"idempotencyKey":key})
		}}
		cmd.Flags().StringVar(&key,"idempotency-key","","required stable caller-generated key, created once per intended operation and reused only to retry")
		_ = cmd.MarkFlagRequired("idempotency-key")
		root.AddCommand(cmd)
	}
	messages:=&cobra.Command{Use:"messages"}
	messages.AddCommand(&cobra.Command{Use:"status ID",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/api/v1/deliveries/"+url.PathEscape(a[0]),nil)}})
	var listLimit int
 listMessages:=&cobra.Command{Use:"list",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{
  if listLimit<1||listLimit>100{return fmt.Errorf("limit must be 1–100")}
  return print(c,"GET","/api/v1/deliveries?limit="+strconv.Itoa(listLimit),nil)
 }}
 listMessages.Flags().IntVar(&listLimit,"limit",50,"recent top-level deliveries")
 messages.AddCommand(listMessages,&cobra.Command{Use:"nudge ID",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{return print(c,"POST","/api/v1/deliveries/"+url.PathEscape(a[0])+"/nudge",nil)}})
 var remoteStopped bool
	abandon:=&cobra.Command{Use:"abandon ID DOWNSTREAM_TASK_ID",Short:"Stop monitoring only after verifying remote execution stopped; outcome remains uncertain",Args:cobra.ExactArgs(2),RunE:func(c *cobra.Command,a []string)error{
		if !remoteStopped{return fmt.Errorf("verify remote execution stopped before using --confirm-remote-stopped")}
		return print(c,"POST","/api/v1/deliveries/"+url.PathEscape(a[0])+"/abandon",map[string]any{"taskId":a[1],"remoteStopped":true})
	}}
	abandon.Flags().BoolVar(&remoteStopped,"confirm-remote-stopped",false,"confirm independently that remote execution stopped; no cancellation or replay")
	messages.AddCommand(abandon,waitCommand(r))
	root.AddCommand(messages)
	root.AddCommand(&cobra.Command{Use:"status",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/readyz",nil)}})
	root.AddCommand(&cobra.Command{Use:"health",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/healthz",nil)}})
	var follow bool
	var after uint64
	var kind string
	events:=&cobra.Command{Use:"events",Args:cobra.NoArgs,RunE:func(c *cobra.Command,a []string)error{
		if follow{return r.follow(c.Context(),c.OutOrStdout(),after,kind)}
		for{
			data,err:=r.call(c.Context(),"GET","/api/v1/events?after="+strconv.FormatUint(after,10),nil);if err!=nil{return err}
			var items []struct{Sequence uint64 `json:"sequence"`}
			var raw []json.RawMessage
			if err=json.Unmarshal(data,&items);err!=nil{return err}
			if err=json.Unmarshal(data,&raw);err!=nil{return err}
			for i,item:=range items{after=item.Sequence;var event struct{Kind string `json:"kind"`};if err=json.Unmarshal(raw[i],&event);err!=nil{return err};if kind==""||event.Kind==kind{if _,err=fmt.Fprintln(c.OutOrStdout(),string(raw[i]));err!=nil{return err}}}
			if len(items)==100{continue}
			return nil
		}
	}}
	events.Flags().BoolVar(&follow,"follow",false,"subscribe to durable event log via SSE")
	events.Flags().Uint64Var(&after,"after",0,"resume after event sequence")
	events.Flags().StringVar(&kind,"kind","","exact event kind filter")
	root.AddCommand(events)
	return root
}

// follow reconnects the SSE stream from the last successfully printed event.
func (c *remote) follow(ctx context.Context,out io.Writer,after uint64,kind string)error{
	h:=&http.Client{Transport:c.http.Transport,CheckRedirect:c.http.CheckRedirect}
	for{
		if ctx.Err()!=nil{return nil}
		q:=url.Values{"after":{strconv.FormatUint(after,10)},"kind":{kind}}
		req,err:=http.NewRequestWithContext(ctx,"GET",strings.TrimRight(c.base,"/")+"/api/v1/events/stream?"+q.Encode(),nil)
		if err!=nil{return err}
		c.authorize(req)
		req.Header.Set("Accept","text/event-stream")
		resp,err:=h.Do(req)
		if err!=nil{if ctx.Err()!=nil{return nil};return err}
		if resp.StatusCode!=200{resp.Body.Close();return fmt.Errorf("event subscription HTTP %d",resp.StatusCode)}
		scanner:=bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte,4096),1<<20)
		for scanner.Scan(){
			line:=scanner.Text()
			if !strings.HasPrefix(line,"data: "){continue}
			data:=strings.TrimPrefix(line,"data: ")
			var event struct{Sequence uint64 `json:"sequence"`}
			if err=json.Unmarshal([]byte(data),&event);err!=nil{resp.Body.Close();return err}
			if event.Sequence<=after{continue}
			if _,err=fmt.Fprintln(out,data);err!=nil{resp.Body.Close();return err}
			after=event.Sequence
		}
		resp.Body.Close()
		if ctx.Err()!=nil{return nil}
		if err=scanner.Err();err!=nil{return err}
		select{case<-ctx.Done():return nil;case<-time.After(time.Second):}
	}
}

// messageInput keeps the document format independent of completion semantics.
func messageInput(c *cobra.Command,args []string,file string)(string,error){
 if file=="" {if len(args)!=1{return "",fmt.Errorf("provide text or --file")};return args[0],nil}
 if len(args)!=0{return "",fmt.Errorf("text and --file are mutually exclusive")}
 var reader io.Reader=c.InOrStdin()
 if file!="-" {f,err:=os.Open(file);if err!=nil{return "",err};defer f.Close();reader=f}
 raw,err:=io.ReadAll(io.LimitReader(reader,(64<<10)+1));if err!=nil{return "",err}
 if len(raw)>64<<10{return "",fmt.Errorf("input exceeds 64 KiB")}
 return string(raw),nil
}
