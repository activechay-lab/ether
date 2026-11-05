package transcode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type ownedHTTPBlockedWriter struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	release chan struct{}
}

func (w *ownedHTTPBlockedWriter) Write(p []byte) (int, error) {
	close(w.entered)
	<-w.release
	return w.ResponseRecorder.Write(p)
}

func TestOwnedHTTPRetirementFencesLazyStartAndWriter(t *testing.T) {
	for _, blockedStart := range []bool{true, false} {
		t.Run(map[bool]string{true: "late-start", false: "blocked-writer"}[blockedStart], func(t *testing.T) {
			fr := newFakeRunner()
			m := newTestManager(t, fr, config.Transcode{})
			entered, release := make(chan struct{}), make(chan struct{})
			released := false
			defer func() {
				if !released {
					close(release)
				}
			}()
			if blockedStart {
				m.runner = retirementRunner{Runner: fr, start: func(c Command) (Process, error) { close(entered); <-release; return fr.Start(c) }}
			}
			input := "http://127.0.0.1/src"
			lease := newTestEncoderInput(input, false)
			hs, proof, err := m.StartHLSOwned(context.Background(), core.HLSRequest{SessionID: "owned-http", InputURL: input, InputLease: lease, Info: httpInfo(), Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1, Container: "mkv"}, Protocol: core.ProtocolHTTP})
			if err != nil {
				t.Fatal(err)
			}
			stream := hs.(*httpSession)
			w := http.ResponseWriter(httptest.NewRecorder())
			if !blockedStart {
				w = &ownedHTTPBlockedWriter{httptest.NewRecorder(), entered, release}
			}
			served := make(chan struct{})
			go func() { defer close(served); stream.ServeHTTP(w, httptest.NewRequest("GET", "/", nil)) }()
			encoderSignal(t, entered)
			retired := make(chan struct{})
			go func() { _ = stream.RetireEncoder(); close(retired) }()
			select {
			case <-retired:
			case <-time.After(time.Second):
				t.Fatal("RetireEncoder waited on blocked producer")
			}
			assertEncoderPending(t, proof)
			close(release)
			released = true
			encoderSignal(t, served)
			encoderSignal(t, proof.EncoderDone())
			if err := proof.EncoderErr(); err != nil {
				t.Fatal(err)
			}
			if _, ok := m.Session("owned-http"); ok {
				t.Fatal("retired HTTP registry entry retained")
			}
			count := fr.startCount()
			late := httptest.NewRecorder()
			stream.ServeHTTP(late, httptest.NewRequest("GET", "/", nil))
			if late.Code != http.StatusGone || fr.startCount() != count {
				t.Fatal("retired HTTP session restarted encoder")
			}
		})
	}
}
