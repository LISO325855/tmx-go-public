package peers

// The Python fleet's state block for this node's head (the owner, 17 Nov 2026: the Go head sees the
// machines as the Python fleet's architect does). A Python daemon answers GET /api/state with
// {"lines": [...]}: per machine RAM, disk, agents, budgets and open alerts, the block its own
// leads get on every prompt. This client reads it in the background from the first peer that
// has one and keeps the last answer; the owner's turn for the head reads that copy and never
// waits on the network (fleet/watch_state.go).

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	fleetStateEvery   = 30 * time.Second
	fleetStateTimeout = 10 * time.Second
	// FleetStateMaxLines is how many lines of a peer's block are kept (Python sends at most 12).
	FleetStateMaxLines = 12
	fleetStateMaxLine  = 400 // characters of one line
)

// FleetState is the last state block read from a Python peer.
type FleetState struct {
	From  string   // the peer that answered; "" = none yet
	At    int64    // Unix ms of that answer; 0 = never
	Lines []string // its lines, cleaned: display text, never instructions
}

// FleetState returns the last block read (a copy).
func (c *Client) FleetState() FleetState {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	st := c.fleetState
	st.Lines = append([]string(nil), st.Lines...)
	return st
}

// RunFleetState reads the block every fleetStateEvery until ctx ends, even when
// initially empty so live additions are discovered. Callers must cancel ctx.
func (c *Client) RunFleetState(ctx context.Context) {
	tick := time.NewTicker(fleetStateEvery)
	defer tick.Stop()
	c.PollFleetState(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			c.PollFleetState(ctx)
		}
	}
}

// PollFleetState asks the peers once, the one that answered last time first, and keeps the
// first block it gets. A peer without the route (a Go node) or without a pair token is passed
// over. With no answer the last block stays as it is: its age says how old it is.
func (c *Client) PollFleetState(ctx context.Context) bool {
	names := c.Names()
	c.stateMu.Lock()
	last := c.fleetState.From
	c.stateMu.Unlock()
	for i, n := range names {
		if n == last {
			names[0], names[i] = names[i], names[0]
			break
		}
	}
	for _, name := range names {
		if ctx.Err() != nil {
			return false
		}
		var res struct {
			Lines []string `json:"lines"`
		}
		peer, ok := c.lookup(name)
		if !ok {
			continue
		}
		if _, err := c.do(ctx, peer, "GET", "/api/state", nil, &res, fleetStateTimeout); err != nil {
			var refused Refused
			if !errors.As(err, &refused) {
				c.log.Debug("fleet state: peer did not answer", "peer", name, "err", err)
			}
			continue
		}
		lines := cleanStateLines(res.Lines)
		if len(lines) == 0 {
			continue
		}
		c.stateMu.Lock()
		c.fleetState = FleetState{From: name, At: time.Now().UnixMilli(), Lines: lines}
		c.stateMu.Unlock()
		return true
	}
	return false
}

// cleanStateLines makes a peer's lines display text: no control or format characters, one line
// each, bounded in length and number, blank ones dropped.
func cleanStateLines(in []string) []string {
	out := make([]string, 0, FleetStateMaxLines)
	for _, s := range in {
		s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == '\t' || r == ' ' || r == ' ' {
				return ' '
			}
			return r
		}, s)), " ")
		if s = clean(s, fleetStateMaxLine); s == "" {
			continue
		}
		if len(out) == FleetStateMaxLines {
			break
		}
		out = append(out, s)
	}
	return out
}
