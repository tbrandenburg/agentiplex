package agentiplex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const ingressPrefix = "a2a-"
const ingressMetadata = "https://github.com/tbrandenburg/agentiplex"

// SendMessage implements the native A2A direct-send skill. The durable
// delivery record is also the task store, avoiding divergent state.
func (b *Bus) SendMessage(ctx context.Context, req *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	if req == nil || req.Message == nil || req.Tenant != "" {
		return nil, a2a.ErrInvalidParams
	}
	msg := req.Message
	if msg.Role != a2a.MessageRoleUser || strings.TrimSpace(msg.ID) == "" || len(msg.ID) > 256 {
		return nil, a2a.NewError(a2a.ErrInvalidParams, "a user message with a nonempty messageId (max 256 bytes) is required")
	}
	if msg.TaskID != "" || msg.ContextID != "" || len(msg.ReferenceTasks) != 0 || len(msg.Extensions) != 0 || len(msg.Metadata) != 0 || len(req.Metadata) != 0 {
		return nil, a2a.NewError(a2a.ErrUnsupportedOperation, "only new direct-send tasks are supported")
	}
	if req.Config != nil {
		if req.Config.PushConfig != nil {
			return nil, a2a.ErrPushNotificationNotSupported
		}
		if req.Config.HistoryLength != nil && *req.Config.HistoryLength < 0 {
			return nil, a2a.ErrInvalidParams
		}
		if len(req.Config.AcceptedOutputModes) > 0 {
			allowed := false
			for _, mode := range req.Config.AcceptedOutputModes {
				if mode == "application/json" {
					allowed = true
				}
			}
			if !allowed {
				return nil, a2a.ErrUnsupportedContentType
			}
		}
	}
	if len(msg.Parts) != 1 || msg.Parts[0] == nil {
		return nil, a2a.NewError(a2a.ErrInvalidParams, "one structured data part is required")
	}
	part := msg.Parts[0]
	if (part.MediaType != "" && part.MediaType != "application/json") || part.Filename != "" || len(part.Metadata) != 0 {
		return nil, a2a.ErrUnsupportedContentType
	}
	data, ok := part.Data().(map[string]any)
	operation, operationOK := data["operation"].(string)
	if !ok || len(data) != 3 || !operationOK || (operation!="send"&&operation!="broadcast"&&operation!="dispatch") {
		return nil, a2a.NewError(a2a.ErrInvalidParams, "expected data {operation: send|broadcast|dispatch, target: agent/group/pool ID, text: prompt}")
	}
	target, targetOK := data["target"].(string)
	text, textOK := data["text"].(string)
	if !targetOK || !textOK || !validID.MatchString(target) {
		return nil, a2a.ErrInvalidParams
	}

	// The reserved prefix cannot collide with UUIDs or hex-only CLI hashes.
	hash := sha256.Sum256([]byte(msg.ID))
	id := ingressPrefix + hex.EncodeToString(hash[:30])
	var d Delivery
	var err error
	if operation=="send"{
		d,err=b.sendWithID(ctx,target,text,id,msg.ID)
	}else{
		d,err=b.submitCollection(ctx,operation,target,text,id,msg.ID)
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return nil, a2a.NewError(a2a.ErrInvalidParams, "unknown target agent or collection")
		case errors.Is(err, ErrConflict):
			return nil, a2a.NewError(a2a.ErrInvalidParams, "messageId already used for different work")
		case errors.Is(err, ErrInvalid):
			return nil, a2a.ErrInvalidParams
		default:
			return nil, a2a.ErrInternalError
		}
	}

	var history *int
	if req.Config != nil {
		history = req.Config.HistoryLength
		if req.Config.ReturnImmediately {
			return ingressTask(d, history), nil
		}
	}

	// A blocking request can time out without canceling its durable work.
	wait, cancel := context.WithTimeout(ctx, b.cfg.Timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		task := ingressTask(d, history)
		if task.Status.State.Terminal() || task.Status.State == a2a.TaskStateInputRequired || task.Status.State == a2a.TaskStateAuthRequired {
			return task, nil
		}
		select {
		case <-wait.Done():
			return nil, a2a.NewError(a2a.ErrServerError, "blocking wait ended; durable task remains available").WithDetails(map[string]any{"taskId": d.ID})
		case <-ticker.C:
			d, err = b.Delivery(d.ID)
			if err != nil {
				return nil, a2a.ErrInternalError
			}
		}
	}
}

