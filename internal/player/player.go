// Package player wraps mpv via its IPC socket. The Player runs mpv as a child
// process, sends JSON commands over a Unix socket, and supervises the process
// (restart on crash). A black TV is the worst failure mode in a restaurant —
// the supervisor must always bring mpv back up.
package player

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"time"
)

// Player drives mpv playback.
type Player interface {
	// OnEndFile registers a callback fired when mpv finishes a track
	// naturally (EOF — not on Stop/Skip/loadfile-replace). The appliance
	// uses this to auto-advance to the next item in the queue. mpv runs
	// with --keep-open=no so when a file ends, mpv idles to a black
	// screen — the callback is what gets pixels back on the TV.
	//
	// Safe to call before or after Play(); the registration outlives mpv
	// crashes and respawns.
	OnEndFile(cb func())
	Play(path string) error
	// PlayAt loads the file and seeks to start before un-pausing. Used on
	// boot to resume the same episode the Pi was on when it lost power.
	PlayAt(path string, start time.Duration) error
	Pause() error
	Resume() error
	Skip() error
	Volume(pct int) error
	Status() Status
	// Position queries mpv for the current playhead. Returns (0, error) if
	// the IPC socket is unavailable or the property isn't ready yet.
	Position() (time.Duration, error)
	// Duration queries mpv for the total length of the current track.
	// Returns (0, error) when no track loaded or duration unknown.
	Duration() (time.Duration, error)
	// Seek jumps to an absolute position within the current track.
	Seek(pos time.Duration) error
	// PlaySlideshow displays a list of image files, each for secsPer
	// seconds, looping forever. Used during off-hours when the schedule
	// says "no episodes" — the TV shows fan art instead.
	PlaySlideshow(paths []string, secsPer int) error
	// ShowOSD renders a text message as an on-screen overlay for dur.
	// Used for maintenance announcements (e.g. "restarting in 10 min").
	// Silently no-ops if the IPC socket is unavailable.
	ShowOSD(message string, dur time.Duration) error
	Close() error
}

// Status snapshots the current playback state.
type Status struct {
	NowPlaying string
	Paused     bool
	Position   time.Duration
}

// Config configures the mpv process.
type Config struct {
	SocketPath string // /tmp/mpv-jerry.sock
	Fullscreen bool
	HWDecode   bool   // --hwdec=auto for Pi
	AudioOut   string // --ao override, e.g. "null" in containers
	VideoOut   string // --vo override, e.g. "x11" in containers
}

// New starts (or returns a stub of) an mpv-backed player. The returned Player
// supervises mpv: if mpv exits unexpectedly, it is restarted.
func New(ctx context.Context, cfg Config) (Player, error) {
	p := &mpvPlayer{cfg: cfg, current: ""}
	if err := p.spawn(ctx); err != nil {
		return nil, fmt.Errorf("spawn mpv: %w", err)
	}
	return p, nil
}

type mpvPlayer struct {
	cfg     Config
	mu      sync.Mutex
	cmd     *exec.Cmd
	current string
	paused  bool

	// reqID is incremented for each IPC request so responses can be matched.
	reqMu sync.Mutex
	reqID int64

	// onEndFile is invoked when mpv's event stream emits an end-file with
	// reason=eof. Survives mpv crash+respawn — set once on app boot.
	endMu     sync.Mutex
	onEndFile func()
}

func (p *mpvPlayer) spawn(ctx context.Context) error {
	args := []string{
		"--idle=yes",
		"--input-ipc-server=" + p.cfg.SocketPath,
		"--no-terminal",
		"--keep-open=no",
		"--osd-level=0",
		// Force-load image-display-duration so still-image playlists
		// (fan-art slideshow during off-hours) advance reliably.
		"--image-display-duration=8",
	}
	if p.cfg.AudioOut != "" {
		args = append(args, "--ao="+p.cfg.AudioOut)
	} else {
		args = append(args, "--audio-fallback-to-null=yes")
	}
	if p.cfg.VideoOut != "" {
		args = append(args, "--vo="+p.cfg.VideoOut)
	}
	if p.cfg.Fullscreen {
		args = append(args, "--fullscreen")
		// Belt-and-suspenders for headless containers (Xvfb has no window
		// manager, so --fullscreen alone is a no-op — mpv asks the WM to
		// fullscreen, nobody answers, and the window stays at default
		// 1280x720 size in the middle of a 1920x1080 root). Explicit
		// geometry forces mpv to take the full canvas, which is also
		// what we want on the real Pi (HDMI is the only display).
		args = append(args, "--geometry=100%x100%+0+0", "--no-border", "--ontop")
	}
	if p.cfg.HWDecode {
		// `--hwdec=auto-safe` falls back to software when no GPU
		// acceleration is available (which is the case in headless
		// containers with Xvfb). Strict --hwdec=auto can stall mpv if
		// it fails to find a backend.
		args = append(args, "--hwdec=auto-safe")
	}
	cmd := exec.CommandContext(ctx, "mpv", args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start mpv: %w", err)
	}
	p.cmd = cmd

	// Event listener: opens a long-lived IPC connection, receives mpv's
	// async event stream, and fires onEndFile when a track finishes
	// naturally (reason=eof). Without this the TV goes black at the end
	// of every track because --keep-open=no makes mpv unload back to idle.
	go p.eventLoop(ctx)

	// Supervisor: if mpv dies, log and respawn after a short backoff.
	go func() {
		err := cmd.Wait()
		if ctx.Err() != nil {
			return
		}
		fmt.Printf("[player] mpv exited unexpectedly: %v — respawning in 1s\n", err)
		time.Sleep(1 * time.Second)
		_ = p.spawn(ctx)
	}()
	return nil
}

