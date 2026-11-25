package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/p2p"
	"github.com/blackcode-ch/tmx-go/internal/store"
	"github.com/blackcode-ch/tmx-go/internal/team"
	"github.com/blackcode-ch/tmx-go/internal/teamstore"
)

// Team status (DESIGN.md "Team status"). Each computer in a group may tell the group's admin
// how it is doing: a chat.status envelope carrying a team.Report, signed by the computer like
// every envelope, sent every 15 minutes and at once when its state changes, and only after its
// owner opted in for that group (teamstore.TeamGroup.Share). The admin's tmx announces that it takes
// reports (chat.team, to every member, once per start and whenever it changes); a member offers
// the opt-in and reports only to the admin that announced it, so an older admin, which would
// drop the unknown type, is never sent one. Reports go straight to the admin (Node.Send), never
// through the outbox: only the latest matters, and an admin offline gets the next one when it
// is back (PeerEvents kicks the loop).
//
// The admin keeps the latest report of each computer and the state changes for 30 days. When
// it opens the team to everyone (TeamGroup.Open), its chat.team carries the members' latest
// signed chat.status envelopes; each member checks every one by its signature (as with the
// admin certificates), so the admin cannot alter what a member said about itself.

type statusBody struct {
	V      int             `json:"v"`
	Report json.RawMessage `json:"report"`
}

type teamBody struct {
	V        int  `json:"v"`
	Collects bool `json:"collects"` // the admin's tmx takes status reports for this group
	Open     bool `json:"open"`     // every member may see the team
	// Reports: the members' (and the admin's own) latest chat.status envelopes, as signed by
	// each computer; only while Open.
	Reports []p2p.Envelope `json:"reports,omitempty"`
}

// maxTeamBundle bounds a chat.team body (the p2p layer takes 48 KB).
const maxTeamBundle = 40 << 10

// TeamSource builds this computer's report: everything but the links and sent_at, which the
// chat layer fills per group (internal/api/team.go collects it).
type TeamSource func() team.Report

type teamSent struct {
	key string
	at  time.Time
}

type teamMem struct {
	mu        sync.Mutex
	source    TeamSource
	sent      map[string]teamSent // member: chat → the last report that reached the admin
	announced map[string]teamSent // admin: chat|peer → what that member last got (key = open + bundle digest)
	inflight  map[string]bool     // chat|peer: a send is running
	kick      chan struct{}
	pruned    time.Time
}

func newTeamMem() *teamMem {
	return &teamMem{sent: map[string]teamSent{}, announced: map[string]teamSent{}, inflight: map[string]bool{},
		kick: make(chan struct{}, 1)}
}

// SetTeamSource sets what builds this computer's report (nil = it sends none).
func (s *Service) SetTeamSource(src TeamSource) {
	s.team.mu.Lock()
	s.team.source = src
	s.team.mu.Unlock()
	s.teamKick()
}

func (s *Service) teamSource() TeamSource {
	s.team.mu.Lock()
	defer s.team.mu.Unlock()
	return s.team.source
}

// teamKick runs the loop's next pass now.
func (s *Service) teamKick() {
	select {
	case s.team.kick <- struct{}{}:
	default:
	}
}

func (s *Service) teamLoop() {
	t := time.NewTicker(s.o.TeamTick)
	defer t.Stop()
	for {
		s.teamPass()
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		case <-s.team.kick:
		}
	}
}

type teamJob struct {
	key  string // chat|peer
	peer string
	env  p2p.Envelope
	done func() // records that it went out
}

