package ass

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const jobOutputReplayLimit = 4 * 1024 * 1024

type jobRegistry struct {
	mu   sync.Mutex
	jobs map[string]*jobSession
}

type jobSession struct {
	registry *jobRegistry
	id       string
	kind     string

	mu          sync.Mutex
	outputWG    sync.WaitGroup
	cmd         *exec.Cmd
	outputPath  string
	outputSize  int64
	replay      []byte
	done        bool
	exitCode    int
	lastError   string
	subscribers map[int]chan jobStreamEvent
	nextSubID   int
}

type jobStreamEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
	Done  bool   `json:"done,omitempty"`
	Error string `json:"error,omitempty"`
}

type jobCreateRequest struct {
	ID    string            `json:"id"`
	Kind  string            `json:"kind"`
	Argv  []string          `json:"argv"`
	CWD   string            `json:"cwd,omitempty"`
	Env   map[string]string `json:"env,omitempty"`
	Stdin string            `json:"stdin,omitempty"`
}

type jobInfo struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Active     bool   `json:"active"`
	Done       bool   `json:"done"`
	OutputSize int64  `json:"output_size"`
	ExitCode   int    `json:"exit_code,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

func newJobRegistry() *jobRegistry {
	return &jobRegistry{jobs: make(map[string]*jobSession)}
}

func normalizeJobID(value string) string {
	return normalizeShellSessionID(value)
}

func (server *Server) handleJobsCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		server.handleListJobs(w, r)
	case http.MethodPost:
		server.handleCreateJob(w, r)
	default:
		server.writeError(w, r, newAPIError(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"))
	}
}

func (server *Server) handleJobItem(w http.ResponseWriter, r *http.Request) {
	remainder := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	if strings.HasSuffix(remainder, "/stream") {
		id := normalizeJobID(strings.TrimSuffix(remainder, "/stream"))
		server.handleJobStream(w, r, id)
		return
	}
	id := normalizeJobID(remainder)
	if id == "" {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "job_invalid", "job id is required"))
		return
	}
	if r.Method != http.MethodGet {
		server.writeError(w, r, newAPIError(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"))
		return
	}
	job := server.jobs.get(id)
	if job == nil {
		server.writeError(w, r, newAPIError(http.StatusNotFound, "job_not_found", "job was not found"))
		return
	}
	server.writeOK(w, r, job.info())
}

func (server *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	items := server.jobs.list()
	server.writeOK(w, r, map[string]any{"jobs": items})
}

func (server *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var request jobCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&request); err != nil {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "encoding_invalid", "job request is invalid"))
		return
	}
	id := normalizeJobID(request.ID)
	if id == "" || len(request.Argv) == 0 {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "job_invalid", "job id and argv are required"))
		return
	}
	cwd := strings.TrimSpace(request.CWD)
	if cwd == "" {
		cwd = server.workspaceRootReal
	}
	job, created, err := server.jobs.start(id, strings.TrimSpace(request.Kind), cwd, request.Argv, request.Env, request.Stdin)
	if err != nil {
		server.writeError(w, r, newAPIError(http.StatusInternalServerError, "job_start_failed", err.Error()))
		return
	}
	statusCode := http.StatusOK
	if created {
		statusCode = http.StatusCreated
	}
	w.WriteHeader(statusCode)
	server.writeOK(w, r, job.info())
}

func (registry *jobRegistry) get(id string) *jobSession {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.jobs[id]
}

func (registry *jobRegistry) list() []jobInfo {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	items := make([]jobInfo, 0, len(registry.jobs))
	for _, job := range registry.jobs {
		items = append(items, job.info())
	}
	return items
}

func (registry *jobRegistry) start(id, kind, cwd string, argv []string, env map[string]string, stdin string) (*jobSession, bool, error) {
	registry.mu.Lock()
	if existing := registry.jobs[id]; existing != nil && !existing.isDone() {
		registry.mu.Unlock()
		return existing, false, nil
	}
	registry.mu.Unlock()

	outputDir, err := ensureJobOutputDir(cwd)
	if err != nil {
		return nil, false, err
	}
	outputPath := filepath.Join(outputDir, id+".log")
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = mergeJobEnv(os.Environ(), env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if strings.TrimSpace(stdin) != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	outputFile, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, false, err
	}
	pipeReader, pipeWriter := io.Pipe()
	cmd.Stdout = pipeWriter
	cmd.Stderr = pipeWriter
	job := &jobSession{
		registry:    registry,
		id:          id,
		kind:        kind,
		cmd:         cmd,
		outputPath:  outputPath,
		subscribers: make(map[int]chan jobStreamEvent),
	}
	registry.mu.Lock()
	if existing := registry.jobs[id]; existing != nil && !existing.isDone() {
		registry.mu.Unlock()
		_ = outputFile.Close()
		_ = pipeWriter.Close()
		return existing, false, nil
	}
	registry.jobs[id] = job
	registry.mu.Unlock()
	if err := cmd.Start(); err != nil {
		registry.mu.Lock()
		delete(registry.jobs, id)
		registry.mu.Unlock()
		_ = outputFile.Close()
		_ = pipeWriter.Close()
		return nil, false, err
	}
	job.outputWG.Add(1)
	go job.copyOutput(pipeReader, outputFile)
	go job.waitAndClose(pipeWriter)
	return job, true, nil
}

func ensureJobOutputDir(cwd string) (string, error) {
	primary := filepath.Join("/run/aiyolo", "jobs")
	if err := os.MkdirAll(primary, 0o755); err == nil {
		return primary, nil
	}
	fallbackRoot := strings.TrimSpace(cwd)
	if fallbackRoot == "" {
		fallbackRoot = os.TempDir()
	}
	fallback := filepath.Join(fallbackRoot, ".aiyolo", "jobs")
	if err := os.MkdirAll(fallback, 0o755); err != nil {
		return "", err
	}
	return fallback, nil
}

func mergeJobEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	env := append([]string(nil), base...)
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}

func (job *jobSession) copyOutput(reader *io.PipeReader, file *os.File) {
	defer job.outputWG.Done()
	defer reader.Close()
	defer file.Close()
	buffered := bufio.NewReaderSize(reader, 64*1024)
	for {
		line, err := buffered.ReadBytes('\n')
		if len(line) > 0 {
			_, _ = file.Write(line)
			job.appendDelta(string(line))
		}
		if err != nil {
			if err != io.EOF && !isJobPipeInfrastructureError(err) {
				job.appendDelta("\n")
			}
			return
		}
	}
}

func (job *jobSession) waitAndClose(pipe *io.PipeWriter) {
	err := job.cmd.Wait()
	_ = pipe.Close()
	job.outputWG.Wait()
	exitCode := 0
	lastError := ""
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if !isJobPipeInfrastructureError(err) {
			exitCode = 1
			lastError = err.Error()
		}
	}
	job.finish(exitCode, lastError)
}

func isJobPipeInfrastructureError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "closed pipe") || strings.Contains(message, "broken pipe")
}

func (job *jobSession) finish(exitCode int, lastError string) {
	job.mu.Lock()
	if job.done {
		job.mu.Unlock()
		return
	}
	job.done = true
	job.exitCode = exitCode
	job.lastError = strings.TrimSpace(lastError)
	subscribers := make([]chan jobStreamEvent, 0, len(job.subscribers))
	for _, subscriber := range job.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	job.subscribers = make(map[int]chan jobStreamEvent)
	job.mu.Unlock()
	event := jobStreamEvent{Type: "done", Done: true}
	if exitCode != 0 && lastError != "" {
		event = jobStreamEvent{Type: "error", Error: lastError, Done: true}
	}
	for _, subscriber := range subscribers {
		select {
		case subscriber <- event:
		default:
		}
		close(subscriber)
	}
}

func (job *jobSession) appendDelta(delta string) {
	if delta == "" {
		return
	}
	job.mu.Lock()
	job.replay = append(job.replay, []byte(delta)...)
	if len(job.replay) > jobOutputReplayLimit {
		job.replay = append([]byte(nil), job.replay[len(job.replay)-jobOutputReplayLimit:]...)
	}
	job.outputSize += int64(len(delta))
	subscribers := make([]chan jobStreamEvent, 0, len(job.subscribers))
	for _, subscriber := range job.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	job.mu.Unlock()
	event := jobStreamEvent{Type: "delta", Delta: delta}
	for _, subscriber := range subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (job *jobSession) info() jobInfo {
	job.mu.Lock()
	defer job.mu.Unlock()
	return jobInfo{
		ID:         job.id,
		Kind:       job.kind,
		Active:     !job.done,
		Done:       job.done,
		OutputSize: job.outputSize,
		ExitCode:   job.exitCode,
		LastError:  job.lastError,
	}
}

func (job *jobSession) isDone() bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.done
}

func (job *jobSession) subscribe() (int, <-chan jobStreamEvent, string, bool) {
	ch := make(chan jobStreamEvent, 128)
	job.mu.Lock()
	defer job.mu.Unlock()
	replay := string(job.replay)
	if job.done {
		return 0, ch, replay, true
	}
	job.nextSubID++
	id := job.nextSubID
	job.subscribers[id] = ch
	return id, ch, replay, false
}

func (job *jobSession) unsubscribe(id int) {
	if id == 0 {
		return
	}
	job.mu.Lock()
	delete(job.subscribers, id)
	job.mu.Unlock()
}

func (server *Server) handleJobStream(w http.ResponseWriter, r *http.Request, id string) {
	job := server.jobs.get(id)
	if job == nil {
		http.Error(w, "job was not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	flusher, _ := w.(http.Flusher)
	writeEvent := func(event jobStreamEvent) error {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(payload, '\n')); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	subscriberID, events, replay, done := job.subscribe()
	defer job.unsubscribe(subscriberID)
	if replay != "" {
		if err := writeEvent(jobStreamEvent{Type: "sync", Delta: replay}); err != nil {
			return
		}
	}
	if done {
		info := job.info()
		if info.ExitCode != 0 && info.LastError != "" {
			_ = writeEvent(jobStreamEvent{Type: "error", Error: info.LastError, Done: true})
			return
		}
		_ = writeEvent(jobStreamEvent{Type: "done", Done: true})
		return
	}
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := writeEvent(event); err != nil {
				return
			}
			if event.Done || event.Type == "done" || event.Type == "error" {
				return
			}
		}
	}
}

func (job *jobSession) snapshotReplay() string {
	job.mu.Lock()
	defer job.mu.Unlock()
	return string(job.replay)
}

func waitForJobDone(ctx context.Context, job *jobSession, interval time.Duration) bool {
	if job == nil {
		return true
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if job.isDone() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}
