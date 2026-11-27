package daemon

import (
	"context"
	"crypto/tls"
	"github.com/tbrandenburg/agentiplex/internal/security"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tbrandenburg/agentiplex"
	"github.com/tbrandenburg/agentiplex/internal/api"
	"github.com/tbrandenburg/agentiplex/internal/dashboard"
	adapter "github.com/tbrandenburg/agentiplex/internal/adapters/a2a"
)

func Command() *cobra.Command {
	v:=viper.New()
	v.SetEnvPrefix("AGENTIPLEX")
	v.SetEnvKeyReplacer(strings.NewReplacer(".","_","-","_"))
	v.AutomaticEnv()
	d:=agentiplex.DefaultConfig()
	defaults:=map[string]any{
		"server.listen":"127.0.0.1:8080","server.public_url":"","server.trusted_network":false,"protocols.mcp.enabled":false,"observability.dashboard":true,"observability.dashboard_schema":"","observability.dashboard_theme":"cxd","observability.dashboard_refresh_interval":2*time.Second,
		"security.operator_token_file":"","security.agent_token_files":map[string]string{},"security.viewer_token_file":"","server.tls_cert_file":"","server.tls_key_file":"","broker.token_file":"","broker.credentials_file":"","broker.ca_file":"","delivery.token_files":map[string]string{},"delivery.ca_file":"",
		"broker.mode":d.BrokerMode,"broker.url":"","broker.storage":d.Storage,"broker.replicas":d.Replicas,"broker.sync":"always",
		"registry.namespace":d.Namespace,"registry.health_interval":d.HealthInterval,"delivery.allowed_origins":[]string{},"delivery.timeout":d.Timeout,
		"delivery.workers":d.Workers,"delivery.retry_delay":d.RetryDelay,"delivery.max_attempts":d.MaxAttempts,"delivery.task_poll_interval":d.TaskPollInterval,"delivery.retention":7*24*time.Hour,
	}
	for k,value:=range defaults{v.SetDefault(k,value)}
	var config string
	cmd:=&cobra.Command{Use:"agentiplexd",Short:"Agentiplex collaboration daemon",SilenceUsage:true,Args:cobra.NoArgs,RunE:func(cmd *cobra.Command,args []string)error{
		if config!=""{v.SetConfigFile(config);if err:=v.ReadInConfig();err!=nil{return err}}
		cfg:=agentiplex.Config{BrokerMode:v.GetString("broker.mode"),BrokerURL:v.GetString("broker.url"),BrokerTokenFile:v.GetString("broker.token_file"),BrokerCredentialsFile:v.GetString("broker.credentials_file"),BrokerCAFile:v.GetString("broker.ca_file"),OutboundTokenFiles:v.GetStringMapString("delivery.token_files"),OutboundCAFile:v.GetString("delivery.ca_file"),Storage:v.GetString("broker.storage"),Replicas:v.GetInt("broker.replicas"),Namespace:v.GetString("registry.namespace"),AllowedOrigins:v.GetStringSlice("delivery.allowed_origins"),Timeout:v.GetDuration("delivery.timeout"),RetryDelay:v.GetDuration("delivery.retry_delay"),Workers:v.GetInt("delivery.workers"),MaxAttempts:v.GetInt("delivery.max_attempts"),TaskPollInterval:v.GetDuration("delivery.task_poll_interval"),HealthInterval:v.GetDuration("registry.health_interval"),Retention:v.GetDuration("delivery.retention")}
		sync,err:=parseSync(v.GetString("broker.sync"));if err!=nil{return err}
		cfg.BrokerSync=sync
		if cfg.BrokerMode=="external"&&v.GetString("broker.sync")!="always"{slog.Warn("broker.sync is ignored with broker.mode=external")}
		auth,err:=security.New(security.Config{OperatorFile:v.GetString("security.operator_token_file"),AgentFiles:v.GetStringMapString("security.agent_token_files"),ViewerFile:v.GetString("security.viewer_token_file")});if err!=nil{return err}
		return run(cmd.Context(),cfg,v.GetString("server.listen"),v.GetString("server.public_url"),v.GetBool("server.trusted_network"),v.GetBool("observability.dashboard"),v.GetBool("protocols.mcp.enabled"),dashboard.Config{Schema:v.GetString("observability.dashboard_schema"),Theme:v.GetString("observability.dashboard_theme"),Refresh:v.GetDuration("observability.dashboard_refresh_interval")},auth,v.GetString("server.tls_cert_file"),v.GetString("server.tls_key_file"))
	}}
	f:=cmd.Flags()
	f.StringVar(&config,"config","","configuration YAML file")
	f.String("listen","127.0.0.1:8080","HTTP listen address")
	f.String("public-url","","advertised HTTP(S) origin; required on non-loopback listeners")
	f.Bool("mcp",false,"enable MCP Streamable HTTP (authentication and TLS required off loopback)")
	f.Bool("dashboard",true,"enable embedded read-only dashboard")
	f.String("dashboard-schema","","trusted AMIS JSON page schema file")
	f.String("dashboard-theme","cxd","AMIS theme: cxd, antd, ang, dark")
	f.Duration("dashboard-refresh-interval",2*time.Second,"default dashboard refresh interval")
	f.Bool("trusted-network",false,"explicitly permit non-loopback bind for isolated trusted demo networks")
	f.String("broker-mode",d.BrokerMode,"embedded or external")
	f.String("broker-url","","external NATS URL")
	f.String("storage",d.Storage,"embedded JetStream storage directory")
	f.String("namespace",d.Namespace,"NATS resource namespace")
	f.Int("workers",d.Workers,"bounded outbound delivery workers (1–16; zero uses default)")
	f.Duration("task-poll-interval",d.TaskPollInterval,"accepted task observation interval; zero inherits delivery retry delay")
	f.Duration("health-interval",d.HealthInterval,"periodic card observation interval; zero disables")
	for key,flag:=range map[string]string{"server.listen":"listen","server.public_url":"public-url","server.trusted_network":"trusted-network","protocols.mcp.enabled":"mcp","observability.dashboard":"dashboard","observability.dashboard_schema":"dashboard-schema","observability.dashboard_theme":"dashboard-theme","observability.dashboard_refresh_interval":"dashboard-refresh-interval","broker.mode":"broker-mode","broker.url":"broker-url","broker.storage":"storage","registry.namespace":"namespace","registry.health_interval":"health-interval","delivery.task_poll_interval":"task-poll-interval","delivery.workers":"workers"}{
		_ = v.BindPFlag(key,f.Lookup(flag))
	}
	return cmd
}

