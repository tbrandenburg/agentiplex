// Package nats owns broker lifecycle and resource provisioning.
package nats

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"github.com/tbrandenburg/agentiplex/internal/security"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	nc "github.com/nats-io/nats.go"
)

// Bound outstanding broker deliveries while allowing delayed/accepted work
// to coexist with work for other available recipients.
const maxAckPending = 64

type Store struct {
	Conn *nc.Conn
	JS nc.JetStreamContext
	Registry nc.KeyValue
	Deliveries nc.KeyValue
	Collections nc.KeyValue
	State nc.KeyValue
	PoolStream string
	PoolPrefix string
	Work *nc.Subscription
	WorkSubject string
	EventSubject string
	EventStream string
	embedded *server.Server
}

type AuthConfig struct{TokenFile,CredentialsFile,CAFile string}

func Open(mode, brokerURL, storage, namespace string, replicas int, timeout, syncEvery time.Duration, auth ...AuthConfig) (_ *Store, err error) {
	s := &Store{}
	var a AuthConfig
	if len(auth)>0{a=auth[0]}
	if a.TokenFile!=""&&a.CredentialsFile!=""{return nil,errors.New("choose either NATS token or credentials file")}
	if mode=="embedded"&&(a!=(AuthConfig{})){return nil,errors.New("external broker authentication settings require external mode")}
	defer func() { if err != nil { s.Close() } }()
	if mode == "embedded" {
		// Acknowledged writes are fsynced unless an interval is explicitly configured.
		s.embedded, err = server.NewServer(&server.Options{
			Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: storage, NoLog: true, NoSigs: true,
			SyncAlways: syncEvery == 0, SyncInterval: syncEvery,
		})
		if err != nil { return nil, err }
		go s.embedded.Start()
		if !s.embedded.ReadyForConnections(10*time.Second) { return nil, errors.New("embedded NATS did not become ready") }
		brokerURL = s.embedded.ClientURL()
	}
	for _,raw:=range strings.Split(brokerURL,","){
		u,e:=url.Parse(strings.TrimSpace(raw))
		if e!=nil||u.User!=nil||u.RawQuery!=""||u.Fragment!=""||u.Hostname()==""||(u.Scheme!="nats"&&u.Scheme!="tls"){return nil,errors.New("NATS URL must be nats:// or tls:// without embedded credentials")}
		if (a.CAFile!=""||a.TokenFile!=""||a.CredentialsFile!="")&&u.Scheme!="tls"{return nil,errors.New("NATS credentials and custom CA require tls://")}
	}
	opts:=[]nc.Option{nc.Timeout(5*time.Second),nc.MaxReconnects(-1)}
	if a.TokenFile!=""{token,e:=security.ReadToken(a.TokenFile);if e!=nil{return nil,e};opts=append(opts,nc.Token(token))}
	if a.CredentialsFile!=""{opts=append(opts,nc.UserCredentials(a.CredentialsFile))}
	if a.CAFile!=""{opts=append(opts,nc.RootCAs(a.CAFile))}
	s.Conn, err = nc.Connect(brokerURL,opts...)
	if err != nil { return nil, errors.New("cannot connect to configured NATS broker") }
	s.JS, err = s.Conn.JetStream(nc.MaxWait(5*time.Second))
	if err != nil { return nil, err }
	workStream := namespace + "_MAILBOXES"
	s.EventStream = namespace + "_EVENTS"
	s.PoolStream = namespace + "_POOLS"
	s.PoolPrefix = namespace + ".pool."
	s.WorkSubject = namespace + ".mailbox"
	s.EventSubject = namespace + ".events"
	for _, cfg := range []*nc.StreamConfig{
		{Name: workStream, Subjects: []string{s.WorkSubject}, Retention: nc.WorkQueuePolicy, Storage: nc.FileStorage, Replicas: replicas, MaxBytes: 64<<20, MaxMsgs: 10000, MaxMsgSize: 1024, Discard: nc.DiscardNew, Duplicates: 2*time.Minute},
		{Name: s.PoolStream, Subjects: []string{s.PoolPrefix+">"}, Retention: nc.WorkQueuePolicy, Storage: nc.FileStorage, Replicas: replicas, MaxBytes: 64<<20, MaxMsgs: 10000, MaxMsgSize: 1024, Discard: nc.DiscardNew, Duplicates: 2*time.Minute},
		{Name: s.EventStream, Subjects: []string{s.EventSubject}, Retention: nc.LimitsPolicy, Storage: nc.FileStorage, Replicas: replicas, MaxMsgs: 10000, MaxBytes: 16<<20, MaxAge: 7*24*time.Hour, Discard: nc.DiscardOld},
	} {
		info, e := s.JS.StreamInfo(cfg.Name)
		if errors.Is(e, nc.ErrStreamNotFound) {
			_, e = s.JS.AddStream(cfg)
		} else if e == nil && (info.Config.Storage != nc.FileStorage || info.Config.Replicas != replicas || info.Config.Retention != cfg.Retention || len(info.Config.Subjects) != 1 || info.Config.Subjects[0] != cfg.Subjects[0]) {
			e = fmt.Errorf("existing stream %s incompatible with configuration", cfg.Name)
		}
		if e != nil { return nil, e }
	}
	for _, b := range []struct{name string; dest *nc.KeyValue}{
		{namespace+"_REGISTRY", &s.Registry}, {namespace+"_DELIVERIES", &s.Deliveries}, {namespace+"_COLLECTIONS", &s.Collections},
	} {
		*b.dest, err = s.JS.KeyValue(b.name)
		if errors.Is(err, nc.ErrBucketNotFound) {
			*b.dest, err = s.JS.CreateKeyValue(&nc.KeyValueConfig{Bucket: b.name, Storage: nc.FileStorage, Replicas: replicas, History: 1, MaxBytes: 128<<20, MaxValueSize: 512<<10})
		}
		if err != nil { return nil, err }
	}
	// Dedicated bounded application-state bucket. Native KV provides CAS and watch.
	bucket:=namespace+"_AGENT_STATE"
	s.State,err=s.JS.KeyValue(bucket)
	if errors.Is(err,nc.ErrBucketNotFound){s.State,err=s.JS.CreateKeyValue(&nc.KeyValueConfig{Bucket:bucket,Storage:nc.FileStorage,Replicas:replicas,History:1,MaxBytes:32<<20,MaxValueSize:17<<10})}
	if err!=nil{return nil,err}
	infoState,err:=s.JS.StreamInfo("KV_"+bucket);if err!=nil{return nil,err}
	if infoState.Config.Storage!=nc.FileStorage||infoState.Config.Replicas!=replicas||infoState.Config.MaxBytes!=32<<20||infoState.Config.MaxMsgSize!=17<<10||infoState.Config.MaxAge!=0||infoState.Config.MaxMsgsPerSubject!=1||infoState.Config.Discard!=nc.DiscardNew{return nil,fmt.Errorf("incompatible application state bucket %s",bucket)}
	const durable = "delivery"
	// Keep crash recovery bounded independently of downstream HTTP timeouts.
	// The worker renews the broker lease while an attempt is active.
	consumer := &nc.ConsumerConfig{Durable: durable, AckPolicy: nc.AckExplicitPolicy, AckWait: 10*time.Second, MaxAckPending: maxAckPending, FilterSubject: s.WorkSubject}
	info, lookupErr := s.JS.ConsumerInfo(workStream, durable)
	switch {
	case errors.Is(lookupErr, nc.ErrConsumerNotFound):
		_, err = s.JS.AddConsumer(workStream, consumer)
	case lookupErr != nil:
		err = lookupErr
	default:
		if info.Config.AckWait != consumer.AckWait || info.Config.MaxAckPending != consumer.MaxAckPending {
			info.Config.AckWait = consumer.AckWait
			info.Config.MaxAckPending = consumer.MaxAckPending
			_, err = s.JS.UpdateConsumer(workStream, &info.Config)
		}
	}
	if err != nil { return nil, err }
	s.Work, err = s.JS.PullSubscribe(s.WorkSubject, durable, nc.Bind(workStream, durable))
	if err != nil { return nil, err }
	return s, nil
}

