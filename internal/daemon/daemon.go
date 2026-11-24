package daemon

import (
	"context"
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
	adapter "github.com/tbrandenburg/agentiplex/internal/adapters/a2a"
)

func Command() *cobra.Command {
	v:=viper.New()
	v.SetEnvPrefix("AGENTIPLEX")
	v.SetEnvKeyReplacer(strings.NewReplacer(".","_","-","_"))
	v.AutomaticEnv()
	d:=agentiplex.DefaultConfig()
	defaults:=map[string]any{
		"server.listen":"127.0.0.1:8080","server.public_url":"","server.trusted_network":false,
		"broker.mode":d.BrokerMode,"broker.url":"","broker.storage":d.Storage,"broker.replicas":d.Replicas,
		"registry.namespace":d.Namespace,"delivery.allowed_origins":[]string{},"delivery.timeout":d.Timeout,
		"delivery.retry_delay":d.RetryDelay,"delivery.max_attempts":d.MaxAttempts,
	}
	for k,value:=range defaults{v.SetDefault(k,value)}
	var config string
	cmd:=&cobra.Command{Use:"agentiplexd",Short:"Agentiplex collaboration daemon",SilenceUsage:true,Args:cobra.NoArgs,RunE:func(cmd *cobra.Command,args []string)error{
		if config!=""{v.SetConfigFile(config);if err:=v.ReadInConfig();err!=nil{return err}}
		cfg:=agentiplex.Config{BrokerMode:v.GetString("broker.mode"),BrokerURL:v.GetString("broker.url"),Storage:v.GetString("broker.storage"),Replicas:v.GetInt("broker.replicas"),Namespace:v.GetString("registry.namespace"),AllowedOrigins:v.GetStringSlice("delivery.allowed_origins"),Timeout:v.GetDuration("delivery.timeout"),RetryDelay:v.GetDuration("delivery.retry_delay"),MaxAttempts:v.GetInt("delivery.max_attempts")}
		return run(cmd.Context(),cfg,v.GetString("server.listen"),v.GetString("server.public_url"),v.GetBool("server.trusted_network"))
	}}
	f:=cmd.Flags()
	f.StringVar(&config,"config","","configuration YAML file")
	f.String("listen","127.0.0.1:8080","HTTP listen address")
	f.String("public-url","","advertised HTTP(S) origin; required on non-loopback listeners")
	f.Bool("trusted-network",false,"explicitly permit non-loopback bind for isolated trusted demo networks")
	f.String("broker-mode",d.BrokerMode,"embedded or external")
	f.String("broker-url","","external NATS URL")
	f.String("storage",d.Storage,"embedded JetStream storage directory")
	f.String("namespace",d.Namespace,"NATS resource namespace")
	for key,flag:=range map[string]string{"server.listen":"listen","server.public_url":"public-url","server.trusted_network":"trusted-network","broker.mode":"broker-mode","broker.url":"broker-url","broker.storage":"storage","registry.namespace":"namespace"}{
		_ = v.BindPFlag(key,f.Lookup(flag))
	}
	return cmd
}

func run(ctx context.Context,cfg agentiplex.Config,listen,publicURL string,trusted bool)error{
	host,_,err:=net.SplitHostPort(listen)
	if err!=nil{return err}
	ip,parseErr:=netip.ParseAddr(host)
	if !trusted&&(parseErr!=nil||!ip.Unmap().IsLoopback()){return errors.New("non-loopback bind requires --trusted-network; no authentication in foundation")}
	ln,err:=net.Listen("tcp",listen);if err!=nil{return err};defer ln.Close()
	publicURL,err=adapter.PublicURL(publicURL,ln.Addr().String());if err!=nil{return err}
	b,err:=agentiplex.Open(ctx,cfg);if err!=nil{return err};defer b.Close()
	srv:=&http.Server{Handler:api.Handler(b,publicURL),ReadHeaderTimeout:5*time.Second,ReadTimeout:15*time.Second,WriteTimeout:cfg.Timeout+15*time.Second,IdleTimeout:30*time.Second}
	result:=make(chan error,1)
	go func(){result<-srv.Serve(ln)}()
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
