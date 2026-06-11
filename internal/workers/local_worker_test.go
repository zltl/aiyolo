package workers

import (
	"net"
	"testing"

	"github.com/zltl/aiyolo/internal/domain"
)

func TestWorkerIsLocalMatchesLoopbackSSHHost(t *testing.T) {
	worker := domain.WorkerServer{ID: "worker-1", SSHHost: "127.0.0.1", SSHPort: 22, SSHUsername: "ubuntu", SSHKeyID: "key-1"}
	if !WorkerIsLocal(worker) {
		t.Fatal("expected loopback worker ssh host to match local machine")
	}
}

func TestWorkerIsLocalDoesNotMatchRemoteSSHHost(t *testing.T) {
	worker := domain.WorkerServer{ID: "worker-1", SSHHost: "203.0.113.10", SSHPort: 22, SSHUsername: "ubuntu", SSHKeyID: "key-1"}
	if WorkerIsLocal(worker) {
		t.Fatal("expected remote worker ssh host to stay remote")
	}
}

func TestWorkerHostMatchesLocalIPs(t *testing.T) {
	localIPs := []net.IP{net.ParseIP("10.0.0.9"), net.ParseIP("127.0.0.1")}
	if !workerHostMatchesLocalIPs("10.0.0.9", localIPs) {
		t.Fatal("expected worker host ip to match local interface ip")
	}
	if workerHostMatchesLocalIPs("203.0.113.10", localIPs) {
		t.Fatal("expected unrelated worker host ip to stay remote")
	}
}

func TestWorkerHostMatchesLocalIPsNormalizesIPv4MappedIPv6(t *testing.T) {
	localIPs := []net.IP{net.ParseIP("10.0.0.9")}
	workerIP := net.ParseIP("::ffff:10.0.0.9")
	if !ipsEqual(workerIP, localIPs[0]) {
		t.Fatal("expected ipv4-mapped ipv6 worker ip to match local ipv4")
	}
}
