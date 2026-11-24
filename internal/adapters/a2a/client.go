// Package a2a wraps the official SDK at the HTTP boundary.
package a2a

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	proto "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
)

type Client struct {
	allowed map[string]bool
	timeout time.Duration
}

func New(allowed []string, timeout time.Duration) (*Client, error) {
	c := &Client{allowed: map[string]bool{}, timeout: timeout}
	for _, raw := range allowed {
		u, err := parse(raw)
		if err != nil || (u.Path != "" && u.Path != "/") { return nil, errors.New("allowed origins must be HTTP(S) origins") }
		c.allowed[origin(u)] = true
	}
	return c, nil
}

func parse(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("expected HTTP(S) URL without credentials, query or fragment")
	}
	return u, nil
}

func origin(u *url.URL) string {
	port := u.Port()
	if port == "" { if u.Scheme == "https" { port = "443" } else { port = "80" } }
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func loopback(host string) bool {
	if strings.EqualFold(host,"localhost") { return true }
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

func (c *Client) check(raw string) (*url.URL,error) {
	u,err := parse(raw)
	if err != nil { return nil,err }
	if !loopback(u.Hostname()) && !c.allowed[origin(u)] { return nil,errors.New("endpoint origin not allowed") }
	return u,nil
}

type limitedTransport struct { base *http.Transport; origin string }
func (t limitedTransport) RoundTrip(r *http.Request) (*http.Response,error) {
	if origin(r.URL) != t.origin { return nil,errors.New("unexpected request origin") }
	resp,err := t.base.RoundTrip(r)
	if err == nil { resp.Body = http.MaxBytesReader(nil, resp.Body, 256<<10) }
	return resp,err
}

func (c *Client) httpClient(u *url.URL) (*http.Client, func()) {
	d := &net.Dialer{Timeout: 3*time.Second}
	t := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn,error) {
		host,port,err := net.SplitHostPort(address)
		if err != nil { return nil,err }
		ips,err := net.DefaultResolver.LookupNetIP(ctx,"ip",host)
		if err != nil { return nil,err }
		var last error = errors.New("no allowed address")
		for _, ip := range ips {
			ip=ip.Unmap()
			if !ip.IsValid() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || (loopback(host)&&!ip.IsLoopback()) { continue }
			conn,e:=d.DialContext(ctx,network,net.JoinHostPort(ip.String(),port))
			if e==nil { return conn,nil }
			last=e
		}
		return nil,last
	}, TLSHandshakeTimeout:5*time.Second, ResponseHeaderTimeout:c.timeout, DisableKeepAlives:true}
	return &http.Client{Transport:limitedTransport{base:t,origin:origin(u)},Timeout:c.timeout,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}},t.CloseIdleConnections
}

func (c *Client) Resolve(ctx context.Context, raw string) (*proto.AgentCard,string,string,error) {
	u,err:=c.check(raw)
	if err!=nil{return nil,"","",err}
	if u.Path==""||u.Path=="/"{u.Path="/.well-known/agent-card.json"}
	h,closeHTTP:=c.httpClient(u);defer closeHTTP()
	card,err:=agentcard.NewResolver(h).Resolve(ctx,u.String())
	if err!=nil{return nil,"","",err}
	if card==nil||strings.TrimSpace(card.Name)==""||strings.TrimSpace(card.Description)==""||card.Version==""||len(card.Skills)==0||len(card.DefaultInputModes)==0||len(card.DefaultOutputModes)==0 {
		return nil,"","",errors.New("incomplete agent card")
	}
	for _,skill:=range card.Skills{
		if skill.ID==""||skill.Name==""||skill.Description==""||len(skill.Tags)==0{return nil,"","",errors.New("incomplete skill")}
	}
	// Foundation supports the native A2A 1.0 JSON-RPC binding only.
	for _,iface:=range card.SupportedInterfaces{
		if iface==nil||iface.ProtocolVersion!=proto.Version||iface.ProtocolBinding!=proto.TransportProtocolJSONRPC||iface.Tenant!=""{continue}
		endpoint,e:=c.check(iface.URL)
		if e==nil&&origin(endpoint)==origin(u){return card,u.String(),endpoint.String(),nil}
	}
	return nil,"","",errors.New("no supported same-origin A2A 1.0 JSON-RPC interface")
}

func (c *Client) connect(ctx context.Context, endpoint string)(*a2aclient.Client,func(),error){
	u,err:=c.check(endpoint)
	if err!=nil{return nil,func(){},err}
	h,closeHTTP:=c.httpClient(u)
	client,err:=a2aclient.NewFromEndpoints(ctx,[]*proto.AgentInterface{proto.NewAgentInterface(endpoint,proto.TransportProtocolJSONRPC)},a2aclient.WithJSONRPCTransport(h))
	if err!=nil{closeHTTP();return nil,func(){},err}
	return client,func(){_ = client.Destroy();closeHTTP()},nil
}

func (c *Client) Send(ctx context.Context,endpoint,id,text string)(proto.SendMessageResult,error){
	cl,closeClient,err:=c.connect(ctx,endpoint);defer closeClient()
	if err!=nil{return nil,err}
	msg:=proto.NewMessage(proto.MessageRoleUser,proto.NewTextPart(text))
	msg.ID=id
	return cl.SendMessage(ctx,&proto.SendMessageRequest{Message:msg,Config:&proto.SendMessageConfig{ReturnImmediately:true}})
}

func (c *Client) Task(ctx context.Context,endpoint string,id proto.TaskID)(*proto.Task,error){
	cl,closeClient,err:=c.connect(ctx,endpoint);defer closeClient()
	if err!=nil{return nil,fmt.Errorf("connect: %w",err)}
	return cl.GetTask(ctx,&proto.GetTaskRequest{ID:id})
}
