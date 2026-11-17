package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- prune ----

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	s := &server{dir: dir, keep: 3}

	names := []string{"a.mp3", "b.mp3", "c.mp3", "d.mp3", "e.mp3"}
	base := time.Now().Add(-time.Hour)
	for i, name := range names {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		mod := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}

	s.prune()

	want := map[string]bool{"c.mp3": true, "d.mp3": true, "e.mp3": true}
	assertDirContents(t, dir, want)
}

func TestPruneSkipsInProgressFiles(t *testing.T) {
	dir := t.TempDir()
	s := &server{dir: dir, keep: 1}

	finished := filepath.Join(dir, "video.mp3")
	if err := os.WriteFile(finished, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(finished, old, old); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"video.mp3.part", "video.mp3.ytdl", "video.f137.temp.webm"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s.prune()

	assertDirContents(t, dir, map[string]bool{
		"video.mp3": true, "video.mp3.part": true, "video.mp3.ytdl": true, "video.f137.temp.webm": true,
	})
}

func TestPruneKeepZeroDisables(t *testing.T) {
	dir := t.TempDir()
	s := &server{dir: dir, keep: 0}

	for i := 0; i < 15; i++ {
		p := filepath.Join(dir, string(rune('a'+i))+".mp3")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s.prune()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 15 {
		t.Errorf("keep=0 should prune nothing, got %d files left", len(entries))
	}
}

func TestPruneNegativeKeepDisables(t *testing.T) {
	dir := t.TempDir()
	s := &server{dir: dir, keep: -1}
	p := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.prune()
	assertDirContents(t, dir, map[string]bool{"a.mp3": true})
}

func TestPruneNoopWhenUnderLimit(t *testing.T) {
	dir := t.TempDir()
	s := &server{dir: dir, keep: 10}
	p := filepath.Join(dir, "a.mp3")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.prune()
	assertDirContents(t, dir, map[string]bool{"a.mp3": true})
}

func assertDirContents(t *testing.T, dir string, want map[string]bool) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = true
	}
	if len(got) != len(want) {
		t.Errorf("dir contents %v, want %v", got, want)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("expected %s to be kept, got %v", name, got)
		}
	}
}

// ---- options.normalize ----

func TestNormalizeDefaults(t *testing.T) {
	o := options{URL: "https://example.com/v"}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if o.Mode != "video" || o.MaxHeight != "best" || o.AudioFormat != "best" || o.Container != "auto" {
		t.Errorf("defaults not applied: %+v", o)
	}
}

func TestNormalizeRejects(t *testing.T) {
	cases := []struct {
		name string
		o    options
	}{
		{"no url", options{}},
		{"non-http scheme", options{URL: "ftp://example.com/v"}},
		{"no host", options{URL: "https:///v"}},
		{"bad mode", options{URL: "https://e.com/v", Mode: "flac"}},
		{"bad quality", options{URL: "https://e.com/v", MaxHeight: "999"}},
		{"bad audio", options{URL: "https://e.com/v", Mode: "audio", AudioFormat: "wav"}},
		{"bad container", options{URL: "https://e.com/v", Container: "mkv"}},
	}
	for _, c := range cases {
		if err := c.o.normalize(); err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestNormalizeTrimsAndCanonicalizesURL(t *testing.T) {
	o := options{URL: "  https://example.com/v?a=1 \n"}
	if err := o.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if o.URL != "https://example.com/v?a=1" {
		t.Errorf("URL not trimmed/canonicalized: %q", o.URL)
	}
}

// ---- options.args ----

func TestArgsVideoDefaults(t *testing.T) {
	o := options{URL: "https://e.com/v", Mode: "video", MaxHeight: "best", Container: "auto"}
	a := o.args("/tmp/dl")
	joined := strings.Join(a, " ")
	for _, want := range []string{"--newline", "--no-playlist", "--no-mtime", "-P /tmp/dl", "--", "https://e.com/v"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, a)
		}
	}
	// URL must come last, after "--", so it can't be read as a flag.
	if a[len(a)-2] != "--" || a[len(a)-1] != "https://e.com/v" {
		t.Errorf("URL not safely terminated: %v", a)
	}
}

func TestArgsAudio(t *testing.T) {
	o := options{URL: "https://e.com/v", Mode: "audio", AudioFormat: "mp3"}
	a := o.args("/tmp/dl")
	joined := strings.Join(a, " ")
	for _, want := range []string{"-f ba/b", "-x", "--audio-quality 0", "--audio-format mp3", "(audio)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("audio args missing %q: %v", want, a)
		}
	}
}

