package agentpolicy

import (
	"regexp"
	"strings"
)

// Here-documents. The text between `<<WORD` and the closing WORD is the standard input of one
// command, not a list of commands. Read as commands, every line of an inline script was put in
// command position, so a Python script that only edits a file was taken for a browser launch
// as soon as one of its lines began with a word such as `browserRule = ...` (a false hold as
// "Scripted browser automation"). So the body is taken out of the command line before the
// segments are checked, and judged for what it is:
//   - fed to a shell (bash, sh, cmd, powershell, ...) or to a command this file does not know:
//     it stays in the line and its lines are checked as commands, as before;
//   - fed to an interpreter or a plain data command (python, node, cat, ...): it counts as a
//     browser command only when the script itself drives a browser (scriptDrivesBrowser).
// A mention of a browser anywhere, body included, is still a mention (the guard's own rule).

// hereDoc is one here-document: the command it is fed to and its text.
type hereDoc struct {
	verb string // the receiving command's name ("" when it could not be read)
	body string
}

var hereDocStart = regexp.MustCompile(`<<(-?)\s*(['"]?)([A-Za-z_][A-Za-z0-9_]*)(['"]?)`)

// hereDocData are commands whose standard input is data or a script in another language, never
// shell commands. Anything else keeps its body in command position.
var hereDocData = map[string]bool{
	"python": true, "python3": true, "py": true, "node": true, "deno": true, "bun": true,
	"ruby": true, "perl": true, "php": true, "cat": true, "tee": true, "sed": true, "awk": true,
	"grep": true, "sort": true, "wc": true, "tr": true, "jq": true, "git": true, "patch": true,
	"sqlite3": true, "psql": true, "mysql": true,
}

// splitHereDocs returns the command line without the bodies of here-documents that are data,
// and those bodies. Bodies fed to a shell or an unknown command are left in place.
func splitHereDocs(command string) (rest string, docs []hereDoc) {
	if !strings.Contains(command, "<<") {
		return command, nil
	}
	lines := strings.Split(command, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		m := hereDocStart.FindStringSubmatchIndex(line)
		if m == nil || (m[0] > 0 && line[m[0]-1] == '<') || strings.HasPrefix(line[m[1]:], "<") {
			out = append(out, line) // no here-document (or a <<< here-string)
			continue
		}
		stripTabs, word := line[m[2]:m[3]] == "-", line[m[6]:m[7]]
		end := -1
		for j := i + 1; j < len(lines); j++ {
			l := strings.TrimRight(lines[j], "\r")
			if stripTabs {
				l = strings.TrimLeft(l, "\t")
			}
			if l == word {
				end = j
				break
			}
		}
		verb := hereDocVerb(line[:m[0]])
		if end < 0 || !hereDocData[verb] {
			out = append(out, line) // never closed, or its lines are commands: leave it all
			continue
		}
		docs = append(docs, hereDoc{verb: verb, body: strings.Join(lines[i+1:end], "\n")})
		out = append(out, line)
		i = end // the closing word is dropped with the body
	}
	return strings.Join(out, "\n"), docs
}

// hereDocVerb is the command a here-document on this line is fed to: the last chain segment
// before the `<<`, unwrapped (env, timeout, cmd /c, ...).
func hereDocVerb(before string) string {
	segs := commandSegments(before)
	words, blocked := unwrapSegment(commandWords(segs[len(segs)-1]))
	if blocked || len(words) == 0 {
		return ""
	}
	return verbBasename(words[0])
}

// scriptDrivesBrowser: an inline script that loads a browser automation library, opens a
// browser's debugging port or starts a browser binary. Editing text that merely contains such
// words is not driving a browser, so the patterns are the code forms, not the words.
var scriptDrivesBrowser = regexp.MustCompile(`(?im)` +
	`^\s*(from|import)\s+(playwright|selenium|pyppeteer|undetected_chromedriver|nodriver|splinter|helium|DrissionPage|webbrowser)\b` +
	`|\brequire\(\s*['"](playwright|playwright-core|puppeteer|puppeteer-core|selenium-webdriver|chrome-remote-interface|webdriverio)\b` +
	`|\bfrom\s+['"](playwright|playwright-core|puppeteer|puppeteer-core|selenium-webdriver|chrome-remote-interface|webdriverio)\b` +
	`|\b(sync_playwright|async_playwright|connect_over_cdp|connectOverCDP)\b` +
	`|\b(chromium|firefox|webkit|puppeteer)\.(launch|connect)\w*\(` +
	`|\bwebdriver\.(Chrome|Firefox|Edge|Remote)\b` +
	`|remote-debugging-(port|pipe)` +
	`|\b(chrome|chromium|msedge|firefox)\.exe\b`)
