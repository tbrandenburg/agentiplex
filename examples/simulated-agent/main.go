package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/tbrandenburg/agentiplex/internal/simagent"
)

func main(){
	name:=flag.String("name","architect","agent name")
	skill:=flag.String("skill","architecture","advertised skill")
	port:=flag.Int("port",8001,"loopback port; zero for dynamic")
	flag.Parse()
	ln,err:=net.Listen("tcp",fmt.Sprintf("127.0.0.1:%d",*port))
	if err!=nil{log.Fatal(err)}
	base:="http://"+ln.Addr().String()
	log.Printf("simulated A2A agent ready: %s",base)
	srv:=&http.Server{Handler:simagent.Handler(base,*name,*skill,nil),ReadHeaderTimeout:5*time.Second}
	log.Fatal(srv.Serve(ln))
}
