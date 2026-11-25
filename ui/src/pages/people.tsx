import { useClassic } from "@/hooks/use-classic";
// People, Clients and Promises (#/people, #/clients, #/promises; docs/PEOPLE_PROMISES.md).
// Phone-first: one column, base text 18 px, every tap target at least 48 px high.
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { toast } from "sonner";
import { PageBody, PageHeader, PageTitle } from "@/components/page";
import { peopleApi, type PersonEdit } from "@/lib/people";
import {
  displayName,
  forClassicTab,
  forPage,
  PEOPLE_TABS,
  groupPromises,
  isoDay,
  keyFromName,
  parseWhen,
  validKey,
  whenLabel,
  type Person,
  type Promise_,
  type Reminder,
} from "@/lib/people-format";
import { href, paths } from "@/lib/router";
import { cn } from "@/lib/utils";

const TAP = "min-h-12 rounded-lg px-4 text-[18px] font-medium";
const FIELD = "min-h-12 w-full rounded-lg border bg-background px-3 text-[18px]";

function Btn({ children, onClick, kind = "plain", disabled, type = "button" }: {
  children: ReactNode; onClick?: () => void; kind?: "primary" | "plain" | "danger"; disabled?: boolean; type?: "button" | "submit";
}) {
  return (
    <button
      type={type}
      disabled={disabled}
      onClick={onClick}
      className={cn(TAP, "w-full disabled:opacity-50",
        kind === "primary" && "bg-brand text-white",
        kind === "plain" && "border bg-card",
        kind === "danger" && "border border-state-error text-state-error")}
    >
      {children}
    </button>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5 text-[18px]">
      <span className="text-muted-foreground">{label}</span>
      {children}
    </label>
  );
}

function fail(e: unknown) {
  toast.error(e instanceof Error ? e.message : String(e));
}

/** The pages' column: phone-first sizes; under classic the normal scale from md up. */
function bodyClass(classic: boolean): string {
  return cn("flex flex-col gap-4 pb-16 text-[18px] leading-relaxed", classic && "md:text-sm md:[&_div]:text-sm md:[&_label]:text-sm md:[&_button]:text-sm md:[&_input]:text-sm md:[&_select]:text-sm md:[&_textarea]:text-sm md:[&_h2]:text-base md:[&_h3]:text-sm md:[&_span]:text-sm md:[&_button]:min-h-[34px] md:[&_input]:min-h-[34px] md:[&_select]:min-h-[34px]");
}

function Shell({ title, children }: { title: string; children: ReactNode }) {
  const classic = useClassic();
  return (
    <>
      <PageHeader><PageTitle>{title}</PageTitle></PageHeader>
      <PageBody width={classic ? "max-w-[65rem]" : "max-w-xl"}>
        <div className={bodyClass(classic)}>{children}</div>
      </PageBody>
    </>
  );
}

/** People (team + external partners) or Clients. */
export function PeopleBookPage({ page }: { page: "people" | "clients" }) {
  const [all, setAll] = useState<Person[] | null>(null);
  const [open, setOpen] = useState<string | null>(null); // a key, or "new"
  const load = useCallback(() => peopleApi.list().then(setAll, fail), []);
  useEffect(() => void load(), [load]);
  const classic = useClassic();
  // Classic: one "People" entry with the tabs Team | Clients & partners, as in Python.
  const list = useMemo(() => (classic ? forClassicTab(all ?? [], page) : forPage(all ?? [], page)), [all, page, classic]);
  const title = classic ? "People" : page === "clients" ? "Clients" : "People";

  if (open) {
    const person = list.find((p) => p.key === open);
    return (
      <Shell title={person ? displayName(person) : page === "clients" ? "New client" : "New person"}>
        <PersonForm person={person} defaultKind={page === "clients" ? "client" : "team"}
          onDone={() => { setOpen(null); void load(); }} />
      </Shell>
    );
  }
  return (
    <Shell title={title}>
      {classic && (
        <nav className="flex gap-1.5" aria-label="People" data-people-tabs>
          {PEOPLE_TABS.map((t) => (
            <a
              key={t.page}
              href={href(t.page === "clients" ? paths.clients() : paths.people())}
              aria-current={t.page === page ? "page" : undefined}
              className={cn(
                "inline-flex h-8 items-center rounded-full border px-3.5 outline-none transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-ring",
                t.page === page ? "border-brand/50 bg-brand-soft font-medium text-foreground" : "text-muted-foreground hover:bg-muted/50 hover:text-foreground",
              )}
            >
              {t.label}
            </a>
          ))}
        </nav>
      )}
      <Btn kind="primary" onClick={() => setOpen("new")}>{page === "clients" ? "Add a client" : "Add a person"}</Btn>
      {all === null ? <p className="text-muted-foreground">Loading…</p> :
        list.length === 0 ? <p className="text-muted-foreground">No one yet.</p> :
        <ul className="flex flex-col gap-3">
          {list.map((p) => (
            <li key={p.key}>
              <button type="button" onClick={() => setOpen(p.key)}
                className="flex min-h-16 w-full flex-col items-start rounded-xl border bg-card px-4 py-3 text-left">
                <span className="text-[20px] font-semibold">{displayName(p)}</span>
                <span className="text-muted-foreground">
                  {[p.role, p.kind === "external" ? "partner" : null, p.preferred].filter(Boolean).join(" · ") || " "}
                </span>
              </button>
            </li>
          ))}
        </ul>}
    </Shell>
  );
}

