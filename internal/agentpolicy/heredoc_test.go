package agentpolicy

import (
	"strings"
	"testing"
)

// An inline script that only edits files is not a browser launch, whatever words its lines
// begin with; one that drives a browser still is; a body fed to a shell is still commands.
func TestHereDocumentsAreJudgedAsWhatTheyAre(t *testing.T) {
	doc := func(head string, body ...string) string {
		return head + " <<'EOF'\n" + strings.Join(body, "\n") + "\nEOF"
	}
	notALaunch := []string{
		// The reported false hold: a text-replace script whose lines begin with such words.
		doc("python -",
			"p='internal/guard/guard.go'",
			"s=open(p).read()",
			"browserRule = 'RuleBrowser'",
			"cdp_note = s.count('x')",
			"s=s.replace('chrome', 'chromium')",
			"open(p,'w').write(s)"),
		doc("cd /d/repo && python3 -I -", "firefox_seen = False", "print(firefox_seen)"),
		doc("cat > notes.md", "chromium is the bundled browser", "playwright installs it"),
		doc("git commit -F -", "browser: fix the launch rule", "", "chrome was matched by mistake"),
		doc("env PYTHONUTF8=1 python -", "browser = 1"),
		"python - <<-EOF\n\tbrowser = 1\n\tEOF",
	}
	for _, c := range notALaunch {
		if got := ClassifyBrowserCommand("windows", false, c); got == BrowserCommand {
			t.Errorf("taken for a browser launch:\n%s", c)
		}
	}
	// It still mentions a browser: the mention rule keeps seeing it.
	if got := ClassifyBrowserCommand("windows", false, notALaunch[0]); got != BrowserMention {
		t.Errorf("the mention was lost: %v", got)
	}
	// No browser word at all: nothing.
	if got := ClassifyBrowserCommand("windows", false, doc("python -", "p='a.go'", "open(p).read()")); got != BrowserOK {
		t.Errorf("a plain script: %v", got)
	}

	launches := []string{
		doc("python -", "from playwright.sync_api import sync_playwright", "sync_playwright().start()"),
		doc("python -", "import selenium", "from selenium import webdriver", "webdriver.Chrome()"),
		doc("python -", "import subprocess", `subprocess.run([r"C:\Program Files\Google\Chrome\Application\chrome.exe"])`),
		doc("python -", "import webbrowser", "webbrowser.open('https://example.test')"),
		doc("node -", "const puppeteer = require('puppeteer')", "puppeteer.launch()"),
		doc("node --input-type=module -", "import { chromium } from 'playwright'", "await chromium.launch()"),
		doc("python -", "args = ['--remote-debugging-port=9222']"),
		// A body fed to a shell is commands, line by line.
		doc("bash", "cd /tmp", "chrome.exe --headless"),
		doc("sh -s", "msedge https://example.test"),
		// An unknown receiver: left as commands (the careful reading).
		doc("some-runner", "chromium --version"),
		// A launch chained after the here-document's closing word is still seen.
		doc("python -", "x = 1") + "\nchrome.exe https://example.test",
		// Never closed: nothing is taken out.
		"python - <<'EOF'\nchrome.exe",
		// A here-string is not a here-document.
		"node -e 0 <<< x; chrome.exe",
	}
	for _, c := range launches {
		if got := ClassifyBrowserCommand("windows", false, c); got != BrowserCommand {
			t.Errorf("not seen as a browser command (%v):\n%s", got, c)
		}
	}
}

func TestSplitHereDocs(t *testing.T) {
	rest, docs := splitHereDocs("cd x && python - <<'PY'\na = 1\nb = 2\nPY\necho done")
	if rest != "cd x && python - <<'PY'\necho done" || len(docs) != 1 || docs[0].verb != "python" || docs[0].body != "a = 1\nb = 2" {
		t.Fatalf("rest %q docs %+v", rest, docs)
	}
	rest, docs = splitHereDocs("bash <<EOF\nls\nEOF")
	if rest != "bash <<EOF\nls\nEOF" || len(docs) != 0 {
		t.Fatalf("a shell's body was taken out: %q %+v", rest, docs)
	}
	if rest, docs = splitHereDocs("echo a << b"); rest != "echo a << b" || len(docs) != 0 {
		t.Fatalf("an unclosed one: %q %+v", rest, docs)
	}
}
