package catalog

import (
	"testing"

	sdk "github.com/Agent-Card/ai-catalog-go/catalog"
	"github.com/Agent-Card/ai-catalog-go/validate"
)

func TestCatalog(t *testing.T){
	raw,err:=Build("demo",[]Entry{{ID:"tester",Name:"Tester",Description:"Test agent",URL:"http://127.0.0.1:8001/.well-known/agent-card.json",Tags:[]string{"testing"}}})
	if err!=nil{t.Fatal(err)}
	doc,err:=sdk.Parse(raw)
	if err!=nil{t.Fatal(err)}
	if !validate.Validate(doc).IsValid||len(doc.GetByTag("testing"))!=1||doc.Entries[0].Type!=sdk.MediaTypeA2AAgentCard{t.Fatal("invalid catalog")}
	if _,err=Build("demo",nil);err!=nil{t.Fatal(err)}
}
