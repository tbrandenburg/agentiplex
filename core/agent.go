package core

import (
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

type Agent struct {
	Reachability string `json:"reachability,omitempty"`
	ObservedAt *time.Time `json:"observedAt,omitempty"`
	ID string `json:"id"`
	CardURL string `json:"cardUrl"`
	Endpoint string `json:"endpoint"`
	Card *a2a.AgentCard `json:"card"`
	UpdatedAt time.Time `json:"updatedAt"`
}
