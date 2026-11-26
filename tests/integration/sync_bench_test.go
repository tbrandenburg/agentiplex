package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex"
)

// BenchmarkBrokerSync compares fsync-every-write with the relaxed 2m interval:
// ns/op is the Send (durable publish) latency; the drain metric is the end-to-end
// delivery throughput of the same messages to a local simulated agent.
func BenchmarkBrokerSync(b *testing.B){
	for _,mode:=range []struct{name string;every time.Duration}{{"always",0},{"interval-2m",2*time.Minute}}{
		b.Run(mode.name,func(b *testing.B){
			cfg:=agentiplex.DefaultConfig()
			cfg.Storage=b.TempDir();cfg.Timeout=4*time.Second;cfg.RetryDelay=time.Second;cfg.BrokerSync=mode.every;cfg.HealthInterval=0
			bus,err:=agentiplex.Open(context.Background(),cfg)
			if err!=nil{b.Fatal(err)}
			defer bus.Close()
			a:=&agentService{};a.start(&testing.T{},false);defer a.stop()
			ctx:=context.Background()
			if _,err=bus.Register(ctx,"architect",a.URL());err!=nil{b.Fatal(err)}
			ids:=make([]string,0,b.N)
			start:=time.Now()
			b.ResetTimer()
			for i:=0;i<b.N;i++{
				d,err:=bus.Send(ctx,"architect","bench",fmt.Sprintf("bench-%d",i))
				if err!=nil{b.Fatal(err)}
				ids=append(ids,d.ID)
			}
			b.StopTimer()
			for _,id:=range ids{
				for{d,err:=bus.Delivery(id);if err==nil&&d.State=="completed"{break};time.Sleep(5*time.Millisecond)}
			}
			b.ReportMetric(float64(b.N)/time.Since(start).Seconds(),"deliveries/s")
		})
	}
}
