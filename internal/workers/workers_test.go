package workers

import (
	"strings"
	"testing"

	"github.com/zltl/aiyolo/internal/domain"
)

func TestRenderProxyEnvIncludesAuthMaterial(t *testing.T) {
	env := RenderProxyEnv(domain.ProxyProfile{ID: "edge", Type: domain.ProxyTypeHTTP, Endpoint: "http://proxy.example.com:8080", Auth: "user:pass"})
	if env["HTTPS_PROXY"] != "http://user:pass@proxy.example.com:8080" {
		t.Fatalf("unexpected https proxy env: %q", env["HTTPS_PROXY"])
	}
	if env["all_proxy"] == "" {
		t.Fatal("expected lowercase all_proxy")
	}
}

func TestBuildBootstrapPlanMentionsSelectedDisks(t *testing.T) {
	plan := BuildBootstrapPlan(
		domain.WorkerServer{ID: "worker-1", SSHHost: "10.0.0.5", SSHUsername: "ubuntu", SSHKeyID: "key-1"},
		[]domain.WorkerDataDisk{{DevicePath: "/dev/vdb", MountPath: "/srv/aiyolo"}},
		domain.ProxyProfile{ID: "edge", Type: domain.ProxyTypeHTTP, Endpoint: "http://proxy.example.com:8080"},
	)
	if !strings.Contains(plan.VarsJSON, `"device_path": "/dev/vdb"`) || !strings.Contains(plan.VarsJSON, `"mount_path": "/srv/aiyolo"`) {
		t.Fatalf("bootstrap vars missing disk selection: %s", plan.VarsJSON)
	}
	if !strings.Contains(plan.VarsJSON, `"HTTPS_PROXY": "http://proxy.example.com:8080"`) {
		t.Fatalf("bootstrap vars missing proxy env: %s", plan.VarsJSON)
	}
	if !strings.Contains(plan.VarsJSON, `"worker_workspace_root": "/var/lib/aiyolo-agent/workspace"`) {
		t.Fatalf("bootstrap vars missing workspace root: %s", plan.VarsJSON)
	}
	if !strings.Contains(plan.VarsJSON, `"worker_docker_data_root": "/var/lib/aiyolo-agent/docker"`) {
		t.Fatalf("bootstrap vars missing docker data root: %s", plan.VarsJSON)
	}
	if !strings.Contains(plan.VarsJSON, `"worker_runtime_service_name": "aiyolo-workerd"`) {
		t.Fatalf("bootstrap vars missing runtime service name: %s", plan.VarsJSON)
	}
	if !strings.Contains(plan.Playbook, "Configure Docker proxy environment") {
		t.Fatalf("bootstrap playbook missing docker proxy task: %s", plan.Playbook)
	}
	if !strings.Contains(plan.Playbook, "Initialize and mount declared worker data disks") || !strings.Contains(plan.Playbook, "Persist Docker daemon data root") || !strings.Contains(plan.Playbook, "Ensure worker runtime service is enabled and restarted") {
		t.Fatalf("bootstrap playbook missing storage or runtime tasks: %s", plan.Playbook)
	}
	if !strings.Contains(plan.Inventory, "ansible_host=10.0.0.5") {
		t.Fatalf("bootstrap inventory missing host: %s", plan.Inventory)
	}
	if !strings.Contains(plan.Summary, "Ansible bootstrap plan prepared") || !strings.Contains(plan.Summary, "1 explicit data disk selection") || !strings.Contains(plan.Summary, "post-init health verification") {
		t.Fatalf("unexpected summary: %s", plan.Summary)
	}
}

func TestInstallProxyEndpointAddressCanonicalizesBareSocks5Endpoint(t *testing.T) {
	host, port, err := installProxyEndpointAddress(domain.ProxyProfile{ID: "edge", Type: domain.ProxyTypeSOCKS5, Endpoint: "127.0.0.1:10808"})
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" || port != 10808 {
		t.Fatalf("host=%q port=%d", host, port)
	}
}

func TestInstallProxyEndpointAddressDefaultsHTTPPort(t *testing.T) {
	host, port, err := installProxyEndpointAddress(domain.ProxyProfile{ID: "edge", Type: domain.ProxyTypeHTTP, Endpoint: "http://proxy.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if host != "proxy.example.com" || port != 80 {
		t.Fatalf("host=%q port=%d", host, port)
	}
}
