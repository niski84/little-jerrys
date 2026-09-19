package player

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestEOFCoalescesAndIgnoresCards(t *testing.T) {
	ch := make(chan string, 10)
	p := &mpvPlayer{current: "episode.mp4"}
	p.OnEndFile(func(path string) { ch <- path })
	p.fireEndFile()
	p.fireEndFile()
	select {
	case path := <-ch:
		if path != "episode.mp4" {
			t.Fatal(path)
		}
	case <-time.After(time.Second):
		t.Fatal("no EOF")
	}
	p.mu.Lock()
	p.current = "waiting.png"
	p.mu.Unlock()
	p.endStateMu.Lock()
	p.endSeen = false
	p.endStateMu.Unlock()
	p.fireEndFile()
	select {
	case <-ch:
		t.Fatal("duplicate/card EOF")
	case <-time.After(30 * time.Millisecond):
	}
}

// Opt-in integration uses real mpv with null audio/video and temporary media.
// Never connects to the Pi, the display, the production socket or state file.
func TestRealMPVMigration(t *testing.T) {
	if os.Getenv("JERRY_REAL_MPV_TEST") != "1" {
		t.Skip("set JERRY_REAL_MPV_TEST=1 for isolated real mpv")
	}
	root := t.TempDir()
	clip := filepath.Join(root, "clip.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=64x64:r=10", "-t", "2", "-c:v", "mpeg4", clip)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture media: %v %s", err, out)
	}
	value, err := New(ctx, Config{SocketPath: filepath.Join(root, "mpv.sock"), AudioOut: "null", VideoOut: "null"})
	if err != nil {
		t.Fatal(err)
	}
	p := value.(*mpvPlayer)
	defer p.Close()
	ended := make(chan string, 10)
	p.OnEndFile(func(path string) { ended <- path })
	if err := p.Play(clip); err != nil {
		t.Fatal(err)
	}
	select {
	case path := <-ended:
		if path != clip {
			t.Fatal(path)
		}
	case <-ctx.Done():
		t.Fatal("real keep-open did not advance")
	}
	if err := p.PlayAt(clip, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := p.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := p.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := p.Mute(true); err != nil {
		t.Fatal(err)
	}
	if err := p.Volume(68); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-ctx.Done():
		t.Fatal("second EOF did not fire")
	}
	select {
	case <-ended:
		t.Fatal("duplicate EOF callback")
	case <-time.After(300 * time.Millisecond):
	}
	// Exercise the actual subprocess supervisor, not just a fake IPC socket.
	if err := p.Play(clip); err != nil { t.Fatal(err) }
	if err := p.Pause(); err != nil { t.Fatal(err) }
	p.mu.Lock()
	oldPID := p.cmd.Process.Pid
	process := p.cmd.Process
	p.mu.Unlock()
	if err := process.Kill(); err != nil { t.Fatal(err) }
	deadline := time.Now().Add(5*time.Second)
	respawned := false
	for time.Now().Before(deadline) {
		p.mu.Lock(); newPID := p.cmd.Process.Pid; p.mu.Unlock()
		if newPID != oldPID { respawned = true; break }
		time.Sleep(25*time.Millisecond)
	}
	if !respawned { t.Fatal("mpv did not respawn") }
	// Wait for restore commands to reach mpv before checking their effects.
	for time.Now().Before(deadline) {
		value, err := p.query("get_property", "mute")
		if err == nil && value == true { break }
		time.Sleep(25*time.Millisecond)
	}
	if value, err := p.query("get_property", "mute"); err != nil || value != true { t.Fatal("quiet-hours mute lost after mpv restart") }
	if value, err := p.query("get_property", "pause"); err != nil || value != true { t.Fatal("pause lost after mpv restart") }
	if err := p.Resume(); err != nil { t.Fatal(err) }
}
