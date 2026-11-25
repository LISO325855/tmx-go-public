package rules

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// Which commits count: a commit is signed the way `git commit -S` signs with an SSH key
// (gpg.format ssh). The signature sits in the commit's "gpgsig" header and covers the commit
// as it is without that header; its format is OpenSSH's PROTOCOL.sshsig with the namespace
// "git". It is checked here, in Go, so a machine's git settings (allowed signers, a missing
// ssh-keygen, an old git) change nothing about which commits a node takes.
//
// Only Ed25519 keys are pinned: one key type, no parameters to get wrong.

const (
	sshsigMagic     = "SSHSIG"
	sshsigNamespace = "git"
	sshsigPEMType   = "SSH SIGNATURE"
)

// MaxSigners bounds the pinned set.
const MaxSigners = 8

var signerIDRx = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

var (
	errUnsigned  = errors.New("the commit is not signed")
	errBadSig    = errors.New("the commit's signature cannot be read")
	errNotPinned = errors.New("the commit is signed by a key that is not pinned here")
)

// signer is a decoded config.RulesSigner.
type signer struct {
	id  string
	key ssh.PublicKey
}

// ParseSigners decodes and checks the pinned signers: ids unique, keys Ed25519 and unique. A
// key is an authorized_keys line ("ssh-ed25519 AAAA...") or the raw 32-byte key in standard
// base64 (the form of config.FleetKey).
func ParseSigners(in []config.RulesSigner) ([]signer, error) {
	if len(in) > MaxSigners {
		return nil, fmt.Errorf("at most %d rule signers may be pinned", MaxSigners)
	}
	out := make([]signer, 0, len(in))
	ids, keys := map[string]bool{}, map[string]bool{}
	for i, s := range in {
		if !signerIDRx.MatchString(s.ID) {
			return nil, fmt.Errorf("rule signer %d: the id must be lowercase letters, digits, dots and dashes", i+1)
		}
		pk, err := parseSignerKey(s.Key)
		if err != nil {
			return nil, fmt.Errorf("rule signer %q: %w", s.ID, err)
		}
		fp := string(pk.Marshal())
		if ids[s.ID] || keys[fp] {
			return nil, fmt.Errorf("rule signer %q is pinned twice", s.ID)
		}
		ids[s.ID], keys[fp] = true, true
		out = append(out, signer{id: s.ID, key: pk})
	}
	return out, nil
}

func parseSignerKey(key string) (ssh.PublicKey, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("the key is empty")
	}
	if strings.HasPrefix(key, "ssh-") {
		pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key))
		if err != nil {
			return nil, errors.New("the key is not an authorized_keys line")
		}
		if pk.Type() != ssh.KeyAlgoED25519 {
			return nil, errors.New("only ssh-ed25519 keys can be pinned")
		}
		return pk, nil
	}
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("the key is neither an ssh-ed25519 line nor a base64 Ed25519 key")
	}
	return ssh.NewPublicKey(ed25519.PublicKey(raw))
}

// SignerFingerprint is the short form shown to the owner: OpenSSH's "SHA256:..." of the key,
// or "" for a key that does not parse.
func SignerFingerprint(s config.RulesSigner) string {
	pk, err := parseSignerKey(s.Key)
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(pk)
}

// splitCommit takes a raw commit object apart: payload is the commit without its gpgsig
// header (what the signature covers), sig the header's value (armored), or nil when the
// commit carries none.
func splitCommit(raw []byte) (payload, sig []byte, err error) {
	head, body := raw, []byte(nil)
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		head, body = raw[:i+1], raw[i+1:]
	}
	var out, sigBuf bytes.Buffer
	inSig, seen := false, false
	for len(head) > 0 {
		line := head
		if i := bytes.IndexByte(head, '\n'); i >= 0 {
			line, head = head[:i+1], head[i+1:]
		} else {
			head = nil
		}
		switch {
		case inSig && bytes.HasPrefix(line, []byte(" ")):
			sigBuf.Write(line[1:])
		case bytes.HasPrefix(line, []byte("gpgsig ")):
			if seen {
				return nil, nil, errBadSig // two signatures: which one counts is not ours to guess
			}
			inSig, seen = true, true
			sigBuf.Write(line[len("gpgsig "):])
		default:
			inSig = false
			out.Write(line)
		}
	}
	out.Write(body)
	if !seen {
		return out.Bytes(), nil, nil
	}
	return out.Bytes(), sigBuf.Bytes(), nil
}

