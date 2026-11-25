package transcribe

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
)

type directTestPeer struct {
	fakeVoicePeer
	languages []string
	outcome   string
}

func (p *directTestPeer) DictatePeer(ctx context.Context, host string, wav []byte, language string) (Result, error) {
	p.languages = append(p.languages, language)
	if p.outcome == "failed" {
		return Result{}, errors.New("offline")
	}
	if p.outcome == "cancelled" {
		return Result{}, context.Canceled
	}
	if p.outcome == "empty" {
		return Result{}, nil
	}
	if language == "" || language == "auto" {
		return Result{Text: "uncertain", Language: "is"}, nil
	}
	return Result{Text: "words", Language: language}, nil
}
func TestPeerDictationNeverSpoolsOrReplays(t *testing.T) {
	for _, outcome := range []string{"success", "failed", "cancelled", "empty"} {
		t.Run(outcome, func(t *testing.T) {
			p := &directTestPeer{outcome: outcome}
			e := newEnv(t, noEngine, func(e *testEnv, o *Options) {
				e.cfg.ViaPeer = "hosta"
				e.cfg.DictationLanguages = []string{"fr", "en"}
				o.Peer = p
			})
			res, err := e.s.Dictate(context.Background(), fallbackWAV(false), "")
			if outcome == "success" {
				if err != nil || res.Language != "fr" || !reflect.DeepEqual(p.languages, []string{"auto", "fr"}) {
					t.Fatal(res, err, p.languages)
				}
			} else if err == nil {
				t.Fatal("expected failure")
			}
			if p.calls.Load() != 0 {
				t.Fatal("durable upload used")
			}
			if files, _ := os.ReadDir(e.s.peerDir()); len(files) != 0 {
				t.Fatal("spooled audio", files)
			}
			if rows, err := e.st.UnfinishedTranscripts(); err != nil || len(rows) != 0 {
				t.Fatal("restartable job", rows, err)
			}
			e.s.Close()
			restarted := New(e.s.o)
			if err := restarted.Start(); err != nil {
				t.Fatal(err)
			}
			restarted.Close()
			if p.calls.Load() != 0 {
				t.Fatal("restart replay")
			}
		})
	}
}

type languageHosts struct {
	fakeHosts
	languages []string
}

func (f *languageHosts) Dictate(_ context.Context, _ string, _ []byte, language string) (Result, error) {
	f.languages = append(f.languages, language)
	if language == "" || language == "auto" {
		return Result{Text: "uncertain", Language: "is"}, nil
	}
	return Result{Text: "words", Language: language}, nil
}
func TestConfiguredDictationLanguageFallback(t *testing.T) {
	f := &languageHosts{fakeHosts: fakeHosts{hosts: []TranscriptionHost{{ID: "remote", Ready: true}}}}
	e := newEnv(t, noEngine, func(_ *testEnv, o *Options) { o.Hosts = f })
	res, err := e.s.Dictate(context.Background(), fallbackWAV(false), "")
	if err != nil || res.Language != "en" || !reflect.DeepEqual(f.languages, []string{"auto", "en"}) {
		t.Fatal(res, err, f.languages)
	}
}
func TestLocalOnlyDictationDoesNotForwardToPeer(t *testing.T) {
	p := &directTestPeer{}
	e := newEnv(t, noEngine, func(e *testEnv, o *Options) { e.cfg.ViaPeer = "hosta"; o.Peer = p })
	_, err := e.s.Dictate(LocalOnly(context.Background()), fallbackWAV(false), "")
	if !errors.Is(err, ErrNotSetUp) || len(p.languages) != 0 || p.calls.Load() != 0 {
		t.Fatal(err, p.languages)
	}
}
