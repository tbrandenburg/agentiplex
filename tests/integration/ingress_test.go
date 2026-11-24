package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/tbrandenburg/agentiplex"
	"github.com/tbrandenburg/agentiplex/internal/api"
)

func ingressClient(t *testing.T, b *agentiplex.Bus) (*httptest.Server, *a2aclient.Client) {
	t.Helper()
	s := httptest.NewUnstartedServer(nil)
	base := "http://" + s.Listener.Addr().String()
	s.Config.Handler = api.Handler(b, base)
	s.Start()
	t.Cleanup(s.Close)
	ctx := context.Background()
	card, err := agentcard.DefaultResolver.Resolve(ctx, s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != "Agentiplex" || len(card.SupportedInterfaces) != 1 ||
		card.SupportedInterfaces[0].URL != s.URL+"/a2a" || card.SupportedInterfaces[0].ProtocolVersion != a2a.Version ||
		card.Capabilities.Streaming || card.Capabilities.PushNotifications {
		t.Fatalf("unexpected advertised card: %+v", card)
	}
	c, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Destroy() })
	return s, c
}

func sendRequest(id, target, text string, immediate bool) *a2a.SendMessageRequest {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{
		"operation": "send", "target": target, "text": text,
	}))
	msg.ID = id
	return &a2a.SendMessageRequest{Message: msg, Config: &a2a.SendMessageConfig{ReturnImmediately: immediate}}
}

func nativeSend(t *testing.T, c *a2aclient.Client, req *a2a.SendMessageRequest) *a2a.Task {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := c.SendMessage(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	task, ok := res.(*a2a.Task)
	if !ok || task == nil || task.ID == "" || task.ContextID == "" {
		t.Fatalf("expected bus-owned A2A task, got %T %+v", res, res)
	}
	return task
}

func nativeTask(t *testing.T, c *a2aclient.Client, id a2a.TaskID) *a2a.Task {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := c.GetTask(ctx, &a2a.GetTaskRequest{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func waitNative(t *testing.T, c *a2aclient.Client, id a2a.TaskID, state a2a.TaskState) *a2a.Task {
	t.Helper()
	deadline := time.Now().Add(30*time.Second)
	for time.Now().Before(deadline) {
		task := nativeTask(t, c, id)
		if task.Status.State == state {
			return task
		}
		if task.Status.State.Terminal() {
			t.Fatalf("unexpected terminal state %s", task.Status.State)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach %s", id, state)
	return nil
}

func TestA2AIngressOfflineRestartAndDeduplication(t *testing.T) {
	cfg := config(t)
	b := open(t, cfg)
	a := &agentService{}
	a.start(t, false)
	defer a.stop()
	ctx := context.Background()
	if _, err := b.Register(ctx, "architect", a.URL()); err != nil {
		t.Fatal(err)
	}
	s, c := ingressClient(t, b)
	a.stop()
	req := sendRequest("native-offline", "architect", "review requirement 42", true)
	task := nativeSend(t, c, req)
	if task.Status.State != a2a.TaskStateSubmitted {
		t.Fatalf("enqueue state: %s", task.Status.State)
	}
	duplicate := nativeSend(t, c, req)
	if task.ID != duplicate.ID || task.ContextID != duplicate.ContextID {
		t.Fatal("duplicate returned another task")
	}
	if _, err := c.SendMessage(ctx, sendRequest("native-offline", "architect", "different", true)); !errors.Is(err, a2a.ErrInvalidParams) {
		t.Fatalf("conflicting messageId: %v", err)
	}
	_ = c.Destroy()
	s.Close()
	b.Close()

	b2 := open(t, cfg)
	_, c2 := ingressClient(t, b2)
	pending := nativeTask(t, c2, task.ID)
	if pending.Status.State != a2a.TaskStateSubmitted || pending.ContextID != task.ContextID {
		t.Fatalf("task after restart: %+v", pending)
	}
	a.start(t, false)
	done := waitNative(t, c2, task.ID, a2a.TaskStateCompleted)
	if len(done.Artifacts) != 1 || done.Artifacts[0].Name != "downstream-response" {
		t.Fatalf("missing downstream artifact: %+v", done)
	}
	result, err := json.Marshal(done.Artifacts[0].Parts[0].Data())
	if err != nil || !strings.Contains(string(result), "SIMULATED architect") {
		t.Fatalf("downstream response: %s %v", result, err)
	}
	replay := nativeSend(t, c2, req)
	if replay.ID != task.ID || replay.Status.State != a2a.TaskStateCompleted || a.calls.Load() != 1 {
		t.Fatalf("retry replayed downstream work: %d", a.calls.Load())
	}
	zero := 0
	withoutHistory, err := c2.GetTask(ctx, &a2a.GetTaskRequest{ID: task.ID, HistoryLength: &zero})
	if err != nil || len(withoutHistory.History) != 0 {
		t.Fatalf("historyLength=0: %v", err)
	}
	if len(done.History) != 1 || done.History[0].ID != req.Message.ID {
		t.Fatal("original message identity missing")
	}
}

func TestA2AIngressBlockingAndErrors(t *testing.T) {
	b := open(t, config(t))
	a := &agentService{}
	a.start(t, false)
	defer a.stop()
	ctx := context.Background()
	if _, err := b.Register(ctx, "architect", a.URL()); err != nil {
		t.Fatal(err)
	}
	_, c := ingressClient(t, b)
	task := nativeSend(t, c, sendRequest("native-blocking", "architect", "blocking request", false))
	if task.Status.State != a2a.TaskStateCompleted || a.calls.Load() != 1 {
		t.Fatalf("blocking response: %+v, calls %d", task, a.calls.Load())
	}
	if _, err := c.GetTask(ctx, &a2a.GetTaskRequest{ID: "missing"}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("unknown task: %v", err)
	}
	cli, err := b.Send(ctx, "architect", "admin message", "cli-only")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(cli.ID)}); !errors.Is(err, a2a.ErrTaskNotFound) {
		t.Fatalf("CLI record exposed as A2A task: %v", err)
	}
	if _, err := c.SendMessage(ctx, sendRequest("unknown", "missing", "text", true)); !errors.Is(err, a2a.ErrInvalidParams) {
		t.Fatalf("unknown target: %v", err)
	}
	invalid := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("not a routing request"))}
	if _, err := c.SendMessage(ctx, invalid); !errors.Is(err, a2a.ErrInvalidParams) {
		t.Fatalf("invalid input: %v", err)
	}
	if _, err := c.CancelTask(ctx, &a2a.CancelTaskRequest{ID: task.ID}); !errors.Is(err, a2a.ErrTaskNotCancelable) {
		t.Fatalf("unsupported cancellation: %v", err)
	}
}

func TestA2AIngressAmbiguousOutcome(t *testing.T) {
	b := open(t, config(t))
	a := &agentService{}
	a.start(t, true)
	defer a.stop()
	if _, err := b.Register(context.Background(), "architect", a.URL()); err != nil {
		t.Fatal(err)
	}
	_, c := ingressClient(t, b)
	req := sendRequest("native-uncertain", "architect", "side effect", false)
	task := nativeSend(t, c, req)
	meta, ok := task.Metadata["https://github.com/tbrandenburg/agentiplex"].(map[string]any)
	if task.Status.State != a2a.TaskStateFailed || !ok || meta["deliveryState"] != "uncertain" || a.calls.Load() != 1 {
		t.Fatalf("ambiguous result not distinguished: %+v", task)
	}
	if replay := nativeSend(t, c, req); replay.ID != task.ID || a.calls.Load() != 1 {
		t.Fatal("ambiguous work replayed")
	}
}