// sshsigBlob is the signature file of PROTOCOL.sshsig after its 6-byte magic.
type sshsigBlob struct {
	Version   uint32
	PublicKey string
	Namespace string
	Reserved  string
	HashAlg   string
	Signature string
}

// sshsigSigned is what the key signs, after the same magic.
type sshsigSigned struct {
	Namespace string
	Reserved  string
	HashAlg   string
	Hash      string
}

func sshsigHash(alg string, msg []byte) ([]byte, bool) {
	switch alg {
	case "sha256":
		h := sha256.Sum256(msg)
		return h[:], true
	case "sha512":
		h := sha512.Sum512(msg)
		return h[:], true
	}
	return nil, false
}

// verifyCommit checks the signature of a raw commit object against the pinned signers and
// returns the id of the signer whose key made it.
func verifyCommit(raw []byte, signers []signer) (string, error) {
	payload, armored, err := splitCommit(raw)
	if err != nil {
		return "", err
	}
	if armored == nil {
		return "", errUnsigned
	}
	blk, _ := pem.Decode(armored)
	if blk == nil || blk.Type != sshsigPEMType || len(blk.Bytes) < len(sshsigMagic) || string(blk.Bytes[:len(sshsigMagic)]) != sshsigMagic {
		return "", errBadSig // a PGP or X.509 signature, or damage
	}
	var b sshsigBlob
	if err := ssh.Unmarshal(blk.Bytes[len(sshsigMagic):], &b); err != nil || b.Version != 1 {
		return "", errBadSig
	}
	if b.Namespace != sshsigNamespace {
		return "", errBadSig // a signature made for something else (a file, an email)
	}
	pk, err := ssh.ParsePublicKey([]byte(b.PublicKey))
	if err != nil || pk.Type() != ssh.KeyAlgoED25519 {
		return "", errBadSig
	}
	var who *signer
	for i := range signers {
		if bytes.Equal(signers[i].key.Marshal(), pk.Marshal()) {
			who = &signers[i]
		}
	}
	if who == nil {
		return "", errNotPinned
	}
	hash, ok := sshsigHash(b.HashAlg, payload)
	if !ok {
		return "", errBadSig
	}
	var sig ssh.Signature
	if err := ssh.Unmarshal([]byte(b.Signature), &sig); err != nil {
		return "", errBadSig
	}
	signed := append([]byte(sshsigMagic), ssh.Marshal(sshsigSigned{Namespace: b.Namespace, Reserved: b.Reserved, HashAlg: b.HashAlg, Hash: string(hash)})...)
	// The pinned key verifies, not the one the signature names: they are equal, and this way a
	// future change above cannot make the unpinned one count.
	if err := who.key.Verify(signed, &sig); err != nil {
		return "", errors.New("the commit's signature does not match the commit")
	}
	return who.id, nil
}

// SignCommit signs a raw, unsigned commit object with an Ed25519 key the way git does and
// returns the commit with its gpgsig header. The daemon never signs; this is for tools and
// tests that make a rules commit without ssh-keygen.
func SignCommit(raw []byte, key ed25519.PrivateKey) ([]byte, error) {
	if _, sig, err := splitCommit(raw); err != nil || sig != nil {
		return nil, errors.New("the commit is already signed")
	}
	i := bytes.Index(raw, []byte("\n\n"))
	if i < 0 {
		return nil, errors.New("not a commit object")
	}
	sg, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, err
	}
	hash, _ := sshsigHash("sha512", raw)
	signed := append([]byte(sshsigMagic), ssh.Marshal(sshsigSigned{Namespace: sshsigNamespace, HashAlg: "sha512", Hash: string(hash)})...)
	sig, err := sg.Sign(nil, signed) // Ed25519 signing takes no randomness
	if err != nil {
		return nil, err
	}
	blob := append([]byte(sshsigMagic), ssh.Marshal(sshsigBlob{Version: 1, PublicKey: string(sg.PublicKey().Marshal()),
		Namespace: sshsigNamespace, HashAlg: "sha512", Signature: string(ssh.Marshal(sig))})...)
	armored := strings.TrimRight(string(pem.EncodeToMemory(&pem.Block{Type: sshsigPEMType, Bytes: blob})), "\n")
	var out bytes.Buffer
	out.Write(raw[:i+1])
	out.WriteString("gpgsig " + strings.ReplaceAll(armored, "\n", "\n ") + "\n")
	out.Write(raw[i+1:])
	return out.Bytes(), nil
}
