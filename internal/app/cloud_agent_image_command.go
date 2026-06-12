package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
	workerops "github.com/zltl/aiyolo/internal/workers"
)

func newBuildCloudAgentImageOnWorkerCommand() *cobra.Command {
	var workerID string
	var image string
	var ubuntuRelease string
	var ubuntuSeries string
	var ubuntuMirror string
	var chromeDEBURL string
	var rootFSURL string
	var rootFSIndexURL string
	var assDownloadURL string
	var assSHA256URL string
	var forceRebuild bool

	cmd := &cobra.Command{
		Use:          "build-cloud-agent-image-on-worker",
		Short:        "Build the cloud agent Docker image on a worker so console ensure can reuse it",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfigFromCommand(cmd)
			if err != nil {
				return err
			}
			workerID = strings.TrimSpace(workerID)
			if workerID == "" {
				return errors.New("--worker is required")
			}
			if assDownloadURL == "" || assSHA256URL == "" {
				resolvedDownloadURL, resolvedSHA256URL, resolveErr := cloudAgentASSArtifactURLs(cfg)
				if resolveErr != nil {
					return resolveErr
				}
				if assDownloadURL == "" {
					assDownloadURL = resolvedDownloadURL
				}
				if assSHA256URL == "" {
					assSHA256URL = resolvedSHA256URL
				}
			}
			store, err := storage.OpenPostgres(cmd.Context(), cfg.DatabaseURL, cfg.SecretKey)
			if err != nil {
				return err
			}
			defer store.Close()

			worker, proxy, key, err := loadWorkerExecutionInputs(cmd.Context(), store, workerID)
			if err != nil {
				return err
			}
			result, err := workerops.BuildCloudAgentImageOnWorkerWithProgress(cmd.Context(), worker, key, proxy, workerops.CloudAgentImageBuildOptions{
				Image:          image,
				UbuntuRelease:  ubuntuRelease,
				UbuntuSeries:   ubuntuSeries,
				UbuntuMirror:   ubuntuMirror,
				ChromeDEBURL:   chromeDEBURL,
				RootFSURL:      rootFSURL,
				RootFSIndexURL: rootFSIndexURL,
				ASSDownloadURL: assDownloadURL,
				ASSSHA256URL:   assSHA256URL,
				ForceRebuild:   forceRebuild,
			}, func(event workerops.CloudAgentEnsureEvent) error {
				if strings.TrimSpace(event.Message) == "" {
					return nil
				}
				log.Printf("cloud agent image build worker=%s phase=%s message=%s", workerID, event.Phase, event.Message)
				return nil
			})
			if err != nil {
				return err
			}
			if result.Reused {
				log.Printf("cloud agent image reused worker=%s image=%q ass_sha256=%s build_revision=%s", result.WorkerID, result.Image, result.ASSSHA256, result.BuildRevision)
				return nil
			}
			log.Printf("cloud agent image built worker=%s image=%q ass_sha256=%s build_revision=%s", result.WorkerID, result.Image, result.ASSSHA256, result.BuildRevision)
			return nil
		},
	}
	cmd.Flags().StringVar(&workerID, "worker", "", "Worker ID to build the cloud agent image on")
	cmd.Flags().StringVar(&image, "image", "", "Docker image tag (defaults to the standard cloud agent image)")
	cmd.Flags().StringVar(&ubuntuRelease, "ubuntu-release", "", "Ubuntu release codename (default resolute)")
	cmd.Flags().StringVar(&ubuntuSeries, "ubuntu-series", "", "Ubuntu series (default 26.04)")
	cmd.Flags().StringVar(&ubuntuMirror, "ubuntu-mirror", "", "APT mirror URL for the image build")
	cmd.Flags().StringVar(&chromeDEBURL, "chrome-deb-url", "", "Chrome .deb download URL for the image build")
	cmd.Flags().StringVar(&rootFSURL, "rootfs-url", "", "Explicit Ubuntu base rootfs tarball URL")
	cmd.Flags().StringVar(&rootFSIndexURL, "rootfs-index-url", "", "Ubuntu base rootfs index URL")
	cmd.Flags().StringVar(&assDownloadURL, "ass-download-url", "", "Published aiyolo-ass download URL")
	cmd.Flags().StringVar(&assSHA256URL, "ass-sha256-url", "", "Published aiyolo-ass checksum URL")
	cmd.Flags().BoolVar(&forceRebuild, "force-rebuild", false, "Rebuild the image even when labels already match")
	_ = cmd.MarkFlagRequired("worker")
	return cmd
}

func cloudAgentASSArtifactURLs(cfg Config) (string, string, error) {
	if !cfg.Artifacts.Enabled() {
		return "", "", errors.New("artifacts.public_base_url is required to resolve aiyolo-ass URLs")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.CodexPublicBaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(cfg.Artifacts.PublicBase(), "/")
	}
	if baseURL == "" {
		return "", "", errors.New("codex.public_base_url or artifacts public base URL is required")
	}
	objectKey := workerops.CloudAgentASSArtifactObjectKey
	downloadURL := baseURL + cfg.Artifacts.ProxyObjectURL(objectKey)
	sha256URL := baseURL + cfg.Artifacts.ProxyObjectURL(objectKey + ".sha256")
	if downloadURL == baseURL || sha256URL == baseURL {
		return "", "", fmt.Errorf("unable to resolve aiyolo-ass artifact URLs from base %q", baseURL)
	}
	return downloadURL, sha256URL, nil
}

func loadWorkerExecutionInputs(ctx context.Context, store storage.Store, workerID string) (domain.WorkerServer, domain.ProxyProfile, domain.WorkerSSHKey, error) {
	worker, err := store.GetWorkerServer(ctx, workerID)
	if err != nil {
		return domain.WorkerServer{}, domain.ProxyProfile{}, domain.WorkerSSHKey{}, err
	}
	key, err := store.GetWorkerSSHKey(ctx, worker.SSHKeyID)
	if err != nil {
		return domain.WorkerServer{}, domain.ProxyProfile{}, domain.WorkerSSHKey{}, err
	}
	proxy := directProxyProfile()
	if strings.TrimSpace(worker.InstallProxyID) != "" && worker.InstallProxyID != domain.ProxyTypeDirect {
		proxy, err = store.GetProxyProfile(ctx, worker.InstallProxyID)
		if err != nil {
			return domain.WorkerServer{}, domain.ProxyProfile{}, domain.WorkerSSHKey{}, err
		}
	}
	return worker, proxy, key, nil
}

func directProxyProfile() domain.ProxyProfile {
	return domain.ProxyProfile{ID: domain.ProxyTypeDirect, Name: "Direct", Type: domain.ProxyTypeDirect}
}