// teamPass sends what is due: our reports to the admins we share with, and announcements (with
// the bundle when open) to the members of the groups we run. It also prunes the admin's store.
func (s *Service) teamPass() {
	src := s.teamSource()
	var base team.Report
	if src != nil {
		base = src() // outside s.mu: it reads the fleet, WhatsApp, the accounts ...
	}
	now := time.Now()
	var jobs []teamJob
	s.mu.Lock()
	self := s.selfLocked()
	chats, _ := s.st.ListChats()
	for _, c := range chats {
		if c.Kind != model.ChatGroup || c.Status != model.ChatActive {
			continue
		}
		admin := s.adminLocked(c.ID)
		g, err := s.teamSt.TeamGroup(c.ID)
		if err != nil || admin == "" {
			continue
		}
		if admin == self {
			if src != nil { // a tmx that collects its own report takes the members' (the daemon always does)
				jobs = append(jobs, s.teamAnnounceLocked(c, g, base, now)...)
			}
			continue
		}
		if src == nil || g.Share != teamstore.ShareOn || g.Collector != admin || s.node.Reachable(admin) == p2p.Offline {
			continue
		}
		r := s.reportForLocked(c.ID, base, now)
		key := team.StateKey(r)
		jk := c.ID + "|" + admin
		s.team.mu.Lock()
		last, busy := s.team.sent[c.ID], s.team.inflight[jk]
		s.team.mu.Unlock()
		if busy || (last.key == key && now.Sub(last.at) < s.o.TeamEvery) {
			continue
		}
		env, err := s.statusEnvelopeLocked(c.ID, r)
		if err != nil {
			s.log.Error("chat: status report", "chat", c.ID, "err", err)
			continue
		}
		chatID := c.ID
		jobs = append(jobs, teamJob{key: jk, peer: admin, env: env, done: func() {
			s.team.mu.Lock()
			s.team.sent[chatID] = teamSent{key: key, at: now}
			s.team.mu.Unlock()
		}})
	}
	s.mu.Unlock()
	for _, j := range jobs {
		s.team.mu.Lock()
		s.team.inflight[j.key] = true
		s.team.mu.Unlock()
		s.goTracked(func() {
			ctx, cancel := context.WithTimeout(s.ctx, s.o.SendTimeout)
			err := s.node.Send(ctx, j.peer, j.env)
			cancel()
			s.team.mu.Lock()
			delete(s.team.inflight, j.key)
			s.team.mu.Unlock()
			if err != nil {
				if !errors.Is(err, p2p.ErrUnreachable) && s.ctx.Err() == nil {
					s.log.Info("chat: team status not sent", "peer", j.peer, "type", j.env.Type, "err", err)
				}
				return
			}
			j.done()
		})
	}
	if now.Sub(s.team.pruned) >= time.Hour {
		s.team.pruned = now
		if n, err := s.teamSt.PruneTeam(now.Add(-team.Keep).UnixMilli()); err != nil {
			s.log.Error("chat: prune team status", "err", err)
		} else if n > 0 {
			s.log.Info("chat: pruned team status older than 30 days", "rows", n)
		}
	}
}

// reportForLocked is our report for one group: base plus our links to its other members.
func (s *Service) reportForLocked(chatID string, base team.Report, now time.Time) team.Report {
	r := base
	r.Report, r.Contract, r.SentAt = team.ReportVersion, team.ContractVersion, now.UnixMilli()
	if r.Fleet.AIAccounts != nil {
		r.Fleet.AIAccounts = maps.Clone(r.Fleet.AIAccounts)
	}
	r.Links = []team.Link{}
	for _, p := range s.othersLocked(chatID) {
		st := team.LinkOffline
		switch s.node.Reachable(p) {
		case p2p.Direct:
			st = team.LinkDirect
		case p2p.Relayed:
			st = team.LinkRelayed
		}
		r.Links = append(r.Links, team.Link{Member: p, State: st})
	}
	return r
}

func (s *Service) statusEnvelopeLocked(chatID string, r team.Report) (p2p.Envelope, error) {
	rb, err := json.Marshal(r)
	if err != nil {
		return p2p.Envelope{}, err
	}
	return envelope(model.NewID(), TypeStatus, chatID, statusBody{V: WireVersion, Report: rb})
}