func (b *Bus) GetTask(ctx context.Context, req *a2a.GetTaskRequest) (*a2a.Task, error) {
	if req == nil || req.Tenant != "" || (req.HistoryLength != nil && *req.HistoryLength < 0) {
		return nil, a2a.ErrInvalidParams
	}
	if !strings.HasPrefix(string(req.ID), ingressPrefix) || !validID.MatchString(string(req.ID)) {
		return nil, a2a.ErrTaskNotFound
	}
	d, err := b.Delivery(string(req.ID))
	if errors.Is(err, ErrNotFound) {
		return nil, a2a.ErrTaskNotFound
	}
	if err != nil {
		return nil, a2a.ErrInternalError
	}
	if d.IngressMessageID == "" || d.State == "preparing" {
		return nil, a2a.ErrTaskNotFound
	}
	return ingressTask(d, req.HistoryLength), nil
}

// The bus owns the task and context; downstream IDs are correlation metadata.
func ingressTask(d Delivery, history *int) *a2a.Task {
	state := a2a.TaskStateFailed
	switch d.State {
	case "preparing", "queued":
		state = a2a.TaskStateSubmitted
	case "attempting", "accepted":
		state = a2a.TaskStateWorking
	case "completed":
		state = a2a.TaskStateCompleted
	case "input-required":
		state = a2a.TaskStateInputRequired
	case "auth-required":
		state = a2a.TaskStateAuthRequired
	}
	task := &a2a.Task{
		ID: a2a.TaskID(d.ID), ContextID: "ctx-" + d.ID,
		Status: a2a.TaskStatus{State: state, Timestamp: &d.UpdatedAt},
		Metadata: map[string]any{ingressMetadata: map[string]any{
			"deliveryId": d.ID, "deliveryState": d.State, "target": d.Target,
			"downstreamTaskId": string(d.TaskID), "downstreamContextId": d.ContextID,
			"operation": d.Kind, "assignedAgent": d.AssignedAgent,
		}},
	}
	detail := d.Detail
	if d.State == "uncertain" {
		detail = "Agentiplex cannot confirm the downstream outcome; automatic replay is disabled. " + detail
	}
	if detail != "" {
		task.Status.Message = a2a.NewMessageForTask(a2a.MessageRoleAgent, task, a2a.NewTextPart(detail))
		task.Status.Message.ID = fmt.Sprintf("status-%s-%d", d.ID, d.UpdatedAt.UnixNano())
	}
	operation:=d.Kind
	if operation==""{operation="send"}
	if history == nil || *history > 0 {
		message := a2a.NewMessageForTask(a2a.MessageRoleUser, task, a2a.NewDataPart(map[string]any{
			"operation": operation, "target": d.Target, "text": d.Text,
		}))
		message.ID = d.IngressMessageID
		task.History = []*a2a.Message{message}
	}
	if d.Kind=="broadcast"{
		task.Artifacts=[]*a2a.Artifact{{ID:a2a.ArtifactID("recipients-"+d.ID),Name:"broadcast-recipients",Parts:a2a.ContentParts{a2a.NewDataPart(d.Children)}}}
	}
	if len(d.Result) > 0 && json.Valid(d.Result) {
		task.Artifacts = []*a2a.Artifact{{
			ID: a2a.ArtifactID("result-" + d.ID), Name: "downstream-response",
			Description: "Original downstream A2A message or task representation",
			Parts: a2a.ContentParts{a2a.NewDataPart(json.RawMessage(d.Result))},
		}}
	}
	return task
}
