package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/fleet"
)

func TestConfigFleetLeadCommand(t *testing.T) {
	t.Setenv(fleet.EnvToken, "")
	lead := ""
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/config/fleet-lead" || r.Header.Get("Authorization") == "" {
			t.Error("wrong endpoint or missing owner credential")
		}
		if r.Method == "PUT" {
			var in map[string]string
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			lead = in["fleet_lead"]
		}
		json.NewEncoder(w).Encode(map[string]string{"fleet_lead": lead})
	}))
	defer stub.Close()
	useHome(t, t.TempDir(), urlPort(t, stub.URL))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"config", "fleet-lead", "head@hosta-go"}, "head@hosta-go"},
		{[]string{"config", "fleet-lead"}, "head@hosta-go"},
		{[]string{"config", "fleet-lead", "none"}, "none"},
	} {
		out, errs, code := tmx(t, tc.args...)
		if code != 0 || !strings.Contains(out, tc.want) {
			t.Fatalf("%v: %d %s %s", tc.args, code, out, errs)
		}
	}
	t.Setenv(fleet.EnvToken, "test-agent")
	if _, _, code := tmx(t, "config", "fleet-lead", "head@hosta-go"); code == 0 {
		t.Fatal("agent configured fleet lead")
	}
}
