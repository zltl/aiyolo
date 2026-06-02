#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "请用 root 运行，例如：sudo -E CLOUD_AGENT_USER='i@quant67.com' make run-cloud-agent-local" >&2
  exit 1
fi

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少依赖命令: $1" >&2
    exit 1
  fi
}

need_cmd docker
need_cmd install
need_cmd python3
need_cmd rsync
need_cmd timeout

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
user_id="${AIYOLO_CLOUD_AGENT_USER_ID:-aiyolo}"
agent_type="${AIYOLO_CLOUD_AGENT_TYPE:-${AIYOLO_AGENT_TYPE:-codex}}"
safe_user="${user_id//@/-}"
safe_user="${safe_user//[^A-Za-z0-9_-]/-}"
container_name="${AIYOLO_CLOUD_AGENT_CONTAINER_NAME:-aiyolo-cloud-agent-${safe_user}}"
image="${AIYOLO_CLOUD_AGENT_IMAGE:-aiyolo/local-cloud-agent:ubuntu-26.04-v4}"
workspace_root="${AIYOLO_CLOUD_AGENT_WORKSPACE_ROOT:-/var/lib/aiyolo-agent/cloud-agents/${user_id}/workspace}"
repo_snapshot_dir="${workspace_root}/aiyolo"
docker_data_root="${AIYOLO_CLOUD_AGENT_DOCKER_DATA_ROOT:-/var/lib/aiyolo-agent/cloud-agents/${user_id}/docker}"
enable_display="${AIYOLO_CLOUD_AGENT_ENABLE_DISPLAY:-1}"
enable_dockerd="${AIYOLO_CLOUD_AGENT_ENABLE_DOCKERD:-1}"
auto_start_chrome="${AIYOLO_CLOUD_AGENT_AUTO_START_CHROME:-1}"
container_vnc_port="${AIYOLO_VNC_PORT:-5900}"
host_vnc_port="${AIYOLO_CLOUD_AGENT_HOST_VNC_PORT:-15900}"
container_chrome_port="${AIYOLO_CHROME_REMOTE_DEBUGGING_PORT:-9222}"
host_chrome_port="${AIYOLO_CLOUD_AGENT_HOST_CHROME_PORT:-19222}"
docker_registry_mirror="${AIYOLO_CLOUD_AGENT_DOCKER_REGISTRY_MIRROR:-}"
docker_storage_driver="${AIYOLO_DOCKER_STORAGE_DRIVER:-vfs}"
display="${AIYOLO_DISPLAY:-:99}"

if ! docker image inspect "$image" >/dev/null 2>&1; then
  AIYOLO_CLOUD_AGENT_IMAGE="$image" "$repo_root/scripts/build-cloud-agent-image-ubuntu-26.04.sh"
fi

install -d -m 0755 "$workspace_root" "$repo_snapshot_dir" "$docker_data_root"
AIYOLO_CLOUD_AGENT_METADATA_PATH="$workspace_root/.aiyolo-cloud-agent.json" \
AIYOLO_CLOUD_AGENT_METADATA_USER="$user_id" \
AIYOLO_CLOUD_AGENT_METADATA_AGENT_TYPE="$agent_type" \
AIYOLO_CLOUD_AGENT_METADATA_IMAGE="$image" \
python3 <<'PY'
import json
import os

payload = {
    "user_id": os.environ["AIYOLO_CLOUD_AGENT_METADATA_USER"],
  "agent_type": os.environ["AIYOLO_CLOUD_AGENT_METADATA_AGENT_TYPE"],
    "workspace_path": "/workspace",
    "created_by": "local-cloud-agent-runner",
    "image": os.environ["AIYOLO_CLOUD_AGENT_METADATA_IMAGE"],
}
with open(os.environ["AIYOLO_CLOUD_AGENT_METADATA_PATH"], "w", encoding="utf-8") as handle:
    json.dump(payload, handle, ensure_ascii=True, indent=2)
    handle.write("\n")