// teamAnnounceLocked: the announcements due for a group we run. A member gets one when it has
// not had this one yet (since this daemon started), and again every TeamEvery: one it could not
// take (still joining) is repeated, and an open team's times stay current.
func (s *Service) teamAnnounceLocked(c model.Chat, g teamstore.TeamGroup, base team.Report, now time.Time) []teamJob {
	body := teamBody{V: WireVersion, Collects: true, Open: g.Open}
	key := "closed"
	if g.Open {
		body.Reports = s.teamBundleLocked(c.ID, base, now)
		h := sha256.New()
		for _, e := range body.Reports {
			var sb statusBody
			if json.Unmarshal(e.Body, &sb) == nil {
				if r, err := team.Decode(sb.Report); err == nil {
					h.Write([]byte(e.From + "\x00" + team.StateKey(r) + "\x00"))
				}
			}
		}
		key = "open:" + hex.EncodeToString(h.Sum(nil))
	}
	var jobs []teamJob
	for _, p := range s.othersLocked(c.ID) {
		jk := c.ID + "|" + p
		s.team.mu.Lock()
		last, busy := s.team.announced[jk], s.team.inflight[jk]
		s.team.mu.Unlock()
		if busy || (last.key == key && now.Sub(last.at) < s.o.TeamEvery) {
			continue
		}
		if s.node.Reachable(p) == p2p.Offline {
			continue
		}
		env, err := envelope(model.NewID(), TypeTeam, c.ID, body)
		if err != nil {
			continue
		}
		jobs = append(jobs, teamJob{key: jk, peer: p, env: env, done: func() {
			s.team.mu.Lock()
			s.team.announced[jk] = teamSent{key: key, at: now}
			s.team.mu.Unlock()
		}})
	}
	return jobs
}

// teamBundleLocked: the latest signed reports of the group's current members, our own (signed
// now) first, within maxTeamBundle (the oldest are left out).
func (s *Service) teamBundleLocked(chatID string, base team.Report, now time.Time) []p2p.Envelope {
	var out []p2p.Envelope
	if env, err := s.statusEnvelopeLocked(chatID, s.reportForLocked(chatID, base, now)); err == nil {
		if signed, err := s.node.Sign(env); err == nil {
			out = append(out, signed)
		}
	}
	rows, _ := s.teamSt.TeamReports(chatID)
	slices.SortFunc(rows, func(a, b teamstore.TeamReport) int { return int(b.SignedAt - a.SignedAt) })
	size := 0
	for _, e := range out {
		size += len(e.Body)*4/3 + len(e.Sig)*4/3 + 200
	}
	for _, r := range rows {
		if r.Via != "" {
			continue
		}
		if _, ok := s.memberLocked(chatID, r.PeerID); !ok {
			continue
		}
		var env p2p.Envelope
		if json.Unmarshal([]byte(r.Envelope), &env) != nil {
			continue
		}
		n := len(env.Body)*4/3 + len(env.Sig)*4/3 + 200
		if size+n > maxTeamBundle {
			break
		}
		size += n
		out = append(out, env)
	}
	return out
}

// ---- receiving

// onStatus: a member's report, at the group's admin.
// errTeamOff answers every team entry point when this daemon keeps no team status
// (Options.TeamStore nil).
var errTeamOff = invalid("team status is off in this daemon")

func (s *Service) onStatus(env p2p.Envelope) error {
	if s.teamSt == nil {
		return errTeamOff
	}
	var b statusBody
	if err := decodeBody(env.Body, &b); err != nil {
		return err
	}
	r, err := team.Decode(b.Report)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, err := s.st.GetChat(env.ChatID)
	if err != nil || ch.Kind != model.ChatGroup || ch.Status != model.ChatActive {
		return errors.New("status report for an unknown or inactive group")
	}
	self := s.selfLocked()
	if !s.isAdminLocked(ch.ID, self) {
		return errors.New("status report, but we are not the group's admin")
	}
	if _, ok := s.memberLocked(ch.ID, env.From); !ok || env.From == self {
		return errors.New("status report from a peer that is not a member")
	}
	if err := s.node.VerifyEnvelope(env); err != nil {
		return errors.New("status report: " + err.Error())
	}
	return s.storeReportLocked(ch.ID, env, r, "")
}