func TestArgsVideoHeightAndMp4(t *testing.T) {
	o := options{URL: "https://e.com/v", Mode: "video", MaxHeight: "720", Container: "mp4"}
	joined := strings.Join(o.args("/tmp/dl"), " ")
	if !strings.Contains(joined, "height<=720") {
		t.Errorf("height cap missing: %s", joined)
	}
	if !strings.Contains(joined, "--merge-output-format mp4") || !strings.Contains(joined, "--remux-video mp4") {
		t.Errorf("mp4 container flags missing: %s", joined)
	}
}

func TestArgsURLStartingWithDash(t *testing.T) {
	// A URL like "-x" must still land after "--".
	o := options{URL: "https://e.com/-weird", Mode: "video", MaxHeight: "best", Container: "auto"}
	a := o.args("/tmp/dl")
	if a[len(a)-2] != "--" {
		t.Errorf("missing -- before URL: %v", a)
	}
}

// ---- HTTP handlers ----

func newTestServer(t *testing.T) (*server, string) {
	t.Helper()
	dir := t.TempDir()
	s := &server{ytdlp: "true", dir: dir, keep: 10, sem: make(chan struct{}, 1), nextID: 0}
	return s, dir
}

func TestHandleJobsPostValid(t *testing.T) {
	s, _ := newTestServer(t)
	body := `{"url":"https://example.com/v"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
	s.handleJobs(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var j job
	if err := json.NewDecoder(w.Body).Decode(&j); err != nil {
		t.Fatal(err)
	}
	if j.ID != 1 || j.Opts.Mode != "video" {
		t.Errorf("unexpected job: %+v", j)
	}
	if j.Status != "queued" && j.Status != "running" {
		t.Errorf("status %q, want queued or running", j.Status)
	}
}

func TestHandleJobsPostInvalid(t *testing.T) {
	s, _ := newTestServer(t)
	for _, body := range []string{`{`, `{"url":""}`, `{"url":"https://e.com/v","mode":"nope"}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
		s.handleJobs(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q: status %d, want 400", body, w.Code)
		}
	}
}

func TestHandleJobsGetOrder(t *testing.T) {
	s, _ := newTestServer(t)
	// Post two jobs but stop them fast by using a no-op binary.
	for range 2 {
		w := httptest.NewRecorder()
		s.handleJobs(w, httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(`{"url":"https://e.com/v"}`)))
	}
	w := httptest.NewRecorder()
	s.handleJobs(w, httptest.NewRequest(http.MethodGet, "/jobs", nil))
	var jobs []job
	if err := json.NewDecoder(w.Body).Decode(&jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("got %d jobs, want 2", len(jobs))
	}
	// Newest first.
	if jobs[0].ID != 2 || jobs[1].ID != 1 {
		t.Errorf("order wrong: %d then %d", jobs[0].ID, jobs[1].ID)
	}
}

func TestHandleJobsJobCap(t *testing.T) {
	s, _ := newTestServer(t)
	for range 205 {
		s.handleJobs(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(`{"url":"https://e.com/v"}`)))
	}
	s.mu.Lock()
	n := len(s.jobs)
	s.mu.Unlock()
	if n != 200 {
		t.Errorf("job list capped at 200, got %d", n)
	}
}

