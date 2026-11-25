package cli

import (
	"context"
	"fmt"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
)

// tmx cards close: retire owner cards without an answer (no decision recorded, no mail).
func cmdCards(e *env, args []string) error {
	const usage = "usage: tmx cards answer ID VERSION ANSWER... | close --reason TEXT (ID... | --imported)"
	if len(args) >= 4 && args[0] == "answer" {
		version, err := strconv.ParseInt(args[2], 10, 64)
		if err != nil || version < 0 {
			return usageErr(usage)
		}
		return withOwner(e, func(c *client.Client, cx context.Context) error {
			var out model.Approval
			req := fleet.DecideRequest{Decision: "answer", Version: version, Answer: strings.Join(args[3:], " ")}
			if err := ownerDo(e, c, cx, http.MethodPost, "/api/approvals/"+client.Seg(args[1]), req, &out); err != nil {
				return err
			}
			fmt.Fprintf(e.out, "%s: %s\n", out.ID, out.State)
			return nil
		})
	}
	if len(args) == 0 || args[0] != "close" {
		return usageErr(usage)
	}
	fs := newFlags("cards close")
	reason := fs.String("reason", "", "why they are closed (stays on the cards)")
	imported := fs.Bool("imported", false, "every waiting card an import brought along")
	ids, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	if strings.TrimSpace(*reason) == "" || (len(ids) == 0 && !*imported) {
		return usageErr(usage)
	}
	return withOwner(e, func(c *client.Client, cx context.Context) error {
		var out fleet.CloseCardsResult
		if err := c.Post(cx, "/api/approvals/close", fleet.CloseCardsRequest{IDs: ids, Imported: *imported, Reason: *reason}, &out); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "closed %d question(s)\n", len(out.Closed))
		skipped := make([]string, 0, len(out.Skipped))
		for id := range out.Skipped {
			skipped = append(skipped, id)
		}
		slices.Sort(skipped)
		for _, id := range skipped {
			fmt.Fprintf(e.out, "not closed %s: %s\n", id, out.Skipped[id])
		}
		return nil
	})
}