function PersonForm({ person, defaultKind, onDone }: { person?: Person; defaultKind: string; onDone: () => void }) {
  const [name, setName] = useState(person?.name ?? "");
  const [key, setKey] = useState(person?.key ?? "");
  const [role, setRole] = useState(person?.role ?? "");
  const [kind, setKind] = useState<string>(person?.kind ?? defaultKind);
  const [lang, setLang] = useState(person?.lang ?? "");
  const [preferred, setPreferred] = useState(person?.preferred ?? "");
  const [phone, setPhone] = useState(person?.channels?.whatsapp ?? "");
  const [email, setEmail] = useState(person?.channels?.email ?? "");
  const [notes, setNotes] = useState(person?.notes ?? "");
  const [busy, setBusy] = useState(false);
  const theKey = person ? person.key : key || keyFromName(name);

  const save = async () => {
    if (!validKey(theKey)) return toast.error("Give a name (or a short key: letters, digits, - . _).");
    setBusy(true);
    const body: PersonEdit = { key: theKey, name, role, kind, lang, preferred, notes, channels: { whatsapp: phone, email } };
    try {
      await peopleApi.put(body);
      toast.success("Saved");
      onDone();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    if (!person) return;
    setBusy(true);
    try {
      await peopleApi.remove(person.key);
      onDone();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
    <form className="flex flex-col gap-4" onSubmit={(e) => { e.preventDefault(); void save(); }}>
      <Field label="Name"><input className={FIELD} value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" /></Field>
      {!person && (
        <Field label="Short key (how agents address them)">
          <input className={FIELD} value={key} placeholder={keyFromName(name)} onChange={(e) => setKey(e.target.value.toLowerCase())} autoCapitalize="none" />
        </Field>
      )}
      <Field label="Role"><input className={FIELD} value={role} onChange={(e) => setRole(e.target.value)} /></Field>
      <Field label="Kind">
        <select className={FIELD} value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="team">Team</option>
          <option value="external">Partner (another company)</option>
          <option value="client">Client</option>
        </select>
      </Field>
      <Field label="Language (en, fr, …)"><input className={FIELD} value={lang} onChange={(e) => setLang(e.target.value)} autoCapitalize="none" /></Field>
      <Field label="Preferred channel">
        <select className={FIELD} value={preferred} onChange={(e) => setPreferred(e.target.value)}>
          <option value="">—</option>
          <option value="whatsapp">WhatsApp</option>
          <option value="email">Email</option>
          <option value="other">Other</option>
        </select>
      </Field>
      <Field label="WhatsApp number"><input className={FIELD} value={phone} onChange={(e) => setPhone(e.target.value)} inputMode="tel" placeholder="+41 79 123 45 67" /></Field>
      <Field label="Email"><input className={FIELD} value={email} onChange={(e) => setEmail(e.target.value)} inputMode="email" autoCapitalize="none" /></Field>
      <Field label="Notes"><textarea className={cn(FIELD, "min-h-28 py-2")} value={notes} onChange={(e) => setNotes(e.target.value)} /></Field>
      <Btn kind="primary" type="submit" disabled={busy}>Save</Btn>
      <Btn onClick={onDone} disabled={busy}>Back</Btn>
    </form>
    {person && <PersonPromises person={person} />}
    {person && <Btn kind="danger" onClick={() => void remove()} disabled={busy}>Remove {displayName(person)}</Btn>}
    </>
  );
}

function PersonPromises({ person }: { person: Person }) {
  const classic = useClassic();
  const [list, setList] = useState<Promise_[] | null>(null);
  const load = useCallback(() => peopleApi.promises(person.key).then(setList, fail), [person.key]);
  useEffect(() => void load(), [load]);
  // Classic (the owner, 17 Nov 18:55; tmx-lead 19:02): promises are no longer made anywhere. A
  // person's existing ones stay listed, read-only; with none there is no section at all.
  if (classic) {
    if (!list?.length) return null;
    return (
      <section className="flex flex-col gap-3 border-t pt-4" data-person-promises>
        <h2 className="text-[20px] font-semibold">Old promises</h2>
        <PromiseList list={list} people={[person]} onChange={() => {}} readOnly />
      </section>
    );
  }
  return (
    <section className="flex flex-col gap-3 border-t pt-4">
      <h2 className="text-[20px] font-semibold">Promises</h2>
      <NewPromise people={[person]} onAdded={() => void load()} />
      <PromiseList list={list} people={[person]} onChange={() => void load()} />
    </section>
  );
}

function NewPromise({ people, onAdded }: { people: Person[]; onAdded: () => void }) {
  const [title, setTitle] = useState("");
  const [due, setDue] = useState("");
  const [who, setWho] = useState(people[0]?.key ?? "");
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (!who && people[0]) setWho(people[0].key); }, [people, who]);
  const add = async () => {
    if (!title.trim() || !who) return toast.error("A promise needs a person and a title.");
    setBusy(true);
    try {
      await peopleApi.addPromise({ person_key: who, title: title.trim(), due: due || undefined });
      setTitle("");
      setDue("");
      onAdded();
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="flex flex-col gap-3 rounded-xl border bg-card p-4">
      {people.length > 1 && (
        <Field label="For">
          <select className={FIELD} value={who} onChange={(e) => setWho(e.target.value)}>
            {people.map((p) => <option key={p.key} value={p.key}>{displayName(p)}</option>)}
          </select>
        </Field>
      )}
      <Field label="What was promised"><input className={FIELD} value={title} onChange={(e) => setTitle(e.target.value)} /></Field>
      <Field label="Due (optional)"><input className={FIELD} type="date" value={due} onChange={(e) => setDue(e.target.value)} /></Field>
      <Btn kind="primary" onClick={() => void add()} disabled={busy}>Add promise</Btn>
    </div>
  );
}

/** readOnly: the records as they are, without Done / Drop / Reopen (the classic "Old promises" page). */
function PromiseList({ list, people, onChange, readOnly }: { list: Promise_[] | null; people: Person[]; onChange: () => void; readOnly?: boolean }) {
  const today = isoDay(new Date());
  const names = useMemo(() => new Map(people.map((p) => [p.key, displayName(p)])), [people]);
  if (list === null) return <p className="text-muted-foreground">Loading…</p>;
  if (list.length === 0) return <p className="text-muted-foreground">No promises.</p>;
  const set = async (id: string, status: Promise_["status"]) => {
    try {
      await peopleApi.setPromise(id, { status });
      onChange();
    } catch (e) {
      fail(e);
    }
  };
  return (
    <>
      {groupPromises(list, today).map((g) => (
        <section key={g.bucket} className="flex flex-col gap-3">
          <h3 className={cn("text-[18px] font-semibold", g.bucket === "overdue" && "text-state-error")}>{g.label}</h3>
          <ul className="flex flex-col gap-3">
            {g.items.map((p) => (
              <li key={p.id} className="flex flex-col gap-3 rounded-xl border bg-card p-4">
                <div>
                  <div className={cn("text-[18px] font-medium", p.status !== "open" && "line-through opacity-70")}>{p.title}</div>
                  <div className="text-muted-foreground">
                    {[names.get(p.person_key) ?? p.person_key, p.due && `due ${p.due}`, p.owner && `owner ${p.owner}`].filter(Boolean).join(" · ")}
                  </div>
                </div>
                {readOnly ? (
                  p.status !== "open" && <div className="text-muted-foreground">{p.status}</div>
                ) : p.status === "open" ? (
                  <div className="grid grid-cols-2 gap-3">
                    <Btn kind="primary" onClick={() => void set(p.id, "done")}>Done</Btn>
                    <Btn onClick={() => void set(p.id, "dropped")}>Drop</Btn>
                  </div>
                ) : (
                  <Btn onClick={() => void set(p.id, "open")}>Reopen</Btn>
                )}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </>
  );
}

/** Promises across everyone, then the pending reminders. */
export function PromisesPage() {
  return (
    <Shell title="Promises">
      <PromisesContent />
    </Shell>
  );
}

/**
 * Classic (the owner, 17 Nov 18:55: "It's completely replaced by matrix… promises is basically
 * useless… It can be removed from that main menu on the side"): Promises is no longer a place.
 * The records are not deleted: this page keeps them readable until he decides. It is reached
 * from a small link at the bottom of the Matrix. Nothing can be added or changed here; a
 * reminder that is still pending can be cancelled, so that nothing fires out of sight.
 */
export function OldPromisesPage() {
  const [people, setPeople] = useState<Person[]>([]);
  const [list, setList] = useState<Promise_[] | null>(null);
  useEffect(() => {
    peopleApi.list().then(setPeople, fail);
    peopleApi.promises().then(setList, fail);
  }, []);
  return (
    <Shell title="Old promises">
      <p className="text-muted-foreground" data-old-promises>
        Promises are no longer kept here: the Matrix has taken their place. These are the ones recorded before, to read.{" "}
        <a className="text-brand hover:underline" href={href(paths.matrix())}>
          Back to the Matrix
        </a>
      </p>
      <PromiseList list={list} people={people} onChange={() => {}} readOnly />
      <Reminders readOnly />
    </Shell>
  );
}

function PromisesContent() {
  const [people, setPeople] = useState<Person[]>([]);
  const [list, setList] = useState<Promise_[] | null>(null);
  const load = useCallback(() => {
    peopleApi.list().then((p) => setPeople(forPage(p, "people").concat(forPage(p, "clients"))), fail);
    peopleApi.promises().then(setList, fail);
  }, []);
  useEffect(() => void load(), [load]);
  return (
    <>
      {people.length > 0 ? <NewPromise people={people} onAdded={load} /> :
        <p className="text-muted-foreground">Add someone under People or Clients first.</p>}
      <PromiseList list={list} people={people} onChange={load} />
      <Reminders />
    </>
  );
}

/** readOnly: only what is still pending, each with its Cancel; no form, and nothing at all when none is pending. */
function Reminders({ readOnly }: { readOnly?: boolean } = {}) {
  const classic = useClassic();
  const [list, setList] = useState<Reminder[] | null>(null);
  const [text, setText] = useState("");
  const [when, setWhen] = useState("");
  const load = useCallback(() => peopleApi.reminders("pending").then(setList, fail), []);
  useEffect(() => void load(), [load]);
  const add = async () => {
    const at = parseWhen(when, Date.now());
    if (!text.trim()) return toast.error("What should the reminder say?");
    if (at === null) return toast.error("When: 30m, 2h, 3d, or 2026-11-09 14:30 (in the future).");
    try {
      await peopleApi.addReminder({ at, text: text.trim() });
      setText("");
      setWhen("");
      void load();
    } catch (e) {
      fail(e);
    }
  };
  const cancel = async (id: string) => {
    try {
      await peopleApi.cancelReminder(id);
      void load();
    } catch (e) {
      fail(e);
    }
  };
  const now = Date.now();
  if (readOnly && !list?.length) return null;
  return (
    <section className="flex flex-col gap-3 border-t pt-4">
      <h2 className="text-[20px] font-semibold">Reminders</h2>
      <p className="text-muted-foreground">{classic ? "Sent to the Architect as a message at the time you choose, even if tmx restarts in between." : "Sent to the head as a message at the time you choose, even if tmx restarts in between."}</p>
      {!readOnly && <div className="flex flex-col gap-3 rounded-xl border bg-card p-4">
        <Field label="Remind about"><input className={FIELD} value={text} onChange={(e) => setText(e.target.value)} /></Field>
        <Field label="When (30m, 2h, 3d or 2026-11-09 14:30)"><input className={FIELD} value={when} onChange={(e) => setWhen(e.target.value)} autoCapitalize="none" /></Field>
        <Btn kind="primary" onClick={() => void add()}>Set reminder</Btn>
      </div>}
      {list === null ? <p className="text-muted-foreground">Loading…</p> :
        list.length === 0 ? <p className="text-muted-foreground">Nothing scheduled.</p> :
        <ul className="flex flex-col gap-3">
          {list.map((r) => (
            <li key={r.id} className="flex flex-col gap-3 rounded-xl border bg-card p-4">
              <div>
                <div className="text-[18px] whitespace-pre-line">{r.text}</div>
                <div className="text-muted-foreground">
                  {new Date(r.fire_at).toLocaleString()} · {whenLabel(r.fire_at, now)}
                  {r.attempts > 0 && r.last_error ? " · retrying" : ""}
                </div>
              </div>
              <Btn onClick={() => void cancel(r.id)}>Cancel</Btn>
            </li>
          ))}
        </ul>}
    </section>
  );
}