// storeReportLocked keeps a verified report (sanitized: links to current members only) and,
// for one received from the computer itself, the state changes since its previous report.
func (s *Service) storeReportLocked(chatID string, env p2p.Envelope, r team.Report, via string) error {
	var others []string
	for _, m := range s.currentMembersLocked(chatID) {
		if m.PeerID != env.From {
			others = append(others, m.PeerID)
		}
	}
	r = team.Sanitize(r, others)
	rb, err := json.Marshal(r)
	if err != nil {
		return err
	}
	eb, err := json.Marshal(env)
	if err != nil {
		return err
	}
	prev, had, err := s.teamSt.PutTeamReport(teamstore.TeamReport{ChatID: chatID, PeerID: env.From, Report: string(rb), Envelope: string(eb),
		SignedAt: env.At, ReceivedAt: model.Now(), Via: via})
	if err != nil {
		return err
	}
	if via == "" && had {
		if old, err := team.Decode([]byte(prev.Report)); err == nil {
			var cs []teamstore.TeamChange
			for _, c := range team.Changes(old, r, env.At) {
				cs = append(cs, teamstore.TeamChange{ChatID: chatID, PeerID: env.From, At: c.At, Field: c.Field, Old: c.Old, New: c.New})
			}
			if err := s.teamSt.AddTeamChanges(cs); err != nil {
				s.log.Error("chat: team changes", "err", err)
			}
		}
	}
	s.publish(model.EvTeamUpdated, map[string]string{"chat_id": chatID})
	if via == "" {
		s.teamKick() // an open team passes it on
	}
	return nil
}

// onTeam: the admin's announcement, at a member.
func (s *Service) onTeam(env p2p.Envelope) error {
	if s.teamSt == nil {
		return errTeamOff
	}
	var b teamBody
	if err := decodeBody(env.Body, &b); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, err := s.st.GetChat(env.ChatID)
	if err != nil || ch.Kind != model.ChatGroup || (ch.Status != model.ChatActive && ch.Status != model.ChatPending) {
		return errors.New("team announcement for an unknown or inactive group")
	}
	if !s.isAdminLocked(ch.ID, env.From) {
		return errors.New("team announcement not from the group's admin")
	}
	g, err := s.teamSt.TeamGroup(ch.ID)
	if err != nil {
		return err
	}
	before := g
	g.Collector, g.Open = "", b.Open
	if b.Collects {
		g.Collector = env.From
	}
	if g != before {
		g.UpdatedAt = model.Now()
		if err := s.teamSt.PutTeamGroup(g); err != nil {
			return err
		}
	}
	// What the admin passes on replaces what it passed on before; closed = nothing of others.
	if err := s.teamSt.DeleteTeamReports(ch.ID, true); err != nil {
		return err
	}
	self := s.selfLocked()
	if b.Open && g.Share == teamstore.ShareOn {
		for i, e := range b.Reports {
			if i >= 2*MaxGroupMembers {
				break
			}
			if err := s.acceptPassedOnLocked(ch.ID, self, e); err != nil {
				s.log.Info("chat: dropped a passed-on status report", "chat", ch.ID, "from", e.From, "err", err)
			}
		}
	}
	s.publish(model.EvTeamUpdated, map[string]string{"chat_id": ch.ID})
	if g.Collector != before.Collector {
		s.teamKick() // we may report now
	}
	return nil
}

// acceptPassedOnLocked checks one report the admin passed on: a chat.status of this chat,
// signed by a current member (not us), the signature valid.
func (s *Service) acceptPassedOnLocked(chatID, self string, e p2p.Envelope) error {
	if e.Type != TypeStatus || e.ChatID != chatID {
		return errors.New("not a status report of this group")
	}
	if e.From == self {
		return nil // ours: shown live
	}
	if _, ok := s.memberLocked(chatID, e.From); !ok {
		return errors.New("not a member")
	}
	if err := s.node.VerifyEnvelope(e); err != nil {
		return err
	}
	var sb statusBody
	if err := decodeBody(e.Body, &sb); err != nil {
		return err
	}
	r, err := team.Decode(sb.Report)
	if err != nil {
		return err
	}
	return s.storeReportLocked(chatID, e, r, "admin")
}

