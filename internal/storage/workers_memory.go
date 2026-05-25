package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

func (store *MemoryStore) UpsertWorkerSSHKey(_ context.Context, key domain.WorkerSSHKey) error {
	normalized, err := domain.NormalizeWorkerSSHKey(key)
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now().UTC()
	if existing, ok := store.workerSSHKeys[normalized.ID]; ok && normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = existing.CreatedAt
	}
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	store.workerSSHKeys[normalized.ID] = normalized
	return nil
}

func (store *MemoryStore) ListWorkerSSHKeys(context.Context) ([]domain.WorkerSSHKey, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	items := make([]domain.WorkerSSHKey, 0, len(store.workerSSHKeys))
	for _, item := range store.workerSSHKeys {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (store *MemoryStore) GetWorkerSSHKey(_ context.Context, id string) (domain.WorkerSSHKey, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	item, ok := store.workerSSHKeys[strings.TrimSpace(id)]
	if !ok {
		return domain.WorkerSSHKey{}, ErrNotFound
	}
	return item, nil
}

func (store *MemoryStore) UpsertWorkerServer(_ context.Context, worker domain.WorkerServer) error {
	normalized, err := domain.NormalizeWorkerServer(worker)
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.workerSSHKeys[normalized.SSHKeyID]; !ok {
		return fmt.Errorf("worker ssh key %s: %w", normalized.SSHKeyID, ErrNotFound)
	}
	now := time.Now().UTC()
	if existing, ok := store.workers[normalized.ID]; ok && normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = existing.CreatedAt
	}
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	store.workers[normalized.ID] = cloneWorkerServer(normalized)
	return nil
}

func (store *MemoryStore) ListWorkerServers(context.Context) ([]domain.WorkerServer, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	items := make([]domain.WorkerServer, 0, len(store.workers))
	for _, item := range store.workers {
		items = append(items, cloneWorkerServer(item))
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (store *MemoryStore) GetWorkerServer(_ context.Context, id string) (domain.WorkerServer, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	item, ok := store.workers[strings.TrimSpace(id)]
	if !ok {
		return domain.WorkerServer{}, ErrNotFound
	}
	return cloneWorkerServer(item), nil
}

func (store *MemoryStore) ReplaceWorkerDataDisks(_ context.Context, workerID string, disks []domain.WorkerDataDisk) error {
	workerID = strings.TrimSpace(workerID)
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.workers[workerID]; !ok {
		return ErrNotFound
	}
	normalized := make([]domain.WorkerDataDisk, 0, len(disks))
	for _, disk := range disks {
		disk.WorkerID = workerID
		item, err := domain.NormalizeWorkerDataDisk(disk)
		if err != nil {
			return err
		}
		normalized = append(normalized, item)
	}
	items, err := domain.NormalizeWorkerDisks(normalized)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for index := range items {
		if items[index].CreatedAt.IsZero() {
			items[index].CreatedAt = now
		}
		items[index].UpdatedAt = now
	}
	store.workerDisks[workerID] = cloneWorkerDisks(items)
	return nil
}

func (store *MemoryStore) ListWorkerDataDisks(_ context.Context, workerID string) ([]domain.WorkerDataDisk, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	items := cloneWorkerDisks(store.workerDisks[strings.TrimSpace(workerID)])
	sort.Slice(items, func(i, j int) bool {
		if items[i].MountPath != items[j].MountPath {
			return items[i].MountPath < items[j].MountPath
		}
		return items[i].DevicePath < items[j].DevicePath
	})
	return items, nil
}

func (store *MemoryStore) UpsertWorkerInitJob(_ context.Context, job domain.WorkerInitJob) error {
	normalized, err := domain.NormalizeWorkerInitJob(job)
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.workers[normalized.WorkerID]; !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	key := workerInitJobStorageKey(normalized.WorkerID, normalized.ID)
	if existing, ok := store.workerJobs[key]; ok && normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = existing.CreatedAt
	}
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	store.workerJobs[key] = normalized
	return nil
}

func (store *MemoryStore) ListWorkerInitJobs(_ context.Context, workerID string, limit int) ([]domain.WorkerInitJob, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	items := make([]domain.WorkerInitJob, 0, len(store.workerJobs))
	for _, item := range store.workerJobs {
		if item.WorkerID != strings.TrimSpace(workerID) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ID < items[j].ID
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (store *MemoryStore) GetWorkerInitJob(_ context.Context, workerID string, jobID string) (domain.WorkerInitJob, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	item, ok := store.workerJobs[workerInitJobStorageKey(strings.TrimSpace(workerID), strings.TrimSpace(jobID))]
	if !ok {
		return domain.WorkerInitJob{}, ErrNotFound
	}
	return item, nil
}

func (store *MemoryStore) AppendWorkerInitJobEvent(_ context.Context, event domain.WorkerInitJobEvent) error {
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
	store.mu.Lock()
	defer store.mu.Unlock()
	key := workerInitJobStorageKey(event.WorkerID, event.JobID)
	if _, ok := store.workerJobs[key]; !ok {
		return ErrNotFound
	}
	if event.Sequence <= 0 {
		for _, existing := range store.workerJobEvents[key] {
			if existing.Sequence > event.Sequence {
				event.Sequence = existing.Sequence
			}
		}
		event.Sequence++
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	store.workerJobEvents[key] = append(store.workerJobEvents[key], event)
	return nil
}

func (store *MemoryStore) ListWorkerInitJobEvents(_ context.Context, workerID string, jobID string, afterSequence int64) ([]domain.WorkerInitJobEvent, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	items := make([]domain.WorkerInitJobEvent, 0, len(store.workerJobEvents[workerInitJobStorageKey(strings.TrimSpace(workerID), strings.TrimSpace(jobID))]))
	for _, item := range store.workerJobEvents[workerInitJobStorageKey(strings.TrimSpace(workerID), strings.TrimSpace(jobID))] {
		if item.Sequence <= afterSequence {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Sequence < items[j].Sequence })
	return items, nil
}

func (store *MemoryStore) UpsertCloudAgentAccount(_ context.Context, account domain.CloudAgentAccount) error {
	normalized, err := domain.NormalizeCloudAgentAccount(account)
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.workers[normalized.WorkerID]; !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	key := cloudAgentStorageKey(normalized.UserID, normalized.ID)
	if existing, ok := store.cloudAgentAccounts[key]; ok && normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = existing.CreatedAt
	}
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	store.cloudAgentAccounts[key] = normalized
	return nil
}

func (store *MemoryStore) ListCloudAgentAccounts(_ context.Context, userID string, workerID string) ([]domain.CloudAgentAccount, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	items := make([]domain.CloudAgentAccount, 0, len(store.cloudAgentAccounts))
	for _, item := range store.cloudAgentAccounts {
		if item.UserID != strings.TrimSpace(userID) {
			continue
		}
		if strings.TrimSpace(workerID) != "" && item.WorkerID != strings.TrimSpace(workerID) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (store *MemoryStore) GetCloudAgentAccount(_ context.Context, userID string, accountID string) (domain.CloudAgentAccount, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	item, ok := store.cloudAgentAccounts[cloudAgentStorageKey(strings.TrimSpace(userID), strings.TrimSpace(accountID))]
	if !ok {
		return domain.CloudAgentAccount{}, ErrNotFound
	}
	return item, nil
}

func (store *MemoryStore) UpsertCloudAgentSession(_ context.Context, session domain.CloudAgentSession) error {
	normalized, err := domain.NormalizeCloudAgentSession(session)
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.workers[normalized.WorkerID]; !ok {
		return ErrNotFound
	}
	if _, ok := store.cloudAgentAccounts[cloudAgentStorageKey(normalized.UserID, normalized.AccountID)]; !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	key := cloudAgentStorageKey(normalized.UserID, normalized.ID)
	if existing, ok := store.cloudAgentSessions[key]; ok && normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = existing.CreatedAt
	}
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = now
	}
	normalized.UpdatedAt = now
	store.cloudAgentSessions[key] = normalized
	return nil
}

func (store *MemoryStore) ListCloudAgentSessions(_ context.Context, userID string, workerID string, limit int) ([]domain.CloudAgentSession, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	items := make([]domain.CloudAgentSession, 0, len(store.cloudAgentSessions))
	for _, item := range store.cloudAgentSessions {
		if item.UserID != strings.TrimSpace(userID) {
			continue
		}
		if strings.TrimSpace(workerID) != "" && item.WorkerID != strings.TrimSpace(workerID) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ID < items[j].ID
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (store *MemoryStore) GetCloudAgentSession(_ context.Context, userID string, sessionID string) (domain.CloudAgentSession, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	item, ok := store.cloudAgentSessions[cloudAgentStorageKey(strings.TrimSpace(userID), strings.TrimSpace(sessionID))]
	if !ok {
		return domain.CloudAgentSession{}, ErrNotFound
	}
	return item, nil
}

func cloneWorkerServer(worker domain.WorkerServer) domain.WorkerServer {
	clone := worker
	clone.Labels = append([]string(nil), worker.Labels...)
	return clone
}

func cloneWorkerDisks(disks []domain.WorkerDataDisk) []domain.WorkerDataDisk {
	if len(disks) == 0 {
		return nil
	}
	clone := make([]domain.WorkerDataDisk, 0, len(disks))
	clone = append(clone, disks...)
	return clone
}

func workerInitJobStorageKey(workerID, jobID string) string {
	return strings.TrimSpace(workerID) + "\x00" + strings.TrimSpace(jobID)
}

func cloudAgentStorageKey(userID, id string) string {
	return strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(id)
}
