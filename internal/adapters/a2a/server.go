package a2a

import (
	"context"
	"errors"
	"iter"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	proto "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

const IngressPath = "/a2a"
const AgentCardPath = a2asrv.WellKnownAgentCardPath

type Ingress interface {
	SendMessage(context.Context, *proto.SendMessageRequest) (proto.SendMessageResult, error)
	GetTask(context.Context, *proto.GetTaskRequest) (*proto.Task, error)
}

type requestHandler struct{ Ingress }

var _ a2asrv.RequestHandler = (*requestHandler)(nil)

func (*requestHandler) ListTasks(context.Context, *proto.ListTasksRequest) (*proto.ListTasksResponse, error) {
	return nil, proto.ErrUnsupportedOperation
}
func (*requestHandler) CancelTask(context.Context, *proto.CancelTaskRequest) (*proto.Task, error) {
	return nil, proto.ErrTaskNotCancelable
}
func (*requestHandler) SubscribeToTask(context.Context, *proto.SubscribeToTaskRequest) iter.Seq2[proto.Event, error] {
	return unsupportedStream
}
func (*requestHandler) SendStreamingMessage(context.Context, *proto.SendMessageRequest) iter.Seq2[proto.Event, error] {
	return unsupportedStream
}
func unsupportedStream(yield func(proto.Event, error) bool) {
	yield(nil, proto.ErrUnsupportedOperation)
}
func (*requestHandler) GetTaskPushConfig(context.Context, *proto.GetTaskPushConfigRequest) (*proto.PushConfig, error) {
	return nil, proto.ErrPushNotificationNotSupported
}
func (*requestHandler) ListTaskPushConfigs(context.Context, *proto.ListTaskPushConfigRequest) (*proto.ListTaskPushConfigResponse, error) {
	return nil, proto.ErrPushNotificationNotSupported
}
func (*requestHandler) CreateTaskPushConfig(context.Context, *proto.PushConfig) (*proto.PushConfig, error) {
	return nil, proto.ErrPushNotificationNotSupported
}
func (*requestHandler) DeleteTaskPushConfig(context.Context, *proto.DeleteTaskPushConfigRequest) error {
	return proto.ErrPushNotificationNotSupported
}
func (*requestHandler) GetExtendedAgentCard(context.Context, *proto.GetExtendedAgentCardRequest) (*proto.AgentCard, error) {
	return nil, proto.ErrExtendedCardNotConfigured
}

// PublicURL never derives the advertised origin from untrusted HTTP headers.
func PublicURL(configured, listen string) (string, error) {
	if configured == "" {
		host, _, err := net.SplitHostPort(listen)
		if err != nil {
			return "", err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Unmap().IsLoopback() {
			return "", errors.New("server.public_url is required for non-loopback listeners")
		}
		configured = "http://" + listen
	}
	u, err := url.Parse(configured)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return "", errors.New("server.public_url must be an HTTP(S) origin without path, query or credentials")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && ip.IsUnspecified() {
		return "", errors.New("server.public_url cannot advertise a wildcard address")
	}
	return strings.TrimRight(configured, "/"), nil
}

func Card(publicURL string) *proto.AgentCard {
	return &proto.AgentCard{
		Name: "Agentiplex", Description: "Durable agent collaboration: direct send, group broadcast and pool dispatch",
		Version: "0.1.0",
		SupportedInterfaces: []*proto.AgentInterface{proto.NewAgentInterface(publicURL+IngressPath, proto.TransportProtocolJSONRPC)},
		DefaultInputModes: []string{"application/json"},
		DefaultOutputModes: []string{"application/json"},
		Skills: []proto.AgentSkill{{
			ID: "send", Name: "Direct send",
			Description: "Submit one data part with operation=send, target=registered agent ID and text=prompt; inspect the returned task via GetTask.",
			Tags: []string{"messaging", "collaboration", "durable-delivery"},
			Examples: []string{`{"operation":"send","target":"architect","text":"Review requirement 42"}`},
		},{
			ID:"broadcast",Name:"Group broadcast",
			Description:"Deliver one text request to every member of a configured group. Inspect child delivery status using GetTask.",
			Tags:[]string{"messaging","broadcast","collaboration"},
			Examples:[]string{`{"operation":"broadcast","target":"engineering","text":"Contract updated"}`},
		},{
			ID:"dispatch",Name:"Pool dispatch",
			Description:"Deliver one text request to one eligible member of a configured pool; inspect assignment and result using GetTask.",
			Tags:[]string{"messaging","pool","collaboration"},
			Examples:[]string{`{"operation":"dispatch","target":"testers","text":"Verify requirement 42"}`},
		}},
	}
}

func Mount(mux *http.ServeMux, service Ingress, publicURL string, secured ...bool) {
	card:=Card(publicURL)
	if len(secured)>0&&secured[0]{
		card.SecuritySchemes=proto.NamedSecuritySchemes{"bearer":proto.HTTPAuthSecurityScheme{Scheme:"Bearer",Description:"Per-agent or operator credential provisioned out of band"}}
		card.SecurityRequirements=proto.SecurityRequirementsOptions{{"bearer":{}}}
	}
	mux.Handle("GET "+AgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	handler := a2asrv.NewJSONRPCHandler(&requestHandler{Ingress: service})
	mux.Handle(IngressPath, http.MaxBytesHandler(handler, 128<<10))
}
