package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/niski84/little-jerrys/internal/episodeid"
)

const reserveBytes = uint64(512 * 1024 * 1024)

type EpisodeStatus struct {
	Code    string `json:"code"`
	Season  int    `json:"season"`
	Title   string `json:"title"`
	Present bool   `json:"present"`
	Path    string `json:"path,omitempty"`
}

type Result struct {
	Source  string `json:"source"`
	Target  string `json:"target,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Status struct {
	Phase        string          `json:"phase"`
	SourceRoot   string          `json:"source_root"`
	Destination  string          `json:"destination"`
	Total        int             `json:"total"`
	Present      int             `json:"present"`
	Missing      int             `json:"missing"`
	CurrentFile  string          `json:"current_file,omitempty"`
	CopiedBytes  int64           `json:"copied_bytes"`
	TotalBytes   int64           `json:"total_bytes"`
	FreeBytes    uint64          `json:"free_bytes"`
	SafeToRemove bool            `json:"safe_to_remove"`
	Error        string          `json:"error,omitempty"`
	Updated      time.Time       `json:"updated"`
	Episodes     []EpisodeStatus `json:"episodes"`
	Results      []Result        `json:"results"`
}

// Importer owns one serialized, cancellable transfer worker. The source is
// always opened read-only. Completed destination files are never overwritten.
type Importer struct {
	catalog                     *Catalog
	source, destination, report string
	mu                          sync.RWMutex
	status                      Status
	requests                    chan struct{}
	probe                       func(context.Context, string) (float64, error)
	free                        func(string) (uint64, error)
	log                         func(string)
	validated                   map[string]validation
}

type validation struct {
	size     int64
	modified time.Time
	duration float64
	err      error
}
type sourceFile struct {
	path, relative string
	info           fs.FileInfo
}

func New(c *Catalog, source, destination, report string, log func(string)) (*Importer, error) {
	if source == "" || destination == "" {
		return nil, fmt.Errorf("USB import requires explicit source and destination paths")
	}
	src, err := filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	dst, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	if within(src, dst) || within(dst, src) {
		return nil, fmt.Errorf("USB import source and SD library must be separate directories")
	}
	if log == nil {
		log = func(string) {}
	}
	i := &Importer{catalog: c, source: src, destination: dst, report: report, requests: make(chan struct{}, 1), probe: probeVideo, free: freeSpace, log: log, validated: map[string]validation{}}
	i.status = Status{Phase: "scanning", SourceRoot: src, Destination: dst, Total: len(c.Episodes), Missing: len(c.Episodes)}
	if report != "" {
		data, err := os.ReadFile(report)
		if err == nil {
			var old Status
			if err = json.Unmarshal(data, &old); err == nil && old.Destination == dst {
				i.status.Results = old.Results
			}
			if err != nil {
				log("Read previous import report: " + err.Error())
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			log("Read previous import report: " + err.Error())
		}
	}
	return i, nil
}

func within(parent, child string) bool {
	r, err := filepath.Rel(parent, child)
	return err == nil && (r == "." || r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)))
}

func (i *Importer) Snapshot() Status {
	i.mu.RLock()
	defer i.mu.RUnlock()
	s := i.status
	s.Episodes = append([]EpisodeStatus(nil), s.Episodes...)
	s.Results = append([]Result(nil), s.Results...)
	return s
}

func (i *Importer) Retry() {
	select {
	case i.requests <- struct{}{}:
	default:
	}
}

// Run rechecks local files and detects a newly attached/changed USB every five
// seconds. A failed unchanged batch is only retried after operator request or
// a USB change, avoiding repeated writes to a full or failing card.
func (i *Importer) Run(ctx context.Context) {
	// A power loss can leave only hidden staging files. Completed canonical
	// videos remain untouched; the interrupted episode restarts from USB.
	staging, err := filepath.Glob(filepath.Join(i.destination, ".import-*"))
	if err != nil {
		i.log("Find interrupted imports: " + err.Error())
	}
	for _, path := range staging {
		if err := os.Remove(path); err != nil {
			i.log("Remove interrupted import " + path + ": " + err.Error())
		}
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	last := ""
	force := true
	for {
		files, fingerprint, err := i.sources()
		if err != nil {
			i.refreshLocal(ctx)
			i.update(func(s *Status) { s.Error = err.Error(); s.Phase = "attention"; s.SafeToRemove = true })
		}
		if err == nil && (force || fingerprint != last) {
			i.runBatch(ctx, files)
			last = fingerprint
			force = false
		} else if err == nil {
			i.refreshLocal(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-i.requests:
			force = true
		case <-ticker.C:
		}
	}
}

func (i *Importer) update(f func(*Status)) {
	i.mu.Lock()
	f(&i.status)
	i.status.Updated = time.Now().UTC()
	i.mu.Unlock()
}

func isVideo(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mkv", ".mov", ".avi", ".m4v", ".webm":
		return true
	}
	return false
}

func (i *Importer) sources() ([]sourceFile, string, error) {
	var files []sourceFile
	if _, err := os.Stat(i.source); errors.Is(err, os.ErrNotExist) {
		return nil, "absent", nil
	} else if err != nil {
		return nil, "", fmt.Errorf("read USB %s: %w", i.source, err)
	}
	err := filepath.WalkDir(i.source, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != i.source && d.IsDir() && (strings.HasPrefix(d.Name(), ".") || strings.EqualFold(d.Name(), "commercials") || strings.EqualFold(d.Name(), "sounds")) {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() || !isVideo(path) || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(i.source, path)
		if err != nil {
			return err
		}
		files = append(files, sourceFile{path, relative, info})
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("scan USB %s: %w", i.source, err)
	}
	sort.Slice(files, func(a, b int) bool { return files[a].relative < files[b].relative })
	h := sha256.New()
	// Device/mount and directory identity distinguish newly attached volumes
	// even when FAT timestamps and file lengths happen to be identical.
	var rootStat unix.Stat_t
	if err := unix.Stat(i.source, &rootStat); err != nil {
		return nil, "", fmt.Errorf("identify USB: %w", err)
	}
	var rootFS unix.Statfs_t
	if err := unix.Statfs(i.source, &rootFS); err != nil {
		return nil, "", fmt.Errorf("identify USB filesystem: %w", err)
	}
	fmt.Fprintf(h, "%d:%d:%v\n", rootStat.Dev, rootStat.Ino, rootFS.Fsid)
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", f.relative, f.info.Size(), f.info.ModTime().UnixNano())
	}
	return files, hex.EncodeToString(h.Sum(nil)), nil
}

func (i *Importer) duration(ctx context.Context, path string, info fs.FileInfo) (float64, error) {
	if v, ok := i.validated[path]; ok && v.size == info.Size() && v.modified.Equal(info.ModTime()) {
		return v.duration, v.err
	}
	d, err := i.probe(ctx, path)
	i.validated[path] = validation{info.Size(), info.ModTime(), d, err}
	return d, err
}

func (i *Importer) local(ctx context.Context) (map[episodeid.ID]string, []Result, error) {
	present := map[episodeid.ID]string{}
	var issues []Result
	err := filepath.WalkDir(i.destination, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if path != i.destination && d.IsDir() && (strings.HasPrefix(d.Name(), ".") || d.Name() == "commercials" || d.Name() == "sounds") {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() || !isVideo(path) || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		duration, err := i.duration(ctx, path, info)
		var ids []episodeid.ID
		if err == nil {
			ids, _, err = i.catalog.Match(path, duration)
		}
		if err != nil {
			issues = append(issues, Result{Source: path, Status: "local error", Message: err.Error()})
			return nil
		}
		for _, id := range ids {
			present[id] = path
		}
		return nil
	})
	return present, issues, err
}

func (i *Importer) refreshLocal(ctx context.Context) {
	present, issues, err := i.local(ctx)
	if err != nil {
		i.update(func(s *Status) { s.Error = "Read SD library: " + err.Error(); s.Phase = "attention" })
		return
	}
	i.setCoverage(present)
	if len(issues) > 0 {
		i.update(func(s *Status) {
			s.Error = fmt.Sprintf("%d local file(s) need attention; use Rescan for details", len(issues))
			s.Phase = "attention"
		})
	}
	if len(present) < len(i.catalog.Episodes) {
		i.update(func(s *Status) {
			if s.Phase == "complete" {
				s.Phase = "waiting for USB"
			}
		})
	}
	if len(present) == len(i.catalog.Episodes) && len(issues) == 0 {
		i.update(func(s *Status) {
			if s.Error == "" {
				s.Phase = "complete"
			}
		})
	}
}

func (i *Importer) setCoverage(present map[episodeid.ID]string) {
	eps := make([]EpisodeStatus, 0, len(i.catalog.Episodes))
	for _, e := range i.catalog.Episodes {
		path, ok := present[e.ID]
		eps = append(eps, EpisodeStatus{e.ID.String(), e.ID.Season, e.Title, ok, path})
	}
	free, err := i.free(i.destination)
	i.update(func(s *Status) {
		s.Episodes = eps
		s.Present = len(present)
		s.Missing = s.Total - s.Present
		s.FreeBytes = free
		if err != nil {
			s.Error = "Read SD free space: " + err.Error()
		}
	})
}

func (i *Importer) runBatch(ctx context.Context, files []sourceFile) {
	// Re-probe inputs and manual SD repairs on each new batch/retry. A
	// replacement can preserve its size and timestamp, so metadata alone
	// must not retain an earlier validation failure or two-part duration.
	i.validated = map[string]validation{}
	i.update(func(s *Status) {
		s.Phase = "scanning"
		s.SafeToRemove = false
		s.Error = ""
		s.CurrentFile = ""
		s.CopiedBytes = 0
		s.TotalBytes = 0
		if len(files) > 0 {
			s.Results = nil
		}
	})
	present, issues, err := i.local(ctx)
	if err != nil {
		i.update(func(s *Status) {
			s.Phase = "attention"
			s.Error = "Read SD library: " + err.Error()
			s.SafeToRemove = true
		})
		return
	}
	i.setCoverage(present)
	i.update(func(s *Status) { s.Results = append(s.Results, issues...) })
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		i.update(func(s *Status) {
			s.Phase = "scanning"
			s.CurrentFile = f.relative
			s.CopiedBytes = 0
			s.TotalBytes = f.info.Size()
		})
		duration, err := i.duration(ctx, f.path, f.info)
		var ids []episodeid.ID
		reason := ""
		if err == nil {
			ids, reason, err = i.catalog.Match(f.relative, duration)
		}
		if err != nil {
			i.result(Result{Source: f.relative, Status: "needs attention", Message: err.Error()})
			continue
		}
		covered := true
		overlap := false
		for _, id := range ids {
			if _, ok := present[id]; !ok {
				covered = false
			} else {
				overlap = true
			}
		}
		if covered {
			i.result(Result{Source: f.relative, Target: present[ids[0]], Status: "already on SD", Message: "All matched episodes are already present"})
			continue
		}
		if overlap {
			i.result(Result{Source: f.relative, Status: "needs attention", Message: "Combined file overlaps an episode already on SD; supply the missing part separately to avoid duplicate playback"})
			continue
		}
		target := filepath.Join(i.destination, i.catalog.filename(ids, filepath.Ext(f.path)))
		i.update(func(s *Status) { s.Phase = "copying" })
		err = i.copy(ctx, f, target)
		if err != nil {
			i.result(Result{Source: f.relative, Target: target, Status: "failed", Message: err.Error()})
			continue
		}
		info, err := os.Stat(target)
		if err != nil {
			i.result(Result{Source: f.relative, Target: target, Status: "failed", Message: err.Error()})
			continue
		}
		i.validated[target] = validation{info.Size(), info.ModTime(), duration, nil}
		for _, id := range ids {
			present[id] = target
		}
		i.result(Result{Source: f.relative, Target: target, Status: "copied", Message: "Verified SHA-256; matched by " + reason})
		i.setCoverage(present)
		if err := i.persist(); err != nil {
			i.update(func(s *Status) { s.Error = err.Error() })
			i.log(err.Error())
		}
	}
	i.setCoverage(present)
	i.update(func(s *Status) {
		s.CurrentFile = ""
		s.CopiedBytes = 0
		s.TotalBytes = 0
		s.SafeToRemove = true
		s.Phase = "waiting for USB"
		if len(files) > 0 {
			s.Phase = "finished"
		}
		if s.Missing == 0 {
			s.Phase = "complete"
		}
		for _, r := range s.Results {
			if r.Status == "failed" || r.Status == "needs attention" || r.Status == "local error" {
				s.Phase = "attention"
				break
			}
		}
		if s.Error != "" || ctx.Err() != nil {
			s.Phase = "attention"
		}
	})
	if err := i.persist(); err != nil {
		i.update(func(s *Status) { s.Error = err.Error(); s.Phase = "attention" })
		i.log(err.Error())
	}
}

func (i *Importer) result(r Result) {
	i.update(func(s *Status) { s.Results = append(s.Results, r) })
	i.log(fmt.Sprintf("USB import %s: %s — %s", r.Status, r.Source, r.Message))
}

func (i *Importer) copy(ctx context.Context, source sourceFile, target string) (err error) {
	free, err := i.free(i.destination)
	if err != nil {
		return fmt.Errorf("check free SD space: %w", err)
	}
	if free < reserveBytes || uint64(source.info.Size()) > free-reserveBytes {
		return fmt.Errorf("not enough SD space for %s (%s needed plus 512 MiB reserve); use a larger card or free space in %s", source.relative, HumanBytes(uint64(source.info.Size())), i.destination)
	}
	in, err := os.Open(source.path)
	if err != nil {
		return fmt.Errorf("open USB file: %w", err)
	}
	defer in.Close()
	before, err := in.Stat()
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() || before.Size() == 0 {
		return fmt.Errorf("source is not a nonempty regular video file")
	}
	if before.Size() != source.info.Size() || !before.ModTime().Equal(source.info.ModTime()) {
		return fmt.Errorf("USB file changed during scan; rescan after the copy to USB finishes")
	}
	tmp, err := os.CreateTemp(i.destination, ".import-*")
	if err != nil {
		return fmt.Errorf("create SD staging file: %w", err)
	}
	defer func() {
		tmp.Close()
		if removeErr := os.Remove(tmp.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			i.log("Remove import staging file: " + removeErr.Error())
		}
	}()
	hash := sha256.New()
	buf := make([]byte, 1024*1024)
	var copied int64
	lastProgress := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, err := tmp.Write(buf[:n]); err != nil {
				return fmt.Errorf("write SD: %w", err)
			}
			hash.Write(buf[:n])
			copied += int64(n)
			if time.Since(lastProgress) > 200*time.Millisecond {
				i.update(func(s *Status) { s.CopiedBytes = copied })
				lastProgress = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read USB (keep it connected): %w", readErr)
		}
	}
	after, err := in.Stat()
	if err != nil {
		return err
	}
	if copied != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return fmt.Errorf("USB file changed or disconnected during transfer; retry with the complete source file")
	}
	if err = tmp.Chmod(0644); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync SD video: %w", err)
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	check := sha256.New()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, e := tmp.Read(buf)
		if n > 0 {
			check.Write(buf[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return fmt.Errorf("verify SD video: %w", e)
		}
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(check.Sum(nil))) {
		return fmt.Errorf("SD checksum verification failed; source was preserved")
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	// Hard-link publication is atomic and refuses to replace an existing
	// destination. Staging stays invisible to the playlist and is on the
	// same local filesystem. No original USB file is modified or deleted.
	if err = os.Link(tmp.Name(), target); err != nil {
		return fmt.Errorf("publish %s without overwriting: %w", target, err)
	}
	dir, err := os.Open(i.destination)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return fmt.Errorf("sync SD library directory: %w", err)
	}
	return nil
}

func (i *Importer) persist() error {
	if i.report == "" {
		return nil
	}
	data, err := json.MarshalIndent(i.Snapshot(), "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(i.report), ".import-report-*")
	if err != nil {
		return fmt.Errorf("save import report: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err = tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), i.report)
}

func freeSpace(path string) (uint64, error) {
	var s unix.Statfs_t
	err := unix.Statfs(path, &s)
	return s.Bavail * uint64(s.Bsize), err
}

func probeVideo(ctx context.Context, path string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration:stream=codec_type", "-of", "json", path)
	var out strings.Builder
	cmd.Stdout = &out
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("video validation failed (ffprobe): %v; %s", err, strings.TrimSpace(stderr.String()))
	}
	var info struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if err := json.Unmarshal([]byte(out.String()), &info); err != nil {
		return 0, fmt.Errorf("read video metadata: %w", err)
	}
	duration, err := strconv.ParseFloat(info.Format.Duration, 64)
	video := false
	for _, stream := range info.Streams {
		if stream.CodecType == "video" {
			video = true
		}
	}
	if err != nil || duration <= 0 || !video {
		return 0, fmt.Errorf("file has no readable video stream or duration")
	}
	return duration, nil
}

func HumanBytes(n uint64) string { return fmt.Sprintf("%.1f GiB", float64(n)/(1024*1024*1024)) }
