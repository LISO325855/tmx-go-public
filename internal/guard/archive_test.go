package guard

import "testing"

// An archive tool's reads are free; what it writes is a write: unpacking into tmx's state
// folder, or making an archive there, is blocked like any write there, while archiving the
// state folder to somewhere else is only a read (review of build6, 18 Nov).
func TestArchiveWritesIntoTheStateFolderAreBlocked(t *testing.T) {
	const state = "/home/dev/.local/state/tmx-go"
	v2Run(t, withDefaults(v2Linux()), ShellPOSIX, []v2Case{
		// unpacking into the state folder, into a folder above it, or into one of its protected folders
		{"tar -xf /tmp/payload.tar -C " + state, Block, ""},
		{"tar -xf /tmp/payload.tar -C /home/dev/.local/state", Block, ""},
		{"tar xzf /tmp/payload.tgz -C " + state + "/rules", Block, ""},
		{"tar -x -f /tmp/payload.tar --directory=" + state + "/secrets", Block, ""},
		{"unzip /tmp/payload.zip -d " + state, Block, ""},
		{"7z x /tmp/payload.7z -o" + state, Block, ""},
		// the value attached to its option, at the end of a cluster, or in the old style
		{"tar -xf /tmp/payload.tar -C" + state, Block, ""},
		{"tar -xvf/tmp/payload.tar -C" + state + "/rules", Block, ""},
		{"tar -xC" + state + " -f /tmp/payload.tar", Block, ""},
		{"tar xvfC /tmp/payload.tar " + state, Block, ""},
		{"tar --extract --file=/tmp/payload.tar --directory " + state, Block, ""},
		{"tar -cf" + state + "/secrets/x.tar notes.txt", Block, ""},
		{"tar -cvf" + state + "/rules/x.tar notes.txt", Block, ""},
		{"tar cf " + state + "/secrets/x.tar notes.txt", Block, ""},
		{"tar --create --file=" + state + "/secrets/x.tar notes.txt", Block, ""},
		// the reviewer's two commands (18 Nov)
		{"tar -xf /tmp/payload.tar -C/home/dev/.local/state/tmx-go", Block, ""},
		{"tar -cf/home/dev/.local/state/tmx-go/config.json notes.txt", Block, ""},
		// options without a value never swallow the options after them (-U, GNU -s), and an
		// option with a value never takes a following option as that value
		{"tar -U -xf /tmp/payload.tar -C" + state, Block, ""},
		{"tar -s -xf /tmp/payload.tar -C " + state, Block, ""},
		{"tar -Uxf /tmp/payload.tar -C " + state, Block, ""},
		{"tar -b -xf /tmp/payload.tar -C " + state, Block, ""},
		{"tar -v -p -k -x -f /tmp/payload.tar -C " + state + "/secrets", Block, ""},
		{"tar -H posix -cf " + state + "/rules/x.tar notes.txt", Block, ""},
		// an archive made inside a protected folder
		{"tar -cf " + state + "/secrets/x.tar notes.txt", Block, ""},
		{"zip " + state + "/rules/x.zip notes.txt", Block, ""},
		// unpacking somewhere else
		{"tar -xf /tmp/payload.tar -C /tmp/out", Allow, ""},
		// nothing of tmx's on the line: free
		{"tar -tf /tmp/payload.tar", Allow, ""},
		{"tar -xf/tmp/payload.tar -C/tmp/out", Allow, ""},
		{"tar -b 20 -xf /tmp/payload.tar -C /tmp/out", Allow, ""},
		{"zip /tmp/s.zip notes.txt", Allow, ""},
		{"7z a /tmp/s.7z notes", Allow, ""},
	})
}

// The rule for archive tools: any word naming a path in tmx's state folder refuses the command,
// however it is attached and whatever the tool would do with it: also making an archive from
// the state folder and listing one there (18 Nov: no mode parsing, no read exceptions). So does
// running the tool inside the state folder. The state folder is read with ordinary tools.
func TestArchiveToolsNeverTouchTheStateFolder(t *testing.T) {
	const state = "/home/dev/.local/state/tmx-go"
	v2Run(t, withDefaults(v2Linux()), ShellPOSIX, []v2Case{
		// options the parser does not know, with the path attached
		{"tar -xf /tmp/payload.tar --one-top-level=" + state, Block, ""},
		{"tar -x --file /tmp/payload.tar --transform s,x,y, --directory=" + state + "/rules", Block, ""},
		{"bsdtar -xf /tmp/payload.tar -C" + state, Block, ""},
		{"gtar -U -xf /tmp/payload.tar -C " + state, Block, ""},
		// the reviewer's examples: an option's VALUE that looks like a mode
		{"tar --delete --exclude --list -f " + state + "/backup.tar config.json", Block, ""},
		{"tar --delete -f" + state + "/data config.json", Block, ""},
		// no mode tmx understands, or another sub-command
		{"tar -f /tmp/payload.tar " + state, Block, ""},
		{"7z rn /tmp/a.7z " + state + "/config.json x", Block, ""},
		{"7za e /tmp/payload.7z -o" + state, Block, ""},
		// making an archive from the state folder, and listing one there: refused too now
		{"tar czf /tmp/s.tgz " + state + "/secrets", Block, ""},
		{"tar -czf/tmp/s.tgz -C " + state + " secrets", Block, ""},
		{"zip /tmp/s.zip " + state + "/config.json", Block, ""},
		{"7z a /tmp/s.7z " + state, Block, ""},
		{"tar -tf " + state + "/backup.tar", Block, ""},
		{"tar --list --file=" + state + "/backup.tar", Block, ""},
		{"7z l " + state + "/x.7z", Block, ""},
		{"unzip -l " + state + "/backup.zip", Block, ""},
		{"tar -xf " + state + "/backup.tar -C /tmp/out", Block, ""},
		// ordinary reads of the state folder stay free
		{"cat " + state + "/config.json", Allow, ""},
		{"cp " + state + "/config.json /tmp/config.copy.json", Allow, ""},
		// nothing of tmx's on the line
		{"tar -xf /tmp/payload.tar --one-top-level=/tmp/out", Allow, ""},
		{"unzip /tmp/payload.zip -d /tmp/out", Allow, ""},
		{"unzip -l /tmp/payload.zip", Allow, ""},
	})
	// run inside the state folder: refused, whatever the words
	in := withDefaults(v2Linux())
	in.Cwd = state
	v2Run(t, in, ShellPOSIX, []v2Case{
		{"tar --delete -fdata config.json", Block, ""},
		{"tar -xf /tmp/payload.tar", Block, ""},
		{"unzip /tmp/payload.zip", Block, ""},
		{"zip x.zip notes.txt", Block, ""},
	})
}
