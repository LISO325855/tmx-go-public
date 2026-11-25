package fleet

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// A project's repository (the owner, 17 Nov 2026: "a GitHub repo per project… agents should find
// the repo, pull etc. when they join a project"). It is one address kept on the project's
// shared page (ProjectSection.Repo, so no schema change). Only the owner sets it: it tells every
// agent of the project where to take code from, which an agent must not be able to redirect.
// Agents read it with their project tools (the section) and are told it when they start
// (systemPromptLocked). The daemon itself never clones, fetches or calls a forge: v1 is the
// address only.

// maxRepoLen bounds the stored address, as the section's other texts are bounded.
const maxRepoLen = 300

// scpRepoRx is the scp-like form git accepts: git@github.com:owner/repo(.git).
var scpRepoRx = regexp.MustCompile(`^git@([A-Za-z0-9.-]+):([A-Za-z0-9._~/-]+)$`)

// NormalizeProjectRepo checks and tidies a repository address. Accepted: https://host/path,
// ssh://git@host/path and git@host:path. Refused: anything carrying credentials (a user or
// password in an https address, a token in the path's query), other schemes (http, file, git,
// a local path), spaces and control characters. "" clears the field.
func NormalizeProjectRepo(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if len(s) > maxRepoLen {
		return "", errf(KindInvalid, "the repository address is too long (at most %d characters)", maxRepoLen)
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", errf(KindInvalid, "the repository address must not contain spaces or control characters")
		}
	}
	if m := scpRepoRx.FindStringSubmatch(s); m != nil {
		if !validRepoHost(m[1]) || !validRepoPath(m[2]) {
			return "", errf(KindInvalid, "the repository address must look like git@github.com:owner/repo")
		}
		return s, nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", errf(KindInvalid, "the repository address must be https://host/owner/repo or git@host:owner/repo")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errf(KindInvalid, "the repository address must not carry a query or a fragment (no tokens in it)")
	}
	switch u.Scheme {
	case "https":
		if u.User != nil {
			return "", errf(KindInvalid, "the repository address must not contain a user name, password or token")
		}
	case "ssh":
		if u.User == nil || u.User.Username() != "git" {
			return "", errf(KindInvalid, "an ssh repository address must be ssh://git@host/owner/repo")
		}
		if _, has := u.User.Password(); has {
			return "", errf(KindInvalid, "the repository address must not contain a password")
		}
	default:
		return "", errf(KindInvalid, "the repository address must be https://host/owner/repo or git@host:owner/repo")
	}
	if !validRepoHost(u.Hostname()) || !validRepoPath(strings.TrimPrefix(u.Path, "/")) {
		return "", errf(KindInvalid, "the repository address must name a host and a repository, like https://github.com/owner/repo")
	}
	return s, nil
}

func validRepoHost(h string) bool {
	return h != "" && strings.Contains(h, ".") && !strings.HasPrefix(h, ".") && !strings.HasSuffix(h, ".") && !strings.HasPrefix(h, "-")
}

// validRepoPath wants at least owner/repo, with no empty, "." or ".." part.
func validRepoPath(p string) bool {
	parts := strings.Split(strings.TrimSuffix(p, "/"), "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// projectRepoLocked is the repository of a project, or "" (none set, no project, or the page
// cannot be read: an agent then simply is not told one).
func (f *Fleet) projectRepoLocked(projectID string) string {
	p := f.projects[projectID]
	if p == nil {
		return ""
	}
	sec, err := f.sectionLocked(p)
	if err != nil {
		return ""
	}
	return sec.Repo
}

// repoNote is what an agent of a project with a repository is told when it starts.
func repoNote(repo string) string {
	return "This project's repository is " + repo + " (set by the owner). " +
		"Before you start, make sure your folder has it: clone it there if it is not there yet, otherwise fetch and pull. " +
		"Work on a branch of your own when you change code, and never push to its main branch unless your lead or the owner told you to."
}

// repoChangedMail is the one short message a project's lead gets when the owner sets, changes
// or removes the project's repository: agents that are already running were told nothing at
// their start, and the lead is the one who can pass it on.
func repoChangedMail(project, repo string) string {
	if repo == "" {
		return "[tmx] The owner removed the repository of project " + project + ". Agents started from now on are no longer told one."
	}
	return "[tmx] The owner set the repository of project " + project + " to " + repo + ". " +
		"Agents started from now on are told it; the ones already running are not, so tell your workers. " +
		"Make sure your folder has it (clone it, or fetch and pull) and work on a branch of your own."
}

// notifyRepoChanged mails the project's lead about a new repository address (tmx-lead, 17 Nov
// 2026). It runs after SetProjectSection has returned, never under the fleet's lock. Only a
// local lead that is not a shell is told; with none, nobody is (the address is on the project's
// page for whoever comes next). A failure is logged: the address itself is already saved.
func (f *Fleet) notifyRepoChanged(projectID, repo string) {
	f.mu.Lock()
	var lead, project string
	if p := f.projects[projectID]; p != nil {
		project = p.Name
		if r := f.agents[p.LeadID]; r != nil && r.a.Harness != model.HarnessShell {
			lead = r.a.ID
		}
	}
	f.mu.Unlock()
	if lead == "" {
		return
	}
	if _, err := f.SendMessage(OwnerCaller, SendRequest{To: lead, Body: repoChangedMail(project, repo)}); err != nil {
		f.log.Warn("project repository: tell the lead", "project", projectID, "err", err)
	}
}
