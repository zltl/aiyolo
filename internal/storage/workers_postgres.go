package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/zltl/aiyolo/internal/domain"
)

func (store *PostgresStore) UpsertWorkerSSHKey(ctx context.Context, key domain.WorkerSSHKey) error {
	normalized, err := domain.NormalizeWorkerSSHKey(key)
	if err != nil {
		return err
	}
	privateKeyCiphertext, err := store.box.Encrypt(normalized.PrivateKey)
	if err != nil {
		return err
	}
	passphraseCiphertext, err := store.box.Encrypt(normalized.PrivateKeyPassphrase)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	_, err = store.pool.Exec(ctx, `
INSERT INTO worker_ssh_keys (id, name, username, public_key, private_key_ciphertext, passphrase_ciphertext, fingerprint, comment, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (id) DO UPDATE SET name = excluded.name, username = excluded.username, public_key = excluded.public_key, private_key_ciphertext = CASE WHEN excluded.private_key_ciphertext = '' THEN worker_ssh_keys.private_key_ciphertext ELSE excluded.private_key_ciphertext END, passphrase_ciphertext = CASE WHEN excluded.passphrase_ciphertext = '' THEN worker_ssh_keys.passphrase_ciphertext ELSE excluded.passphrase_ciphertext END, fingerprint = excluded.fingerprint, comment = excluded.comment, updated_at = excluded.updated_at`,
		normalized.ID, normalized.Name, normalized.Username, normalized.PublicKey, privateKeyCiphertext, passphraseCiphertext, normalized.Fingerprint, normalized.Comment, normalized.CreatedAt, normalized.UpdatedAt)
	return err
}

func (store *PostgresStore) ListWorkerSSHKeys(ctx context.Context) ([]domain.WorkerSSHKey, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, name, username, public_key, private_key_ciphertext, passphrase_ciphertext, fingerprint, comment, created_at, updated_at FROM worker_ssh_keys ORDER BY updated_at DESC, created_at DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.WorkerSSHKey, 0)
	for rows.Next() {
		item, err := store.scanWorkerSSHKey(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *PostgresStore) GetWorkerSSHKey(ctx context.Context, id string) (domain.WorkerSSHKey, error) {
	row := store.pool.QueryRow(ctx, `SELECT id, name, username, public_key, private_key_ciphertext, passphrase_ciphertext, fingerprint, comment, created_at, updated_at FROM worker_ssh_keys WHERE id = $1`, strings.TrimSpace(id))
	item, err := store.scanWorkerSSHKey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkerSSHKey{}, ErrNotFound
	}
	return item, err
}

func (store *PostgresStore) UpsertWorkerServer(ctx context.Context, worker domain.WorkerServer) error {
	normalized, err := domain.NormalizeWorkerServer(worker)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	_, err = store.pool.Exec(ctx, `
INSERT INTO worker_servers (id, name, expected_ubuntu_version, ssh_host, ssh_port, ssh_username, ssh_key_id, install_proxy_id, labels, data_root, status, last_probe_status, last_probe_error, last_probe_summary_json, last_probed_at, last_init_job_id, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (id) DO UPDATE SET name = excluded.name, expected_ubuntu_version = excluded.expected_ubuntu_version, ssh_host = excluded.ssh_host, ssh_port = excluded.ssh_port, ssh_username = excluded.ssh_username, ssh_key_id = excluded.ssh_key_id, install_proxy_id = excluded.install_proxy_id, labels = excluded.labels, data_root = excluded.data_root, status = excluded.status, last_probe_status = excluded.last_probe_status, last_probe_error = excluded.last_probe_error, last_probe_summary_json = excluded.last_probe_summary_json, last_probed_at = excluded.last_probed_at, last_init_job_id = excluded.last_init_job_id, updated_at = excluded.updated_at`,
		normalized.ID, normalized.Name, normalized.ExpectedUbuntuVersion, normalized.SSHHost, normalized.SSHPort, normalized.SSHUsername, normalized.SSHKeyID, normalized.InstallProxyID, nonNilStrings(normalized.Labels), normalized.DataRoot, normalized.Status, normalized.LastProbeStatus, normalized.LastProbeError, normalized.LastProbeSummaryJSON, normalized.LastProbedAt, normalized.LastInitJobID, normalized.CreatedAt, normalized.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "worker_servers_ssh_key_id") {
		return fmt.Errorf("worker ssh key %s: %w", normalized.SSHKeyID, ErrNotFound)
	}
	return err
}

