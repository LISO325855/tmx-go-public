//go:build aclmutate

package codexguard

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"
)

const systemSID = "S-1-5-18"

func currentUserSID(t *testing.T) string {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return u.User.Sid.String()
}

// aclOf returns whether path's DACL is protected, the SIDs of its allow entries (sorted; any
// other kind of entry shows as "ace-type-N") and its owner.
func aclOf(t *testing.T, path string) (protected bool, sids []string, owner string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || sd == nil {
		t.Fatalf("%s: security info: %v", path, err)
	}
	ctl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	o, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("%s: no DACL (%v)", path, err)
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			sids = append(sids, fmt.Sprintf("ace-type-%d", ace.Header.AceType))
			continue
		}
		sids = append(sids, (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String())
	}
	slices.Sort(sids)
	sids = slices.Compact(sids)
	return ctl&windows.SE_DACL_PROTECTED != 0, sids, o.String()
}

// assertPrivate checks that only the user and SYSTEM get in, that the DACL is protected (not
// for files that inherit theirs) and that the user owns it (not for what tmx did not create).
func assertPrivate(t *testing.T, path string, wantProtected, wantOwner bool) {
	t.Helper()
	user := currentUserSID(t)
	protected, sids, owner := aclOf(t, path)
	want := []string{user, systemSID}
	slices.Sort(want)
	if !slices.Equal(sids, want) {
		t.Fatalf("%s: DACL lets in %v, want only the user and SYSTEM %v", path, sids, want)
	}
	if wantProtected && !protected {
		t.Fatalf("%s: DACL is not protected", path)
	}
	if wantOwner && owner != user {
		t.Fatalf("%s: owner %s, want the user %s", path, owner, user)
	}
}

// setDACL gives path an SDDL DACL, as an older version or a shared folder might have.
func setDACL(t *testing.T, path, sddl string) {
	t.Helper()
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerAuthCopyProtectsPermissiveParent(t *testing.T) {
	source, dest := t.TempDir(), t.TempDir()
	setDACL(t, dest, "D:(A;OICI;FA;;;WD)")
	for _, body := range []string{`{"fixture":1}`, `{"fixture":2}`} {
		if err := os.WriteFile(filepath.Join(source, "auth.json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := SyncOwnerAuth(source, dest); err != nil {
			t.Fatal(err)
		}
		assertPrivate(t, filepath.Join(dest, "auth.json"), true, true)
		if _, sids, _ := aclOf(t, dest); !slices.Contains(sids, "S-1-1-0") {
			t.Fatal("changed existing parent ACL")
		}
	}
}
