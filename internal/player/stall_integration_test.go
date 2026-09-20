package player

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Real fault injection is strictly local: a generated clip, null outputs,
// private socket, and SIGSTOP only to the child process created by this test.
func TestRealMPVRecoversUnresponsiveProcess(t *testing.T) {
	if os.Getenv("JERRY_STALL_TEST") != "1" {
		t.Skip("set JERRY_STALL_TEST=1")
	}
	root := t.TempDir()
	clip := filepath.Join(root, "clip.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=64x64:r=10", "-t", "120", "-c:v", "mpeg4", clip)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	value, err := New(ctx, Config{SocketPath: filepath.Join(root, "mpv.sock"), AudioOut: "null", VideoOut: "null"})
	if err != nil {
		t.Fatal(err)
	}
	p := value.(*mpvPlayer)
	defer p.Close()
	if err := p.Play(clip); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if position, err := p.Position(); err != nil || position <= 0 {
		t.Fatalf("initial playback: %v %v", position, err)
	}
	p.mu.Lock()
	process := p.cmd.Process
	p.mu.Unlock()
	defer process.Signal(syscall.SIGCONT) // never leave our deliberately stopped child behind
	if err := process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(65 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		changed := p.cmd.Process.Pid != process.Pid
		p.mu.Unlock()
		if changed && p.PlaybackHealth().State == "playing" {
			before, e1 := p.Position()
			time.Sleep(time.Second)
			after, e2 := p.Position()
			if e1 == nil && e2 == nil && after > before {
				t.Log("owned frozen subprocess restarted and playback advanced")
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no autonomous recovery within 65s: %+v", p.PlaybackHealth())
}
