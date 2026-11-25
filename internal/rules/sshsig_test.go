package rules

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

const rawCommit = "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\n" +
	"author rules-editor <editor@example.invalid> 1767261600 +0000\n" +
	"committer rules-editor <editor@example.invalid> 1767261600 +0000\n" +
	"\n" +
	"first rules\n\ngpgsig in the message is text, not a header\n"

func testKey(t *testing.T, id string) (ed25519.PrivateKey, config.RulesSigner) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, config.RulesSigner{ID: id, Key: base64.StdEncoding.EncodeToString(pub)}
}

func TestVerifyCommit(t *testing.T) {
	key, pinned := testKey(t, "head")
	_, stranger := testKey(t, "stranger")
	signers, err := ParseSigners([]config.RulesSigner{pinned})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCommit([]byte(rawCommit), signers); err != errUnsigned {
		t.Fatalf("an unsigned commit: %v", err)
	}
	signed, err := SignCommit([]byte(rawCommit), key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(signed, []byte("\ngpgsig -----BEGIN SSH SIGNATURE-----\n ")) {
		t.Fatalf("the signature is not a gpgsig header:\n%s", signed)
	}
	payload, sig, err := splitCommit(signed)
	if err != nil || string(payload) != rawCommit || sig == nil {
		t.Fatalf("splitCommit: %v\n%s", err, payload)
	}
	if id, err := verifyCommit(signed, signers); err != nil || id != "head" {
		t.Fatalf("a signed commit: %q %v", id, err)
	}
	// The same signature on other content does not pass.
	tampered := bytes.Replace(signed, []byte("first rules"), []byte("other rules"), 1)
	if _, err := verifyCommit(tampered, signers); err == nil {
		t.Fatal("a changed commit passed with the old signature")
	}
	// A key that is not pinned does not count, however valid its signature.
	others, _ := ParseSigners([]config.RulesSigner{stranger})
	if _, err := verifyCommit(signed, others); err != errNotPinned {
		t.Fatalf("a commit signed by a key that is not pinned: %v", err)
	}
	if _, err := verifyCommit(signed, nil); err != errNotPinned {
		t.Fatalf("no pinned keys: %v", err)
	}
	// Two signature headers, or something that is not an SSH signature.
	double := bytes.Replace(signed, []byte("\n\nfirst"), []byte("\ngpgsig x\n\nfirst"), 1)
	if _, err := verifyCommit(double, signers); err != errBadSig {
		t.Fatalf("two signatures: %v", err)
	}
	pgp := strings.Replace(rawCommit, "\n\n", "\ngpgsig -----BEGIN PGP SIGNATURE-----\n \n abcd\n -----END PGP SIGNATURE-----\n\n", 1)
	if _, err := verifyCommit([]byte(pgp), signers); err != errBadSig {
		t.Fatalf("a PGP signature: %v", err)
	}
	if _, err := SignCommit(signed, key); err == nil {
		t.Fatal("a signed commit was signed again")
	}
}

func TestParseSigners(t *testing.T) {
	_, a := testKey(t, "head")
	raw, _ := base64.StdEncoding.DecodeString(a.Key)
	pk, _ := ssh.NewPublicKey(ed25519.PublicKey(raw))
	line := config.RulesSigner{ID: "gate", Key: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk))) + " a comment"}
	if got, err := ParseSigners([]config.RulesSigner{line}); err != nil || len(got) != 1 {
		t.Fatalf("an authorized_keys line: %v", err)
	}
	if SignerFingerprint(a) != SignerFingerprint(line) || !strings.HasPrefix(SignerFingerprint(a), "SHA256:") {
		t.Errorf("fingerprints: %q %q", SignerFingerprint(a), SignerFingerprint(line))
	}
	for name, in := range map[string][]config.RulesSigner{
		"the same key twice": {a, line},
		"the same id twice":  {a, {ID: "head", Key: line.Key}},
		"a bad id":           {{ID: "Head Key", Key: a.Key}},
		"not a key":          {{ID: "x", Key: "c2VjcmV0"}},
		"an RSA line":        {{ID: "x", Key: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQC7"}},
		"empty":              {{ID: "x"}},
	} {
		if _, err := ParseSigners(in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
