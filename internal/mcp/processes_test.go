package mcp

import (
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMCPProcessHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != "mcp-process-helper" {
		return
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestProcessToolsStartWithoutApproval(t *testing.T) {
	d := apitest.New(t)
	cs := session(t, d, "head")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		out := mustOK(t, cs, "process_start", map[string]any{"purpose": "owned test", "command": exe, "args": []string{"-test.run=^TestMCPProcessHelper$", "--", "mcp-process-helper"}})
		if !strings.Contains(out, "Started process") {
			t.Fatal(out)
		}
	}
	aps, err := d.Fleet.Approvals(model.ApprovalPending)
	if err != nil || len(aps) != 0 {
		t.Fatalf("approval cards: %v %v", aps, err)
	}
	rows := d.Fleet.ProcessTools()
	if len(rows) != 2 || rows[0].PID == 0 || rows[1].PID == 0 {
		t.Fatalf("missing running tools: %+v", rows)
	}
	if _, err := d.Fleet.StopAgent(fleet.OwnerCaller, "head"); err != nil {
		t.Fatal(err)
	}
	for _, p := range d.Fleet.ProcessTools() {
		if p.PID != 0 {
			t.Fatalf("surviving tool: %+v", p)
		}
	}
}

func TestProcessToolsRequestWithoutExecution(t *testing.T) {
	d := apitest.New(t)
	cs := session(t, d, "head")
	mustOK(t, cs, "processes", nil)
	out := mustOK(t, cs, "process_start", map[string]any{"purpose": "test command", "command": "sudo", "args": []string{"true"}})
	if !strings.Contains(out, "no process has started") {
		t.Fatal(out)
	}
	aps, err := d.Fleet.Approvals(model.ApprovalPending)
	if err != nil || len(aps) != 1 {
		t.Fatalf("approval missing: %v %v", aps, err)
	}
	if _, failed := call(t, cs, "process_stop", map[string]any{"id": "missing"}); !failed {
		t.Fatal("unknown tool stopped")
	}
}

func TestMachineRegistryTools(t *testing.T) {
	d := apitest.New(t)
	cs := session(t, d, "head")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mustOK(t, cs, "process_start", map[string]any{"purpose": "preview server", "command": exe, "args": []string{"-test.run=^TestMCPProcessHelper$", "--", "mcp-process-helper"}})
	tools := d.Fleet.ProcessTools()
	if len(tools) != 1 || tools[0].PID == 0 {
		t.Fatalf("tool: %+v", tools)
	}
	id := "tool:" + tools[0].ID
	out := mustOK(t, cs, "machine_processes", nil)
	for _, want := range []string{id, `"kind":"tool"`, `"what":"preview server"`, `"agent":"head"`, `"state":"running"`, `"actions":["stop","restart"]`, `"orphans":0`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
	if strings.Contains(out, "mcp-process-helper") {
		t.Fatalf("arguments are listed (they may hold secrets): %s", out)
	}
	out = mustOK(t, cs, "machine_process_restart", map[string]any{"id": id})
	now := d.Fleet.ProcessTools()
	if len(now) != 1 || now[0].ID == tools[0].ID || now[0].PID == 0 || now[0].PID == tools[0].PID || !strings.Contains(out, "tool:"+now[0].ID) {
		t.Fatalf("restart: %s %+v", out, now)
	}
	mustOK(t, cs, "machine_process_stop", map[string]any{"id": "tool:" + now[0].ID})
	if left := d.Fleet.ProcessTools(); len(left) != 1 || left[0].PID != 0 {
		t.Fatalf("after stop: %+v", left)
	}
	// The entry stays listed as stopped; a second stop has nothing to stop.
	list, err := d.Fleet.Registry(fleet.OwnerCaller, "")
	if err != nil || len(list) != 1 || list[0].State != "stopped" || strings.Join(list[0].Actions, ",") != "restart" {
		t.Fatalf("stopped entry: %+v %v", list, err)
	}
}
