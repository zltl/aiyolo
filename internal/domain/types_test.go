package domain

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"reflect"
	"testing"
)

func TestNormalizeProxyProfileCanonicalizesEndpoints(t *testing.T) {
	testCases := []struct {
		name     string
		profile  ProxyProfile
		expected string
	}{
		{
			name:     "http adds scheme",
			profile:  ProxyProfile{ID: "http-proxy", Type: ProxyTypeHTTP, Endpoint: "127.0.0.1:10809"},
			expected: "http://127.0.0.1:10809",
		},
		{
			name:     "socks5 adds scheme",
			profile:  ProxyProfile{ID: "socks-proxy", Type: ProxyTypeSOCKS5, Endpoint: "127.0.0.1:10808"},
			expected: "socks5://127.0.0.1:10808",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			normalized, err := NormalizeProxyProfile(testCase.profile)
			if err != nil {
				t.Fatal(err)
			}
			if normalized.Endpoint != testCase.expected {
				t.Fatalf("Endpoint=%q, want %q", normalized.Endpoint, testCase.expected)
			}
		})
	}
}

func TestNormalizeWorkerServerAppliesDefaults(t *testing.T) {
	server, err := NormalizeWorkerServer(WorkerServer{
		ID:          "worker-1",
		SSHHost:     " 10.0.0.5 ",
		SSHUsername: " ubuntu ",
		SSHKeyID:    " key-1 ",
		Labels:      []string{" GPU ", "gpu", "Prod"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "worker-1" {
		t.Fatalf("Name=%q", server.Name)
	}
	if server.SSHPort != DefaultWorkerSSHPort {
		t.Fatalf("SSHPort=%d", server.SSHPort)
	}
	if server.ExpectedUbuntuVersion != DefaultWorkerExpectedUbuntuVersion {
		t.Fatalf("ExpectedUbuntuVersion=%q", server.ExpectedUbuntuVersion)
	}
	if server.InstallProxyID != ProxyTypeDirect {
		t.Fatalf("InstallProxyID=%q", server.InstallProxyID)
	}
	if server.DataRoot != DefaultWorkerDataRoot {
		t.Fatalf("DataRoot=%q", server.DataRoot)
	}
	if server.Status != WorkerStatusPending {
		t.Fatalf("Status=%q", server.Status)
	}
	if server.LastProbeStatus != WorkerProbeStatusUnknown {
		t.Fatalf("LastProbeStatus=%q", server.LastProbeStatus)
	}
	if !reflect.DeepEqual(server.Labels, []string{"gpu", "prod"}) {
		t.Fatalf("Labels=%v", server.Labels)
	}
}

func TestNormalizeWorkerSSHKeyDerivesPublicKeyAndFingerprint(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	key, err := NormalizeWorkerSSHKey(WorkerSSHKey{ID: "worker-key-1", PrivateKey: string(privatePEM)})
	if err != nil {
		t.Fatal(err)
	}
	if key.PublicKey == "" {
		t.Fatal("expected normalized public key")
	}
	if key.Fingerprint == "" {
		t.Fatal("expected fingerprint")
	}
	if key.Name != "worker-key-1" {
		t.Fatalf("Name=%q", key.Name)
	}
}

func TestNormalizeWorkerDataDiskRejectsRootMount(t *testing.T) {
	_, err := NormalizeWorkerDataDisk(WorkerDataDisk{DevicePath: "/dev/vdb", MountPath: "/"})
	if err == nil {
		t.Fatal("expected validation error")
	}
}