// ---- the owner's side: settings and the Team page

// Share values on the API.
const (
	ShareUnasked = "unasked"
	ShareOn      = "on"
	ShareOff     = "off"
)

func shareWord(v int) string {
	switch v {
	case teamstore.ShareOn:
		return ShareOn
	case teamstore.ShareOff:
		return ShareOff
	}
	return ShareUnasked
}

// TeamSharing is one group in Settings → Team status: whether we share with its admin, or (a
// group we run) whether everyone sees the team.
type TeamSharing struct {
	ChatID    string `json:"chat_id"`
	Name      string `json:"name"`
	AdminID   string `json:"admin_id"`
	AdminName string `json:"admin_name"`
	Admin     bool   `json:"admin"`    // we run this group
	Collects  bool   `json:"collects"` // its admin's tmx takes reports (always true for ours)
	Share     string `json:"share"`    // unasked | on | off (member)
	Open      bool   `json:"open"`     // every member sees the team
}

// TeamGroupView is one group on the Team page.
type TeamGroupView struct {
	ChatID    string        `json:"chat_id"`
	Name      string        `json:"name"`
	AdminName string        `json:"admin_name"`
	Admin     bool          `json:"admin"`
	Open      bool          `json:"open"`
	Scope     string        `json:"scope"`   // all: every member's rows; own: only ours
	Reports   int           `json:"reports"` // computers that reported (besides us)
	People    []team.Person `json:"people"`
	Summary   team.Summary  `json:"summary"`
}

// TeamPage is GET /api/team.
type TeamPage struct {
	// Visible: the Team page is in the navigation (we run a group with reports, or we share ours).
	Visible  bool            `json:"visible"`
	Groups   []TeamGroupView `json:"groups"`
	Sharing  []TeamSharing   `json:"sharing"`
	Self     *team.NodeRow   `json:"self,omitempty"` // this computer's report and checks
	Contract int             `json:"contract"`
	Floor    string          `json:"floor"`
	Checks   int             `json:"checks"` // clauses in the contract (10)
}

