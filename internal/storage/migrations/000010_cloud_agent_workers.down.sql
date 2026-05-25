DROP INDEX IF EXISTS idx_cloud_agent_sessions_user_worker;
DROP TABLE IF EXISTS cloud_agent_sessions;

DROP INDEX IF EXISTS idx_cloud_agent_accounts_tuple;
DROP INDEX IF EXISTS idx_cloud_agent_accounts_user_worker;
DROP TABLE IF EXISTS cloud_agent_accounts;

DROP INDEX IF EXISTS idx_worker_init_job_events_created;
DROP TABLE IF EXISTS worker_init_job_events;

DROP INDEX IF EXISTS idx_worker_init_jobs_worker_updated;
DROP TABLE IF EXISTS worker_init_jobs;

DROP TABLE IF EXISTS worker_data_disks;

DROP INDEX IF EXISTS idx_worker_servers_updated;
DROP TABLE IF EXISTS worker_servers;

DROP TABLE IF EXISTS worker_ssh_keys;