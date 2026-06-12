package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

func BuildCloudAgentImageOnWorker(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options CloudAgentImageBuildOptions) (CloudAgentImageBuildResult, error) {
	return buildCloudAgentImageOnWorker(ctx, worker, key, proxy, options, nil)
}

func BuildCloudAgentImageOnWorkerWithProgress(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options CloudAgentImageBuildOptions, onEvent func(CloudAgentEnsureEvent) error) (CloudAgentImageBuildResult, error) {
	return buildCloudAgentImageOnWorker(ctx, worker, key, proxy, options, onEvent)
}

func buildCloudAgentImageOnWorker(ctx context.Context, worker domain.WorkerServer, key domain.WorkerSSHKey, proxy domain.ProxyProfile, options CloudAgentImageBuildOptions, onEvent func(CloudAgentEnsureEvent) error) (CloudAgentImageBuildResult, error) {
	select {
	case <-ctx.Done():
		return CloudAgentImageBuildResult{}, ctx.Err()
	default:
	}
	emitPhase := func(phase, message string) {
		if onEvent == nil {
			return
		}
		_ = onEvent(CloudAgentEnsureEvent{Type: "phase", Phase: phase, Message: message})
	}
	worker, err := domain.NormalizeWorkerServer(worker)
	if err != nil {
		return CloudAgentImageBuildResult{}, err
	}
	key, err = domain.NormalizeWorkerSSHKey(key)
	if err != nil {
		return CloudAgentImageBuildResult{}, err
	}
	options, err = normalizeCloudAgentImageBuildOptions(options)
	if err != nil {
		return CloudAgentImageBuildResult{}, err
	}
	localWorker := WorkerIsLocal(worker)
	if localWorker {
		emitPhase("connect", fmt.Sprintf("Building cloud agent image locally on worker %s", worker.ID))
	} else {
		emitPhase("connect", fmt.Sprintf("Connecting to worker %s over SSH", worker.ID))
	}
	proxyEnv := RenderCloudAgentBuildProxyEnv(proxy)
	emitPhase("resolve_ass", "Resolving published aiyolo-ass checksum")
	resolveCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	assSHA256, err := ResolveCloudAgentASSSHA256(resolveCtx, options.ASSSHA256URL)
	if err != nil {
		return CloudAgentImageBuildResult{}, err
	}
	buildRevision := cloudAgentBuildRevisionFromImageBuild(options, assSHA256)
	payload, err := json.Marshal(cloudAgentRemotePayload{
		WorkerID:       worker.ID,
		Image:          options.Image,
		ProxyEnv:       proxyEnv,
		UbuntuRelease:  options.UbuntuRelease,
		UbuntuSeries:   options.UbuntuSeries,
		UbuntuMirror:   options.UbuntuMirror,
		ChromeDEBURL:   options.ChromeDEBURL,
		RootFSURL:      options.RootFSURL,
		RootFSIndexURL: options.RootFSIndexURL,
		ASSDownloadURL: options.ASSDownloadURL,
		ASSSHA256:      assSHA256,
		BuildRevision:  buildRevision,
		Files:          cloudAgentBuildContextFiles,
		BuildImageOnly: true,
		ForceRebuild:   options.ForceRebuild,
	})
	if err != nil {
		return CloudAgentImageBuildResult{}, err
	}
	command := buildCloudAgentRemoteCommand(string(payload))
	emitPhase("build_remote", "Building cloud agent image on worker")
	var output string
	if localWorker {
		output, err = runLocalCommandWithProgress(ctx, command, onEvent)
	} else {
		client, dialErr := dialSSH(worker, key)
		if dialErr != nil {
			return CloudAgentImageBuildResult{}, dialErr
		}
		defer client.Close()
		output, err = runSSHCommandWithProgress(ctx, client, command, onEvent)
	}
	if err != nil {
		return CloudAgentImageBuildResult{}, fmt.Errorf("build cloud agent image on %s: %w", worker.ID, err)
	}
	result, err := parseCloudAgentImageBuildResponse(output)
	if err != nil {
		return CloudAgentImageBuildResult{}, err
	}
	result.BuildRevision = buildRevision
	return result, nil
}

func normalizeCloudAgentImageBuildOptions(options CloudAgentImageBuildOptions) (CloudAgentImageBuildOptions, error) {
	options.Image = strings.TrimSpace(options.Image)
	if options.Image == "" {
		options.Image = defaultCloudAgentImage
	}
	if strings.TrimSpace(options.UbuntuRelease) == "" {
		options.UbuntuRelease = defaultCloudAgentUbuntuRelease
	}
	if strings.TrimSpace(options.UbuntuSeries) == "" {
		options.UbuntuSeries = defaultCloudAgentUbuntuSeries
	}
	if strings.TrimSpace(options.UbuntuMirror) == "" {
		options.UbuntuMirror = defaultCloudAgentUbuntuMirror
	}
	if strings.TrimSpace(options.ChromeDEBURL) == "" {
		options.ChromeDEBURL = defaultCloudAgentChromeDEBURL
	}
	if strings.TrimSpace(options.RootFSIndexURL) == "" {
		options.RootFSIndexURL = defaultCloudAgentRootFSIndexURL
	}
	options.RootFSURL = strings.TrimSpace(options.RootFSURL)
	options.ASSDownloadURL = strings.TrimSpace(options.ASSDownloadURL)
	if options.ASSDownloadURL == "" {
		return CloudAgentImageBuildOptions{}, fmt.Errorf("cloud agent aiyolo-ass download url is required")
	}
	options.ASSSHA256URL = strings.TrimSpace(options.ASSSHA256URL)
	if options.ASSSHA256URL == "" {
		return CloudAgentImageBuildOptions{}, fmt.Errorf("cloud agent aiyolo-ass sha256 url is required")
	}
	return options, nil
}

func cloudAgentBuildRevisionFromImageBuild(options CloudAgentImageBuildOptions, assSHA256 string) string {
	return cloudAgentBuildRevision(CloudAgentStartOptions{
		UbuntuRelease:  options.UbuntuRelease,
		UbuntuSeries:   options.UbuntuSeries,
		UbuntuMirror:   options.UbuntuMirror,
		ChromeDEBURL:   options.ChromeDEBURL,
		RootFSURL:      options.RootFSURL,
		RootFSIndexURL: options.RootFSIndexURL,
		ASSDownloadURL: options.ASSDownloadURL,
	}, cloudAgentBuildContextFiles, assSHA256)
}

func parseCloudAgentImageBuildResponse(output string) (CloudAgentImageBuildResult, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return CloudAgentImageBuildResult{}, fmt.Errorf("parse cloud agent image build response: empty output")
	}
	lines := strings.Split(trimmed, "\n")
	var lastErr error
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, cloudAgentProgressPrefix) {
			continue
		}
		var result CloudAgentImageBuildResult
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			lastErr = err
			continue
		}
		return result, nil
	}
	if lastErr != nil {
		return CloudAgentImageBuildResult{}, fmt.Errorf("parse cloud agent image build response: %w", lastErr)
	}
	return CloudAgentImageBuildResult{}, fmt.Errorf("parse cloud agent image build response: no JSON payload in output")
}
