package agentiplex

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid = errors.New("invalid request")
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

type Delivery struct {
	Kind string `json:"kind,omitempty"`
	ParentID string `json:"parentId,omitempty"`
	Members []string `json:"members,omitempty"`
	AssignedAgent string `json:"assignedAgent,omitempty"`
	Children []ChildDelivery `json:"children,omitempty"`
	ID string `json:"id"`
	IngressMessageID string `json:"ingressMessageId,omitempty"`
	Target string `json:"target"`
	Text string `json:"text"`
	Endpoint string `json:"endpoint"`
	CardURL string `json:"cardUrl"`
	State string `json:"state"`
	Attempts int `json:"attempts"`
	Polls int `json:"polls"`
	TaskID a2a.TaskID `json:"taskId,omitempty"`
	ContextID string `json:"contextId,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Detail string `json:"detail,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Event struct {
	Sequence uint64 `json:"sequence"`
	Kind string `json:"kind"`
	ID string `json:"id"`
	State string `json:"state,omitempty"`
	At time.Time `json:"at"`
}

type Collection struct {
	ID string `json:"id"`
	Kind string `json:"kind"`
	Members []string `json:"members"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ChildDelivery struct {
	ID string `json:"id"`
	Target string `json:"target"`
	State string `json:"state"`
	Detail string `json:"detail,omitempty"`
}
