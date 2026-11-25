package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/fake"
	"github.com/blackcode-ch/tmx-go/internal/store"
)

type pendingSubmissionRunner struct{ base *fake.Runner }

func (r *pendingSubmissionRunner) Start(ctx context.Context, s runner.Spec) (runner.Process, error) {
	p, err := r.base.Start(ctx, s)
	if err != nil {
		return nil, err
	}
	return &pendingSubmissionProcess{Process: p}, nil
}

type pendingSubmissionProcess struct{ runner.Process }

// Handoff succeeds, but only the test's explicit receipt confirms Enter.
func (p *pendingSubmissionProcess) SendMail(string, []string) error { return nil }

func TestOwnerSendRemainsSendingUntilTerminalSubmission(t *testing.T) {
	r := &pendingSubmissionRunner{base: fake.New()}
	var st *store.Store
	d := apitest.New(t, func(o *fleet.Options) { o.Runners[model.ModeTerminal] = r; st = o.Store })
	a, err := d.Fleet.LaunchAgent(fleet.OwnerCaller, fleet.LaunchRequest{ProjectID: workerProject(t, d), Name: "receipt-target", Harness: model.HarnessClaude, Mode: model.ModeTerminal})
	if err != nil {
		t.Fatal(err)
	}
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	r.base.Last(a.ID).State(model.StateIdle)
	ready := time.Now().Add(time.Second)
	for {
		current, err := d.Fleet.Agent(a.ID)
		if err == nil && current.State == model.StateIdle {
			break
		}
		if time.Now().After(ready) {
			t.Fatal("terminal did not report ready")
		}
		time.Sleep(time.Millisecond)
	}
	var sent model.Message
	if code := owner.do("POST", "/api/agents/"+a.ID+"/send", map[string]string{"text": "wait for actual Enter", "retry_id": "one-send"}, &sent); code != 200 {
		t.Fatal(code)
	}
	if sent.State != model.MsgSending || sent.Receipt != "dispatching" || sent.DeliveredAt != 0 {
		t.Fatalf("premature API delivery: %+v", sent)
	}
	stored, err := st.GetMessage(sent.ID)
	if err != nil || stored.State != model.MsgSending || stored.DeliveredAt != 0 {
		t.Fatalf("premature stored delivery: %+v %v", stored, err)
	}
	// An idempotent retry while typing must not claim delivery either.
	var retry model.Message
	owner.do("POST", "/api/agents/"+a.ID+"/send", map[string]string{"text": "wait for actual Enter", "retry_id": "one-send"}, &retry)
	if retry.ID != sent.ID || retry.State != model.MsgSending || retry.DeliveredAt != 0 {
		t.Fatal(retry)
	}
	before := model.Now()
	r.base.Last(a.ID).Emit(runner.Event{Kind: runner.EvMailDelivery, MessageIDs: []string{sent.ID}, Delivery: "submitted"})
	deadline := time.Now().Add(time.Second)
	for {
		stored, err = st.GetMessage(sent.ID)
		if err == nil && stored.State == model.MsgDelivered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no submitted state: %+v %v", stored, err)
		}
		time.Sleep(time.Millisecond)
	}
	if stored.Receipt != "submitted" || stored.DeliveredAt < before || stored.DeliveredAt != stored.ReceiptAt {
		t.Fatalf("wrong submission timestamp: %+v", stored)
	}
	var delivered model.Message
	owner.do("POST", "/api/agents/"+a.ID+"/send", map[string]string{"text": "wait for actual Enter", "retry_id": "one-send"}, &delivered)
	if delivered.State != model.MsgDelivered || delivered.DeliveredAt != stored.DeliveredAt {
		t.Fatal(delivered)
	}
}
