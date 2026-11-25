package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
)

// cmdConfig keeps sensitive settings in the daemon's owner-gated config writer.
func cmdConfig(e *env, args []string) error {
	if len(args) < 1 || len(args) > 2 || args[0] != "fleet-lead" {
		return usageErr("usage: tmx config fleet-lead [agent@node|none]")
	}
	if os.Getenv(fleet.EnvToken) != "" {
		return fmt.Errorf("only the owner can configure fleet leadership")
	}
	return withOwner(e, func(c *client.Client, cx context.Context) error {
		var out struct {
			FleetLead string `json:"fleet_lead"`
		}
		if len(args) == 1 {
			if err := c.Get(cx, "/api/config/fleet-lead", &out); err != nil {
				return err
			}
		} else {
			lead := args[1]
			if lead == "none" {
				lead = ""
			}
			if err := ownerDo(e, c, cx, http.MethodPut, "/api/config/fleet-lead", map[string]string{"fleet_lead": lead}, &out); err != nil {
				return err
			}
		}
		if out.FleetLead == "" {
			fmt.Fprintln(e.out, "fleet lead: none")
		} else {
			fmt.Fprintln(e.out, "fleet lead:", out.FleetLead)
		}
		return nil
	})
}
