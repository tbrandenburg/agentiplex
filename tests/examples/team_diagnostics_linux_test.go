//go:build linux

package examples_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tbrandenburg/agentiplex/core"
)

// Only synthetic, credential-free acceptance runs use this export. The
// private evidence.json, runtime home, raw provider payloads, environment,
// workspace files and JetStream storage are deliberately not copied.
const diagnosticLogBytes = 32 << 10

type diagnosticDelivery struct {
	ID, Target, State, Detail, TaskID, ContextID string
	Kind, Caller string
	ContractRound bool
	Attempts, Polls, PollFailures int
	UpdatedAt time.Time
	LastObservedAt *time.Time
	DownstreamState, DownstreamMessage string
}

func deliveryDiagnostic(d core.Delivery) diagnosticDelivery {
	out := diagnosticDelivery{
		ID:d.ID, Target:d.Target, State:d.State, Detail:boundedDiagnostic(d.Detail,2048),
		Kind:d.Kind, Caller:d.Caller, ContractRound:strings.HasPrefix(d.Text,"Agentiplex until-done request "),
		TaskID:string(d.TaskID), ContextID:d.ContextID, Attempts:d.Attempts, Polls:d.Polls,
		PollFailures:d.PollFailures, UpdatedAt:d.UpdatedAt, LastObservedAt:d.LastObservedAt,
	}
	// The gateway authors a sanitized phase-level error in the failed
	// A2A task status. Do not copy history, artifacts or arbitrary metadata.
	if d.State!="completed" {
		var task struct {
			Status struct {
				State string
				Message struct { Parts []struct { Text string } }
			}
		}
		if json.Unmarshal(d.Result,&task)==nil {
			out.DownstreamState=boundedDiagnostic(task.Status.State,128)
			var parts []string
			for _,p:=range task.Status.Message.Parts {
				if p.Text!="" { parts=append(parts,p.Text) }
			}
			out.DownstreamMessage=boundedDiagnostic(strings.Join(parts,"\n"),2048)
		}
	}
	return out
}

func boundedDiagnostic(s string, n int) string {
	if len(s)>n { return strings.ToValidUTF8(s[:n],"�")+" [truncated]" }
	return strings.ToValidUTF8(s,"�")
}

func diagnosticLogTail(path string) (string,error) {
	// Refuse symlinks and non-regular files; read only our process logs.
	info,err:=os.Lstat(path)
	if err!=nil { return "",err }
	if !info.Mode().IsRegular() { return "",fmt.Errorf("not a regular process log") }
	f,err:=os.Open(path)
	if err!=nil { return "",err }
	defer f.Close()
	if info.Size()>diagnosticLogBytes {
		if _,err=f.Seek(-diagnosticLogBytes,io.SeekEnd);err!=nil { return "",err }
	}
	raw,err:=io.ReadAll(io.LimitReader(f,diagnosticLogBytes))
	if err!=nil { return "",err }
	prefix:=""
	if info.Size()>diagnosticLogBytes { prefix="[earlier log omitted]\n" }
	return prefix+strings.ToValidUTF8(string(raw),"�"),nil
}

type diagnosticProcess struct {
	Name string
	Started time.Time
	Exited bool
	ExitError, LogTail string
}

type diagnosticNote struct { Key, Topic string; At time.Time; Bytes int }

type teamFailureReport struct {
	Scenario, Model string
	CapturedAt time.Time
	Processes []diagnosticProcess
	Deliveries []diagnosticDelivery
	Events []core.Event
	Notes []diagnosticNote
	CollectionErrors []string
}

func (l *liveTeam) collectFailure(namespace,scenario string) teamFailureReport {
	r:=teamFailureReport{Scenario:scenario,Model:*liveModel,CapturedAt:time.Now().UTC()}
	// Keep diagnostic collection bounded even if the daemon disappeared.
	ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second)
	defer cancel()
	get:=func(path string,out any) bool {
		req,err:=http.NewRequestWithContext(ctx,"GET",l.api+path,nil)
		if err==nil {
			var resp *http.Response
			resp,err=l.client.Do(req)
			if err==nil {
				defer resp.Body.Close()
				if resp.StatusCode!=http.StatusOK { err=fmt.Errorf("HTTP %d",resp.StatusCode) } else {
					err=json.NewDecoder(io.LimitReader(resp.Body,2<<20)).Decode(out)
				}
			}
		}
		if err!=nil {
			r.CollectionErrors=append(r.CollectionErrors,path+": "+boundedDiagnostic(err.Error(),512))
			return false
		}
		return true
	}
	if l.daemon!=nil {
		var summaries []core.DeliverySummary
		if get("/api/v1/deliveries?limit=100",&summaries) {
			ids:=map[string]bool{}
			for _,s:=range summaries {
				ids[s.ID]=true
				for _,child:=range s.Children { ids[child.ID]=true }
			}
			for i,id:=range sortedTeamIDs(ids) {
				if i>=100 || ctx.Err()!=nil { r.CollectionErrors=append(r.CollectionErrors,"delivery detail collection truncated");break }
				var d core.Delivery
				if get("/api/v1/deliveries/"+url.PathEscape(id),&d) {
					r.Deliveries=append(r.Deliveries,deliveryDiagnostic(d))
				}
			}
		}
		var notes []core.Note
		if get("/api/v1/notes/"+url.PathEscape(namespace),&notes) {
			for _,n:=range notes { r.Notes=append(r.Notes,diagnosticNote{n.Key,n.Topic,n.At,len(n.Text)}) }
		}
		var after uint64
		for i:=0;i<3 && ctx.Err()==nil;i++ {
			var page []core.Event
			if !get(fmt.Sprintf("/api/v1/events?after=%d",after),&page) { break }
			r.Events=append(r.Events,page...)
			if len(page)<100 { break }
			after=page[len(page)-1].Sequence
			if i==2 { r.CollectionErrors=append(r.CollectionErrors,"event history limited to first 300 retained events") }
		}
	}
	for _,p:=range l.processes {
		d:=diagnosticProcess{Name:filepath.Base(p.log),Started:p.started}
		select {
		case <-p.done:
			d.Exited=true
			if p.exitErr!=nil { d.ExitError=p.exitErr.Error() }
		default:
		}
		tail,err:=diagnosticLogTail(p.log)
		if err!=nil { r.CollectionErrors=append(r.CollectionErrors,d.Name+": "+err.Error()) } else { d.LogTail=tail }
		r.Processes=append(r.Processes,d)
	}
	return r
}

