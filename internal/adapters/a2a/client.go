// Package a2a wraps the official SDK at the HTTP boundary.
package a2a

import (
	"context"
	"crypto/tls"
	"github.com/tbrandenburg/agentiplex/internal/security"
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
	tokens map[string]string
	tls *tls.Config
}

func New(allowed []string, timeout time.Duration) (*Client, error) {
	return NewWithCredentials(allowed,timeout,nil,"")
}

func NewWithCredentials(allowed []string,timeout time.Duration,tokenFiles map[string]string,caFile string)(*Client,error){
	tlsConfig,err:=security.TLSConfig(caFile);if err!=nil{return nil,err}
	c := &Client{allowed: map[string]bool{}, timeout: timeout,tokens:map[string]string{},tls:tlsConfig}
	for _, raw := range allowed {
		u, err := security.ParseHTTPURL(raw)
		if err != nil || (u.Path != "" && u.Path != "/") || u.RawPath != "" { return nil, errors.New("allowed origins must be HTTP(S) origins") }
		c.allowed[security.HTTPOrigin(u)] = true
	}
	normalized,err:=security.NormalizeOutboundTokenFiles(tokenFiles);if err!=nil{return nil,err}
	for key,path:=range normalized{
		if _,err:=c.check(key);err!=nil{return nil,errors.New("outbound credentials require an allowed HTTPS origin")}
		token,err:=security.ReadToken(path);if err!=nil{return nil,err};c.tokens[key]=token
	}
	return c, nil
}

func loopback(host string) bool {
	if strings.EqualFold(host,"localhost") { return true }
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

func (c *Client) check(raw string) (*url.URL,error) {
	u,err := security.ParseHTTPURL(raw)
	if err != nil { return nil,err }
	if !loopback(u.Hostname()) && !c.allowed[security.HTTPOrigin(u)] { return nil,errors.New("endpoint origin not allowed") }
	return u,nil
}

type limitedTransport struct { base *http.Transport; origin string; token string }
func (t limitedTransport) RoundTrip(r *http.Request) (*http.Response,error) {
	if security.HTTPOrigin(r.URL) != t.origin { return nil,errors.New("unexpected request origin") }
	if t.token!=""{r=r.Clone(r.Context());r.Header.Set("Authorization","Bearer "+t.token)}
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
			if !ip.IsValid() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || (loopback(host)!=ip.IsLoopback()) { continue }
			conn,e:=d.DialContext(ctx,network,net.JoinHostPort(ip.String(),port))
			if e==nil { return conn,nil }
			last=e
		}
		return nil,last
	}, TLSClientConfig:c.tls.Clone(), TLSHandshakeTimeout:5*time.Second, ResponseHeaderTimeout:c.timeout, DisableKeepAlives:true}
	return &http.Client{Transport:limitedTransport{base:t,origin:security.HTTPOrigin(u),token:c.tokens[security.HTTPOrigin(u)]},Timeout:c.timeout,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}},t.CloseIdleConnections
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
		if e==nil&&security.HTTPOrigin(endpoint)==security.HTTPOrigin(u){return card,u.String(),endpoint.String(),nil}
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
