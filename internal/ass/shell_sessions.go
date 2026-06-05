package ass

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/net/websocket"
)

const shellSessionReplayLimit = 512 * 1024

type shellSessionRegistry struct {
	mu       sync.Mutex
	sessions map[string]*shellSession
}

type shellSession struct {
	registry *shellSessionRegistry
	id       string
	cwd      string

	mu            sync.Mutex
	pty           *os.File
	cmd           *exec.Cmd
	replay        []byte
	done          bool
	closedMessage string
	subscribers   map[int]chan shellSessionEvent
	nextSubID     int
}

type shellSessionEvent struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	Message string `json:"message,omitempty"`
}

type shellSessionCreateRequest struct {
	ID   string `json:"id"`
	CWD  string `json:"cwd,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type shellSessionInfo struct {
	ID     string `json:"id"`
	CWD    string `json:"cwd"`
	Active bool   `json:"active"`
}

type shellSessionSocketRequest struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

func newShellSessionRegistry() *shellSessionRegistry {
	return &shellSessionRegistry{sessions: make(map[string]*shellSession)}
}

func normalizeShellSessionID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range trimmed {
		if r <= 32 || strings.ContainsRune("/?#&=\\", r) {
			continue
		}
		builder.WriteRune(r)
		if builder.Len() >= 80 {
			break
		}
	}
	return builder.String()
}

func (server *Server) handleShellSessionsCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		server.handleListShellSessions(w, r)
	case http.MethodPost:
		server.handleCreateShellSession(w, r)
	default:
		server.writeError(w, r, newAPIError(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"))
	}
}

func (server *Server) handleShellSessionItem(w http.ResponseWriter, r *http.Request) {
	id := normalizeShellSessionID(strings.TrimPrefix(r.URL.Path, "/v1/shell/sessions/"))
	if id == "" {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "session_invalid", "shell session id is required"))
		return
	}
	if strings.HasSuffix(r.URL.Path, "/ws") {
		id = normalizeShellSessionID(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/shell/sessions/"), "/ws"))
		server.handleShellSessionWebSocket(w, r, id)
		return
	}
	if r.Method != http.MethodGet {
		server.writeError(w, r, newAPIError(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"))
		return
	}
	session := server.shellSessions.get(id)
	if session == nil {
		server.writeError(w, r, newAPIError(http.StatusNotFound, "session_not_found", "shell session was not found"))
		return
	}
	server.writeOK(w, r, session.info())
}

func (server *Server) handleListShellSessions(w http.ResponseWriter, r *http.Request) {
	items := server.shellSessions.list()
	server.writeOK(w, r, map[string]any{"sessions": items})
}

func (server *Server) handleCreateShellSession(w http.ResponseWriter, r *http.Request) {
	var request shellSessionCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&request); err != nil {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "encoding_invalid", "shell session request is invalid"))
		return
	}
	id := normalizeShellSessionID(request.ID)
	if id == "" {
		server.writeError(w, r, newAPIError(http.StatusBadRequest, "session_invalid", "shell session id is required"))
		return
	}
	cwd := strings.TrimSpace(request.CWD)
	if cwd == "" {
		cwd = server.workspaceRootReal
	}
	session, created, err := server.shellSessions.getOrCreate(id, cwd, request.Cols, request.Rows, server.startShellSessionCommand)
	if err != nil {
		server.writeError(w, r, newAPIError(http.StatusInternalServerError, "session_start_failed", err.Error()))
		return
	}
	statusCode := http.StatusOK
	if created {
		statusCode = http.StatusCreated
	}
	w.WriteHeader(statusCode)
	server.writeOK(w, r, session.info())
}

func (server *Server) startShellSessionCommand(cwd string) (*exec.Cmd, error) {
	shellPath := "/bin/bash"
	if _, err := os.Stat(shellPath); err != nil {
		return nil, err
	}
	cmd := exec.Command(shellPath, "--rcfile", server.shellRCPath(), "-i")
	cmd.Dir = cwd
	cmd.Env = server.shellSessionEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, nil
}

func (server *Server) shellRCPath() string {
	rcDir := strings.TrimSpace(server.execHome) + "/.cache/aiyolo"
	_ = os.MkdirAll(rcDir, 0o755)
	rcPath := rcDir + "/chat-shell-bashrc"
	_ = os.WriteFile(rcPath, []byte(shellSessionBashRC), 0o644)
	return rcPath
}

func (server *Server) shellSessionEnv() []string {
	env := os.Environ()
	extra := map[string]string{
		"HOME":               server.execHome,
		"USER":               server.execUser,
		"TERM":               "xterm-256color",
		"COLORTERM":          "truecolor",
		"SHELL":              "/bin/bash",
		"LANG":               "C.UTF-8",
		"LC_ALL":             "C.UTF-8",
		"CLICOLOR":           "1",
		"CLICOLOR_FORCE":     "1",
		"FORCE_COLOR":        "1",
		"npm_config_color":   "always",
	}
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}

const shellSessionBashRC = `force_color_prompt=yes
export TERM="${TERM:-xterm-256color}"
export COLORTERM="${COLORTERM:-truecolor}"
if [[ -r "$HOME/.bashrc" ]]; then
  . "$HOME/.bashrc"
fi
alias ls='ls --color=auto'
PS1='\[\e[01;32m\]\u@\h\[\e[00m\]:\[\e[01;34m\]\w\[\e[00m\]\$ '`

func (registry *shellSessionRegistry) get(id string) *shellSession {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.sessions[id]
}

func (registry *shellSessionRegistry) list() []shellSessionInfo {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	items := make([]shellSessionInfo, 0, len(registry.sessions))
	for _, session := range registry.sessions {
		items = append(items, session.info())
	}
	return items
}

func (registry *shellSessionRegistry) getOrCreate(id, cwd string, cols, rows int, build func(string) (*exec.Cmd, error)) (*shellSession, bool, error) {
	registry.mu.Lock()
	if existing := registry.sessions[id]; existing != nil && !existing.isDone() {
		registry.mu.Unlock()
		return existing, false, nil
	}
	registry.mu.Unlock()

	cmd, err := build(cwd)
	if err != nil {
		return nil, false, err
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, false, err
	}
	if cols > 0 && rows > 0 {
		_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	}
	session := &shellSession{
		registry:    registry,
		id:          id,
		cwd:         cwd,
		pty:         ptmx,
		cmd:         cmd,
		subscribers: make(map[int]chan shellSessionEvent),
	}
	registry.mu.Lock()
	if existing := registry.sessions[id]; existing != nil && !existing.isDone() {
		registry.mu.Unlock()
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		return existing, false, nil
	}
	registry.sessions[id] = session
	registry.mu.Unlock()
	session.startReadLoop()
	return session, true, nil
}

func (session *shellSession) info() shellSessionInfo {
	session.mu.Lock()
	defer session.mu.Unlock()
	return shellSessionInfo{ID: session.id, CWD: session.cwd, Active: !session.done}
}

func (session *shellSession) isDone() bool {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.done
}

func (session *shellSession) startReadLoop() {
	go func() {
		buffer := make([]byte, 4096)
		for {
			count, err := session.pty.Read(buffer)
			if count > 0 {
				session.broadcast(shellSessionEvent{Type: "output", Data: string(buffer[:count])})
			}
			if err != nil {
				message := "Terminal disconnected"
				if err != io.EOF {
					message = err.Error()
				}
				session.close(message)
				return
			}
		}
	}()
	go func() {
		_ = session.cmd.Wait()
		if !session.isDone() {
			session.close("Terminal exited")
		}
	}()
}

func (session *shellSession) subscribe() (int, <-chan shellSessionEvent, string, bool, string) {
	ch := make(chan shellSessionEvent, 128)
	session.mu.Lock()
	defer session.mu.Unlock()
	replay := string(session.replay)
	if session.done {
		return 0, ch, replay, true, session.closedMessage
	}
	session.nextSubID++
	id := session.nextSubID
	session.subscribers[id] = ch
	return id, ch, replay, false, ""
}

func (session *shellSession) unsubscribe(id int) {
	if id == 0 {
		return
	}
	session.mu.Lock()
	delete(session.subscribers, id)
	session.mu.Unlock()
}

func (session *shellSession) writeInput(data string) error {
	if session.isDone() {
		return io.ErrClosedPipe
	}
	_, err := session.pty.Write([]byte(data))
	return err
}

func (session *shellSession) resize(cols, rows int) error {
	if session.isDone() {
		return io.ErrClosedPipe
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	return pty.Setsize(session.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (session *shellSession) close(message string) {
	session.mu.Lock()
	if session.done {
		session.mu.Unlock()
		return
	}
	session.done = true
	session.closedMessage = strings.TrimSpace(message)
	if session.closedMessage == "" {
		session.closedMessage = "Terminal disconnected"
	}
	if session.pty != nil {
		_ = session.pty.Close()
	}
	subscribers := make([]chan shellSessionEvent, 0, len(session.subscribers))
	for _, subscriber := range session.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	session.subscribers = make(map[int]chan shellSessionEvent)
	session.mu.Unlock()
	closed := shellSessionEvent{Type: "closed", Message: session.closedMessage}
	for _, subscriber := range subscribers {
		select {
		case subscriber <- closed:
		default:
		}
		close(subscriber)
	}
	session.registry.mu.Lock()
	delete(session.registry.sessions, session.id)
	session.registry.mu.Unlock()
}

func (session *shellSession) broadcast(event shellSessionEvent) {
	session.mu.Lock()
	if session.done {
		session.mu.Unlock()
		return
	}
	if event.Type == "output" && event.Data != "" {
		session.replay = append(session.replay, []byte(event.Data)...)
		if len(session.replay) > shellSessionReplayLimit {
			session.replay = append([]byte(nil), session.replay[len(session.replay)-shellSessionReplayLimit:]...)
		}
	}
	subscribers := make([]chan shellSessionEvent, 0, len(session.subscribers))
	for _, subscriber := range session.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	session.mu.Unlock()
	for _, subscriber := range subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (server *Server) handleShellSessionWebSocket(w http.ResponseWriter, r *http.Request, id string) {
	session := server.shellSessions.get(id)
	if session == nil {
		http.Error(w, "shell session was not found", http.StatusNotFound)
		return
	}
	websocket.Server{
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler: func(ws *websocket.Conn) {
			server.serveShellSessionSocket(ws, session)
		},
	}.ServeHTTP(w, r)
}

func (server *Server) serveShellSessionSocket(ws *websocket.Conn, session *shellSession) {
	defer ws.Close()
	subscriberID, events, replay, done, closedMessage := session.subscribe()
	defer session.unsubscribe(subscriberID)
	_ = websocket.JSON.Send(ws, shellSessionEvent{Type: "ready", Message: "Terminal connected"})
	if replay != "" {
		_ = websocket.JSON.Send(ws, shellSessionEvent{Type: "output", Data: replay})
	}
	if done {
		_ = websocket.JSON.Send(ws, shellSessionEvent{Type: "closed", Message: closedMessage})
		return
	}
	eventDone := make(chan struct{})
	stopEvents := make(chan struct{})
	go func() {
		defer close(eventDone)
		for {
			select {
			case event, ok := <-events:
				if !ok {
					return
				}
				if err := websocket.JSON.Send(ws, event); err != nil {
					return
				}
				if event.Type == "closed" {
					return
				}
			case <-stopEvents:
				return
			}
		}
	}()
	for {
		var payload shellSessionSocketRequest
		if err := websocket.JSON.Receive(ws, &payload); err != nil {
			break
		}
		switch strings.TrimSpace(payload.Type) {
		case "input":
			if payload.Data != "" {
				_ = session.writeInput(payload.Data)
			}
		case "resize":
			_ = session.resize(payload.Cols, payload.Rows)
		case "close":
			session.close("Terminal closed")
			close(stopEvents)
			<-eventDone
			return
		}
	}
	close(stopEvents)
	<-eventDone
}

func (server *Server) execUserLookup() (*user.User, error) {
	return user.Lookup(server.execUser)
}
