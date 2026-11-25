package cli

import (
	"bufio"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func stateCommand(r *remote,print func(*cobra.Command,string,string,any)error)*cobra.Command{
	root:=&cobra.Command{Use:"state",Short:"Revision-checked persistent application state"}
	path:=func(a []string)string{return "/api/v1/state/"+url.PathEscape(a[0])+"/"+url.PathEscape(a[1])}
	var revision uint64
	put:=&cobra.Command{Use:"put NAMESPACE KEY VALUE",Args:cobra.ExactArgs(3),RunE:func(c *cobra.Command,a []string)error{
		return print(c,"PUT",path(a),map[string]any{"value":a[2],"revision":revision})
	}}
	put.Flags().Uint64Var(&revision,"revision",0,"expected revision (zero creates only)")
	var removeRevision uint64
	remove:=&cobra.Command{Use:"delete NAMESPACE KEY",Args:cobra.ExactArgs(2),RunE:func(c *cobra.Command,a []string)error{
		if removeRevision==0{return fmt.Errorf("positive revision required")}
		return print(c,"DELETE",path(a)+"?revision="+strconv.FormatUint(removeRevision,10),nil)
	}}
	remove.Flags().Uint64Var(&removeRevision,"revision",0,"required current revision")
	_ = remove.MarkFlagRequired("revision")
	root.AddCommand(put,remove,
		&cobra.Command{Use:"get NAMESPACE KEY",Args:cobra.ExactArgs(2),RunE:func(c *cobra.Command,a []string)error{return print(c,"GET",path(a),nil)}},
		&cobra.Command{Use:"list NAMESPACE",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{return print(c,"GET","/api/v1/state/"+url.PathEscape(a[0]),nil)}},
		&cobra.Command{Use:"watch NAMESPACE",Short:"Watch current state and changes; reconnect starts a new snapshot",Args:cobra.ExactArgs(1),RunE:func(c *cobra.Command,a []string)error{
			req,err:=http.NewRequestWithContext(c.Context(),"GET",strings.TrimRight(r.base,"/")+"/api/v1/state-watch/"+url.PathEscape(a[0]),nil)
			if err!=nil{return err}
			r.authorize(req)
			h:=&http.Client{Transport:r.http.Transport,CheckRedirect:r.http.CheckRedirect}
			resp,err:=h.Do(req);if err!=nil{return err};defer resp.Body.Close()
			if resp.StatusCode!=200{return fmt.Errorf("state watch HTTP %d",resp.StatusCode)}
			scanner:=bufio.NewScanner(resp.Body);scanner.Buffer(make([]byte,4096),1<<20)
			for scanner.Scan(){if line:=scanner.Text();strings.HasPrefix(line,"data: "){if _,err=fmt.Fprintln(c.OutOrStdout(),strings.TrimPrefix(line,"data: "));err!=nil{return err}}}
			if c.Context().Err()!=nil{return nil}
			if err=scanner.Err();err!=nil{return err}
			return fmt.Errorf("state watch ended; reconnect to obtain a fresh snapshot")
		}},
	)
	return root
}
