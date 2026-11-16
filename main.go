// goyt is a tiny web frontend for yt-dlp.
package main

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var static embed.FS

type options struct {
	URL         string `json:"url"`
	Mode        string `json:"mode"`        // "video" or "audio"
	MaxHeight   string `json:"maxHeight"`   // "best", "2160", "1440", "1080", "720", "480", "360"
	AudioFormat string `json:"audioFormat"` // "best", "mp3", "m4a", "opus", "flac"
	Container   string `json:"container"`   // "auto" or "mp4"
}

type job struct {
	ID       int       `json:"id"`
	Opts     options   `json:"opts"`
	Status   string    `json:"status"` // queued, running, done, failed
	Last     string    `json:"last"`   // last line of yt-dlp output
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
}

type server struct {
	ytdlp string
	dir   string
	sem   chan struct{}

	mu     sync.Mutex
	nextID int
	jobs   []*job
}

var (
	validHeights = map[string]bool{"best": true, "2160": true, "1440": true, "1080": true, "720": true, "480": true, "360": true}
	validAudio   = map[string]bool{"best": true, "mp3": true, "m4a": true, "opus": true, "flac": true}
)

func (o *options) normalize() error {
	if o.Mode == "" {
		o.Mode = "video"
	}
	if o.MaxHeight == "" {
		o.MaxHeight = "best"
	}
	if o.AudioFormat == "" {
		o.AudioFormat = "best"
	}
	if o.Container == "" {
		o.Container = "auto"
	}
	u, err := url.Parse(strings.TrimSpace(o.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("that doesn't look like an http(s) link")
	}
	o.URL = u.String()
	switch {
	case o.Mode != "video" && o.Mode != "audio":
		return errors.New("bad mode")
	case !validHeights[o.MaxHeight]:
		return errors.New("bad quality")
	case !validAudio[o.AudioFormat]:
		return errors.New("bad audio format")
	case o.Container != "auto" && o.Container != "mp4":
		return errors.New("bad container")
	}
	return nil
}

// args builds the yt-dlp command line. Defaults grab the best video + best audio.
func (o options) args(dir string) []string {
	// Audio gets its own name so it can't clobber (or get treated as) the video file.
	name := "%(title).200B [%(id)s].%(ext)s"
	if o.Mode == "audio" {
		name = "%(title).200B [%(id)s] (audio).%(ext)s"
	}
	a := []string{
		"--newline", "--no-playlist", "--no-mtime",
		"-P", dir,
		"-o", name,
	}
	if o.Mode == "audio" {
		a = append(a, "-f", "ba/b", "-x", "--audio-quality", "0")
		if o.AudioFormat != "best" {
			a = append(a, "--audio-format", o.AudioFormat)
		}
	} else {
		if o.MaxHeight == "best" {
			a = append(a, "-f", "bv*+ba/b")
		} else {
			h := o.MaxHeight
			a = append(a, "-f", fmt.Sprintf("bv*[height<=%s]+ba/b[height<=%s]/b", h, h))
		}
		if o.Container == "mp4" {
			a = append(a, "--merge-output-format", "mp4", "--remux-video", "mp4")
		}
	}
	// "--" so a URL can never be read as a flag.
	return append(a, "--", o.URL)
}

func (s *server) run(j *job) {
	s.sem <- struct{}{}
	defer func() { <-s.sem }()
	s.update(j, func(j *job) { j.Status = "running" })

	cmd := exec.Command(s.ytdlp, j.Opts.args(s.dir)...)
	pr, pw, _ := os.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	err := cmd.Start()
	pw.Close()
	if err == nil {
		sc := bufio.NewScanner(pr)
		sc.Split(scanLinesCR)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "" {
				s.update(j, func(j *job) { j.Last = line })
			}
		}
		err = cmd.Wait()
	}
	pr.Close()
	s.update(j, func(j *job) {
		j.Finished = time.Now()
		if err != nil {
			j.Status = "failed"
			if j.Last == "" {
				j.Last = err.Error()
			}
		} else {
			j.Status = "done"
		}
	})
	log.Printf("job %d %s: %s", j.ID, j.Status, j.Opts.URL)
}

// scanLinesCR splits on \n or \r so progress lines show up as they're printed.
func scanLinesCR(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func (s *server) update(j *job, f func(*job)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(j)
}

func (s *server) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		out := make([]job, 0, len(s.jobs))
		for i := len(s.jobs) - 1; i >= 0; i-- {
			out = append(out, *s.jobs[i])
		}
		s.mu.Unlock()
		writeJSON(w, out)
	case http.MethodPost:
		var o options
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&o); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := o.normalize(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.nextID++
		j := &job{ID: s.nextID, Opts: o, Status: "queued", Started: time.Now()}
		s.jobs = append(s.jobs, j)
		if len(s.jobs) > 200 {
			s.jobs = s.jobs[len(s.jobs)-200:]
		}
		s.mu.Unlock()
		go s.run(j)
		writeJSON(w, j)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type fileInfo struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func (s *server) handleFiles(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	files := []fileInfo{}
	for _, e := range entries {
		// skip dirs and yt-dlp's in-progress files
		if e.IsDir() || strings.HasSuffix(e.Name(), ".part") || strings.HasSuffix(e.Name(), ".ytdl") || strings.Contains(e.Name(), ".temp.") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{e.Name(), info.Size(), info.ModTime()})
	}
	sort.Slice(files, func(i, k int) bool { return files[i].Modified.After(files[k].Modified) })
	writeJSON(w, files)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func main() {
	var (
		listen   = flag.String("listen", ":9090", "address to listen on")
		dir      = flag.String("dir", "downloads", "where downloads are saved")
		ytdlp    = flag.String("yt-dlp", "yt-dlp", "path to yt-dlp (or youtube-dl)")
		parallel = flag.Int("parallel", 2, "max downloads at once")
	)
	flag.Parse()

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}
	abs, _ := filepath.Abs(*dir)
	s := &server{ytdlp: *ytdlp, dir: abs, sem: make(chan struct{}, max(*parallel, 1))}

	mux := http.NewServeMux()
	mux.Handle("GET /{$}", http.FileServerFS(static))
	mux.HandleFunc("/api/jobs", s.handleJobs)
	mux.HandleFunc("GET /api/files", s.handleFiles)
	mux.Handle("GET /files/", http.StripPrefix("/files/", http.FileServer(http.Dir(abs))))

	log.Printf("listening on %s, saving to %s", *listen, abs)
	log.Fatal(http.ListenAndServe(*listen, mux))
}
