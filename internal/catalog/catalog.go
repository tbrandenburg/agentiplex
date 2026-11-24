package catalog

import (
	"fmt"

	sdk "github.com/Agent-Card/ai-catalog-go/catalog"
	"github.com/Agent-Card/ai-catalog-go/validate"
)

const Path = sdk.WellKnownPath
const MediaType = sdk.MediaTypeCatalog

type Entry struct {
	ID, Name, Description, URL, UpdatedAt string
	Tags []string
}

func Build(namespace string, entries []Entry) ([]byte,error) {
	doc:=sdk.AICatalog{SpecVersion:"1.0",Host:&sdk.HostInfo{DisplayName:"Agentiplex ("+namespace+")"}}
	for _,e:=range entries{
		doc.Entries=append(doc.Entries,sdk.CatalogEntry{
			Identifier:"urn:agentiplex:"+namespace+":"+e.ID,DisplayName:e.Name,Description:e.Description,
			URL:e.URL,Type:sdk.MediaTypeA2AAgentCard,Tags:e.Tags,UpdatedAt:e.UpdatedAt,
		})
	}
	result:=validate.Validate(&doc)
	if !result.IsValid{return nil,fmt.Errorf("catalog validation failed: %v",result.Errors)}
	return doc.ToJSON()
}
