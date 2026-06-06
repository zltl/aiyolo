package workers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

const cloudAgentProgressPrefix = "AIYOLO_PROGRESS\t"

type CloudAgentEnsureEvent struct {
	Type          string `json:"type"`
	Phase         string `json:"phase,omitempty"`
	Message       string `json:"message,omitempty"`
	WorkerID      string `json:"workerId,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
}

func parseCloudAgentProgressLine(line string) (CloudAgentEnsureEvent, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, cloudAgentProgressPrefix) {
		return CloudAgentEnsureEvent{}, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, cloudAgentProgressPrefix))
	if payload == "" {
		return CloudAgentEnsureEvent{}, false
	}
	var event CloudAgentEnsureEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return CloudAgentEnsureEvent{}, false
	}
	if strings.TrimSpace(event.Type) == "" {
		if strings.TrimSpace(event.Phase) != "" {
			event.Type = "phase"
		} else {
			event.Type = "log"
		}
	}
	if event.Type == "phase" && strings.TrimSpace(event.Message) == "" {
		return CloudAgentEnsureEvent{}, false
	}
	if event.Type == "log" && strings.TrimSpace(event.Message) == "" {
		return CloudAgentEnsureEvent{}, false
	}
	return event, true
}

func runSSHCommandWithProgress(ctx context.Context, client *ssh.Client, command string, onEvent func(CloudAgentEnsureEvent) error) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	stdout, err := session.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := session.Start(command); err != nil {
		return "", err
	}

	var (
		mu          sync.Mutex
		outputLines []string
		waitErr     error
	)
	emit := func(event CloudAgentEnsureEvent) {
		if onEvent == nil {
			return
		}
		_ = onEvent(event)
	}
	consume := func(reader io.Reader) {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			line := scanner.Text()
			if event, ok := parseCloudAgentProgressLine(line); ok {
				emit(event)
				continue
			}
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			mu.Lock()
			outputLines = append(outputLines, trimmed)
			mu.Unlock()
			emit(CloudAgentEnsureEvent{Type: "log", Message: trimmed})
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		consume(stdout)
	}()
	go func() {
		defer wg.Done()
		consume(stderr)
	}()

	done := make(chan error, 1)
	go func() {
		wg.Wait()
		done <- session.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = session.Close()
		return "", ctx.Err()
	case waitErr = <-done:
	}

	mu.Lock()
	output := strings.Join(outputLines, "\n")
	mu.Unlock()
	if waitErr != nil {
		if strings.TrimSpace(output) == "" {
			return "", waitErr
		}
		return output, fmt.Errorf("%w: %s", waitErr, strings.TrimSpace(output))
	}
	return output, nil
}