func (s *Store) Close() {
	if s.Conn != nil {
		// NAK and progress acknowledgments are asynchronous. Flush before
		// closing the connection or stopping an embedded server.
		_ = s.Conn.FlushTimeout(5*time.Second)
		s.Conn.Close()
	}
	if s.embedded != nil { s.embedded.Shutdown(); s.embedded.WaitForShutdown() }
}

// PoolConsumer binds a single durable pull consumer to a pool subject.
// Explicit acknowledgments and a renewable lease match the direct worker.
func (s *Store) PoolConsumer(id string) (*nc.Subscription,error) {
	subject:=s.PoolPrefix+id
	durable:="pool_"+id
	cfg:=&nc.ConsumerConfig{Durable:durable,AckPolicy:nc.AckExplicitPolicy,AckWait:10*time.Second,MaxAckPending:maxAckPending,FilterSubject:subject}
	info,err:=s.JS.ConsumerInfo(s.PoolStream,durable)
	if errors.Is(err,nc.ErrConsumerNotFound) {
		_,err=s.JS.AddConsumer(s.PoolStream,cfg)
	} else if err==nil && (info.Config.FilterSubject!=subject || info.Config.AckPolicy!=nc.AckExplicitPolicy || info.Config.MaxAckPending!=maxAckPending || info.Config.AckWait!=cfg.AckWait) {
		return nil,fmt.Errorf("incompatible pool consumer %s",id)
	}
	if err!=nil{return nil,err}
	return s.JS.PullSubscribe(subject,durable,nc.Bind(s.PoolStream,durable))
}