// Team is the Team page: the groups we run (everyone), the groups we share with (our row, or
// everyone when the admin opened it) and what we could share.
func (s *Service) Team() (TeamPage, error) {
	if s.teamSt == nil {
		return TeamPage{}, errTeamOff
	}
	page := TeamPage{Groups: []TeamGroupView{}, Sharing: []TeamSharing{}, Contract: team.ContractVersion, Floor: team.Floor, Checks: 10}
	if !s.Enabled() {
		return page, ErrOff
	}
	src := s.teamSource()
	var base team.Report
	if src != nil {
		base = src()
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	self := s.selfLocked()
	chats, err := s.st.ListChats()
	if err != nil {
		return page, err
	}
	if src != nil {
		row := s.selfRowLocked("", base, now)
		page.Self = &row
	}
	for _, c := range chats {
		if c.Kind != model.ChatGroup || c.Status != model.ChatActive {
			continue
		}
		admin := s.adminLocked(c.ID)
		g, err := s.teamSt.TeamGroup(c.ID)
		if err != nil || admin == "" {
			continue
		}
		mine := admin == self
		sh := TeamSharing{ChatID: c.ID, Name: c.Name, AdminID: admin, AdminName: s.nameLocked(c.ID, admin), Admin: mine,
			Collects: mine || g.Collector == admin, Share: shareWord(g.Share), Open: g.Open}
		if sh.Collects {
			page.Sharing = append(page.Sharing, sh)
		}
		sharing := !mine && sh.Collects && g.Share == teamstore.ShareOn
		if !mine && !sharing {
			continue
		}
		v := TeamGroupView{ChatID: c.ID, Name: c.Name, AdminName: sh.AdminName, Admin: mine, Open: g.Open, Scope: "all"}
		if !mine && !g.Open {
			v.Scope = "own"
		}
		reports := map[string]team.Stored{}
		rows, _ := s.teamSt.TeamReports(c.ID)
		for _, r := range rows {
			if r.Via != "" && (mine || !g.Open) {
				continue // passed on by an admin before (we run it now, or it closed)
			}
			st, ok := s.storedLocked(c.ID, r, mine)
			if ok {
				reports[r.PeerID] = st
				if r.PeerID != self {
					v.Reports++
				}
			}
		}
		if src != nil {
			if st, ok := s.ownStoredLocked(c.ID, base, now); ok {
				reports[self] = st
			}
		}
		var members []team.Member
		for _, m := range s.currentMembersLocked(c.ID) {
			if v.Scope == "own" && m.PeerID != self {
				continue
			}
			members = append(members, team.Member{PeerID: m.PeerID, Name: s.nameLocked(c.ID, m.PeerID), Me: m.PeerID == self,
				Admin: m.Role == model.RoleAdmin})
		}
		v.People, v.Summary = team.BuildPeople(members, reports, now)
		page.Groups = append(page.Groups, v)
		if (mine && v.Reports > 0) || sharing {
			page.Visible = true
		}
	}
	return page, nil
}

// storedLocked turns a stored report into the checker's input (the signature checked again).
func (s *Service) storedLocked(chatID string, r teamstore.TeamReport, withChanges bool) (team.Stored, bool) {
	rep, err := team.Decode([]byte(r.Report))
	if err != nil {
		return team.Stored{}, false
	}
	var env p2p.Envelope
	if err := json.Unmarshal([]byte(r.Envelope), &env); err != nil {
		return team.Stored{}, false
	}
	st := team.Stored{Report: rep, SignedBy: env.From, SigOK: s.node.VerifyEnvelope(env) == nil, At: env.At}
	if withChanges {
		cs, _ := s.teamSt.TeamChanges(chatID, r.PeerID, 10)
		for _, c := range cs {
			st.Changes = append(st.Changes, team.Change{At: c.At, Field: c.Field, Old: c.Old, New: c.New})
		}
	}
	return st, true
}

// ownStoredLocked: our own report for a group, signed now (so C2 and C3 check what we send).
func (s *Service) ownStoredLocked(chatID string, base team.Report, now time.Time) (team.Stored, bool) {
	r := s.reportForLocked(chatID, base, now)
	env, err := s.statusEnvelopeLocked(chatID, r)
	if err != nil {
		return team.Stored{}, false
	}
	signed, err := s.node.Sign(env)
	if err != nil {
		return team.Stored{}, false
	}
	return team.Stored{Report: r, SignedBy: signed.From, SigOK: s.node.VerifyEnvelope(signed) == nil, At: signed.At}, true
}

// selfRowLocked is this computer's own row (no group: no links).
func (s *Service) selfRowLocked(chatID string, base team.Report, now time.Time) team.NodeRow {
	self := s.selfLocked()
	st, ok := s.ownStoredLocked(chatID, base, now)
	reports := map[string]team.Stored{}
	if ok {
		reports[self] = st
	}
	people, _ := team.BuildPeople([]team.Member{{PeerID: self, Name: s.myName(), Me: true}}, reports, now)
	return people[0].Nodes[0]
}

// TeamReport is this computer's report as it would go to a group's admin now (what is shared).
func (s *Service) TeamReport(chatID string) (team.Report, error) {
	if s.teamSt == nil {
		return team.Report{}, errTeamOff
	}
	if !s.Enabled() {
		return team.Report{}, ErrOff
	}
	src := s.teamSource()
	if src == nil {
		return team.Report{}, errf(fleet.KindConflict, "this tmx does not collect a status report")
	}
	base := src()
	s.mu.Lock()
	defer s.mu.Unlock()
	if chatID != "" {
		if _, err := s.teamGroupLocked(chatID); err != nil {
			return team.Report{}, err
		}
	}
	return s.reportForLocked(chatID, base, time.Now()), nil
}

func (s *Service) teamGroupLocked(chatID string) (model.Chat, error) {
	ch, err := s.st.GetChat(chatID)
	if errors.Is(err, store.ErrNotFound) || err == nil && ch.Kind != model.ChatGroup {
		return model.Chat{}, notFound("no group %q", chatID)
	}
	if err != nil {
		return model.Chat{}, err
	}
	if ch.Status != model.ChatActive {
		return model.Chat{}, errf(fleet.KindConflict, "this group is not active")
	}
	return ch, nil
}

// SetTeamShare turns sharing this computer's status with a group's admin on or off (the
// owner's one-tap opt-in, and Settings).
func (s *Service) SetTeamShare(chatID string, on bool) (TeamSharing, error) {
	if s.teamSt == nil {
		return TeamSharing{}, errTeamOff
	}
	if !s.Enabled() {
		return TeamSharing{}, ErrOff
	}
	s.mu.Lock()
	ch, err := s.teamGroupLocked(chatID)
	if err != nil {
		s.mu.Unlock()
		return TeamSharing{}, err
	}
	admin := s.adminLocked(ch.ID)
	if admin == s.selfLocked() {
		s.mu.Unlock()
		return TeamSharing{}, invalid("you run this group: your own status is on its Team page already")
	}
	g, err := s.teamSt.TeamGroup(ch.ID)
	if err != nil {
		s.mu.Unlock()
		return TeamSharing{}, err
	}
	if on && g.Collector != admin {
		s.mu.Unlock()
		return TeamSharing{}, errf(fleet.KindConflict, "%s's tmx does not take status reports yet", s.nameLocked(ch.ID, admin))
	}
	g.Share, g.UpdatedAt = teamstore.ShareOff, model.Now()
	if on {
		g.Share = teamstore.ShareOn
	} else {
		_ = s.teamSt.DeleteTeamReports(ch.ID, true) // nothing of others stays once we stop sharing
	}
	if err := s.teamSt.PutTeamGroup(g); err != nil {
		s.mu.Unlock()
		return TeamSharing{}, err
	}
	s.team.mu.Lock()
	delete(s.team.sent, ch.ID) // on: send at once
	s.team.mu.Unlock()
	out := TeamSharing{ChatID: ch.ID, Name: ch.Name, AdminID: admin, AdminName: s.nameLocked(ch.ID, admin), Collects: g.Collector == admin,
		Share: shareWord(g.Share), Open: g.Open}
	s.publish(model.EvTeamUpdated, map[string]string{"chat_id": ch.ID})
	s.mu.Unlock()
	s.teamKick()
	return out, nil
}

// SetTeamOpen (the group's admin) lets every member who shares see the whole team, or only
// their own row again.
func (s *Service) SetTeamOpen(chatID string, open bool) (TeamSharing, error) {
	if s.teamSt == nil {
		return TeamSharing{}, errTeamOff
	}
	if !s.Enabled() {
		return TeamSharing{}, ErrOff
	}
	s.mu.Lock()
	ch, err := s.teamGroupLocked(chatID)
	if err != nil {
		s.mu.Unlock()
		return TeamSharing{}, err
	}
	self := s.selfLocked()
	if s.adminLocked(ch.ID) != self {
		s.mu.Unlock()
		return TeamSharing{}, forbidden("only the group's admin decides who sees the team")
	}
	g, err := s.teamSt.TeamGroup(ch.ID)
	if err != nil {
		s.mu.Unlock()
		return TeamSharing{}, err
	}
	g.Open, g.UpdatedAt = open, model.Now()
	if err := s.teamSt.PutTeamGroup(g); err != nil {
		s.mu.Unlock()
		return TeamSharing{}, err
	}
	out := TeamSharing{ChatID: ch.ID, Name: ch.Name, AdminID: self, AdminName: s.nameLocked(ch.ID, self), Admin: true, Collects: true,
		Share: shareWord(g.Share), Open: open}
	s.publish(model.EvTeamUpdated, map[string]string{"chat_id": ch.ID})
	s.mu.Unlock()
	s.teamKick()
	return out, nil
}