func TestHandleJobsMethodNotAllowed(t *testing.T) {
	s, _ := newTestServer(t)
	w := httptest.NewRecorder()
	s.handleJobs(w, httptest.NewRequest(http.MethodDelete, "/jobs", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status %d, want 405", w.Code)
	}
}

func TestHandleFiles(t *testing.T) {
	s, dir := newTestServer(t)
	base := time.Now().Add(-time.Hour)
	names := []string{"new.mp3", "old.mp3", "skip.part", "skip.ytdl", "skip.temp.webm"}
	for i, name := range names {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("xy"), 0o644); err != nil {
			t.Fatal(err)
		}
		mod := base.Add(time.Duration(len(names)-i) * time.Minute) // new.mp3 (i=0) ends up newest
		os.Chtimes(p, mod, mod)
	}

	w := httptest.NewRecorder()
	s.handleFiles(w, httptest.NewRequest(http.MethodGet, "/files", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var files []fileInfo
	if err := json.NewDecoder(w.Body).Decode(&files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2: %+v", len(files), files)
	}
	if files[0].Name != "new.mp3" || files[1].Name != "old.mp3" {
		t.Errorf("not sorted newest first: %+v", files)
	}
	if files[0].Size != 2 {
		t.Errorf("size wrong: %+v", files[0])
	}
}

func TestRunWithFakeYtdlp(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-ytdlp")
	code := "#!/bin/sh\nwhile getopts x flag; do :; done\nwhile [[ \"$1\" != \"--\" ]]; do shift; done\necho '[download] 50% of 1MiB'\necho 'done'\ntouch \"$TMPDIR_DIR/out.mp3\"\n"
	// write output file into the -P dir: find it from args
	code = `#!/bin/sh
dir=""
prev=""
for a in "$@"; do
  if [[ "$prev" == "-P" ]]; then dir="$a"; fi
  prev="$a"
done
echo "[download] starting"
echo "[download] done"
printf 'audio' > "$dir/out [abc123].mp3"
`
	if err := os.WriteFile(script, []byte(code), 0o755); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	s := &server{ytdlp: script, dir: dir, keep: 10, sem: make(chan struct{}, 1)}
	j := &job{ID: 1, Opts: options{URL: "https://e.com/v", Mode: "video", MaxHeight: "best", Container: "auto"}, Status: "queued"}
	s.jobs = append(s.jobs, j)
	s.run(j)

	if j.Status != "done" {
		t.Fatalf("status %s, want done (last=%q)", j.Status, j.Last)
	}
	if j.Last != "[download] done" {
		t.Errorf("last line %q", j.Last)
	}
	if j.Finished.IsZero() {
		t.Error("Finished not set")
	}
	if _, err := os.Stat(filepath.Join(dir, "out [abc123].mp3")); err != nil {
		t.Errorf("output file missing: %v", err)
	}
}

func TestRunWithFailingYtdlp(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-ytdlp")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'video unavailable' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := &server{ytdlp: script, dir: dir, keep: 10, sem: make(chan struct{}, 1)}
	j := &job{ID: 1, Opts: options{URL: "https://e.com/v", Mode: "video", MaxHeight: "best", Container: "auto"}, Status: "queued"}
	s.jobs = append(s.jobs, j)
	s.run(j)

	if j.Status != "failed" {
		t.Fatalf("status %s, want failed", j.Status)
	}
	if j.Last != "video unavailable" {
		t.Errorf("last %q, want yt-dlp's error line", j.Last)
	}
	if j.Finished.IsZero() {
		t.Error("Finished not set")
	}
}

func TestRunPrunesAfterSuccess(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-ytdlp")
	code := `#!/bin/sh
dir=""
prev=""
for a in "$@"; do
  if [[ "$prev" == "-P" ]]; then dir="$a"; fi
  prev="$a"
done
printf 'new' > "$dir/new.mp3"
`
	if err := os.WriteFile(script, []byte(code), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// Pre-fill the cache past the limit with older files.
	for i := range 12 {
		p := filepath.Join(dir, string(rune('a'+i))+".mp3")
		os.WriteFile(p, []byte("x"), 0o644)
		mod := time.Now().Add(-2 * time.Hour).Add(time.Duration(i) * time.Minute)
		os.Chtimes(p, mod, mod)
	}
	s := &server{ytdlp: script, dir: dir, keep: 10, sem: make(chan struct{}, 1)}
	j := &job{ID: 1, Opts: options{URL: "https://e.com/v", Mode: "video", MaxHeight: "best", Container: "auto"}, Status: "queued"}
	s.jobs = append(s.jobs, j)
	s.run(j)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 10 {
		t.Errorf("got %d files, want 10 after prune", len(entries))
	}
	found := false
	for _, e := range entries {
		if e.Name() == "new.mp3" {
			found = true
		}
	}
	if !found {
		t.Error("new download was pruned, should have been kept")
	}
}

func TestRunDoesNotPruneAfterFailure(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-ytdlp")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for i := range 12 {
		p := filepath.Join(dir, string(rune('a'+i))+".mp3")
		os.WriteFile(p, []byte("x"), 0o644)
	}
	s := &server{ytdlp: script, dir: dir, keep: 10, sem: make(chan struct{}, 1)}
	j := &job{ID: 1, Opts: options{URL: "https://e.com/v", Mode: "video", MaxHeight: "best", Container: "auto"}, Status: "queued"}
	s.jobs = append(s.jobs, j)
	s.run(j)

	entries, _ := os.ReadDir(dir)
	if len(entries) != 12 {
		t.Errorf("failed job should not prune, got %d files", len(entries))
	}
}
