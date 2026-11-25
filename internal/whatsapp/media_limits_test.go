package whatsapp

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
)

func TestLimitedMediaFile(t *testing.T) {
	for _, op := range []string{"copy", "write_at", "truncate", "disk_floor"} {
		t.Run(op, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "bounded")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &limitedMediaFile{file: file, limit: 8, cancel: cancel}
			switch op {
			case "copy":
				_, err = io.Copy(f, bytes.NewBufferString("0123456789"))
			case "write_at":
				_, err = f.WriteAt([]byte("abc"), 7)
			case "truncate":
				err = f.Truncate(9)
			case "disk_floor":
				f.free = func() (uint64, error) { return mediaFreeFloor, nil }
				_, err = f.Write([]byte("a"))
			}
			st, _ := file.Stat()
			if err == nil || ctx.Err() == nil || st.Size() != 0 {
				t.Fatalf("limit not enforced: err=%v canceled=%v size=%d", err, ctx.Err(), st.Size())
			}
		})
	}
}

func TestLimitedMediaFileDecrypt(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "decrypt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	f := &limitedMediaFile{file: file, limit: 26}
	if _, err = f.Write(make([]byte, 26)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt([]byte("plain"), 0); err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(5); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(f)
	if err != nil || string(b) != "plain" {
		t.Fatalf("decryption operations: %q %v", b, err)
	}
}
