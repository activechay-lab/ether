package transcode

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"

	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// continuousStore receives a continuous encoder's HLS output over loopback
// HTTP PUT. Publication never waits on storage: ffmpeg hands every finished
// segment, fragment, init section and playlist to this receiver, which keeps
// the newest window in memory and serves it from there. DVR history goes to
// disk on one background writer per encoder; a slow or stalled disk delays
// (and, past a bounded queue, drops) only history, never a live segment.
type continuousStore struct {
	dir    string
	token  string
	ln     net.Listener
	srv    *http.Server
	keep   int // segment files retained in memory per container
	notify func()

	mu      sync.Mutex
	files   map[string]*storeFile
	order   []string // segment files in arrival order, for the memory window
	queue   chan storeJob
	closed  bool
	done    chan struct{}
	dropped int // DVR writes skipped because the disk fell behind
	// writeFile persists DVR history (os.WriteFile; tests stall it).
	writeFile func(string, []byte, os.FileMode) error
}

type storeFile struct {
	data     []byte // nil once only the disk copy remains
	onDisk   bool
	received time.Time
}

type storeJob struct {
	name   string
	data   []byte
	remove bool
}

// storeQueue bounds DVR writes waiting for the disk (about a minute of
// segments in both containers at 2 s).
const storeQueue = 64

// storeMaxBody bounds one PUT: a segment at the highest continuous bitrate.
const storeMaxBody = 64 << 20

func newContinuousStore(dir string, keep int, notify func()) (*continuousStore, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		ln.Close()
		return nil, err
	}
	s := &continuousStore{dir: dir, token: hex.EncodeToString(b[:]), ln: ln, keep: max(keep, 4), notify: notify, files: map[string]*storeFile{}, queue: make(chan storeJob, storeQueue), done: make(chan struct{}), writeFile: os.WriteFile}
	s.srv = &http.Server{Handler: http.HandlerFunc(s.serveHTTP), ReadHeaderTimeout: 10 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	go s.srv.Serve(ln)
	go s.write()
	return s, nil
}

// url is the base ffmpeg writes to; names below it are file names.
func (s *continuousStore) url() string { return "http://" + s.ln.Addr().String() + "/" + s.token + "/" }

func (s *continuousStore) serveHTTP(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutPrefix(r.URL.Path, "/"+s.token+"/")
	if !ok || name == "" || strings.ContainsAny(name, "/\\") || name != filepath.Base(name) {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		data, err := io.ReadAll(io.LimitReader(r.Body, storeMaxBody+1))
		if err != nil || len(data) > storeMaxBody {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		s.put(name, data)
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		// ffmpeg's own window is not the DVR window; the index evicts.
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func storeSegment(name string) bool {
	return strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".m4s")
}

func (s *continuousStore) put(name string, data []byte) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	f := &storeFile{data: data, received: time.Now()}
	s.files[name] = f
	if storeSegment(name) {
		s.order = append(s.order, name)
		select {
		case s.queue <- storeJob{name: name, data: data}:
		default:
			s.dropped++
		}
		s.trimLocked()
	} else if name == "init.mp4" {
		// The init section is tiny and needed for the whole DVR window.
		select {
		case s.queue <- storeJob{name: name, data: data}:
		default:
		}
	}
	notify := s.notify
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// trimLocked releases memory beyond the live window once a disk copy exists.
func (s *continuousStore) trimLocked() {
	for len(s.order) > 2*s.keep {
		name := s.order[0]
		f := s.files[name]
		if f != nil && f.data != nil && !f.onDisk {
			// Still queued for the disk: keep it until written or dropped.
			if len(s.order) <= 2*s.keep+storeQueue {
				return
			}
			delete(s.files, name)
		} else if f != nil {
			f.data = nil
		}
		s.order = s.order[1:]
	}
}

func (s *continuousStore) write() {
	defer close(s.done)
	for job := range s.queue {
		path := filepath.Join(s.dir, job.name)
		if job.remove {
			_ = os.Remove(path)
			continue
		}
		err := s.writeFile(path, job.data, 0o600)
		s.mu.Lock()
		if f := s.files[job.name]; f != nil && err == nil {
			f.onDisk = true
		}
		s.trimLocked()
		s.mu.Unlock()
	}
}

// has reports whether name was received.
func (s *continuousStore) has(name string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.files[name]
	if f == nil {
		return time.Time{}, false
	}
	return f.received, true
}

// open returns name's body from memory, or its DVR copy from disk.
func (s *continuousStore) open(name string) (io.ReadSeeker, time.Time, func(), error) {
	s.mu.Lock()
	f := s.files[name]
	s.mu.Unlock()
	if f == nil {
		return nil, time.Time{}, nil, os.ErrNotExist
	}
	if data := f.data; data != nil {
		return bytes.NewReader(data), f.received, func() {}, nil
	}
	file, err := os.Open(filepath.Join(s.dir, name))
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	return file, f.received, func() { file.Close() }, nil
}

// read returns a small file (init section or playlist) whole.
func (s *continuousStore) read(name string) ([]byte, bool) {
	r, _, done, err := s.open(name)
	if err != nil {
		return nil, false
	}
	defer done()
	b, err := io.ReadAll(r)
	return b, err == nil
}

// remove forgets name; its disk copy is deleted in the background.
func (s *continuousStore) remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.files[name]
	delete(s.files, name)
	if f != nil && f.onDisk && !s.closed {
		select {
		case s.queue <- storeJob{name: name, remove: true}:
		default:
		}
	}
}

func (s *continuousStore) droppedWrites() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

func (s *continuousStore) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.srv.Close()
	close(s.queue)
	<-s.done
}
