package cli

import (
	"context"
	"fmt"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// tmx shelve NAME [--note TEXT]: stop an agent and put it back into the Library (it keeps its
// conversation and its place; Start brings it back). tmx unshelve NAME [--start]: take it out
// of the Library again, and start it with --start.
func cmdShelve(e *env, args []string) error {
	fs := newFlags("shelve")
	note := fs.String("note", "", "why it is shelved (shown in its timeline)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("usage: tmx shelve NAME [--note TEXT]")
	}
	return withOwner(e, func(c *client.Client, cx context.Context) error {
		var a model.Agent
		if err := c.Post(cx, "/api/agents/"+client.Seg(pos[0])+"/shelve", map[string]string{"note": *note}, &a); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "%s is shelved (in the Library); tmx unshelve %s --start brings it back\n", a.Name, a.Name)
		return nil
	})
}

func cmdUnshelve(e *env, args []string) error {
	fs := newFlags("unshelve")
	start := fs.Bool("start", false, "also start it")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("usage: tmx unshelve NAME [--start]")
	}
	return withOwner(e, func(c *client.Client, cx context.Context) error {
		var a model.Agent
		if err := c.Post(cx, "/api/agents/"+client.Seg(pos[0])+"/unshelve", map[string]bool{"start": *start}, &a); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "%s is out of the Library: %s\n", a.Name, a.State)
		return nil
	})
}
