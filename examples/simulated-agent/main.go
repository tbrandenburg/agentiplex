package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"github.com/tbrandenburg/agentiplex/internal/adapters/a2a"
	"time"

	"github.com/tbrandenburg/agentiplex/internal/simagent"
)

func main(){
	name:=flag.String("name","architect","agent name")
	skill:=flag.String("skill","architecture","advertised skill")
	port:=flag.Int("port",8001,"loopback port; zero for dynamic")
	host:=flag.String("host","127.0.0.1","listen IP")
	publicURL:=flag.String("public-url","","advertised origin")
	trusted:=flag.Bool("trusted-network",false,"allow isolated non-loopback demo network")
	flag.Parse()
	ip,e:=netip.ParseAddr(*host);if e!=nil||(!ip.Unmap().IsLoopback()&&!*trusted){log.Fatal("simulator requires loopback or explicit trusted network")}
	ln,err:=net.Listen("tcp",net.JoinHostPort(*host,fmt.Sprint(*port)))
	if err!=nil{log.Fatal(err)}
	base,err:=a2a.PublicURL(*publicURL,ln.Addr().String());if err!=nil{log.Fatal(err)}
	log.Printf("simulated A2A agent ready: %s",base)
	srv:=&http.Server{Handler:simagent.Handler(base,*name,*skill,nil),ReadHeaderTimeout:5*time.Second}
	log.Fatal(srv.Serve(ln))
}
