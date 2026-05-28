package console

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

func TestWorkersHandlersCreateProbeAndInitialize(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)
	handler.probeWorker = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile) (workerops.ProbeResult, error) {
		return workerops.ProbeResult{
			OSName:           "Ubuntu 26.04 LTS",
			UbuntuVersion:    "26.04",
			DockerInstalled:  true,
			DockerVersion:    "28.1.1",
			ProxyReachable:   true,
			ProxyEndpoint:    proxy.Endpoint,
			DataRootWritable: true,
			LSBLKJSON:        `{"blockdevices":[{"path":"/dev/vdb"}]}`,
			MountsJSON:       `{"filesystems":[]}`,
			CheckedAt:        time.Now().UTC(),
		}, nil
	}
	handler.buildWorkerBootstrap = func(worker domain.WorkerServer, disks []domain.WorkerDataDisk, proxy domain.ProxyProfile) workerops.BootstrapPlan {
		return workerops.BootstrapPlan{
			Summary: "bootstrap plan ready",
			Script:  mustReadWorkersTestAsset(t, "bootstrap-install-data-root.sh"),
			ProxyEnv: map[string]string{
				"HTTPS_PROXY": proxy.Endpoint,
			},
		}
	}
	handler.executeWorkerBootstrap = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, plan workerops.BootstrapPlan) (string, error) {
		if worker.ID != "worker-1" || key.ID != "ssh-key-1" || !strings.Contains(plan.Script, "install -d /var/lib/aiyolo-agent") {
			t.Fatalf("unexpected bootstrap execution inputs worker=%+v key=%+v plan=%+v", worker, key, plan)
		}
		return "docker installed", nil
	}
	handler.verifyWorkerBootstrap = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey) (workerops.BootstrapHealth, error) {
		return workerops.BootstrapHealth{
			Status:             "ready",
			WorkerID:           worker.ID,
			DataRoot:           worker.DataRoot,
			WorkspaceRoot:      worker.DataRoot + "/workspace",
			DockerSocketExists: true,
		}, nil
	}

	router := chi.NewRouter()
	router.Mount("/console", handler.Routes())
	server := httptest.NewServer(router)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	sshKeyForm := url.Values{
		"id":          {"ssh-key-1"},
		"name":        {"Primary Key"},
		"private_key": {mustGenerateWorkersPrivateKeyPEM(t)},
	}
	sshKeyResponse, err := client.PostForm(server.URL+"/console/workers/ssh-keys", sshKeyForm)
	if err != nil {
		t.Fatal(err)
	}
	defer sshKeyResponse.Body.Close()
	if sshKeyResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sshKeyResponse.Body)
		t.Fatalf("ssh key create status=%d body=%s", sshKeyResponse.StatusCode, body)
	}

	workerForm := url.Values{
		"id":                      {"worker-1"},
		"name":                    {"Tokyo GPU"},
		"expected_ubuntu_version": {"26.04"},
		"ssh_host":                {"10.0.0.9"},
		"ssh_port":                {"22"},
		"ssh_username":            {"ubuntu"},
		"ssh_key_id":              {"ssh-key-1"},
		"install_proxy_id":        {"direct"},
		"labels":                  {"gpu,tokyo"},
		"data_disks":              {"/dev/vdb /srv/aiyolo"},
	}
	workerResponse, err := client.PostForm(server.URL+"/console/workers", workerForm)
	if err != nil {
		t.Fatal(err)
	}
	defer workerResponse.Body.Close()
	if workerResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(workerResponse.Body)
		t.Fatalf("worker create status=%d body=%s", workerResponse.StatusCode, body)
	}

	probeRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/workers/worker-1/probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	probeResponse, err := client.Do(probeRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer probeResponse.Body.Close()
	if probeResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(probeResponse.Body)
		t.Fatalf("probe status=%d body=%s", probeResponse.StatusCode, body)
	}
	worker, err := store.GetWorkerServer(context.Background(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if worker.LastProbeStatus != domain.WorkerProbeStatusReady {
		t.Fatalf("unexpected probe status: %+v", worker)
	}

	initRequest, err := http.NewRequest(http.MethodPost, server.URL+"/console/workers/worker-1/initialize", nil)
	if err != nil {
		t.Fatal(err)
	}
	initResponse, err := client.Do(initRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer initResponse.Body.Close()
	if initResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(initResponse.Body)
		t.Fatalf("initialize status=%d body=%s", initResponse.StatusCode, body)
	}
	worker, err = store.GetWorkerServer(context.Background(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if worker.LastInitJobID == "" {
		t.Fatal("expected latest init job id")
	}
	job, err := store.GetWorkerInitJob(context.Background(), "worker-1", worker.LastInitJobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != domain.WorkerJobStatusSucceeded || job.StartedAt == nil || job.CompletedAt == nil || !strings.Contains(job.LogSummary, "docker installed") || !strings.Contains(job.LogSummary, `"status":"ready"`) {
		t.Fatalf("unexpected init job: %+v", job)
	}
	eventsResponse, err := client.Get(server.URL + "/console/workers/worker-1/jobs/" + worker.LastInitJobID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer eventsResponse.Body.Close()
	if eventsResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(eventsResponse.Body)
		t.Fatalf("events status=%d body=%s", eventsResponse.StatusCode, body)
	}
	var events []domain.WorkerInitJobEvent
	if err := json.NewDecoder(eventsResponse.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || !strings.Contains(events[0].Message, "bootstrap plan ready") {
		t.Fatalf("unexpected worker job events: %+v", events)
	}
	foundExecutionComplete := false
	for _, event := range events {
		if strings.Contains(event.Message, "Bootstrap execution completed") || strings.Contains(event.Message, "初始化执行完成") {
			foundExecutionComplete = true
			break
		}
	}
	if !foundExecutionComplete {
		t.Fatalf("expected execution completion event, got %+v", events)
	}
	foundHealthVerification := false
	for _, event := range events {
		if strings.Contains(event.Message, "post-bootstrap health verification") || strings.Contains(event.Message, "初始化后的健康检查") {
			foundHealthVerification = true
			break
		}
	}
	if !foundHealthVerification {
		t.Fatalf("expected health verification event, got %+v", events)
	}
	pageResponse, err := client.Get(server.URL + "/console/workers")
	if err != nil {
		t.Fatal(err)
	}
	defer pageResponse.Body.Close()
	pageBody, _ := io.ReadAll(pageResponse.Body)
	pageHTML := string(pageBody)
	if !strings.Contains(pageHTML, "Tokyo GPU") || !strings.Contains(pageHTML, "Ubuntu 26.04 LTS") {
		t.Fatalf("workers page missing probe summary: %s", pageHTML)
	}
}

func TestWorkersHandlerInitializeFailureMarksJobFailed(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)
	handler.probeWorker = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile) (workerops.ProbeResult, error) {
		return workerops.ProbeResult{OSName: "Ubuntu 26.04 LTS", UbuntuVersion: "26.04", DockerInstalled: false, ProxyReachable: true, DataRootWritable: true, CheckedAt: time.Now().UTC()}, nil
	}
	handler.buildWorkerBootstrap = func(worker domain.WorkerServer, disks []domain.WorkerDataDisk, proxy domain.ProxyProfile) workerops.BootstrapPlan {
		return workerops.BootstrapPlan{Summary: "bootstrap plan ready", Script: mustReadWorkersTestAsset(t, "bootstrap-exit-1.sh")}
	}
	handler.executeWorkerBootstrap = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, plan workerops.BootstrapPlan) (string, error) {
		return "apt-get failed", errors.New("exit status 100")
	}

	router := chi.NewRouter()
	router.Mount("/console", handler.Routes())
	server := httptest.NewServer(router)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	sshKeyResponse, err := client.PostForm(server.URL+"/console/workers/ssh-keys", url.Values{"id": {"ssh-key-1"}, "name": {"Primary Key"}, "private_key": {mustGenerateWorkersPrivateKeyPEM(t)}})
	if err != nil {
		t.Fatal(err)
	}
	defer sshKeyResponse.Body.Close()
	if sshKeyResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sshKeyResponse.Body)
		t.Fatalf("ssh key create status=%d body=%s", sshKeyResponse.StatusCode, body)
	}
	workerResponse, err := client.PostForm(server.URL+"/console/workers", url.Values{"id": {"worker-1"}, "name": {"Tokyo GPU"}, "expected_ubuntu_version": {"26.04"}, "ssh_host": {"10.0.0.9"}, "ssh_port": {"22"}, "ssh_username": {"ubuntu"}, "ssh_key_id": {"ssh-key-1"}, "install_proxy_id": {"direct"}})
	if err != nil {
		t.Fatal(err)
	}
	defer workerResponse.Body.Close()
	if workerResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(workerResponse.Body)
		t.Fatalf("worker create status=%d body=%s", workerResponse.StatusCode, body)
	}
	probeResponse, err := client.PostForm(server.URL+"/console/workers/worker-1/probe", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	defer probeResponse.Body.Close()
	if probeResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(probeResponse.Body)
		t.Fatalf("probe status=%d body=%s", probeResponse.StatusCode, body)
	}
	initResponse, err := client.PostForm(server.URL+"/console/workers/worker-1/initialize", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	defer initResponse.Body.Close()
	if initResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(initResponse.Body)
		t.Fatalf("initialize status=%d body=%s", initResponse.StatusCode, body)
	}
	body, _ := io.ReadAll(initResponse.Body)
	html := string(body)
	if !strings.Contains(html, "exit status 100") {
		t.Fatalf("expected initialize error in page, got %s", html)
	}
	worker, err := store.GetWorkerServer(context.Background(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.GetWorkerInitJob(context.Background(), "worker-1", worker.LastInitJobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != domain.WorkerJobStatusFailed || job.LastError != "exit status 100" || worker.Status != domain.WorkerStatusFailed {
		t.Fatalf("unexpected failed init state worker=%+v job=%+v", worker, job)
	}
}

func TestWorkersHandlerInitializeHealthFailureMarksJobFailed(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)
	handler.probeWorker = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile) (workerops.ProbeResult, error) {
		return workerops.ProbeResult{OSName: "Ubuntu 26.04 LTS", UbuntuVersion: "26.04", DockerInstalled: true, ProxyReachable: true, DataRootWritable: true, CheckedAt: time.Now().UTC()}, nil
	}
	handler.buildWorkerBootstrap = func(worker domain.WorkerServer, disks []domain.WorkerDataDisk, proxy domain.ProxyProfile) workerops.BootstrapPlan {
		return workerops.BootstrapPlan{Summary: "bootstrap plan ready", Script: mustReadWorkersTestAsset(t, "bootstrap-ansible-preview.txt")}
	}
	handler.executeWorkerBootstrap = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, plan workerops.BootstrapPlan) (string, error) {
		return "runtime installed", nil
	}
	handler.verifyWorkerBootstrap = func(_ context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey) (workerops.BootstrapHealth, error) {
		return workerops.BootstrapHealth{}, errors.New("readyz returned 503")
	}

	router := chi.NewRouter()
	router.Mount("/console", handler.Routes())
	server := httptest.NewServer(router)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	sshKeyResponse, err := client.PostForm(server.URL+"/console/workers/ssh-keys", url.Values{"id": {"ssh-key-1"}, "name": {"Primary Key"}, "private_key": {mustGenerateWorkersPrivateKeyPEM(t)}})
	if err != nil {
		t.Fatal(err)
	}
	defer sshKeyResponse.Body.Close()
	if sshKeyResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sshKeyResponse.Body)
		t.Fatalf("ssh key create status=%d body=%s", sshKeyResponse.StatusCode, body)
	}
	workerResponse, err := client.PostForm(server.URL+"/console/workers", url.Values{"id": {"worker-1"}, "name": {"Tokyo GPU"}, "expected_ubuntu_version": {"26.04"}, "ssh_host": {"10.0.0.9"}, "ssh_port": {"22"}, "ssh_username": {"ubuntu"}, "ssh_key_id": {"ssh-key-1"}, "install_proxy_id": {"direct"}})
	if err != nil {
		t.Fatal(err)
	}
	defer workerResponse.Body.Close()
	if workerResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(workerResponse.Body)
		t.Fatalf("worker create status=%d body=%s", workerResponse.StatusCode, body)
	}
	probeResponse, err := client.PostForm(server.URL+"/console/workers/worker-1/probe", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	defer probeResponse.Body.Close()
	if probeResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(probeResponse.Body)
		t.Fatalf("probe status=%d body=%s", probeResponse.StatusCode, body)
	}
	initResponse, err := client.PostForm(server.URL+"/console/workers/worker-1/initialize", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	defer initResponse.Body.Close()
	if initResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(initResponse.Body)
		t.Fatalf("initialize status=%d body=%s", initResponse.StatusCode, body)
	}
	body, _ := io.ReadAll(initResponse.Body)
	html := string(body)
	if !strings.Contains(html, "readyz returned 503") {
		t.Fatalf("expected verify error in page, got %s", html)
	}
	worker, err := store.GetWorkerServer(context.Background(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.GetWorkerInitJob(context.Background(), "worker-1", worker.LastInitJobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != domain.WorkerJobStatusFailed || job.LastError != "readyz returned 503" || worker.Status != domain.WorkerStatusFailed {
		t.Fatalf("unexpected failed verify state worker=%+v job=%+v", worker, job)
	}
}

func TestWorkersHandlerCreatesWorkerWithBuiltInDirectProxy(t *testing.T) {
	store := storage.NewMemoryStore()
	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)

	router := chi.NewRouter()
	router.Mount("/console", handler.Routes())
	server := httptest.NewServer(router)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	sshKeyForm := url.Values{
		"id":          {"ssh-key-1"},
		"name":        {"Primary Key"},
		"private_key": {mustGenerateWorkersPrivateKeyPEM(t)},
	}
	sshKeyResponse, err := client.PostForm(server.URL+"/console/workers/ssh-keys", sshKeyForm)
	if err != nil {
		t.Fatal(err)
	}
	defer sshKeyResponse.Body.Close()
	if sshKeyResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sshKeyResponse.Body)
		t.Fatalf("ssh key create status=%d body=%s", sshKeyResponse.StatusCode, body)
	}

	pageResponse, err := client.Get(server.URL + "/console/workers")
	if err != nil {
		t.Fatal(err)
	}
	defer pageResponse.Body.Close()
	pageBody, _ := io.ReadAll(pageResponse.Body)
	pageHTML := string(pageBody)
	if !strings.Contains(pageHTML, `<option value="direct"`) {
		t.Fatalf("workers page missing built-in direct proxy option: %s", pageHTML)
	}

	workerForm := url.Values{
		"id":                      {"worker-1"},
		"name":                    {"Tokyo GPU"},
		"expected_ubuntu_version": {"26.04"},
		"ssh_host":                {"10.0.0.9"},
		"ssh_port":                {"22"},
		"ssh_username":            {"ubuntu"},
		"ssh_key_id":              {"ssh-key-1"},
		"labels":                  {"gpu,tokyo"},
		"data_disks":              {"/dev/vdb /srv/aiyolo"},
	}
	workerResponse, err := client.PostForm(server.URL+"/console/workers", workerForm)
	if err != nil {
		t.Fatal(err)
	}
	defer workerResponse.Body.Close()
	if workerResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(workerResponse.Body)
		t.Fatalf("worker create status=%d body=%s", workerResponse.StatusCode, body)
	}

	worker, err := store.GetWorkerServer(context.Background(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if worker.InstallProxyID != domain.ProxyTypeDirect {
		t.Fatalf("unexpected install proxy id: %+v", worker)
	}
}

func TestWorkersHandlerShowsHTMXValidationErrorOnInvalidWorkerForm(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)

	router := chi.NewRouter()
	router.Mount("/console", handler.Routes())
	server := httptest.NewServer(router)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	sshKeyForm := url.Values{
		"id":          {"ssh-key-1"},
		"name":        {"Primary Key"},
		"username":    {"root"},
		"private_key": {mustGenerateWorkersPrivateKeyPEM(t)},
	}
	sshKeyResponse, err := client.PostForm(server.URL+"/console/workers/ssh-keys", sshKeyForm)
	if err != nil {
		t.Fatal(err)
	}
	defer sshKeyResponse.Body.Close()
	if sshKeyResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sshKeyResponse.Body)
		t.Fatalf("ssh key create status=%d body=%s", sshKeyResponse.StatusCode, body)
	}

	workerForm := url.Values{
		"id":                      {"worker-1"},
		"name":                    {"Tokyo GPU"},
		"expected_ubuntu_version": {"26.04"},
		"ssh_host":                {"8.138.123.24"},
		"ssh_port":                {"22"},
		"ssh_username":            {"root"},
		"ssh_key_id":              {"ssh-key-1"},
		"install_proxy_id":        {"direct"},
		"labels":                  {"gpu,tokyo"},
		"data_disks":              {"/dev/vdb"},
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/console/workers", strings.NewReader(workerForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("expected htmx validation response status=200 body=%s", body)
	}
	body, _ := io.ReadAll(response.Body)
	html := string(body)
	if !strings.Contains(html, `flash flash-error`) || !strings.Contains(html, `invalid worker data disk line &#34;/dev/vdb&#34;, want &#39;/dev/... /mount/path&#39;`) {
		t.Fatalf("expected inline validation error, got %s", html)
	}
	if !strings.Contains(html, `value="8.138.123.24"`) || !strings.Contains(html, `value="root"`) {
		t.Fatalf("expected submitted worker form values to be preserved, got %s", html)
	}
	if _, err := store.GetWorkerServer(context.Background(), "worker-1"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected worker not to be saved, err=%v", err)
	}
}

