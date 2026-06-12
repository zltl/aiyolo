package workers

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/zltl/aiyolo/internal/domain"
)

// CloudAgentVNCHostPort returns the loopback host port where the user's cloud-agent
// container exposes its VNC server on the worker machine.
func CloudAgentVNCHostPort(userID, workerID string) int {
	return cloudAgentHostPort(userID, workerID, defaultCloudAgentHostVNCBasePort)
}

// DialCloudAgentVNC opens a TCP connection to the cloud-agent VNC endpoint on the worker.
func DialCloudAgentVNC(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, userID string) (net.Conn, error) {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(CloudAgentVNCHostPort(userID, worker.ID)))
	if WorkerIsLocal(worker) {
		dialer := net.Dialer{Timeout: 10 * time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, fmt.Errorf("dial local cloud agent vnc %s: %w", address, err)
		}
		return conn, nil
	}
	client, err := dialSSHStreaming(worker, key)
	if err != nil {
		return nil, err
	}
	conn, err := client.Dial("tcp", address)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("dial remote cloud agent vnc %s: %w", address, err)
	}
	return &sshForwardedConn{Client: client, Conn: conn}, nil
}

type sshForwardedConn struct {
	*ssh.Client
	net.Conn
}

func (conn *sshForwardedConn) Close() error {
	var firstErr error
	if conn.Conn != nil {
		if err := conn.Conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if conn.Client != nil {
		if err := conn.Client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