func (store *PostgresStore) ListWorkerServers(ctx context.Context) ([]domain.WorkerServer, error) {
	rows, err := store.pool.Query(ctx, `SELECT id, name, expected_ubuntu_version, ssh_host, ssh_port, ssh_username, ssh_key_id, install_proxy_id, labels, data_root, status, last_probe_status, last_probe_error, last_probe_summary_json, last_probed_at, last_init_job_id, created_at, updated_at FROM worker_servers ORDER BY updated_at DESC, created_at DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.WorkerServer, 0)
	for rows.Next() {
		item, err := scanWorkerServer(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *PostgresStore) GetWorkerServer(ctx context.Context, id string) (domain.WorkerServer, error) {
	row := store.pool.QueryRow(ctx, `SELECT id, name, expected_ubuntu_version, ssh_host, ssh_port, ssh_username, ssh_key_id, install_proxy_id, labels, data_root, status, last_probe_status, last_probe_error, last_probe_summary_json, last_probed_at, last_init_job_id, created_at, updated_at FROM worker_servers WHERE id = $1`, strings.TrimSpace(id))
	item, err := scanWorkerServer(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkerServer{}, ErrNotFound
	}
	return item, err
}

func (store *PostgresStore) ReplaceWorkerDataDisks(ctx context.Context, workerID string, disks []domain.WorkerDataDisk) error {
	workerID = strings.TrimSpace(workerID)
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM worker_servers WHERE id = $1`, workerID).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM worker_data_disks WHERE worker_id = $1`, workerID); err != nil {
		return err
	}
	items := make([]domain.WorkerDataDisk, 0, len(disks))
	for _, disk := range disks {
		disk.WorkerID = workerID
		item, err := domain.NormalizeWorkerDataDisk(disk)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	items, err = domain.NormalizeWorkerDisks(items)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, item := range items {
		createdAt := item.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		if _, err := tx.Exec(ctx, `INSERT INTO worker_data_disks (worker_id, device_path, mount_path, created_at, updated_at) VALUES ($1,$2,$3,$4,$5)`, workerID, item.DevicePath, item.MountPath, createdAt, now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) ListWorkerDataDisks(ctx context.Context, workerID string) ([]domain.WorkerDataDisk, error) {
	rows, err := store.pool.Query(ctx, `SELECT worker_id, device_path, mount_path, created_at, updated_at FROM worker_data_disks WHERE worker_id = $1 ORDER BY mount_path ASC, device_path ASC`, strings.TrimSpace(workerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.WorkerDataDisk, 0)
	for rows.Next() {
		item, err := scanWorkerDataDisk(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *PostgresStore) UpsertWorkerInitJob(ctx context.Context, job domain.WorkerInitJob) error {
	normalized, err := domain.NormalizeWorkerInitJob(job)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	_, err = store.pool.Exec(ctx, `
INSERT INTO worker_init_jobs (worker_id, id, action, status, triggered_by, log_summary, last_error, created_at, updated_at, started_at, completed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (worker_id, id) DO UPDATE SET action = excluded.action, status = excluded.status, triggered_by = excluded.triggered_by, log_summary = excluded.log_summary, last_error = excluded.last_error, updated_at = excluded.updated_at, started_at = excluded.started_at, completed_at = excluded.completed_at`,
		normalized.WorkerID, normalized.ID, normalized.Action, normalized.Status, normalized.TriggeredBy, normalized.LogSummary, normalized.LastError, normalized.CreatedAt, normalized.UpdatedAt, normalized.StartedAt, normalized.CompletedAt)
	return err
}

func (store *PostgresStore) ListWorkerInitJobs(ctx context.Context, workerID string, limit int) ([]domain.WorkerInitJob, error) {
	if limit <= 0 {
		limit = 16
	}
	rows, err := store.pool.Query(ctx, `SELECT worker_id, id, action, status, triggered_by, log_summary, last_error, created_at, updated_at, started_at, completed_at FROM worker_init_jobs WHERE worker_id = $1 ORDER BY updated_at DESC, created_at DESC LIMIT $2`, strings.TrimSpace(workerID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.WorkerInitJob, 0, limit)
	for rows.Next() {
		item, err := scanWorkerInitJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *PostgresStore) GetWorkerInitJob(ctx context.Context, workerID string, jobID string) (domain.WorkerInitJob, error) {
	row := store.pool.QueryRow(ctx, `SELECT worker_id, id, action, status, triggered_by, log_summary, last_error, created_at, updated_at, started_at, completed_at FROM worker_init_jobs WHERE worker_id = $1 AND id = $2`, strings.TrimSpace(workerID), strings.TrimSpace(jobID))
	item, err := scanWorkerInitJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkerInitJob{}, ErrNotFound
	}
	return item, err
}

func (store *PostgresStore) AppendWorkerInitJobEvent(ctx context.Context, event domain.WorkerInitJobEvent) error {
	event.WorkerID = strings.TrimSpace(event.WorkerID)
	event.JobID = strings.TrimSpace(event.JobID)
	event.Level = strings.ToLower(strings.TrimSpace(event.Level))
	event.Message = strings.TrimSpace(event.Message)
	if event.WorkerID == "" || event.JobID == "" {
		return fmt.Errorf("worker init job event requires worker id and job id")
	}
	if event.Message == "" {
		return fmt.Errorf("worker init job event message is required")
	}
	if event.Level == "" {
		event.Level = domain.WorkerJobEventInfo
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM worker_init_jobs WHERE worker_id = $1 AND id = $2 FOR UPDATE`, event.WorkerID, event.JobID).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if event.Sequence <= 0 {
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(sequence), 0) + 1 FROM worker_init_job_events WHERE worker_id = $1 AND job_id = $2`, event.WorkerID, event.JobID).Scan(&event.Sequence); err != nil {
			return err
		}
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if _, err := tx.Exec(ctx, `INSERT INTO worker_init_job_events (worker_id, job_id, sequence, level, message, created_at) VALUES ($1,$2,$3,$4,$5,$6)`, event.WorkerID, event.JobID, event.Sequence, event.Level, event.Message, event.CreatedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *PostgresStore) ListWorkerInitJobEvents(ctx context.Context, workerID string, jobID string, afterSequence int64) ([]domain.WorkerInitJobEvent, error) {
	rows, err := store.pool.Query(ctx, `SELECT worker_id, job_id, sequence, level, message, created_at FROM worker_init_job_events WHERE worker_id = $1 AND job_id = $2 AND sequence > $3 ORDER BY sequence ASC`, strings.TrimSpace(workerID), strings.TrimSpace(jobID), afterSequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.WorkerInitJobEvent, 0)
	for rows.Next() {
		item, err := scanWorkerInitJobEvent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *PostgresStore) UpsertCloudAgentAccount(ctx context.Context, account domain.CloudAgentAccount) error {
	normalized, err := domain.NormalizeCloudAgentAccount(account)
	if err != nil {
		return err
	}
	credentialCiphertext, err := store.box.Encrypt(normalized.Credential)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	_, err = store.pool.Exec(ctx, `
INSERT INTO cloud_agent_accounts (user_id, id, worker_id, agent_type, model_public_name, container_id, container_name, workspace_path, credential_ciphertext, status, last_error, last_ass_sha256, last_build_revision, created_at, updated_at, last_started_at, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (user_id, id) DO UPDATE SET worker_id = excluded.worker_id, agent_type = excluded.agent_type, model_public_name = excluded.model_public_name, container_id = excluded.container_id, container_name = excluded.container_name, workspace_path = excluded.workspace_path, credential_ciphertext = CASE WHEN excluded.credential_ciphertext = '' THEN cloud_agent_accounts.credential_ciphertext ELSE excluded.credential_ciphertext END, status = excluded.status, last_error = excluded.last_error, last_ass_sha256 = excluded.last_ass_sha256, last_build_revision = excluded.last_build_revision, updated_at = excluded.updated_at, last_started_at = excluded.last_started_at, last_seen_at = excluded.last_seen_at`,
		normalized.UserID, normalized.ID, normalized.WorkerID, normalized.AgentType, normalized.ModelPublicName, normalized.ContainerID, normalized.ContainerName, normalized.WorkspacePath, credentialCiphertext, normalized.Status, normalized.LastError, normalized.LastASSSHA256, normalized.LastBuildRevision, normalized.CreatedAt, normalized.UpdatedAt, normalized.LastStartedAt, normalized.LastSeenAt)
	return err
}

func (store *PostgresStore) ListCloudAgentAccounts(ctx context.Context, userID string, workerID string) ([]domain.CloudAgentAccount, error) {
	var rows pgx.Rows
	var err error
	if strings.TrimSpace(workerID) == "" {
		rows, err = store.pool.Query(ctx, `SELECT user_id, id, worker_id, agent_type, model_public_name, container_id, container_name, workspace_path, credential_ciphertext, status, last_error, last_ass_sha256, last_build_revision, created_at, updated_at, last_started_at, last_seen_at FROM cloud_agent_accounts WHERE user_id = $1 ORDER BY updated_at DESC, created_at DESC, id ASC`, strings.TrimSpace(userID))
	} else {
		rows, err = store.pool.Query(ctx, `SELECT user_id, id, worker_id, agent_type, model_public_name, container_id, container_name, workspace_path, credential_ciphertext, status, last_error, last_ass_sha256, last_build_revision, created_at, updated_at, last_started_at, last_seen_at FROM cloud_agent_accounts WHERE user_id = $1 AND worker_id = $2 ORDER BY updated_at DESC, created_at DESC, id ASC`, strings.TrimSpace(userID), strings.TrimSpace(workerID))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.CloudAgentAccount, 0)
	for rows.Next() {
		item, err := store.scanCloudAgentAccount(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *PostgresStore) GetCloudAgentAccount(ctx context.Context, userID string, accountID string) (domain.CloudAgentAccount, error) {
	row := store.pool.QueryRow(ctx, `SELECT user_id, id, worker_id, agent_type, model_public_name, container_id, container_name, workspace_path, credential_ciphertext, status, last_error, last_ass_sha256, last_build_revision, created_at, updated_at, last_started_at, last_seen_at FROM cloud_agent_accounts WHERE user_id = $1 AND id = $2`, strings.TrimSpace(userID), strings.TrimSpace(accountID))
	item, err := store.scanCloudAgentAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CloudAgentAccount{}, ErrNotFound
	}
	return item, err
}

func (store *PostgresStore) UpsertCloudAgentSession(ctx context.Context, session domain.CloudAgentSession) error {
	normalized, err := domain.NormalizeCloudAgentSession(session)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	_, err = store.pool.Exec(ctx, `
INSERT INTO cloud_agent_sessions (user_id, id, worker_id, account_id, agent_type, chat_session_id, workspace_path, shell_state_json, status, last_error, created_at, updated_at, closed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (user_id, id) DO UPDATE SET worker_id = excluded.worker_id, account_id = excluded.account_id, agent_type = excluded.agent_type, chat_session_id = excluded.chat_session_id, workspace_path = excluded.workspace_path, shell_state_json = excluded.shell_state_json, status = excluded.status, last_error = excluded.last_error, updated_at = excluded.updated_at, closed_at = excluded.closed_at`,
		normalized.UserID, normalized.ID, normalized.WorkerID, normalized.AccountID, normalized.AgentType, normalized.ChatSessionID, normalized.WorkspacePath, normalized.ShellStateJSON, normalized.Status, normalized.LastError, normalized.CreatedAt, normalized.UpdatedAt, normalized.ClosedAt)
	return err
}

func (store *PostgresStore) ListCloudAgentSessions(ctx context.Context, userID string, workerID string, limit int) ([]domain.CloudAgentSession, error) {
	if limit <= 0 {
		limit = 24
	}
	var rows pgx.Rows
	var err error
	if strings.TrimSpace(workerID) == "" {
		rows, err = store.pool.Query(ctx, `SELECT user_id, id, worker_id, account_id, agent_type, chat_session_id, workspace_path, shell_state_json, status, last_error, created_at, updated_at, closed_at FROM cloud_agent_sessions WHERE user_id = $1 ORDER BY updated_at DESC, created_at DESC, id ASC LIMIT $2`, strings.TrimSpace(userID), limit)
	} else {
		rows, err = store.pool.Query(ctx, `SELECT user_id, id, worker_id, account_id, agent_type, chat_session_id, workspace_path, shell_state_json, status, last_error, created_at, updated_at, closed_at FROM cloud_agent_sessions WHERE user_id = $1 AND worker_id = $2 ORDER BY updated_at DESC, created_at DESC, id ASC LIMIT $3`, strings.TrimSpace(userID), strings.TrimSpace(workerID), limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.CloudAgentSession, 0, limit)
	for rows.Next() {
		item, err := scanCloudAgentSession(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (store *PostgresStore) GetCloudAgentSession(ctx context.Context, userID string, sessionID string) (domain.CloudAgentSession, error) {
	row := store.pool.QueryRow(ctx, `SELECT user_id, id, worker_id, account_id, agent_type, chat_session_id, workspace_path, shell_state_json, status, last_error, created_at, updated_at, closed_at FROM cloud_agent_sessions WHERE user_id = $1 AND id = $2`, strings.TrimSpace(userID), strings.TrimSpace(sessionID))
	item, err := scanCloudAgentSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CloudAgentSession{}, ErrNotFound
	}
	return item, err
}

func (store *PostgresStore) scanWorkerSSHKey(scanner interface{ Scan(dest ...any) error }) (domain.WorkerSSHKey, error) {
	var item domain.WorkerSSHKey
	var privateKeyCiphertext string
	var passphraseCiphertext string
	if err := scanner.Scan(&item.ID, &item.Name, &item.Username, &item.PublicKey, &privateKeyCiphertext, &passphraseCiphertext, &item.Fingerprint, &item.Comment, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return domain.WorkerSSHKey{}, err
	}
	privateKey, err := store.box.Decrypt(privateKeyCiphertext)
	if err != nil {
		return domain.WorkerSSHKey{}, err
	}
	passphrase, err := store.box.Decrypt(passphraseCiphertext)
	if err != nil {
		return domain.WorkerSSHKey{}, err
	}
	item.PrivateKey = privateKey
	item.PrivateKeyPassphrase = passphrase
	return item, nil
}

func (store *PostgresStore) scanCloudAgentAccount(scanner interface{ Scan(dest ...any) error }) (domain.CloudAgentAccount, error) {
	var item domain.CloudAgentAccount
	var credentialCiphertext string
	if err := scanner.Scan(&item.UserID, &item.ID, &item.WorkerID, &item.AgentType, &item.ModelPublicName, &item.ContainerID, &item.ContainerName, &item.WorkspacePath, &credentialCiphertext, &item.Status, &item.LastError, &item.LastASSSHA256, &item.LastBuildRevision, &item.CreatedAt, &item.UpdatedAt, &item.LastStartedAt, &item.LastSeenAt); err != nil {
		return domain.CloudAgentAccount{}, err
	}
	credential, err := store.box.Decrypt(credentialCiphertext)
	if err != nil {
		return domain.CloudAgentAccount{}, err
	}
	item.Credential = credential
	return item, nil
}

func scanWorkerServer(scanner interface{ Scan(dest ...any) error }) (domain.WorkerServer, error) {
	var item domain.WorkerServer
	err := scanner.Scan(&item.ID, &item.Name, &item.ExpectedUbuntuVersion, &item.SSHHost, &item.SSHPort, &item.SSHUsername, &item.SSHKeyID, &item.InstallProxyID, &item.Labels, &item.DataRoot, &item.Status, &item.LastProbeStatus, &item.LastProbeError, &item.LastProbeSummaryJSON, &item.LastProbedAt, &item.LastInitJobID, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanWorkerDataDisk(scanner interface{ Scan(dest ...any) error }) (domain.WorkerDataDisk, error) {
	var item domain.WorkerDataDisk
	err := scanner.Scan(&item.WorkerID, &item.DevicePath, &item.MountPath, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanWorkerInitJob(scanner interface{ Scan(dest ...any) error }) (domain.WorkerInitJob, error) {
	var item domain.WorkerInitJob
	err := scanner.Scan(&item.WorkerID, &item.ID, &item.Action, &item.Status, &item.TriggeredBy, &item.LogSummary, &item.LastError, &item.CreatedAt, &item.UpdatedAt, &item.StartedAt, &item.CompletedAt)
	return item, err
}

func scanWorkerInitJobEvent(scanner interface{ Scan(dest ...any) error }) (domain.WorkerInitJobEvent, error) {
	var item domain.WorkerInitJobEvent
	err := scanner.Scan(&item.WorkerID, &item.JobID, &item.Sequence, &item.Level, &item.Message, &item.CreatedAt)
	return item, err
}

func scanCloudAgentSession(scanner interface{ Scan(dest ...any) error }) (domain.CloudAgentSession, error) {
	var item domain.CloudAgentSession
	err := scanner.Scan(&item.UserID, &item.ID, &item.WorkerID, &item.AccountID, &item.AgentType, &item.ChatSessionID, &item.WorkspacePath, &item.ShellStateJSON, &item.Status, &item.LastError, &item.CreatedAt, &item.UpdatedAt, &item.ClosedAt)
	return item, err
}
