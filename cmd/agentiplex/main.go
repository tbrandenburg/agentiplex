package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/tbrandenburg/agentiplex/internal/cli"
)

func main(){
	ctx,cancel:=signal.NotifyContext(context.Background(),os.Interrupt)
	defer cancel()
	if err:=cli.Command().ExecuteContext(ctx);err!=nil{os.Exit(1)}
}
