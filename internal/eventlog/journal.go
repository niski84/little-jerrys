package eventlog

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf8"
)

// A private, bounded, fsynced append journal. A lock excludes a second process.
// Atomic rotation retains stream identity and monotonically increasing sequence.
type journal struct {
	path       string
	file, lock *os.File
	records    int
}
type journalHeader struct {
	Schema int    `json:"schema"`
	Stream string `json:"stream"`
}
type Page struct {
	Schema  int     `json:"schema"`
	Stream  string  `json:"stream"`
	Boot    string  `json:"boot"`
	Durable bool    `json:"durable"`
	Gap     string  `json:"gap,omitempty"`
	Oldest  int64   `json:"oldest"`
	Newest  int64   `json:"newest"`
	Next    int64   `json:"next"`
	More    bool    `json:"more"`
	Events  []Event `json:"events"`
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func bounded(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func NewPersistent(path string, capacity int) (*Log, error) {
	if capacity < 16 || capacity > 2000 {
		return nil, fmt.Errorf("invalid retained event capacity")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("event journal already owned")
	}
	j := &journal{path: path, lock: lock}
	l := New(capacity)
	l.journal = j
	fail := func(err error) (*Log, error) { _ = l.Close(); return nil, err }
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		if err = j.compact(l.stream, nil); err != nil {
			return fail(err)
		}
		return l, nil
	}
	if err != nil {
		return fail(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32*1024*1024 {
		return fail(fmt.Errorf("invalid or oversized event journal"))
	}
	scan := bufio.NewScanner(io.LimitReader(f, 32*1024*1024+1))
	scan.Buffer(make([]byte, 8192), 32768)
	if !scan.Scan() {
		return fail(fmt.Errorf("missing journal header"))
	}
	var h journalHeader
	if err := json.Unmarshal(scan.Bytes(), &h); err != nil || h.Schema != 1 || len(h.Stream) != 32 {
		return fail(fmt.Errorf("invalid journal header"))
	}
	if _, err := hex.DecodeString(h.Stream); err != nil {
		return fail(fmt.Errorf("invalid journal stream"))
	}
	l.stream = h.Stream
	for scan.Scan() {
		var e Event
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil || e.Sequence <= 0 || (l.sequence > 0 && e.Sequence != l.sequence+1) || len(e.Message) > 4000 || len(e.Source) > 120 {
			return fail(fmt.Errorf("corrupt journal; original file retained for recovery"))
		}
		l.sequence = e.Sequence
		l.events = append(l.events, e)
		j.records++
		if len(l.events) > capacity {
			l.events = l.events[1:]
		}
	}
	if err := scan.Err(); err != nil {
		return fail(err)
	}
	if info.Size() > 0 {
		var tail [1]byte
		if _, err := f.ReadAt(tail[:], info.Size()-1); err != nil || tail[0] != '\n' {
			return fail(fmt.Errorf("incomplete journal tail; recovery required"))
		}
	}
	if err := j.compact(l.stream, l.events); err != nil {
		return fail(err)
	}
	return l, nil
}
func (j *journal) append(e Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if n, err := j.file.Write(b); err != nil {
		return err
	} else if n != len(b) {
		return io.ErrShortWrite
	}
	if err := j.file.Sync(); err != nil {
		return err
	}
	j.records++
	return nil
}
func (j *journal) compact(stream string, events []Event) error {
	f, err := os.CreateTemp(filepath.Dir(j.path), ".events-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(journalHeader{Schema: 1, Stream: stream}); err != nil {
		return err
	}
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, j.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(j.path))
	if err != nil {
		return err
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		return err
	}
	if j.file != nil {
		_ = j.file.Close()
		j.file = nil
	}
	j.file, err = os.OpenFile(j.path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	j.records = len(events)
	return nil
}
func (l *Log) Since(stream string, after int64, limit int) Page {
	if l == nil {
		return Page{Schema: 1, Gap: "event log unavailable", Events: []Event{}}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	p := Page{Schema: 1, Stream: l.stream, Boot: l.boot, Durable: l.journal != nil && l.durabilityError == "", Newest: l.sequence, Next: after, Events: []Event{}}
	if len(l.events) > 0 {
		p.Oldest = l.events[0].Sequence
	}
	if !p.Durable {
		p.Gap = "volatile event history"
		if l.durabilityError != "" {
			p.Gap = l.durabilityError
		}
	}
	if stream != "" && stream != l.stream {
		p.Gap = "event stream replaced"
		after = 0
	}
	if after > l.sequence {
		p.Gap = "cursor ahead of retained source"
		after = 0
	}
	if p.Oldest > after+1 {
		p.Gap = "older events exceeded retention"
	}
	p.Next = after
	if limit < 1 || limit > 200 {
		limit = 200
	}
	for _, e := range l.events {
		if e.Sequence <= after {
			continue
		}
		if len(p.Events) == limit {
			p.More = true
			break
		}
		p.Events = append(p.Events, e)
		p.Next = e.Sequence
	}
	return p
}
func (l *Log) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.journal == nil {
		return nil
	}
	j := l.journal
	l.journal = nil
	var err error
	if j.file != nil {
		err = j.file.Close()
	}
	if j.lock != nil {
		_ = syscall.Flock(int(j.lock.Fd()), syscall.LOCK_UN)
		_ = j.lock.Close()
	}
	return err
}