func run(ctx context.Context,cfg agentiplex.Config,listen,publicURL string,trusted,dashboardEnabled,mcpEnabled bool,dashboardConfig dashboard.Config,auth *security.Auth,certFile,keyFile string)error{
	var ui *dashboard.UI
	if dashboardEnabled{var err error;ui,err=dashboard.New(dashboardConfig);if err!=nil{return err}}
	host,_,err:=net.SplitHostPort(listen)
	if err!=nil{return err}
	ip,parseErr:=netip.ParseAddr(host)
	if (certFile=="")!=(keyFile==""){return errors.New("both TLS certificate and key are required")}
	if auth!=nil&&(certFile==""||!strings.HasPrefix(publicURL,"https://")){return errors.New("authenticated service requires TLS certificate, key and explicit HTTPS public URL")}
	if auth!=nil&&dashboardEnabled&&!auth.HasViewer(){return errors.New("authenticated dashboard requires a viewer credential or --dashboard=false")}
	if mcpEnabled&&auth==nil&&(parseErr!=nil||!ip.Unmap().IsLoopback()){return errors.New("MCP requires a literal loopback listener; remote access is not supported") }
	if auth==nil&&!trusted&&(parseErr!=nil||!ip.Unmap().IsLoopback()){return errors.New("non-loopback bind requires authenticated TLS or explicit isolated --trusted-network demo mode")}
	ln,err:=net.Listen("tcp",listen);if err!=nil{return err};defer ln.Close()
	publicURL,err=adapter.PublicURL(publicURL,ln.Addr().String());if err!=nil{return err}
	if (certFile!="")!=strings.HasPrefix(publicURL,"https://"){return errors.New("public URL scheme must match listener TLS configuration")}
	var tlsConfig *tls.Config
	if certFile!=""{pair,e:=tls.LoadX509KeyPair(certFile,keyFile);if e!=nil{return errors.New("cannot load TLS certificate/key")};tlsConfig=&tls.Config{MinVersion:tls.VersionTLS12,Certificates:[]tls.Certificate{pair}}}
	b,err:=agentiplex.Open(ctx,cfg);if err!=nil{return err};defer b.Close()
	srv:=NewServer(api.HandlerSecure(b,ui,publicURL,mcpEnabled,auth),tlsConfig,cfg.Timeout)
	result:=make(chan error,1)
	go func(){if tlsConfig!=nil{result<-srv.ServeTLS(ln,"","")}else{result<-srv.Serve(ln)}}()
	slog.Info("agentiplexd ready","listen",ln.Addr().String(),"broker_mode",cfg.BrokerMode,"namespace",cfg.Namespace)
	select{
	case err:=<-result:
		if !errors.Is(err,http.ErrServerClosed){return fmt.Errorf("HTTP server: %w",err)}
	case <-ctx.Done():
		stop,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel()
		if err:=srv.Shutdown(stop);err!=nil{_ = srv.Close();return err}
	}
	return nil
}

// NewServer applies the daemon's HTTP deadlines; /mcp clears its own per request.
func NewServer(h http.Handler,tlsConfig *tls.Config,timeout time.Duration)*http.Server{
	return &http.Server{Handler:h,TLSConfig:tlsConfig,ReadHeaderTimeout:5*time.Second,ReadTimeout:15*time.Second,WriteTimeout:timeout+15*time.Second,IdleTimeout:30*time.Second}
}

// parseSync maps broker.sync ("always" or a duration) to an fsync interval; zero means every write.
func parseSync(raw string)(time.Duration,error){
	if raw==""||raw=="always"{return 0,nil}
	d,err:=time.ParseDuration(raw)
	if err!=nil||d<=0{return 0,errors.New(`broker.sync must be "always" or a positive duration such as 2m`)}
	return d,nil
}
