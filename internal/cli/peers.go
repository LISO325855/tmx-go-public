package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"text/tabwriter"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

// cmdPeers: `tmx peers import-python [--json]` moves this machine's Python tmx pair
// tokens into this daemon's secrets (production profile with "peers"; DESIGN.md
// "Python fleet interop"). It prints fingerprints only, never a secret, and never overwrites a
// secret this daemon already holds with another value.
func cmdPeers(e *env, args []string) error {
	if len(args) > 0 && (args[0] == "invite" || args[0] == "join" || args[0] == "list" || args[0] == "remove") {
		return cmdPeerManage(e, args)
	}
	fs := newFlags("peers")
	asJSON := fs.Bool("json", false, "print the JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] != "import-python" {
		return usageErr("usage: tmx peers import-python [--json]")
	}
	if err := e.load(); err != nil {
		return err
	}
	if !e.cfg.Production() {
		return fmt.Errorf("import needs the production profile")
	}
	if os.Getenv(fleet.EnvToken) != "" {
		return fmt.Errorf("only the owner can manage fleet peers")
	}
	var rows []peers.Imported
	if err := withOwner(e, func(c *client.Client, cx context.Context) error {
		return ownerDo(e, c, cx, http.MethodPost, "/api/peers/import-python", nil, &rows)
	}); err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(e.out, string(b))
	} else {
		w := tabwriter.NewWriter(e.out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "SECRET\tSTATUS\tFINGERPRINT")
		for _, r := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\n", r.Name, r.Status, r.Fingerprint)
		}
		w.Flush()
	}
	for _, r := range rows {
		if r.Status == peers.ImportConflict || r.Status == peers.ImportInvalid {
			return fmt.Errorf("%s: %s (nothing overwritten; compare fingerprints with that machine)", r.Name, r.Status)
		}
	}
	return nil
}

const peerUsage = "usage: tmx peers invite --name NAME --url HTTPS_URL | join --code CODE --url HTTPS_URL [--own-url HTTPS_URL] | list | remove NAME | import-python"

func cmdPeerManage(e *env, args []string) error {
	fs := newFlags("peers " + args[0])
	name := fs.String("name", "", "new peer's machine name")
	url := fs.String("url", "", "peer's HTTPS address")
	code := fs.String("code", "", "single-use invite code")
	own := fs.String("own-url", "", "this machine's HTTPS address (default: first trusted host)")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	switch args[0] {
	case "invite":
		if *name == "" || *url == "" || len(pos) != 0 {
			return usageErr(peerUsage)
		}
	case "join":
		if *code == "" || *url == "" || len(pos) != 0 {
			return usageErr(peerUsage)
		}
	case "list":
		if len(pos) != 0 {
			return usageErr(peerUsage)
		}
	case "remove":
		if len(pos) != 1 {
			return usageErr(peerUsage)
		}
	default:
		return usageErr(peerUsage)
	}
	if os.Getenv(fleet.EnvToken) != "" {
		return fmt.Errorf("only the owner can manage fleet peers")
	}
	return withOwner(e, func(c *client.Client, cx context.Context) error {
		switch args[0] {
		case "invite":
			var out struct {
				Code string `json:"code"`
			}
			if err := ownerDo(e, c, cx, http.MethodPost, "/api/peers/invite", config.Peer{Name: *name, URL: *url}, &out); err != nil {
				return err
			}
			fmt.Fprintln(e.out, out.Code)
			fmt.Fprintln(e.errw, "Single use; expires in 10 minutes. Give it only to the new node's owner.")
		case "join":
			var out struct {
				Machine string `json:"machine"`
			}
			if err := ownerDo(e, c, cx, http.MethodPost, "/api/peers/join", map[string]string{"code": *code, "url": *url, "own_url": *own}, &out); err != nil {
				return err
			}
			fmt.Fprintf(e.out, "Paired with %s. No restart needed.\n", out.Machine)
		case "list":
			var out struct {
				Peers []config.Peer `json:"peers"`
			}
			if err := c.Get(cx, "/api/peers", &out); err != nil {
				return err
			}
			for _, p := range out.Peers {
				fmt.Fprintf(e.out, "%s\t%s\n", p.Name, p.URL)
			}
		case "remove":
			if err := ownerDo(e, c, cx, http.MethodDelete, "/api/peers/"+client.Seg(pos[0]), nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(e.out, "Peer removed locally.")
		}
		return nil
	})
}
