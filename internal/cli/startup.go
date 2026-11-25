package cli

import (
	"os"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
)

func cmdStartup(e *env, args []string) error {
	if len(args) != 1 {
		return usageErr("usage: tmx startup install|status|remove")
	}
	switch args[0] {
	case "status":
		home, err := config.RegisteredLogonHome()
		if err == nil {
			e.printJSON(map[string]any{"installed": home != "", "home": home})
		}
		return err
	case "install":
		if os.Getenv(fleet.EnvToken) != "" {
			return usageErr("startup registration is an operator command")
		}
		home, err := config.DefaultHome()
		if err != nil {
			return err
		}
		st, err := os.Stat(home)
		if err != nil || !st.IsDir() {
			return usageErr("startup requires an existing Tiamatrix Go state folder")
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if err := config.InstallLogon(exe, home); err != nil {
			return err
		}
		e.printJSON(map[string]any{"installed": true, "home": home})
		return nil
	case "remove":
		if os.Getenv(fleet.EnvToken) != "" {
			return usageErr("startup registration is an operator command")
		}
		return config.RemoveLogon()
	default:
		return usageErr("usage: tmx startup install|status|remove")
	}
}
