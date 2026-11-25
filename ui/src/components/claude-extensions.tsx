// Profile editor: the Claude Code MCP servers a profile adds, and installing more through
// Claude Code itself (internal/claudeext, DESIGN.md "Profiles").
import { useCallback, useEffect, useState } from "react";
import { AlertTriangle, Loader2, Plus, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ToggleChip } from "@/components/profile-fields";
import { api } from "@/lib/api";
import { missingServers, parseServerConfig, serverSource, toggleServer, validServerName } from "@/lib/claude-extensions";
import type { ClaudeExtensions } from "@/lib/types";
import { Row } from "@/pages/settings";

export function useClaudeExtensions() {
  const [ext, setExt] = useState<ClaudeExtensions | null>(null);
  const [loading, setLoading] = useState(false);
  const load = useCallback(async (fresh = false) => {
    setLoading(true);
    try {
      setExt(await api.claudeExtensions(fresh));
    } catch (e) {
      setExt({ available: false, error: (e as Error).message, servers: [], plugins: [] });
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => void load(), [load]);
  return { ext, setExt, loading, load };
}

export function ClaudeExtensionRows({ servers, onServers }: { servers: string[]; onServers: (v: string[]) => void }) {
  const { ext, setExt, loading, load } = useClaudeExtensions();
  const [plugin, setPlugin] = useState("");
  const [adding, setAdding] = useState(false);
  const [srvName, setSrvName] = useState("");
  const [srvJSON, setSrvJSON] = useState("");
  const [busy, setBusy] = useState(false);

  const known = ext?.servers ?? [];
  const missing = missingServers(servers, ext);
  const toggle = (id: string) => onServers(toggleServer(servers, id));

  async function install() {
    const p = plugin.trim();
    if (!p) return;
    setBusy(true);
    try {
      const next = await api.installClaudePlugin(p);
      if (next) setExt(next);
      setPlugin("");
      toast.success(`Installed ${p} in Claude Code`);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function addServer() {
    const name = srvName.trim();
    if (!validServerName(name)) {
      toast.error("The name takes letters, digits, - and _, and cannot be tmx's own");
      return;
    }
    const parsed = parseServerConfig(srvJSON);
    if ("error" in parsed) {
      toast.error(parsed.error);
      return;
    }
    setBusy(true);
    try {
      const next = await api.addClaudeServer(name, parsed.config);
      if (next) setExt(next);
      onServers([...servers.filter((x) => x !== name), name]);
      setSrvName("");
      setSrvJSON("");
      setAdding(false);
      toast.success(`Added ${name} to Claude Code`);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Row label="MCP servers" hint="From Claude Code (claude mcp and plugins). Added next to the tmx tools when an agent starts. Unsupported entries are shown on the agent and profile.">
        {!ext ? (
          <p className="flex items-center gap-2 pt-1.5 text-sm text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" /> Asking Claude Code…
          </p>
        ) : (
          <div className="space-y-2">
            {!ext.available && <p className="pt-1.5 text-sm text-muted-foreground">{ext.error || "Claude Code is not available."}</p>}
            {ext.available && ext.error && (
              <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
                <AlertTriangle className="mt-px size-3.5 shrink-0 text-state-waiting" /> {ext.error}
              </p>
            )}
            {ext.available && known.length === 0 && missing.length === 0 && (
              <p className="pt-1.5 text-sm text-muted-foreground">Claude Code has no MCP servers yet. Add one below.</p>
            )}
            <div className="flex flex-wrap gap-1.5">
              {known.map((s) => (
                <ToggleChip key={s.id} pressed={servers.includes(s.id)} onToggle={() => toggle(s.id)} title={`${s.id} · ${s.transport}: ${s.detail}`}>
                  {s.name}
                  <span className="text-2xs text-subtle-foreground">{serverSource(s)}</span>
                </ToggleChip>
              ))}
              {missing.map((id) => (
                <ToggleChip key={id} pressed onToggle={() => toggle(id)} title="Claude Code doesn't have this server (any more): agents start without it">
                  <AlertTriangle className="size-3 text-state-waiting" />
                  {id}
                </ToggleChip>
              ))}
            </div>
            <Button variant="ghost" size="sm" className="h-7 px-2 text-xs text-muted-foreground" disabled={loading} onClick={() => void load(true)}>
              <RefreshCw className={loading ? "animate-spin" : undefined} /> Refresh
            </Button>
          </div>
        )}
      </Row>
      <Row label="Install" hint="Runs Claude Code's own install, for you and every profile. Asks you to confirm first.">
        <div className="space-y-2">
          <div className="flex gap-2">
            <Input
              value={plugin}
              onChange={(e) => setPlugin(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && void install()}
              placeholder="plugin@marketplace, e.g. playwright@claude-plugins-official"
              className="font-mono text-[0.7812rem] sm:w-96"
              spellCheck={false}
            />
            <Button variant="outline" size="sm" className="h-9" disabled={busy || !plugin.trim()} onClick={() => void install()}>
              {busy && !adding ? <Loader2 className="animate-spin" /> : null}
              Install plugin
            </Button>
          </div>
          {!adding ? (
            <Button variant="ghost" size="sm" className="h-7 px-2 text-xs text-muted-foreground" onClick={() => setAdding(true)}>
              <Plus /> Add an MCP server by its config
            </Button>
          ) : (
            <div className="space-y-2 rounded-lg border p-3">
              <Input value={srvName} onChange={(e) => setSrvName(e.target.value)} placeholder="name" className="font-mono text-[0.7812rem] sm:w-60" spellCheck={false} />
              <Textarea
                value={srvJSON}
                onChange={(e) => setSrvJSON(e.target.value)}
                placeholder={'{"command": "npx", "args": ["some-mcp-server"]}  or  {"type": "http", "url": "https://…"}'}
                className="min-h-16 font-mono text-[0.75rem]"
                spellCheck={false}
              />
              <p className="text-xs leading-5 text-muted-foreground">
                No keys or passwords in the config: name a variable (<code className="font-mono">{"${NAME}"}</code>) instead of its value.{" "}
                <code className="font-mono">{"${TMXG_AGENT_NAME}"}</code> and <code className="font-mono">{"${TMXG_AGENT_ID}"}</code> become each agent's own
                name and id.
              </p>
              <div className="flex gap-2">
                <Button size="sm" disabled={busy || !srvName.trim() || !srvJSON.trim()} onClick={() => void addServer()}>
                  {busy && <Loader2 className="animate-spin" />} Add to Claude Code
                </Button>
                <Button variant="ghost" size="sm" onClick={() => setAdding(false)}>
                  Cancel
                </Button>
              </div>
            </div>
          )}
        </div>
      </Row>
    </>
  );
}
