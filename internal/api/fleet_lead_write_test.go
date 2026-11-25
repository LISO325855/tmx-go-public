package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/peers"
	"github.com/blackcode-ch/tmx-go/internal/runner/fake"
)

func TestPausedLeadWriteDoesNotBlockOtherMailOrRotation(t *testing.T) {
	s := newPeerSetup(t)
	f := s.d.Fleet
	f.SetFleetLead("tmx-lead@hosta")
	if _, err := f.CreateProject(fleet.OwnerCaller, fleet.ProjectRequest{Name: "independent"}); err != nil {
		t.Fatal(err)
	}
	head, err := f.Agent("head")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.Agent("independent-lead")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []model.Agent{head, other} {
		s.d.Chat.Last(a.ID).State(model.StateIdle)
		waitFor(t, "agent idle", func() bool { got, _ := f.Agent(a.ID); return got.State == model.StateIdle })
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.d.Chat.Script(nil, func(p *fake.Process, text string) {
		if p.Spec.AgentID == head.ID && strings.Contains(text, "paused-lead-write") {
			once.Do(func() { close(entered) })
			<-resume
		}
	})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	var requests sync.WaitGroup
	// WithPeers cleanup closes client connections. Join every request before that
	// cleanup runs, including on failed assertions; releasing the writer alone is
	// insufficient because its HTTP response still has to reach the client.
	defer func() {
		release()
		done := make(chan struct{})
		go func() { requests.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(12 * time.Second):
			t.Error("request cleanup timed out")
		}
	}()
	type result struct {
		status int
		err    error
	}
	post := func(token, id, body, to string) <-chan result {
		ch := make(chan result, 1)
		requests.Add(1)
		go func() {
			defer requests.Done()
			raw, err := json.Marshal(incoming(id, body, to))
			if err != nil {
				ch <- result{err: err}
				return
			}
			req, err := http.NewRequest(http.MethodPost, s.url+"/api/messages/incoming", bytes.NewReader(raw))
			if err != nil {
				ch <- result{err: err}
				return
			}
			req.Header.Set(peers.HeaderFrom, "hosta")
			req.Header.Set(peers.HeaderToken, token)
			req.Header.Set(peers.HeaderOnBehalf, "daemon")
			req.Header.Set(peers.HeaderVerifiedAgent, "tmx-lead")
			response, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
			if err != nil {
				ch <- result{err: err}
				return
			}
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			ch <- result{status: response.StatusCode, err: err}
		}()
		return ch
	}
	firstDone := post(s.pair, "0123456789ab", "paused-lead-write", "head")
	select {
	case <-entered:
	case got := <-firstDone:
		t.Fatalf("write did not pause: status %d, error %v", got.status, got.err)
	case <-time.After(5 * time.Second):
		t.Fatal("write never entered")
	}

	// This uses the very same transaction mutex as real rotation. It must finish
	// while the first runner is still paused, not after we release that runner.
	current := peers.NewPairToken()
	rotated := make(chan error, 1)
	requests.Add(1)
	go func() {
		defer requests.Done()
		_, err := s.pc.Tokens().Store("hosta", current, "test-rotation")
		rotated <- err
	}()
	select {
	case err := <-rotated:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled write blocked rotation")
	}

	secondDone := post(current, "0123456789ac", "independent-mail", "independent-lead")
	select {
	case got := <-secondDone:
		if got.err != nil || got.status != 201 {
			t.Fatalf("other mail: status %d, error %v", got.status, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled write blocked another agent's mail")
	}
	sent := strings.Join(s.d.Chat.Last(other.ID).Sent(), "\n")
	if !strings.Contains(sent, "independent-mail") || !strings.Contains(sent, "[message from tmx-lead@hosta (fleet lead, other machine, verified sender)]") {
		t.Fatal("other agent did not receive verified mail during stalled write")
	}
	release()
	select {
	case got := <-firstDone:
		if got.err != nil || got.status != 201 {
			t.Fatalf("resumed mail: status %d, error %v", got.status, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resumed request did not finish")
	}

}
