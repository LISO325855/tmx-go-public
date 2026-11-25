// The phone's bottom bar in the classic profile, as in the Python dashboard's mobile app: Home,
// Projects, Agents and Your turn, always within reach of a thumb; it replaces the top bar and the
// menu. Python's sizes: 76 px tall, 12 px labels, 21 px icons, the count on Your turn only.
import { Bot, FolderClosed, House, Inbox } from "lucide-react";
import { usePhoneClassic } from "@/hooks/use-phone-classic";
import { PHONE_TABS, phoneTabOf, tabCount, type PhoneTab } from "@/lib/phone";
import { href, useRoute } from "@/lib/router";
import { pendingApprovals, useStore } from "@/lib/store";

const ICON: Record<PhoneTab, React.ComponentType<{ className?: string }>> = { home: House, projects: FolderClosed, agents: Bot, turn: Inbox };

export function PhoneBottomBar() {
  const phone = usePhoneClassic();
  const route = useRoute();
  const pending = pendingApprovals(useStore((s) => s.approvals)).length;
  if (!phone) return null;
  const current = phoneTabOf(route.name);
  return (
    <nav
      aria-label="Main navigation"
      data-phone-bottom-bar
      className="flex shrink-0 items-stretch border-t bg-card px-[10px] pt-[7px] pb-[max(9px,env(safe-area-inset-bottom))]"
    >
      {PHONE_TABS.map((t) => {
        const Icon = ICON[t.id];
        const active = current === t.id;
        const count = t.id === "turn" ? tabCount(pending) : "";
        return (
          <a
            key={t.id}
            href={href(t.path)}
            aria-current={active ? "page" : undefined}
            aria-label={count ? `${t.label}, ${pending} waiting` : undefined}
            className={
              "relative flex h-[59px] min-w-0 flex-1 flex-col items-center justify-center gap-1 text-[12px] leading-none outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset " +
              (active ? "font-bold text-brand" : "text-muted-foreground")
            }
          >
            {active && <span aria-hidden className="absolute top-0 h-[3px] w-[34px] rounded-full bg-brand" />}
            <span className="relative">
              <Icon className="size-[21px]" />
              {count && (
                <span className="tabular absolute -top-2 left-3.5 inline-flex h-5 min-w-5 items-center justify-center rounded-full border-2 border-card bg-state-waiting px-1 text-[12px] leading-none font-semibold text-black/80">
                  {count}
                </span>
              )}
            </span>
            <span className="max-w-full truncate">{t.label}</span>
          </a>
        );
      })}
    </nav>
  );
}
