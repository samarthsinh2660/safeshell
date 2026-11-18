package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Download fetches url to dest, resuming a partial file, and keeps the result
// only if its SHA-256 matches. A mismatch deletes the file: a corrupt or
// tampered download is never used.
func Download(ctx context.Context, client *http.Client, url, dest, wantSHA string, size int64, w io.Writer) error {
	if ok, _ := fileMatches(dest, wantSHA); ok {
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
	ok, err := fileMatches(part, wantSHA)
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
	p := &progress{w: w, total: size, done: have, start: time.Now(), base: have}
	// One byte more than expected is enough to tell the server sent too much.
	n, err := io.Copy(io.MultiWriter(f, p), io.LimitReader(resp.Body, size-have+1))
	p.finish()
	if err == nil && n > size-have {
		_ = os.Remove(part)
		return fmt.Errorf("the server sent more data than expected, so the download was deleted (%s). Try again later", url)
	}
	return err
}

type progress struct {
	w           io.Writer
	total, done int64
	base        int64
	start, last time.Time
}

func (p *progress) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.w != nil && time.Since(p.last) > 300*time.Millisecond {
		p.last = time.Now()
		p.print()
	}
	return len(b), nil
}

func (p *progress) print() {
	rate := float64(p.done-p.base) / time.Since(p.start).Seconds() / 1e6
	fmt.Fprintf(p.w, "\r  %3d%%  %s of %s  %.1f MB/s   ", p.done*100/max(p.total, 1), mb(p.done), mb(p.total), rate)
}

func (p *progress) finish() {
	if p.w != nil {
		p.print()
		fmt.Fprintln(p.w)
	}
}

func mb(n int64) string { return fmt.Sprintf("%.0f MB", float64(n)/1e6) }

func fileMatches(path, wantSHA string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == wantSHA, nil
}
