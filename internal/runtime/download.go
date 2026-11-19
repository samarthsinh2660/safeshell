package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Download fetches url to dest, resuming a partial file, and keeps the result
// only if its SHA-256 matches. A mismatch deletes the file: a corrupt or
// tampered download is never used.
func Download(ctx context.Context, client *http.Client, url, dest, wantSHA string, size int64, w io.Writer) error {
	if ok, _ := fileMatches(dest, wantSHA, w); ok {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	part := dest + ".part"
	have := int64(0)
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
		if have > size {
			_ = os.Remove(part)
			have = 0
		}
	}
	if have < size {
		if err := fetchRange(ctx, client, url, part, have, size, w); err != nil {
			return err
		}
	}
	ok, err := fileMatches(part, wantSHA, w)
	if err != nil {
		return err
	}
	if !ok {
		_ = os.Remove(part)
		return fmt.Errorf("the downloaded file does not match its expected checksum, so it was deleted (%s)", url)
	}
	return os.Rename(part, dest)
}

func fetchRange(ctx context.Context, client *http.Client, url, part string, have, size int64, w io.Writer) error {
	if client == nil {
		client = &http.Client{} // no overall timeout: the files are large
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK: // the server ignored the range: start over
		flags |= os.O_TRUNC
		have = 0
	default:
		return fmt.Errorf("download failed: %s answered %s", url, resp.Status)
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	p := newProgress(w, size, have, true)
	// One byte more than expected is enough to tell the server sent too much.
	n, err := io.Copy(io.MultiWriter(f, p), io.LimitReader(resp.Body, size-have+1))
	p.Finish()
	if err == nil && n > size-have {
		_ = os.Remove(part)
		return fmt.Errorf("the server sent more data than expected, so the download was deleted (%s). Try again later", url)
	}
	return err
}

// Progress reports percent done to w: a rewritten line on a terminal, a few
// plain lines (25/50/75/100%) anywhere else, so logs do not fill with "\r".
type Progress struct {
	w           io.Writer
	total, done int64
	base        int64
	start, last time.Time
	rate        bool // also show size, speed and time left
	tty         bool
	shown       int // plain mode: the last percent line printed
}

func newProgress(w io.Writer, total, base int64, rate bool) *Progress {
	return &Progress{w: w, total: total, done: base, base: base, start: time.Now(), rate: rate, tty: isTerminal(w)}
}

func (p *Progress) Write(b []byte) (int, error) {
	p.Update(p.done + int64(len(b)))
	return len(b), nil
}

func (p *Progress) Update(done int64) {
	p.done = done
	if p.w == nil {
		return
	}
	if p.tty {
		if time.Since(p.last) > 300*time.Millisecond {
			p.last = time.Now()
			p.print()
		}
		return
	}
	pct := p.percent()
	for _, t := range []int{25, 50, 75, 100} {
		if t <= pct && t > p.shown {
			p.shown = t
			p.print()
		}
	}
}

func (p *Progress) percent() int { return int(p.done * 100 / max(p.total, 1)) }

func (p *Progress) print() {
	text := fmt.Sprintf("%3d%%", p.percent())
	if p.rate {
		text += fmt.Sprintf("  %s of %s  %.1f MB/s%s", mb(p.done), mb(p.total), p.speed()/1e6, p.timeLeft())
	}
	if p.tty {
		fmt.Fprintf(p.w, "\r  %s   ", text)
	} else {
		fmt.Fprintf(p.w, "  %s\n", text)
	}
}

func (p *Progress) Finish() {
	if p.w != nil && p.tty {
		p.print()
		fmt.Fprintln(p.w)
	}
}

// speed is bytes per second since this run started.
func (p *Progress) speed() float64 {
	el := time.Since(p.start).Seconds()
	if el <= 0 {
		return 0
	}
	return float64(p.done-p.base) / el
}

// timeLeft is an estimate, shown once there are 3 seconds of data.
func (p *Progress) timeLeft() string {
	if time.Since(p.start) < 3*time.Second || p.speed() <= 0 {
		return ""
	}
	secs := int(math.Ceil(float64(p.total-p.done) / p.speed()))
	if secs < 60 {
		return fmt.Sprintf("  %d s left", max(secs, 1))
	}
	return fmt.Sprintf("  about %d min left", (secs+59)/60)
}

// isTerminal reports whether w is a terminal. Anything that is not a file
// (a buffer, a pipe wrapper) counts as not a terminal.
func isTerminal(w io.Writer) bool {
	if t, ok := w.(interface{ Terminal() bool }); ok {
		return t.Terminal()
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// NewPercent reports progress of a job measured in percent (0 to 100).
func NewPercent(w io.Writer) *Progress { return newProgress(w, 100, 0, false) }

func mb(n int64) string { return fmt.Sprintf("%.0f MB", float64(n)/1e6) }

// fileSize is the size of f, or 0 when it cannot be read (progress then
// shows 0%).
func fileSize(f *os.File) int64 {
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
	return fi.Size()
}

// fileMatches hashes path, showing Progress on w (which may be nil).
func fileMatches(path, wantSHA string, w io.Writer) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if w != nil {
		fmt.Fprintln(w, "Checking the download...")
	}
	p := newProgress(w, fileSize(f), 0, false)
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(h, p), f); err != nil {
		return false, err
	}
	p.Finish()
	return hex.EncodeToString(h.Sum(nil)) == wantSHA, nil
}
