package examples_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTeamEvidence(t *testing.T) {
	objective:="# Repair\n\nPreserve scope."
	result:=json.RawMessage("{\"text\":\"finding\"}")
	root:=teamDelivery{ID:"root",Target:"pm",State:"completed",Text:objective,Result:result}
	developer:=teamDelivery{ID:"dev",Target:"developer",State:"completed",Result:result}
	quality:=teamDelivery{ID:"quality",Target:"quality",State:"completed",Result:result}
	tester:=teamDelivery{ID:"tester",Target:"tester",State:"completed",Result:result}
	state:=[]teamState{{Key:"objective",Value:objective,Revision:7}}
	for _,role:=range []string{"pm","developer","quality","tester"} {
		state=append(state,teamState{Key:"note-"+role+"-fixture",Value:"{\"text\":\"finding\"}",Revision:8})
	}
	deliveries:=[]teamDelivery{root,developer,quality,tester}
	if err:=verifyTeamEvidence(root,deliveries,state,objective,7,"integration");err!=nil { t.Fatal(err) }
	if err:=verifyTeamEvidence(root,deliveries[:3],state[:4],objective,7,"defect");err!=nil { t.Fatal(err) }
	for _,tc:=range []struct{name string; ds []teamDelivery; ss []teamState; scenario string}{
		{"missing implementer",[]teamDelivery{root,quality,tester},state,"defect"},
		{"missing review",[]teamDelivery{root,developer},state,"defect"},
		{"missing tester",deliveries[:3],state,"integration"},
		{"missing tester note",deliveries,state[:4],"integration"},
		{"missing objective",deliveries,state[1:],"defect"},
		{"rewritten objective",deliveries,append([]teamState{{Key:"objective",Value:objective,Revision:9}},state[1:]...),"defect"},
		{"altered objective",deliveries,append([]teamState{{Key:"objective",Value:"different",Revision:7}},state[1:]...),"defect"},
		{"unresolved work",append(append([]teamDelivery(nil),deliveries...),teamDelivery{ID:"pending",State:"accepted"}),state,"defect"},
		{"no results",[]teamDelivery{root,{ID:"dev",Target:"developer",State:"completed"},quality},state,"defect"},
		{"missing notes",deliveries,state[:1],"defect"},
	} {
		t.Run(tc.name,func(t *testing.T){
			if err:=verifyTeamEvidence(root,tc.ds,tc.ss,objective,7,tc.scenario);err==nil { t.Fatal("accepted invalid evidence") }
		})
	}
}

func TestHybridEvidence(t *testing.T) {
	result:=json.RawMessage("{\"text\":\"finding\"}")
	root:=teamDelivery{ID:"root",Target:"pm",State:"completed",Text:"review",Result:result}
	worker:=teamDelivery{ID:"w1",Target:"architect",State:"completed",Result:result}
	state:=[]teamState{{Key:"pm",Value:"plan",Revision:1},{Key:"note-architect-1",Value:"finding",Revision:2}}
	if err:=verifyTeamEvidence(root,[]teamDelivery{root,worker},state,"review",0,"autonomous");err!=nil { t.Fatal(err) }
	for _,tc:=range []struct{ds []teamDelivery; ss []teamState}{
		{[]teamDelivery{root},state},
		{[]teamDelivery{root,worker},state[:1]},
		{[]teamDelivery{root,worker},state[1:]},
		{[]teamDelivery{root,worker},[]teamState{state[0],{Key:"note-tester-1",Value:"finding",Revision:2}}},
		{[]teamDelivery{root,{ID:"w1",Target:"architect",State:"accepted",Result:result}},state},
		{[]teamDelivery{root,{ID:"w1",Target:"architect",State:"completed"}},state},
	} {
		if err:=verifyTeamEvidence(root,tc.ds,tc.ss,"review",0,"autonomous");err==nil { t.Fatal("accepted invalid hybrid evidence") }
	}
}

func TestTeamWorkspace(t *testing.T) {
	for _,name:=range []string{"docs","defect","integration","fast-forward","dirty merge","wrong merge","modified check","unauthorized branch","missing candidate","uncommitted integration","staged patch","extra file"} {
		t.Run(name,func(t *testing.T){
			ws:=t.TempDir()
			base:=initTeamFixture(t,ws)
			scenario:=name
			if name=="docs" {
				writeTeamFile(t,filepath.Join(ws,"README.md"),"# Synthetic repository\n\n[Usage](docs/usage.md)\n")
			} else {
				if name!="defect" && name!="unauthorized branch" && name!="staged patch" && name!="extra file" && name!="uncommitted integration" {
					mustTeamGit(t,ws,"switch","-c","candidate")
				}
				fix:=fixedCalc
				if name=="wrong merge" { fix=brokenCalc+"\n// incorrect fix\n" }
				writeTeamFile(t,filepath.Join(ws,"calc.go"),fix)
				switch name {
				case "defect":
				case "unauthorized branch":
					scenario="defect";mustTeamGit(t,ws,"branch","unexpected")
				case "staged patch":
					scenario="defect";mustTeamGit(t,ws,"add","calc.go")
				case "extra file":
					scenario="defect";writeTeamFile(t,filepath.Join(ws,"unexpected"),"not in scope")
				case "uncommitted integration":
					scenario="integration"
				default:
					scenario="integration"
					if name=="modified check" { writeTeamFile(t,filepath.Join(ws,"calc_test.go"),"package fixture\n") }
					mustTeamGit(t,ws,"add",".")
					mustTeamGit(t,ws,"commit","-qm","candidate")
					mustTeamGit(t,ws,"switch","main")
					if name=="fast-forward" { mustTeamGit(t,ws,"merge","--ff-only","candidate") } else { mustTeamGit(t,ws,"merge","--no-ff","-m","integrate","candidate") }
					if name=="dirty merge" { writeTeamFile(t,filepath.Join(ws,"unexpected"),"dirty") }
					if name=="missing candidate" { mustTeamGit(t,ws,"branch","-d","candidate") }
				}
			}
			err:=verifyTeamWorkspace(ws,base,scenario)
			wantPass:=name=="docs" || name=="defect" || name=="integration"
			if (err==nil)!=wantPass { t.Fatalf("scenario %s: expected pass=%t, got %v",scenario,wantPass,err) }
			if name=="integration" && verifyTeamWorkspace(ws,base,"defect")==nil { t.Fatal("patch-only verifier accepted merge") }
		})
	}
}

func TestTeamCommandVerification(t *testing.T) {
	for _,tc:=range []struct{name,cmd string; timeout time.Duration; pass bool}{
		{"clean","true",10*time.Second,true},
		{"nonzero","exit 3",10*time.Second,false},
		{"mutates","echo changed > README.md",10*time.Second,false},
		{"timeout","exec sleep 5",100*time.Millisecond,false},
	} {
		t.Run(tc.name,func(t *testing.T){
			ws:=t.TempDir()
			initTeamFixture(t,ws)
			err:=verifyTeamCommand(ws,tc.timeout,os.Environ(),"sh","-c",tc.cmd)
			if (err==nil)!=tc.pass { t.Fatalf("expected pass=%t, got %v",tc.pass,err) }
		})
	}
}
