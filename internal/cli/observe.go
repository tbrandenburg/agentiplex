package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

func waitCommand(r *remote) *cobra.Command {
	var raw bool
	cmd:=&cobra.Command{Use:"wait ID",Short:"Wait for an existing delivery (no resubmission)",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{
		return waitAndPrint(c,r,a[0],raw)
	}}
	cmd.Flags().BoolVar(&raw,"json",false,"print full delivery JSON")
	return cmd
}

func waitAndPrint(c *cobra.Command,r *remote,id string,raw bool)error{
	req,err:=http.NewRequestWithContext(c.Context(),"GET",strings.TrimRight(r.base,"/")+"/api/v1/deliveries/"+url.PathEscape(id)+"/wait",nil)
	if err!=nil{return err}
	r.authorize(req)
	// Preserve TLS and redirect restrictions, but never apply the ordinary
	// REST client timeout to an observation of a durable delivery.
	h:=&http.Client{Transport:r.http.Transport,CheckRedirect:r.http.CheckRedirect}
	resp,err:=h.Do(req)
	if err!=nil{return fmt.Errorf("wait detached; reattach with messages wait %s: %w",id,err)}
	defer resp.Body.Close()
	data,err:=io.ReadAll(http.MaxBytesReader(nil,resp.Body,32<<20))
	if err!=nil{return fmt.Errorf("wait detached; reattach with messages wait %s: %w",id,err)}
	if resp.StatusCode!=200{return fmt.Errorf("wait HTTP %d: %s",resp.StatusCode,strings.TrimSpace(string(data)))}
	var d struct{
		ID,Target,State,Detail string
		Result json.RawMessage
		Children []struct{ID,Target,State,Detail string}
	}
	if err=json.Unmarshal(data,&d);err!=nil{return err}
	if d.ID!=id{return fmt.Errorf("wait returned unexpected delivery ID")}
	switch d.State{case "completed","failed","partial","uncertain","input-required","auth-required":default:return fmt.Errorf("wait returned nonterminal state %q",d.State)}
	out:=c.OutOrStdout()
	if raw{
		if _,err=fmt.Fprintln(out,strings.TrimSpace(string(data)));err!=nil{return err}
	}else{
		if _,err=fmt.Fprintf(out,"%s: %s (%s)\n",safeText(d.Target),d.State,d.ID);err!=nil{return err}
		if d.Detail!=""{if _,err=fmt.Fprintln(out,safeText(d.Detail));err!=nil{return err}}
		for _,child:=range d.Children{
			if _,err=fmt.Fprintf(out,"  %s: %s (%s)\n",safeText(child.Target),safeText(child.State),safeText(child.ID));err!=nil{return err}
		}
		if len(d.Result)>0{
			text:=resultText(d.Result)
			if text!=""{if _,err=fmt.Fprintln(out,"\n"+safeText(text));err!=nil{return err}}else{
				if _,err=fmt.Fprintln(out,string(d.Result));err!=nil{return err}
			}
		}
	}
	if d.State!="completed"{return fmt.Errorf("delivery %s ended in %s; inspect its record, do not blindly resubmit",d.ID,d.State)}
	return nil
}

// A2A Message text or Task artifact text; prefer final artifacts over status
// messages/history. Structured/non-text results remain available via --json.
func resultText(raw json.RawMessage)string{
	type part struct{Text string}
	type message struct{Parts []part}
	var v struct{
		Parts []part
		Artifacts []message
		Status struct{Message message}
	}
	if json.Unmarshal(raw,&v)!=nil{return ""}
	var text []string
	appendParts:=func(parts []part){for _,p:=range parts{if p.Text!=""{text=append(text,p.Text)}}}
	for _,a:=range v.Artifacts{appendParts(a.Parts)}
	if len(text)==0{appendParts(v.Parts)}
	if len(text)==0{appendParts(v.Status.Message.Parts)}
	return strings.Join(text,"\n\n")
}

// Model output is untrusted. Do not send terminal control sequences.
func safeText(s string)string{
	return strings.Map(func(r rune)rune{if unicode.IsControl(r)&&r!='\n'&&r!='\t'{return -1};return r},s)
}

func notesCommand(r *remote)*cobra.Command{
	root:=&cobra.Command{Use:"notes",Short:"Read shared notes"}
	var raw bool
	var limit int
	list:=&cobra.Command{Use:"list NAMESPACE [TOPIC]",Short:"Read recent notes in time order",Args:cobra.RangeArgs(1,2),RunE:func(c *cobra.Command,a []string)error{
		if limit<1||limit>100{return fmt.Errorf("limit must be 1–100")}
		q:=url.Values{"limit":{strconv.Itoa(limit)}}
		if len(a)==2{q.Set("topic",a[1])}
		data,err:=r.call(c.Context(),"GET","/api/v1/notes/"+url.PathEscape(a[0])+"?"+q.Encode(),nil)
		if err!=nil{return err}
		if raw{_,err=fmt.Fprintln(c.OutOrStdout(),strings.TrimSpace(string(data)));return err}
		var notes []struct{Topic,Author,Text,At string}
		if err=json.Unmarshal(data,&notes);err!=nil{return err}
		if len(notes)==0{_,err=fmt.Fprintln(c.OutOrStdout(),"No notes.");return err}
		for _,n:=range notes{
			author:=n.Author
			if author==""{author="unverified local author"}
			if _,err=fmt.Fprintf(c.OutOrStdout(),"[%s] %s — %s\n%s\n\n",safeText(n.Topic),safeText(author),safeText(n.At),safeText(n.Text));err!=nil{return err}
		}
		return nil
	}}
	list.Flags().BoolVar(&raw,"json",false,"print note array as JSON")
	list.Flags().IntVar(&limit,"limit",100,"last N notes (1–100)")
	root.AddCommand(list)
	return root
}
