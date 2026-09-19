package eventlog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPersistentRestartRotationAndGaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, err := NewPersistent(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPersistent(path, 16); err == nil {
		t.Fatal("concurrent writer accepted")
	}
	for n := 0; n < 40; n++ {
		l.Info("fixture", "event")
	}
	p := l.Since("", 0, 5)
	if !p.Durable || p.Gap == "" || p.Oldest != 25 || p.Newest != 40 || p.Next != 29 || !p.More {
		t.Fatalf("rotation: %+v", p)
	}
	stream, boot := p.Stream, p.Boot
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l, err = NewPersistent(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Error("fixture", "after restart")
	p = l.Since(stream, 40, 200)
	if p.Stream != stream || p.Boot == boot || p.Gap != "" || len(p.Events) != 1 || p.Events[0].Sequence != 41 || p.Next != 41 {
		t.Fatalf("restart: %+v", p)
	}
	if l.Since("different-stream", 40, 200).Gap == "" || l.Since(stream, 500, 200).Gap == "" {
		t.Fatal("gap hidden")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("journal permissions")
	}
}
func TestJournalTornTailPreservesEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, err := NewPersistent(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	l.Info("fixture", "retained")
	l.Close()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"sequence":`)
	f.Close()
	before, _ := os.ReadFile(path)
	if _, err := NewPersistent(path, 16); err == nil {
		t.Fatal("torn tail accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("corruption erased evidence")
	}
}
func TestJournalConcurrentAndVolatile(t *testing.T) {
	l, err := NewPersistent(filepath.Join(t.TempDir(), "events.jsonl"), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				l.Info("fixture", strings.Repeat("é", 2500))
				l.Since("", 0, 200)
			}
		}()
	}
	wg.Wait()
	p := l.Since("", 0, 200)
	if len(p.Events) != 64 || p.Next != 64 || p.Gap != "" {
		t.Fatal("lost concurrent event")
	}
	for _, e := range p.Events {
		if len(e.Message) > 4000 {
			t.Fatal("unbounded event")
		}
	}
	if p := New(16).Since("", 0, 200); p.Durable || p.Gap == "" {
		t.Fatal("volatile coverage misrepresented")
	}
	l.journal.file.Close()
	l.Info("fixture", "failed write")
	if p := l.Since("", 64, 200); p.Durable || p.Gap == "" {
		t.Fatal("failed disk write hidden")
	}
}
