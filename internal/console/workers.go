package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

type workerSSHKeyFormView struct {
	ID                   string
	Name                 string
	PrivateKey           string
	PrivateKeyPassphrase string
	Comment              string
}

type workerServerFormView struct {
	ID                    string
	Name                  string
	ExpectedUbuntuVersion string
	SSHHost               string
	SSHPort               int
	SSHUsername           string
	SSHKeyID              string
	InstallProxyID        string
	LabelsText            string
	DataRoot              string
	DataDisksText         string
	Editing               bool
}

type workerServerCardView struct {
	Worker    domain.WorkerServer
	Disks     []domain.WorkerDataDisk
	LatestJob *domain.WorkerInitJob
	Probe     workerops.ProbeResult
	HasProbe  bool
}

func (handler *Handler) workers(w http.ResponseWriter, r *http.Request) {
	data, err := handler.workersViewData(r.Context(), r, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "workers-content", data)
		return
	}
	handler.render(w, r, "workers", data)
}

func (handler *Handler) createWorkerSSHKey(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key, err := domain.NormalizeWorkerSSHKey(domain.WorkerSSHKey{
		ID:                   formDefault(r, "id", newConsoleID("worker_ssh")),
		Name:                 formDefault(r, "name", "Worker SSH key"),
		PrivateKey:           r.FormValue("private_key"),
		PrivateKeyPassphrase: r.FormValue("private_key_passphrase"),
		Comment:              strings.TrimSpace(r.FormValue("comment")),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := handler.store.UpsertWorkerSSHKey(r.Context(), key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := handler.workersViewData(r.Context(), r, handler.requestText(r, "SSH 密钥已保存", "Worker SSH key saved"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "workers-content", data)
		return
	}
	handler.render(w, r, "workers", data)
}

func (handler *Handler) probeWorkerServer(w http.ResponseWriter, r *http.Request) {
	worker, proxy, key, err := handler.workerExecutionInputs(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	result, probeErr := handler.probeWorker(r.Context(), worker, key, proxy)
	if result.CheckedAt.IsZero() {
		result.CheckedAt = time.Now().UTC()
	}
	worker.LastProbedAt = &result.CheckedAt
	if payload, err := json.Marshal(result); err == nil {
		worker.LastProbeSummaryJSON = string(payload)
	}
	worker.LastProbeError = ""
	worker.LastProbeStatus = domain.WorkerProbeStatusReady
	worker.Status = domain.WorkerStatusReady
	if probeErr != nil {
		worker.LastProbeStatus = domain.WorkerProbeStatusFailed
		worker.LastProbeError = probeErr.Error()
		worker.Status = domain.WorkerStatusFailed
	}
	if probeErr == nil && strings.TrimSpace(worker.ExpectedUbuntuVersion) != "" && result.UbuntuVersion != "" && strings.TrimSpace(worker.ExpectedUbuntuVersion) != strings.TrimSpace(result.UbuntuVersion) {
		worker.LastProbeStatus = domain.WorkerProbeStatusFailed
		worker.LastProbeError = fmt.Sprintf("expected Ubuntu %s, got %s", worker.ExpectedUbuntuVersion, result.UbuntuVersion)
		worker.Status = domain.WorkerStatusFailed
	}
	if probeErr == nil && !result.DataRootWritable {
		worker.LastProbeStatus = domain.WorkerProbeStatusFailed
		worker.LastProbeError = handler.requestText(r, "数据根目录当前不可写", "The selected data root is not writable")
		worker.Status = domain.WorkerStatusFailed
	}
	if probeErr == nil && proxy.Type != domain.ProxyTypeDirect && !result.ProxyReachable {
		worker.LastProbeStatus = domain.WorkerProbeStatusFailed
		worker.LastProbeError = firstNonEmptyString(result.ProxyError, handler.requestText(r, "安装代理当前不可达", "The selected install proxy is not reachable"))
		worker.Status = domain.WorkerStatusFailed
	}
	if err := handler.store.UpsertWorkerServer(r.Context(), worker); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	notice := handler.requestText(r, "Worker 探测完成", "Worker probe completed")
	errorMessage := ""
	if worker.LastProbeStatus != domain.WorkerProbeStatusReady {
		notice = ""
		errorMessage = worker.LastProbeError
	}
	data, err := handler.workersViewData(r.Context(), r, notice)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if errorMessage != "" {
		data["Error"] = errorMessage
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "workers-content", data)
		return
	}
	handler.render(w, r, "workers", data)
}

func (handler *Handler) initializeWorkerServer(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithoutCancel(r.Context())
	worker, proxy, key, err := handler.workerExecutionInputs(ctx, chi.URLParam(r, "id"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	if worker.LastProbeStatus != domain.WorkerProbeStatusReady {
		http.Error(w, handler.requestText(r, "请先成功执行 Probe，再生成初始化作业", "Run a successful probe before preparing an initialization job"), http.StatusBadRequest)
		return
	}
	disks, err := handler.store.ListWorkerDataDisks(ctx, worker.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	plan := handler.buildWorkerBootstrap(worker, disks, proxy)
	job := domain.WorkerInitJob{
		ID:          newConsoleID("job"),
		WorkerID:    worker.ID,
		Action:      domain.WorkerInitActionBootstrap,
		Status:      domain.WorkerJobStatusQueued,
		TriggeredBy: currentConsoleSessionSubject(r, handler.cfg.SecretKey),
		LogSummary:  plan.Script,
	}
	if err := handler.store.UpsertWorkerInitJob(ctx, job); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	events := []string{plan.Summary}
	for _, disk := range disks {
		events = append(events, fmt.Sprintf("%s -> %s", disk.DevicePath, disk.MountPath))
	}
	for _, message := range events {
		if err := handler.store.AppendWorkerInitJobEvent(ctx, domain.WorkerInitJobEvent{WorkerID: worker.ID, JobID: job.ID, Message: message}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	startedAt := time.Now().UTC()
	job.Status = domain.WorkerJobStatusRunning
	job.StartedAt = &startedAt
	worker.Status = domain.WorkerStatusInitializing
	worker.LastInitJobID = job.ID
	if err := handler.store.UpsertWorkerInitJob(ctx, job); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := handler.store.AppendWorkerInitJobEvent(ctx, domain.WorkerInitJobEvent{WorkerID: worker.ID, JobID: job.ID, Message: handler.requestText(r, "开始通过 SSH 执行初始化脚本", "Starting bootstrap execution over SSH")}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := handler.store.UpsertWorkerServer(ctx, worker); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	executionOutput, execErr := handler.executeWorkerBootstrap(ctx, worker, key, plan)
	if trimmedOutput := strings.TrimSpace(executionOutput); trimmedOutput != "" {
		job.LogSummary = workerInitJobLogSummary(plan.Script, trimmedOutput)
		for _, line := range workerInitOutputLines(trimmedOutput) {
			if err := handler.store.AppendWorkerInitJobEvent(ctx, domain.WorkerInitJobEvent{WorkerID: worker.ID, JobID: job.ID, Message: line}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
	completedAt := time.Now().UTC()
	job.CompletedAt = &completedAt
	notice := handler.requestText(r, "初始化完成", "Initialization completed")
	errorMessage := ""
	if execErr != nil {
		job.Status = domain.WorkerJobStatusFailed
		job.LastError = strings.TrimSpace(execErr.Error())
		worker.Status = domain.WorkerStatusFailed
		notice = ""
		errorMessage = job.LastError
		if err := handler.store.AppendWorkerInitJobEvent(ctx, domain.WorkerInitJobEvent{WorkerID: worker.ID, JobID: job.ID, Level: domain.WorkerJobEventError, Message: job.LastError}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		job.Status = domain.WorkerJobStatusSucceeded
		worker.Status = domain.WorkerStatusReady
		if err := handler.store.AppendWorkerInitJobEvent(ctx, domain.WorkerInitJobEvent{WorkerID: worker.ID, JobID: job.ID, Message: handler.requestText(r, "初始化执行完成", "Bootstrap execution completed")}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := handler.store.UpsertWorkerInitJob(ctx, job); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := handler.store.UpsertWorkerServer(ctx, worker); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := handler.workersViewData(ctx, r, notice)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if errorMessage != "" {
		data["Error"] = errorMessage
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "workers-content", data)
		return
	}
	handler.render(w, r, "workers", data)
}

func workerInitJobLogSummary(script string, output string) string {
	trimmedScript := strings.TrimSpace(script)
	trimmedOutput := strings.TrimSpace(output)
	if trimmedOutput == "" {
		return trimmedScript
	}
	if trimmedScript == "" {
		return trimmedOutput
	}
	return trimmedScript + "\n\n# Remote output\n" + trimmedOutput
}

func workerInitOutputLines(output string) []string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 40 {
		tail := lines[len(lines)-40:]
		return append([]string{"... remote output truncated ..."}, tail...)
	}
	return lines
}

func (handler *Handler) workerJobEvents(w http.ResponseWriter, r *http.Request) {
	afterSequence := int64(formInt(r, "after", 0))
	events, err := handler.store.ListWorkerInitJobEvents(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "jobID"), afterSequence)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(events)
}

func (handler *Handler) createWorkerServer(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sshKeyID := strings.TrimSpace(r.FormValue("ssh_key_id"))
	if _, err := handler.store.GetWorkerSSHKey(r.Context(), sshKeyID); err != nil {
		statusCode := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			statusCode = http.StatusBadRequest
		}
		if statusCode == http.StatusBadRequest {
			handler.renderWorkerServerFormError(w, r, err.Error())
			return
		}
		http.Error(w, err.Error(), statusCode)
		return
	}
	proxyID := formDefault(r, "install_proxy_id", domain.ProxyTypeDirect)
	if strings.EqualFold(proxyID, domain.ProxyTypeDirect) {
		proxyID = domain.ProxyTypeDirect
	} else {
		if _, err := handler.store.GetProxyProfile(r.Context(), proxyID); err != nil {
			statusCode := http.StatusInternalServerError
			if errors.Is(err, storage.ErrNotFound) {
				statusCode = http.StatusBadRequest
			}
			if statusCode == http.StatusBadRequest {
				handler.renderWorkerServerFormError(w, r, err.Error())
				return
			}
			http.Error(w, err.Error(), statusCode)
			return
		}
	}
	disks, err := parseWorkerDataDisks(r.FormValue("data_disks"))
	if err != nil {
		handler.renderWorkerServerFormError(w, r, err.Error())
		return
	}
	worker, err := domain.NormalizeWorkerServer(domain.WorkerServer{
		ID:                    formDefault(r, "id", newConsoleID("worker")),
		Name:                  formDefault(r, "name", "Worker"),
		ExpectedUbuntuVersion: formDefault(r, "expected_ubuntu_version", domain.DefaultWorkerExpectedUbuntuVersion),
		SSHHost:               strings.TrimSpace(r.FormValue("ssh_host")),
		SSHPort:               formInt(r, "ssh_port", domain.DefaultWorkerSSHPort),
		SSHUsername:           formDefault(r, "ssh_username", "ubuntu"),
		SSHKeyID:              sshKeyID,
		InstallProxyID:        proxyID,
		Labels:                splitCSV(r.FormValue("labels")),
		DataRoot:              formDefault(r, "data_root", domain.DefaultWorkerDataRoot),
	})
	if err != nil {
		handler.renderWorkerServerFormError(w, r, err.Error())
		return
	}
	if err := handler.store.UpsertWorkerServer(r.Context(), worker); err != nil {
		statusCode := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			statusCode = http.StatusBadRequest
		}
		if statusCode == http.StatusBadRequest {
			handler.renderWorkerServerFormError(w, r, err.Error())
			return
		}
		http.Error(w, err.Error(), statusCode)
		return
	}
	if err := handler.store.ReplaceWorkerDataDisks(r.Context(), worker.ID, disks); err != nil {
		statusCode := http.StatusInternalServerError
		if errors.Is(err, storage.ErrNotFound) {
			statusCode = http.StatusBadRequest
		}
		if statusCode == http.StatusBadRequest {
			handler.renderWorkerServerFormError(w, r, err.Error())
			return
		}
		http.Error(w, err.Error(), statusCode)
		return
	}
	data, err := handler.workersViewData(r.Context(), r, handler.requestText(r, "Worker 已保存", "Worker saved"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "workers-content", data)
		return
	}
	handler.render(w, r, "workers", data)
}

func (handler *Handler) workersPageData(ctx context.Context, notice string) (map[string]any, error) {
	sshKeys, err := handler.store.ListWorkerSSHKeys(ctx)
	if err != nil {
		return nil, err
	}
	workers, err := handler.store.ListWorkerServers(ctx)
	if err != nil {
		return nil, err
	}
	proxies, err := handler.store.ListProxyProfiles(ctx)
	if err != nil {
		return nil, err
	}
	proxies = workerInstallProxyProfiles(proxies)
	cards, err := workerServerCards(ctx, handler.store, workers)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"Title":            "Workers",
		"SSHKeys":          sshKeys,
		"Workers":          cards,
		"Proxies":          proxies,
		"HasWorkerSSHKeys": len(sshKeys) > 0,
		"Notice":           notice,
	}, nil
}

func (handler *Handler) workersViewData(ctx context.Context, r *http.Request, notice string) (map[string]any, error) {
	data, err := handler.workersPageData(ctx, notice)
	if err != nil {
		return nil, err
	}
	if err := buildWorkersViewData(ctx, handler.store, data, r); err != nil {
		return nil, err
	}
	return data, nil
}

func (handler *Handler) renderWorkerServerFormError(w http.ResponseWriter, r *http.Request, errorMessage string) {
	data, err := handler.workersViewData(r.Context(), r, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data["Error"] = errorMessage
	if form, ok := data["WorkerServerForm"].(workerServerFormView); ok {
		data["WorkerServerForm"] = submittedWorkerServerFormView(r, form)
	}
	if isHTMXRequest(r) {
		handler.renderFragment(w, r, "workers-content", data)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	handler.render(w, r, "workers", data)
}

func buildWorkersViewData(ctx context.Context, store storage.Store, data map[string]any, r *http.Request) error {
	sshKeys, _ := data["SSHKeys"].([]domain.WorkerSSHKey)
	proxies, _ := data["Proxies"].([]domain.ProxyProfile)
	defaultProxyID := defaultWorkerInstallProxyID(proxies)
	data["WorkerSSHKeyForm"] = workerSSHKeyFormView{}
	defaultSSHKeyID := ""
	if len(sshKeys) > 0 {
		defaultSSHKeyID = sshKeys[0].ID
	}
	form := workerServerFormView{
		ExpectedUbuntuVersion: domain.DefaultWorkerExpectedUbuntuVersion,
		SSHPort:               domain.DefaultWorkerSSHPort,
		SSHUsername:           "ubuntu",
		SSHKeyID:              defaultSSHKeyID,
		InstallProxyID:        defaultProxyID,
		DataRoot:              domain.DefaultWorkerDataRoot,
	}
	if r != nil {
		workerID := strings.TrimSpace(r.URL.Query().Get("edit_worker_id"))
		if workerID != "" {
			worker, err := store.GetWorkerServer(ctx, workerID)
			if err != nil {
				if err != storage.ErrNotFound {
					return err
				}
			} else {
				disks, err := store.ListWorkerDataDisks(ctx, worker.ID)
				if err != nil {
					return err
				}
				form = workerServerFormView{
					ID:                    worker.ID,
					Name:                  worker.Name,
					ExpectedUbuntuVersion: firstNonEmpty(worker.ExpectedUbuntuVersion, domain.DefaultWorkerExpectedUbuntuVersion),
					SSHHost:               worker.SSHHost,
					SSHPort:               defaultWorkerPort(worker.SSHPort),
					SSHUsername:           firstNonEmpty(worker.SSHUsername, "ubuntu"),
					SSHKeyID:              firstNonEmpty(worker.SSHKeyID, defaultSSHKeyID),
					InstallProxyID:        firstNonEmpty(worker.InstallProxyID, defaultProxyID),
					LabelsText:            strings.Join(worker.Labels, ","),
					DataRoot:              firstNonEmpty(worker.DataRoot, domain.DefaultWorkerDataRoot),
					DataDisksText:         formatWorkerDataDisks(disks),
					Editing:               true,
				}
			}
		}
	}
	data["WorkerServerForm"] = form
	if _, err := store.ListWorkerSSHKeys(ctx); err != nil {
		return err
	}
	return nil
}

func defaultWorkerInstallProxyID(proxies []domain.ProxyProfile) string {
	for _, profile := range proxies {
		if strings.EqualFold(strings.TrimSpace(profile.ID), domain.ProxyTypeDirect) {
			return profile.ID
		}
	}
	if len(proxies) > 0 {
		return proxies[0].ID
	}
	return domain.ProxyTypeDirect
}

func defaultWorkerPort(port int) int {
	if port <= 0 {
		return domain.DefaultWorkerSSHPort
	}
	return port
}

func formatWorkerDataDisks(disks []domain.WorkerDataDisk) string {
	if len(disks) == 0 {
		return ""
	}
	lines := make([]string, 0, len(disks))
	for _, disk := range disks {
		lines = append(lines, strings.TrimSpace(disk.DevicePath)+" "+strings.TrimSpace(disk.MountPath))
	}
	return strings.Join(lines, "\n")
}

func submittedWorkerServerFormView(r *http.Request, fallback workerServerFormView) workerServerFormView {
	if r == nil {
		return fallback
	}
	form := fallback
	if _, ok := r.Form["id"]; ok {
		form.ID = strings.TrimSpace(r.FormValue("id"))
	}
	if _, ok := r.Form["name"]; ok {
		form.Name = strings.TrimSpace(r.FormValue("name"))
	}
	if _, ok := r.Form["expected_ubuntu_version"]; ok {
		form.ExpectedUbuntuVersion = formDefault(r, "expected_ubuntu_version", domain.DefaultWorkerExpectedUbuntuVersion)
	}
	if _, ok := r.Form["ssh_host"]; ok {
		form.SSHHost = strings.TrimSpace(r.FormValue("ssh_host"))
	}
	if _, ok := r.Form["ssh_port"]; ok {
		form.SSHPort = formInt(r, "ssh_port", domain.DefaultWorkerSSHPort)
	}
	if _, ok := r.Form["ssh_username"]; ok {
		form.SSHUsername = formDefault(r, "ssh_username", "ubuntu")
	}
	if _, ok := r.Form["ssh_key_id"]; ok {
		form.SSHKeyID = strings.TrimSpace(r.FormValue("ssh_key_id"))
	}
	if _, ok := r.Form["install_proxy_id"]; ok {
		form.InstallProxyID = formDefault(r, "install_proxy_id", domain.ProxyTypeDirect)
	}
	if _, ok := r.Form["labels"]; ok {
		form.LabelsText = strings.TrimSpace(r.FormValue("labels"))
	}
	if _, ok := r.Form["data_root"]; ok {
		form.DataRoot = formDefault(r, "data_root", domain.DefaultWorkerDataRoot)
	}
	if _, ok := r.Form["data_disks"]; ok {
		form.DataDisksText = strings.TrimSpace(r.FormValue("data_disks"))
	}
	if strings.TrimSpace(form.ID) != "" {
		form.Editing = true
	}
	return form
}

func workerInstallProxyProfiles(profiles []domain.ProxyProfile) []domain.ProxyProfile {
	for _, profile := range profiles {
		if strings.EqualFold(strings.TrimSpace(profile.ID), domain.ProxyTypeDirect) {
			return profiles
		}
	}
	return append([]domain.ProxyProfile{workerDirectProxyProfile()}, profiles...)
}

func workerDirectProxyProfile() domain.ProxyProfile {
	return domain.ProxyProfile{
		ID:                       domain.ProxyTypeDirect,
		Name:                     domain.ProxyTypeDirect,
		Type:                     domain.ProxyTypeDirect,
		Status:                   domain.StatusEnabled,
		TimeoutSeconds:           domain.DefaultProxyTimeoutSeconds,
		StreamIdleTimeoutSeconds: domain.DefaultProxyStreamIdleTimeoutSeconds,
	}
}

func workerServerCards(ctx context.Context, store storage.Store, workers []domain.WorkerServer) ([]workerServerCardView, error) {
	items := make([]workerServerCardView, 0, len(workers))
	for _, worker := range workers {
		disks, err := store.ListWorkerDataDisks(ctx, worker.ID)
		if err != nil {
			return nil, err
		}
		jobs, err := store.ListWorkerInitJobs(ctx, worker.ID, 1)
		if err != nil {
			return nil, err
		}
		card := workerServerCardView{Worker: worker, Disks: disks}
		if len(jobs) > 0 {
			job := jobs[0]
			card.LatestJob = &job
		}
		if probe, ok := probeSummaryFromJSON(worker.LastProbeSummaryJSON); ok {
			card.Probe = probe
			card.HasProbe = true
		}
		items = append(items, card)
	}
	return items, nil
}

func probeSummaryFromJSON(raw string) (workerops.ProbeResult, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return workerops.ProbeResult{}, false
	}
	var probe workerops.ProbeResult
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return workerops.ProbeResult{}, false
	}
	return probe, true
}

func (handler *Handler) workerExecutionInputs(ctx context.Context, workerID string) (domain.WorkerServer, domain.ProxyProfile, domain.WorkerSSHKey, error) {
	worker, err := handler.store.GetWorkerServer(ctx, workerID)
	if err != nil {
		return domain.WorkerServer{}, domain.ProxyProfile{}, domain.WorkerSSHKey{}, err
	}
	key, err := handler.store.GetWorkerSSHKey(ctx, worker.SSHKeyID)
	if err != nil {
		return domain.WorkerServer{}, domain.ProxyProfile{}, domain.WorkerSSHKey{}, err
	}
	proxy := workerDirectProxyProfile()
	if strings.TrimSpace(worker.InstallProxyID) != "" && worker.InstallProxyID != domain.ProxyTypeDirect {
		proxy, err = handler.store.GetProxyProfile(ctx, worker.InstallProxyID)
		if err != nil {
			return domain.WorkerServer{}, domain.ProxyProfile{}, domain.WorkerSSHKey{}, err
		}
	}
	return worker, proxy, key, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseWorkerDataDisks(raw string) ([]domain.WorkerDataDisk, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	items := make([]domain.WorkerDataDisk, 0)
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var parts []string
		if strings.Contains(trimmed, ",") {
			parts = strings.SplitN(trimmed, ",", 2)
		} else {
			parts = strings.Fields(trimmed)
		}
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid worker data disk line %q, want '/dev/... /mount/path'", trimmed)
		}
		items = append(items, domain.WorkerDataDisk{DevicePath: strings.TrimSpace(parts[0]), MountPath: strings.TrimSpace(parts[1])})
	}
	return domain.NormalizeWorkerDisks(items)
}
