package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"net/url"
)

func (t *Tools) registerProcesses(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "process_start", Description: "Start an owned background command in your workspace. Ordinary commands start immediately; existing guard holds require owner approval. It stops with you unless explicitly promoted."}, t.processStart)
	sdk.AddTool(s, &sdk.Tool{Name: "processes", Description: "List your owned tools and current harness child processes, including purpose, PID, executable summary and lifetime owner. Owned tools stop with you unless the owner explicitly promotes them to a project."}, t.processes)
	sdk.AddTool(s, &sdk.Tool{Name: "machine_processes", Description: "The machine registry: every shell, registered tool and long-running process agents started on this machine, with what it is, who started it, the project, the executable, since when, its state and PID. A shell also shows the thread it was launched on, its start command and whether it starts again with the node. Entries marked orphan run on although their agent is stopped or gone. The head sees all; other agents see what they started. Optional project (ID) filters."}, t.machineProcesses)
	sdk.AddTool(s, &sdk.Tool{Name: "machine_process_stop", Description: "Stop one registry entry by its ID from machine_processes (a registered tool, a shell, or one process by PID together with its start time; never by name). The head may stop registered tools, shells agents launched and orphans; other agents only what they started. A live agent's processes, the owner's own shells and tmx's own tools are refused."}, t.machineProcessStop)
	sdk.AddTool(s, &sdk.Tool{Name: "machine_process_restart", Description: "Restart a registered tool (ID tool:...; same command, a new ID) or a shell (ID shell:...; a new process, its start command is typed again). Processes tmx only saw cannot be restarted: tmx does not know their command."}, t.machineProcessRestart)
	sdk.AddTool(s, &sdk.Tool{Name: "process_stop", Description: "Stop one of your registered tools by its registry ID. Observed harness children and System tools cannot be stopped here."}, t.processStop)
}

func (t *Tools) processStart(ctx context.Context, _ *sdk.CallToolRequest, in fleet.ProcessRequest) (*sdk.CallToolResult, any, error) {
	var ap fleet.ProcessResult
	if err := t.c.Post(ctx, "/api/processes/requests", in, &ap); err != nil {
		return nil, nil, err
	}
	if ap.Approval != nil {
		return text("Waiting for owner approval " + ap.Approval.ID + "; no process has started."), nil, nil
	}
	if ap.Process == nil {
		return nil, nil, fmt.Errorf("missing process result")
	}
	return text("Started process " + ap.Process.ID + "; use processes to inspect it."), nil, nil
}
func (t *Tools) processes(ctx context.Context, _ *sdk.CallToolRequest, _ noArgs) (*sdk.CallToolResult, any, error) {
	var rows []model.ProcessInfo
	if err := t.c.Get(ctx, "/api/agents/"+client.Seg(t.agentID)+"/processes", &rows); err != nil {
		return nil, nil, err
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return nil, nil, err
	}
	return untrusted(string(b)), nil, nil
}

type machineProcessesArgs struct {
	Project string `json:"project,omitempty" jsonschema:"project ID to filter by; empty lists every project"`
}

func (t *Tools) machineProcesses(ctx context.Context, _ *sdk.CallToolRequest, in machineProcessesArgs) (*sdk.CallToolResult, any, error) {
	var out struct {
		Entries []model.MachineProcess `json:"entries"`
		Orphans int                    `json:"orphans"`
	}
	path := "/api/registry"
	if in.Project != "" {
		path += "?project=" + url.QueryEscape(in.Project)
	}
	if err := t.c.Get(ctx, path, &out); err != nil {
		return nil, nil, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, nil, err
	}
	return untrusted(string(b)), nil, nil
}

type machineProcessArgs struct {
	ID string `json:"id" jsonschema:"registry ID from machine_processes (tool:... or pid:...)"`
}

func (t *Tools) machineProcessStop(ctx context.Context, _ *sdk.CallToolRequest, in machineProcessArgs) (*sdk.CallToolResult, any, error) {
	if err := t.c.Post(ctx, "/api/registry/stop", in, nil); err != nil {
		return nil, nil, err
	}
	return text("Stopped " + in.ID + "."), nil, nil
}

func (t *Tools) machineProcessRestart(ctx context.Context, _ *sdk.CallToolRequest, in machineProcessArgs) (*sdk.CallToolResult, any, error) {
	var e model.MachineProcess
	if err := t.c.Post(ctx, "/api/registry/restart", in, &e); err != nil {
		return nil, nil, err
	}
	return text(fmt.Sprintf("Restarted; it is now %s (PID %d).", e.ID, e.PID)), nil, nil
}

type processStopArgs struct {
	ID string `json:"id" jsonschema:"registered tool ID from processes"`
}

func (t *Tools) processStop(ctx context.Context, _ *sdk.CallToolRequest, in processStopArgs) (*sdk.CallToolResult, any, error) {
	if err := t.c.Post(ctx, "/api/processes/"+client.Seg(in.ID)+"/stop", nil, nil); err != nil {
		return nil, nil, err
	}
	return text("Process stopped."), nil, nil
}
