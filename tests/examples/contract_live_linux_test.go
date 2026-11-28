//go:build linux

package examples_test

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (l *liveTeam) contract(ws, base string, seconds int) {
	l.t.Helper()
	env := append(append([]string(nil), l.env...), "AGENTIPLEX_BIN="+filepath.Join(l.run,"agentiplex"), "AGENTIPLEX_SERVER="+l.api)
	script := filepath.Join(l.example,"run-contract.sh")
	run := func(mode string, deadline time.Duration) string {
		l.t.Helper()
		out, err := commandOutput(l.root,deadline,env,"bash",script,mode)
		l.t.Log(string(out))
		if err != nil { l.t.Fatalf("contract %s: %v",mode,err) }
		return string(out)
	}
	run("init",30*time.Second)
	initial := teamCLI[teamState](l,"state","get","repository","objective")
	if initial.Value != strings.TrimRight(string(l.read("objective.contract.md")),"\n") || initial.Revision==0 { l.t.Fatal("wrong approved objective") }
	run("run",time.Duration(seconds)*time.Second)
	final := teamCLI[teamState](l,"state","get","repository","objective")
	if initial != final { l.t.Fatal("approved objective changed") }
	cursor := teamCLI[teamState](l,"state","get","repository","contract-run")
	var c struct { Round int }
	if err := json.Unmarshal([]byte(cursor.Value),&c); err!=nil || c.Round!=0 { l.t.Fatal("contract runner did not close") }
	checklist := teamCLI[teamState](l,"state","get","repository","contract")
	var contract struct { Criteria []struct { ID, Owner, Status, Delivery, Evidence string } }
	if err := json.Unmarshal([]byte(checklist.Value),&contract); err!=nil || len(contract.Criteria)!=4 { l.t.Fatal("invalid contract checklist") }
	owners := []string{"tester","developer","tester","quality"}
	criteria := []string{"baseline","implementation","tests","quality"}
	ids := map[string]bool{}
	for i, item := range contract.Criteria {
		if item.ID!=criteria[i] || item.Owner!=owners[i] || item.Status!="verified" || item.Evidence=="" || ids[item.Delivery] { l.t.Fatal("missing distinct criterion evidence",item.ID) }
		ids[item.Delivery]=true
		d := teamCLI[teamDelivery](l,"messages","status",item.Delivery)
		if d.Target!=item.Owner || d.State!="completed" || !d.hasResult() { l.t.Fatal("invalid independent delivery",item.ID) }
	}
	var records []teamDelivery
	if err := l.get(l.api+"/api/v1/deliveries?limit=100",&records); err!=nil || len(records)>=100 { l.t.Fatal("cannot inspect complete delivery set",err) }
	rounds := map[int]bool{}
	for _, summary := range records {
		d := teamCLI[teamDelivery](l,"messages","status",summary.ID)
		if d.State!="completed" { l.t.Fatal("unfinished or unsuccessful delivery",d.ID) }
		if d.Target=="pm" && strings.HasPrefix(d.Text,"Contract round ") {
			fields:=strings.Fields(d.Text)
			n,err:=strconv.Atoi(strings.TrimSuffix(fields[2],":"))
			if err!=nil || rounds[n] || !d.hasResult() { l.t.Fatal("invalid or duplicate PM activation") }
			rounds[n]=true
		}
	}
	if !rounds[1] || !rounds[2] { l.t.Fatal("contract did not continue across independent PM turns") }
	if err:=verifyTeamWorkspace(ws,base,"defect"); err!=nil { l.t.Fatal(err) }
	run("run",30*time.Second)
	var after []teamDelivery
	if err:=l.get(l.api+"/api/v1/deliveries?limit=100",&after); err!=nil || len(after)!=len(records) { l.t.Fatal("fulfilled contract scheduled additional work") }
	l.t.Log("PASS: baseline failure checkpoint, multiple durable PM turns, independent owner deliveries, actual repaired workspace and closed-contract reattachment.")
}