PY
rsync -a --delete --exclude '.git' "$repo_root/" "$repo_snapshot_dir/"

if docker ps -a --format '{{.Names}}' | grep -Fxq "$container_name"; then
  docker rm -f "$container_name" >/dev/null
fi

docker_run_args=(
  -d
  --name "$container_name" \
  --hostname "$container_name" \
  --restart unless-stopped \
  --add-host host.docker.internal:host-gateway \
  --privileged \
  --shm-size "${AIYOLO_CLOUD_AGENT_SHM_SIZE:-2g}" \
  --label aiyolo.user_id="$user_id" \
  --label aiyolo.agent_type="$agent_type" \
  --label aiyolo.workspace_path='/workspace' \
  -e AIYOLO_USER_ID="$user_id" \
  -e AIYOLO_AGENT_TYPE="$agent_type" \
  -e AIYOLO_CLOUD_AGENT_ENABLE_DISPLAY="$enable_display" \
  -e AIYOLO_CLOUD_AGENT_ENABLE_DOCKERD="$enable_dockerd" \
  -e AIYOLO_CLOUD_AGENT_AUTO_START_CHROME="$auto_start_chrome" \
  -e AIYOLO_DISPLAY="$display" \
  -e AIYOLO_VNC_PORT="$container_vnc_port" \
  -e AIYOLO_CHROME_REMOTE_DEBUGGING_PORT="$container_chrome_port" \
  -e AIYOLO_DOCKER_REGISTRY_MIRROR="$docker_registry_mirror" \
  -e AIYOLO_DOCKER_STORAGE_DRIVER="$docker_storage_driver" \
  -v "$workspace_root:/workspace" \
  -v "$docker_data_root:/var/lib/docker" \
  -w /workspace \
)

if [[ "$enable_display" == "1" ]]; then
  docker_run_args+=(-p "127.0.0.1:${host_vnc_port}:${container_vnc_port}")
fi

if [[ "$enable_display" == "1" && "$auto_start_chrome" == "1" ]]; then
  docker_run_args+=(-p "127.0.0.1:${host_chrome_port}:${container_chrome_port}")
fi

docker_run_args+=("$image")

container_id="$(docker run "${docker_run_args[@]}")"

if [[ "$enable_display" == "1" ]]; then
  docker exec "$container_name" bash -lc "timeout 30 bash -lc 'until nc -z 127.0.0.1 ${container_vnc_port}; do sleep 1; done'"
fi

if [[ "$enable_dockerd" == "1" ]]; then
  docker exec "$container_name" bash -lc "timeout 60 bash -lc 'until docker info >/dev/null 2>&1; do sleep 1; done'"
fi

if [[ "$enable_display" == "1" && "$auto_start_chrome" == "1" ]]; then
  docker exec "$container_name" bash -lc "timeout 60 bash -lc 'until nc -z 127.0.0.1 ${container_chrome_port}; do sleep 1; done'"
fi

printf 'container_id=%s\n' "$container_id"
printf 'container_name=%s\n' "$container_name"
printf 'image=%s\n' "$image"
printf 'workspace_root=%s\n' "$workspace_root"
printf 'docker_data_root=%s\n' "$docker_data_root"
if [[ "$enable_display" == "1" ]]; then
  printf 'vnc=%s\n' "127.0.0.1:${host_vnc_port}"
fi
if [[ "$enable_display" == "1" && "$auto_start_chrome" == "1" ]]; then
  printf 'chrome_devtools=%s\n' "http://127.0.0.1:${host_chrome_port}/json/version"
fi
printf -- '--- docker ps ---\n'
docker ps --filter "name=^/${container_name}$" --format 'table {{.ID}}\t{{.Image}}\t{{.Names}}\t{{.Status}}'
printf -- '\n--- agent info ---\n'
docker exec "$container_name" /usr/local/bin/aiyolo-cloud-agent-info