// eventLoop holds an IPC connection open and decodes mpv's async event
// stream. The mpv JSON IPC protocol pushes events for state changes
// (file loaded, playback ended, paused, etc.) on the same socket the
// commands ride on. We only care about end-file with reason=eof —
// everything else is filtered out.
//
// On any read error (mpv crashed, connection dropped) the loop exits;
// the spawn() supervisor will restart mpv and a fresh eventLoop will be
// kicked off as part of that respawn.
func (p *mpvPlayer) eventLoop(ctx context.Context) {
	// Wait for the socket — mpv hasn't necessarily created it yet by the
	// time we get here.
	var conn net.Conn
	for i := 0; i < 100; i++ {
		if ctx.Err() != nil {
			return
		}
		c, err := net.DialTimeout("unix", p.cfg.SocketPath, 500*time.Millisecond)
		if err == nil {
			conn = c
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if conn == nil {
		fmt.Printf("[player] event loop: socket never appeared at %s\n", p.cfg.SocketPath)
		return
	}
	defer conn.Close()

	// Debounce token. mpv emits a second end-file (reason=stop) for the
	// previously-loaded entry every time we issue loadfile, even when
	// that previous entry already ended naturally. Without this guard,
	// every PlayNext would trigger another PlayNext, skipping every
	// other track. We fire at most one callback per second.
	var lastFire time.Time

	dec := json.NewDecoder(conn)
	for {
		if ctx.Err() != nil {
			return
		}
		var msg map[string]interface{}
		if err := dec.Decode(&msg); err != nil {
			// mpv died or socket closed — the supervisor will respawn
			// and start a fresh event loop. Don't log here; the
			// supervisor's own message is enough.
			return
		}
		evt, _ := msg["event"].(string)
		if evt != "end-file" {
			continue
		}
		// mpv reasons: eof | stop | quit | error | redirect | unknown.
		// Only EOF means "track played to completion" — the others are
		// triggered by us calling Play()/Skip()/Stop and we DON'T want
		// to double-advance in those cases.
		reason, _ := msg["reason"].(string)
		if reason != "eof" {
			continue
		}
		if time.Since(lastFire) < 1*time.Second {
			continue
		}
		lastFire = time.Now()
		p.endMu.Lock()
		cb := p.onEndFile
		p.endMu.Unlock()
		if cb != nil {
			// Run the callback in its own goroutine so a slow PlayNext
			// (TMDB lookup, webhook fanout) doesn't stall the listener.
			go cb()
		}
	}
}

// OnEndFile registers the natural end-of-track callback. See interface doc.
func (p *mpvPlayer) OnEndFile(cb func()) {
	p.endMu.Lock()
	p.onEndFile = cb
	p.endMu.Unlock()
}

func (p *mpvPlayer) dialSocket() (net.Conn, error) {
	for i := 0; i < 100; i++ {
		c, err := net.DialTimeout("unix", p.cfg.SocketPath, 500*time.Millisecond)
		if err == nil {
			return c, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("dial mpv socket: timed out waiting for %s", p.cfg.SocketPath)
}

func (p *mpvPlayer) command(args ...interface{}) error {
	conn, err := p.dialSocket()
	if err != nil {
		return err
	}
	defer conn.Close()
	// mpv's IPC protocol is one \n-delimited JSON object per line. Use a
	// bufio.Scanner so we read exactly one line at a time and parse it
	// in isolation. The earlier json.NewDecoder approach was brittle
	// because the underlying buffered reader could end up holding the
	// start of one async event and the start of another, producing
	// "invalid character ''" decode errors mid-stream.
	p.reqMu.Lock()
	p.reqID++
	id := p.reqID
	p.reqMu.Unlock()

	payload, _ := json.Marshal(map[string]interface{}{
		"command":    args,
		"request_id": id,
	})
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = conn.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(line, &resp); err != nil {
			// One bad line in the stream is non-fatal — keep scanning
			// for our reply. mpv occasionally interleaves logs or
			// property updates that might not be perfectly clean JSON.
			continue
		}
		// Skip async events (which arrive on this connection too) until
		// we see our own request_id echo back.
		if rid, ok := resp["request_id"].(float64); !ok || int64(rid) != id {
			continue
		}
		if errStr, _ := resp["error"].(string); errStr != "success" {
			return fmt.Errorf("mpv: cmd=%v err=%s", args, errStr)
		}
		return nil
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	return fmt.Errorf("mpv closed connection without reply (cmd=%v)", args)
}

// query sends a command and waits for the matching response by request_id,
// then returns the data field. Used for get_property calls.
func (p *mpvPlayer) query(args ...interface{}) (interface{}, error) {
	p.reqMu.Lock()
	p.reqID++
	id := p.reqID
	p.reqMu.Unlock()

	conn, err := p.dialSocket()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	payload, _ := json.Marshal(map[string]interface{}{
		"command":    args,
		"request_id": id,
	})
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	dec := json.NewDecoder(conn)
	for {
		var resp map[string]interface{}
		if err := dec.Decode(&resp); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		// mpv emits async events too — skip until we see our request_id.
		if rid, ok := resp["request_id"].(float64); !ok || int64(rid) != id {
			continue
		}
		if errStr, _ := resp["error"].(string); errStr != "success" {
			return nil, fmt.Errorf("mpv error: %s", errStr)
		}
		return resp["data"], nil
	}
}

func (p *mpvPlayer) Play(path string) error {
	p.mu.Lock()
	p.current = path
	p.paused = false
	p.mu.Unlock()
	if err := p.command("loadfile", path, "replace"); err != nil {
		// Surface to logs so a black-screen-and-divergent-state condition
		// stops being silent. Without this, the binary's idea of "now
		// playing" can drift out of sync with what mpv has loaded.
		fmt.Printf("[player] loadfile FAILED for %s: %v\n", path, err)
		return err
	}
	return nil
}

func (p *mpvPlayer) PlayAt(path string, start time.Duration) error {
	p.mu.Lock()
	p.current = path
	p.paused = false
	p.mu.Unlock()
	if start <= 0 {
		return p.command("loadfile", path, "replace")
	}
	// Pass start= as a loadfile option so mpv seeks before the first frame
	// is decoded — avoids the race of a separate seek command arriving before
	// the file is loaded.
	opts := fmt.Sprintf("start=%f", start.Seconds())
	if err := p.command("loadfile", path, "replace", opts); err != nil {
		fmt.Printf("[player] loadfile FAILED for %s: %v\n", path, err)
		return err
	}
	return nil
}

func (p *mpvPlayer) PlaySlideshow(paths []string, secsPer int) error {
	if len(paths) == 0 {
		return fmt.Errorf("no images to display")
	}
	if secsPer < 1 {
		secsPer = 8
	}
	// Ensure mpv keeps each image up for secsPer seconds and loops the
	// playlist when it reaches the end.
	if err := p.command("set_property", "image-display-duration", secsPer); err != nil {
		return fmt.Errorf("set image-display-duration: %w", err)
	}
	if err := p.command("set_property", "loop-playlist", "inf"); err != nil {
		return fmt.Errorf("set loop-playlist: %w", err)
	}
	// Replace the playlist with the first image, then append the rest.
	if err := p.command("loadfile", paths[0], "replace"); err != nil {
		return err
	}
	for _, path := range paths[1:] {
		if err := p.command("loadfile", path, "append"); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.current = paths[0]
	p.paused = false
	p.mu.Unlock()
	return nil
}

func (p *mpvPlayer) Position() (time.Duration, error) {
	v, err := p.query("get_property", "time-pos")
	if err != nil {
		return 0, err
	}
	secs, ok := v.(float64)
	if !ok {
		return 0, fmt.Errorf("unexpected time-pos type: %T", v)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

func (p *mpvPlayer) Duration() (time.Duration, error) {
	v, err := p.query("get_property", "duration")
	if err != nil {
		return 0, err
	}
	secs, ok := v.(float64)
	if !ok {
		return 0, fmt.Errorf("unexpected duration type: %T", v)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

func (p *mpvPlayer) Seek(pos time.Duration) error {
	if pos < 0 {
		pos = 0
	}
	return p.command("seek", pos.Seconds(), "absolute")
}

func (p *mpvPlayer) Pause() error {
	p.mu.Lock()
	p.paused = true
	p.mu.Unlock()
	return p.command("set_property", "pause", true)
}

func (p *mpvPlayer) Resume() error {
	p.mu.Lock()
	p.paused = false
	p.mu.Unlock()
	return p.command("set_property", "pause", false)
}

func (p *mpvPlayer) Skip() error {
	return p.command("playlist-next", "force")
}

func (p *mpvPlayer) Volume(pct int) error {
	return p.command("set_property", "volume", pct)
}

func (p *mpvPlayer) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Status{NowPlaying: p.current, Paused: p.paused}
}

func (p *mpvPlayer) ShowOSD(message string, dur time.Duration) error {
	ms := int(dur.Milliseconds())
	if ms <= 0 {
		ms = 5000
	}
	// mpv show-text: text, duration_ms, level (omit level → default)
	return p.command("show-text", message, ms)
}

func (p *mpvPlayer) Close() error {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}
