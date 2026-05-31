package console

import (
	"context"
	"io"
	"strings"
	"sync"

	workerops "github.com/zltl/aiyolo/internal/workers"
)

const consoleChatShellReplayLimit = 512 * 1024

type consoleChatShellRegistry struct {
	mu       sync.Mutex
	sessions map[string]*consoleChatShellSession
}

type consoleChatShellSession struct {
	registry *consoleChatShellRegistry
	key      string
	shell    workerops.InteractiveShell

	ioMu      sync.Mutex
	closeOnce sync.Once

	mu            sync.Mutex
	replay        []byte
	done          bool
	closedMessage string
	subscribers   map[int]chan consoleChatShellSocketEvent
	nextSubID     int
}

func newConsoleChatShellRegistry() *consoleChatShellRegistry {
	return &consoleChatShellRegistry{sessions: make(map[string]*consoleChatShellSession)}
}

func consoleChatShellRegistryKey(userID, chatSessionID, terminalID string) string {
	return strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(chatSessionID) + "\x00" + strings.TrimSpace(terminalID)
}

func (registry *consoleChatShellRegistry) getOrCreate(key string, open func(context.Context) (workerops.InteractiveShell, error)) (*consoleChatShellSession, error) {
	registry.mu.Lock()
	if existing := registry.sessions[key]; existing != nil && !existing.isDone() {
		registry.mu.Unlock()
		return existing, nil
	}
	registry.mu.Unlock()

	shell, err := open(context.Background())
	if err != nil {
		return nil, err
	}
	session := &consoleChatShellSession{
		registry:    registry,
		key:         key,
		shell:       shell,
		subscribers: make(map[int]chan consoleChatShellSocketEvent),
	}

	registry.mu.Lock()
	if existing := registry.sessions[key]; existing != nil && !existing.isDone() {
		registry.mu.Unlock()
		_ = shell.Close()
		return existing, nil
	}
	registry.sessions[key] = session
	registry.mu.Unlock()

	session.start()
	return session, nil
}

func (registry *consoleChatShellRegistry) delete(key string, session *consoleChatShellSession) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sessions[key] == session {
		delete(registry.sessions, key)
	}
}

func (session *consoleChatShellSession) start() {
	go session.readLoop()
}

func (session *consoleChatShellSession) readLoop() {
	buffer := make([]byte, 4096)
	for {
		count, err := session.shell.Read(buffer)
		if count > 0 {
			session.broadcast(consoleChatShellSocketEvent{Type: "output", Data: string(buffer[:count])})
		}
		if err != nil {
			message := "Terminal disconnected"
			if !consoleChatShellIgnorableError(err) {
				message = err.Error()
				session.broadcast(consoleChatShellSocketEvent{Type: "error", Message: message})
			}
			session.close(message)
			return
		}
	}
}

func (session *consoleChatShellSession) isDone() bool {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.done
}

func (session *consoleChatShellSession) subscribe() (int, <-chan consoleChatShellSocketEvent, string, bool, string) {
	channel := make(chan consoleChatShellSocketEvent, 128)
	session.mu.Lock()
	defer session.mu.Unlock()
	replay := string(session.replay)
	if session.done {
		return 0, channel, replay, true, session.closedMessage
	}
	session.nextSubID++
	id := session.nextSubID
	session.subscribers[id] = channel
	return id, channel, replay, false, ""
}

func (session *consoleChatShellSession) unsubscribe(id int) {
	if id == 0 {
		return
	}
	session.mu.Lock()
	delete(session.subscribers, id)
	session.mu.Unlock()
}

func (session *consoleChatShellSession) Write(payload []byte) (int, error) {
	if session.isDone() {
		return 0, io.ErrClosedPipe
	}
	session.ioMu.Lock()
	defer session.ioMu.Unlock()
	return session.shell.Write(payload)
}

func (session *consoleChatShellSession) Resize(cols, rows int) error {
	if session.isDone() {
		return io.ErrClosedPipe
	}
	session.ioMu.Lock()
	defer session.ioMu.Unlock()
	return session.shell.Resize(cols, rows)
}

func (session *consoleChatShellSession) close(message string) {
	session.closeOnce.Do(func() {
		closedMessage := strings.TrimSpace(message)
		if closedMessage == "" {
			closedMessage = "Terminal disconnected"
		}
		_ = session.shell.Close()
		session.registry.delete(session.key, session)

		session.mu.Lock()
		session.done = true
		session.closedMessage = closedMessage
		subscribers := make([]chan consoleChatShellSocketEvent, 0, len(session.subscribers))
		for _, subscriber := range session.subscribers {
			subscribers = append(subscribers, subscriber)
		}
		session.subscribers = make(map[int]chan consoleChatShellSocketEvent)
		session.mu.Unlock()

		closedEvent := consoleChatShellSocketEvent{Type: "closed", Message: closedMessage}
		for _, subscriber := range subscribers {
			select {
			case subscriber <- closedEvent:
			default:
			}
			close(subscriber)
		}
	})
}

func (session *consoleChatShellSession) broadcast(event consoleChatShellSocketEvent) {
	session.mu.Lock()
	if session.done {
		session.mu.Unlock()
		return
	}
	if event.Type == "output" && event.Data != "" {
		session.replay = append(session.replay, []byte(event.Data)...)
		if len(session.replay) > consoleChatShellReplayLimit {
			session.replay = append([]byte(nil), session.replay[len(session.replay)-consoleChatShellReplayLimit:]...)
		}
	}
	subscribers := make([]chan consoleChatShellSocketEvent, 0, len(session.subscribers))
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
