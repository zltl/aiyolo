package workers

import (
	"context"
	"net"
	"os/exec"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
)

func WorkerIsLocal(worker domain.WorkerServer) bool {
	worker, err := domain.NormalizeWorkerServer(worker)
	if err != nil {
		return false
	}
	host := strings.TrimSpace(worker.SSHHost)
	if host == "" {
		return false
	}
	return workerHostMatchesLocalIPs(host, localMachineIPs())
}

func workerHostMatchesLocalIPs(host string, localIPs []net.IP) bool {
	if len(localIPs) == 0 {
		return false
	}
	for _, workerIP := range resolveWorkerHostIPs(host) {
		for _, localIP := range localIPs {
			if ipsEqual(workerIP, localIP) {
				return true
			}
		}
	}
	return false
}

func localMachineIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		var ifaceIP net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ifaceIP = value.IP
		case *net.IPAddr:
			ifaceIP = value.IP
		}
		if ifaceIP == nil || ifaceIP.IsUnspecified() {
			continue
		}
		normalized := normalizeIP(ifaceIP)
		key := normalized.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ips = append(ips, normalized)
	}
	return ips
}

func resolveWorkerHostIPs(host string) []net.IP {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{normalizeIP(ip)}
	}
	lookupIPs, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	ips := make([]net.IP, 0, len(lookupIPs))
	for _, ip := range lookupIPs {
		normalized := normalizeIP(ip)
		key := normalized.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ips = append(ips, normalized)
	}
	return ips
}

func normalizeIP(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

func ipsEqual(left, right net.IP) bool {
	left = normalizeIP(left)
	right = normalizeIP(right)
	return left != nil && right != nil && left.Equal(right)
}

func runLocalCommand(command string) (string, error) {
	cmd := exec.Command("bash", "-lc", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", formatLocalCommandError(err, output)
	}
	return string(output), nil
}

func runLocalCommandWithProgress(ctx context.Context, command string, onEvent func(CloudAgentEnsureEvent) error) (string, error) {
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	wait := func() error {
		return cmd.Wait()
	}
	return consumeCommandProgress(ctx, stdout, stderr, wait, onEvent, func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
}

func formatLocalCommandError(err error, output []byte) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return err
	}
	return execError{err: err, detail: detail}
}

type execError struct {
	err    error
	detail string
}

func (e execError) Error() string {
	return e.err.Error() + ": " + e.detail
}

func (e execError) Unwrap() error {
	return e.err
}