func mustReadWorkersTestAsset(t *testing.T, name string) string {
	t.Helper()
	payload, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read workers test asset %s: %v", name, err)
	}
	return string(payload)
}

func TestWorkersHandlerEditsRegisteredWorker(t *testing.T) {
	store := storage.NewMemoryStore()
	if err := store.SeedDefaults(context.Background(), storage.SeedData{}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Config{SecretKey: "test-secret", AdminEmail: "admin@example.com", AdminPassword: "password"}, store)

	router := chi.NewRouter()
	router.Mount("/console", handler.Routes())
	server := httptest.NewServer(router)
	defer server.Close()

	client, err := loggedInWorkersClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	sshKeyForm := url.Values{
		"id":          {"ssh-key-1"},
		"name":        {"Primary Key"},
		"private_key": {mustGenerateWorkersPrivateKeyPEM(t)},
	}
	sshKeyResponse, err := client.PostForm(server.URL+"/console/workers/ssh-keys", sshKeyForm)
	if err != nil {
		t.Fatal(err)
	}
	defer sshKeyResponse.Body.Close()
	if sshKeyResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sshKeyResponse.Body)
		t.Fatalf("ssh key create status=%d body=%s", sshKeyResponse.StatusCode, body)
	}

	workerForm := url.Values{
		"id":                      {"worker-1"},
		"name":                    {"Tokyo GPU"},
		"expected_ubuntu_version": {"26.04"},
		"ssh_host":                {"10.0.0.9"},
		"ssh_port":                {"22"},
		"ssh_username":            {"ubuntu"},
		"ssh_key_id":              {"ssh-key-1"},
		"install_proxy_id":        {"direct"},
		"labels":                  {"gpu,tokyo"},
		"data_root":               {"/var/lib/aiyolo-agent"},
		"data_disks":              {"/dev/vdb /srv/aiyolo"},
	}
	workerResponse, err := client.PostForm(server.URL+"/console/workers", workerForm)
	if err != nil {
		t.Fatal(err)
	}
	defer workerResponse.Body.Close()
	if workerResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(workerResponse.Body)
		t.Fatalf("worker create status=%d body=%s", workerResponse.StatusCode, body)
	}

	pageResponse, err := client.Get(server.URL + "/console/workers")
	if err != nil {
		t.Fatal(err)
	}
	defer pageResponse.Body.Close()
	pageBody, _ := io.ReadAll(pageResponse.Body)
	pageHTML := string(pageBody)
	if !strings.Contains(pageHTML, `href="/console/workers?edit_worker_id=worker-1"`) {
		t.Fatalf("workers page missing edit link: %s", pageHTML)
	}

	editRequest, err := http.NewRequest(http.MethodGet, server.URL+"/console/workers?edit_worker_id=worker-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	editRequest.Header.Set("HX-Request", "true")
	editResponse, err := client.Do(editRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer editResponse.Body.Close()
	if editResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(editResponse.Body)
		t.Fatalf("edit worker page status=%d body=%s", editResponse.StatusCode, body)
	}
	editBody, _ := io.ReadAll(editResponse.Body)
	editHTML := string(editBody)
	if !strings.Contains(editHTML, "编辑 Worker 主机") || !strings.Contains(editHTML, `name="id" value="worker-1" placeholder="worker-tokyo-1" readonly`) {
		t.Fatalf("edit worker form missing heading or readonly id: %s", editHTML)
	}
	if !strings.Contains(editHTML, `value="10.0.0.9"`) || !strings.Contains(editHTML, `>/dev/vdb /srv/aiyolo</textarea>`) || !strings.Contains(editHTML, `>取消编辑</a>`) {
		t.Fatalf("edit worker form did not prefill worker values: %s", editHTML)
	}

	updatedForm := url.Values{
		"id":                      {"worker-1"},
		"name":                    {"Tokyo GPU Updated"},
		"expected_ubuntu_version": {"26.04"},
		"ssh_host":                {"8.138.123.24"},
		"ssh_port":                {"22"},
		"ssh_username":            {"root"},
		"ssh_key_id":              {"ssh-key-1"},
		"install_proxy_id":        {"direct"},
		"labels":                  {"gpu,cn-hz"},
		"data_root":               {"/workspace/aiyolo"},
		"data_disks":              {"/dev/vdb /workspace/data"},
	}
	updateResponse, err := client.PostForm(server.URL+"/console/workers", updatedForm)
	if err != nil {
		t.Fatal(err)
	}
	defer updateResponse.Body.Close()
	if updateResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(updateResponse.Body)
		t.Fatalf("worker update status=%d body=%s", updateResponse.StatusCode, body)
	}

	worker, err := store.GetWorkerServer(context.Background(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if worker.Name != "Tokyo GPU Updated" || worker.SSHHost != "8.138.123.24" || worker.SSHUsername != "root" || worker.DataRoot != "/workspace/aiyolo" {
		t.Fatalf("worker update did not persist: %+v", worker)
	}
	if len(worker.Labels) != 2 || worker.Labels[0] != "gpu" || worker.Labels[1] != "cn-hz" {
		t.Fatalf("worker labels were not updated: %+v", worker.Labels)
	}
	disks, err := store.ListWorkerDataDisks(context.Background(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || disks[0].DevicePath != "/dev/vdb" || disks[0].MountPath != "/workspace/data" {
		t.Fatalf("worker disks were not updated: %+v", disks)
	}
}

func loggedInWorkersClient(serverURL string) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.PostForm(serverURL+"/console/login", url.Values{"email": {"admin@example.com"}, "password": {"password"}})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(response.Body)
		return nil, errors.New(string(body))
	}
	return client, nil
}

func mustGenerateWorkersPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
}