// Called before process cleanup, including Fatal/Goexit and startup failure.
// Diagnostic collection never changes the acceptance verdict.
func (l *liveTeam) reportFailure(namespace,scenario string) {
	if !l.t.Failed() { return }
	report:=l.collectFailure(namespace,scenario)
	for _,d:=range report.Deliveries {
		if d.State!="completed" {
			l.t.Logf("Failure detail: target=%s id=%s state=%s kind=%s caller=%q contract_round=%t task=%s attempts=%d polls=%d pollFailures=%d detail=%q downstream=%q message=%q",
				d.Target,d.ID,d.State,d.Kind,d.Caller,d.ContractRound,d.TaskID,d.Attempts,d.Polls,d.PollFailures,d.Detail,d.DownstreamState,d.DownstreamMessage)
		}
	}
	for _,p:=range report.Processes {
		l.t.Logf("Process %s started=%s exited=%t exit=%q log_bytes=%d",p.Name,p.Started,p.Exited,p.ExitError,len(p.LogTail))
	}
	for _,e:=range report.CollectionErrors { l.t.Logf("Diagnostic collection: %q",e) }
	raw,err:=json.MarshalIndent(report,"","  ")
	if err!=nil { l.t.Log("Cannot encode diagnostics:",err);return }
	local:=filepath.Join(l.run,"failure-diagnostics.json")
	if err=os.WriteFile(local,raw,0600);err!=nil { l.t.Log("Cannot write diagnostics:",err) } else { l.t.Log("Failure diagnostics:",local) }
	if *liveDiagnosticsDir!="" {
		path:=filepath.Join(*liveDiagnosticsDir,"failure-diagnostics.json")
		if err=os.WriteFile(path,raw,0600);err!=nil { l.t.Log("Cannot export diagnostics:",err) } else { l.t.Log("Exported synthetic failure diagnostics:",path) }
	}
}

func TestDeliveryDiagnosticUsesOnlyFailedStatus(t *testing.T) {
	d:=core.Delivery{ID:"receipt",Target:"quality",State:"failed",Detail:"failed",TaskID:"task",Polls:3,PollFailures:1,
		Result:json.RawMessage("{\"status\":{\"state\":\"TASK_STATE_FAILED\",\"message\":{\"parts\":[{\"text\":\"ACP execution failed\"}]}},\"history\":[{\"secret\":\"not exported\"}],\"artifacts\":[{\"text\":\"not exported\"}]}")}
	got:=deliveryDiagnostic(d)
	if got.DownstreamMessage!="ACP execution failed"||got.DownstreamState!="TASK_STATE_FAILED"||got.TaskID!="task"||got.PollFailures!=1 { t.Fatalf("unexpected diagnostic: %+v",got) }
	raw,err:=json.Marshal(got)
	if err!=nil||strings.Contains(string(raw),"not exported") { t.Fatalf("raw payload escaped projection: %s %v",raw,err) }
	d.State="completed"
	if got=deliveryDiagnostic(d);got.DownstreamMessage!="" { t.Fatal("completed output exported") }
}

func TestDiagnosticLogTail(t *testing.T) {
	dir:=t.TempDir()
	path:=filepath.Join(dir,"process.log")
	if err:=os.WriteFile(path,[]byte("discard-me"+strings.Repeat("x",diagnosticLogBytes)+"last-line"),0600);err!=nil { t.Fatal(err) }
	tail,err:=diagnosticLogTail(path)
	if err!=nil||!strings.HasPrefix(tail,"[earlier log omitted]")||!strings.HasSuffix(tail,"last-line")||strings.Contains(tail,"discard-me") { t.Fatal("log tail not bounded:",err) }
	link:=filepath.Join(dir,"link.log")
	if err=os.Symlink(path,link);err!=nil { t.Fatal(err) }
	if _,err=diagnosticLogTail(link);err==nil { t.Fatal("symlink accepted") }
}
