package integration_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/tbrandenburg/agentiplex"
)

func TestNotesConcurrentOrderedAndRestart(t *testing.T){
	cfg:=config(t);cfg.HealthInterval=0
	b:=open(t,cfg);ctx:=context.Background()
	var wg sync.WaitGroup
	for g:=0;g<8;g++{wg.Add(1);go func(){defer wg.Done()
		for i:=0;i<10;i++{if _,err:=b.Note(ctx,"team","findings",fmt.Sprintf("g%d-%d",g,i),fmt.Sprintf("a%d",g));err!=nil{t.Error(err)}}
	}()}
	wg.Wait()
	all,err:=b.ReadNotes("team","findings",0);if err!=nil{t.Fatal(err)}
	if len(all)!=80{t.Fatalf("got %d notes, want 80",len(all))}
	seen:=map[string]bool{}
	for i,n:=range all{
		seen[n.Text]=true
		if i>0&&all[i-1].Key>=n.Key{t.Fatalf("not ordered at %d",i)}
	}
	if len(seen)!=80{t.Fatalf("lost or duplicated notes: %d unique",len(seen))}
	last,err:=b.ReadNotes("team","findings",5);if err!=nil{t.Fatal(err)}
	if len(last)!=5||last[4].Key!=all[79].Key||last[0].Key!=all[75].Key{t.Fatalf("limit result %+v",last)}
	if _,err=b.Note(ctx,"team","findings-2","other","");err!=nil{t.Fatal(err)}
	if n,_:=b.ReadNotes("team","findings",100);len(n)!=80{t.Fatalf("topic prefix leaked: %d",len(n))}
	if n,err:=b.ReadNotes("team","unknown",10);err!=nil||len(n)!=0{t.Fatalf("unknown topic %v %v",n,err)}
	b.Close()
	b2:=open(t,cfg)
	after,err:=b2.ReadNotes("team","findings",100);if err!=nil{t.Fatal(err)}
	if len(after)!=80||after[0].Author==""||after[0].At.IsZero(){t.Fatalf("after restart %d",len(after))}
}

func TestNotesRejections(t *testing.T){
	b:=open(t,config(t));ctx:=context.Background()
	for _,topic:=range []string{"","a.b","A","a b","a/b",strings.Repeat("a",agentiplex.MaxNoteTopic+1)}{
		if _,err:=b.Note(ctx,"team",topic,"x","");err==nil{t.Fatalf("topic %q accepted",topic)}
		if _,err:=b.ReadNotes("team",topic,1);err==nil{t.Fatalf("read topic %q accepted",topic)}
	}
	if _,err:=b.Note(ctx,"team","ok","  ","");err==nil{t.Fatal("empty text accepted")}
	_,err:=b.Note(ctx,"team","ok",strings.Repeat("x",agentiplex.MaxStateValueBytes),"")
	if err==nil||!strings.Contains(err.Error(),"value limit"){t.Fatalf("oversize error %v",err)}
	if n,_:=b.ReadNotes("team","ok",10);len(n)!=0{t.Fatal("rejected note stored")}
}

func TestNotesBeyondNamespaceKeyLimit(t *testing.T){
	b:=open(t,config(t));ctx:=context.Background()
	for i:=0;i<agentiplex.MaxStateKeys+5;i++{if _,err:=b.Note(ctx,"big","log","n",""); err!=nil{t.Fatalf("note %d: %v",i,err)}}
	if n,_:=b.ReadNotes("big","log",100);len(n)!=100{t.Fatalf("got %d",len(n))}
}
