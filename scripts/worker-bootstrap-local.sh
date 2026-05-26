#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "请用 root 运行，例如：sudo -E make bootstrap-local-worker" >&2
  exit 1
fi

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少依赖命令: $1" >&2
    exit 1
  fi
}

need_cmd apt-get
need_cmd blkid
need_cmd cp
need_cmd curl
need_cmd findmnt
need_cmd getent
need_cmd install
need_cmd lsblk
need_cmd mount
need_cmd mountpoint
need_cmd python3
need_cmd rm
need_cmd systemctl
need_cmd groupadd
need_cmd useradd

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_script_template="${repo_root}/internal/workers/ansible/templates/aiyolo-workerd.py.j2"

if [[ ! -f "${runtime_script_template}" ]]; then
  echo "未找到 runtime 脚本模板: ${runtime_script_template}" >&2
  exit 1
fi

. /etc/os-release
if [[ "${ID:-}" != "ubuntu" ]]; then
  echo "当前脚本只支持 Ubuntu，本机识别为: ${PRETTY_NAME:-unknown}" >&2
  exit 1
fi

worker_id="${AIYOLO_LOCAL_WORKER_ID:-local-worker}"
worker_data_root="${AIYOLO_LOCAL_DATA_ROOT:-/var/lib/aiyolo-agent}"
worker_workspace_root="${AIYOLO_LOCAL_WORKSPACE_ROOT:-${worker_data_root%/}/workspace}"
worker_runtime_state_root="${AIYOLO_LOCAL_RUNTIME_STATE_ROOT:-${worker_data_root%/}/workerd}"
worker_runtime_service_name="${AIYOLO_LOCAL_RUNTIME_SERVICE_NAME:-aiyolo-workerd}"
worker_runtime_user="${AIYOLO_LOCAL_RUNTIME_USER:-aiyolo-workerd}"
worker_runtime_listen_host="${AIYOLO_LOCAL_RUNTIME_LISTEN_HOST:-127.0.0.1}"
worker_runtime_listen_port="${AIYOLO_LOCAL_RUNTIME_LISTEN_PORT:-17810}"
worker_runtime_config_path="${AIYOLO_LOCAL_RUNTIME_CONFIG_PATH:-/etc/aiyolo/workerd.json}"
worker_runtime_script_path="${AIYOLO_LOCAL_RUNTIME_SCRIPT_PATH:-/usr/local/lib/aiyolo/aiyolo_workerd.py}"
worker_runtime_docker_socket="${AIYOLO_LOCAL_RUNTIME_DOCKER_SOCKET_PATH:-/var/run/docker.sock}"
worker_runtime_health_url="${AIYOLO_LOCAL_RUNTIME_HEALTH_URL:-http://${worker_runtime_listen_host}:${worker_runtime_listen_port}/readyz}"
worker_skip_proxy_env="${AIYOLO_LOCAL_SKIP_PROXY_ENV:-0}"
worker_data_device="${AIYOLO_LOCAL_DATA_DEVICE:-}"
worker_docker_data_root="${AIYOLO_LOCAL_DOCKER_DATA_ROOT:-${worker_data_root%/}/docker}"

proxy_env_keys=()
for key in ALL_PROXY HTTP_PROXY HTTPS_PROXY all_proxy http_proxy https_proxy; do
  if [[ "${worker_skip_proxy_env}" != "1" ]] && [[ -n "${!key-}" ]]; then
    proxy_env_keys+=("${key}")
  fi
done

run_with_optional_proxy() {
  local env_args=("DEBIAN_FRONTEND=noninteractive")
  local key
  for key in "${proxy_env_keys[@]}"; do
    env_args+=("${key}=${!key}")
  done
  env "${env_args[@]}" "$@"
}

run_with_optional_proxy apt-get install -y ca-certificates curl e2fsprogs gnupg docker.io

if [[ -n "${worker_data_device}" ]]; then
  if [[ ! -b "${worker_data_device}" ]]; then
    echo "数据盘不存在或不是块设备: ${worker_data_device}" >&2
    exit 1
  fi

  current_mountpoint="$(lsblk -nr -o MOUNTPOINT "${worker_data_device}" | head -n 1 | xargs || true)"
  if [[ -n "${current_mountpoint}" ]] && [[ "${current_mountpoint}" != "${worker_data_root}" ]]; then
    echo "数据盘 ${worker_data_device} 已挂载到 ${current_mountpoint}，不能直接改挂到 ${worker_data_root}" >&2
    exit 1
  fi

  current_fstype="$(lsblk -nr -o FSTYPE "${worker_data_device}" | head -n 1 | xargs || true)"
  if [[ -z "${current_fstype}" ]]; then
    mkfs.ext4 -F "${worker_data_device}"
    current_fstype="ext4"
  fi

  existing_data_stage=""
  if [[ -d "${worker_data_root}" ]] && ! findmnt -rn -T "${worker_data_root}" >/dev/null 2>&1; then
    if find "${worker_data_root}" -mindepth 1 -maxdepth 1 | read -r _; then
      existing_data_stage="$(mktemp -d -t aiyolo-data-root-stage-XXXXXX)"
      cp -a "${worker_data_root}/." "${existing_data_stage}/"
    fi
  fi

  install -d -m 0755 "${worker_data_root}"
  device_uuid="$(blkid -o value -s UUID "${worker_data_device}" | xargs)"
  fstab_src="UUID=${device_uuid}"
  if grep -Eq "[[:space:]]${worker_data_root//\//\\/}[[:space:]]" /etc/fstab; then
    awk -v target="${worker_data_root}" '$2 != target { print }' /etc/fstab > /etc/fstab.aiyolo.tmp
    mv /etc/fstab.aiyolo.tmp /etc/fstab
  fi
  printf '%s %s %s defaults,nofail 0 2\n' "${fstab_src}" "${worker_data_root}" "${current_fstype}" >> /etc/fstab
  mountpoint -q "${worker_data_root}" || mount "${worker_data_root}"

  if [[ -n "${existing_data_stage}" ]]; then
    cp -a "${existing_data_stage}/." "${worker_data_root}/"
    rm -rf "${existing_data_stage}"
  fi
fi

install -d -m 0755 /etc/docker
install -d -m 0711 "${worker_docker_data_root}"
export AIYOLO_LOCAL_DOCKER_DATA_ROOT="${worker_docker_data_root}"

docker_source_root="/var/lib/docker"
if systemctl is-active --quiet docker; then
  docker_source_root="$(docker info --format '{{.DockerRootDir}}')"
fi

python3 <<'PY'
import json
import os

path = "/etc/docker/daemon.json"
target = os.environ["AIYOLO_LOCAL_DOCKER_DATA_ROOT"]
data = {}
if os.path.exists(path):
    with open(path, "r", encoding="utf-8") as handle:
        raw = handle.read().strip()
    if raw:
        data = json.loads(raw)
        if not isinstance(data, dict):
            raise SystemExit("docker daemon config must be a JSON object")
data["data-root"] = target
with open(path, "w", encoding="utf-8") as handle:
    json.dump(data, handle, ensure_ascii=True, indent=2, sort_keys=True)
    handle.write("\n")
PY

if [[ "${docker_source_root}" != "${worker_docker_data_root}" ]]; then
  systemctl stop docker || true
  if [[ -d "${docker_source_root}" ]] && find "${docker_source_root}" -mindepth 1 -maxdepth 1 | read -r _; then
    if ! find "${worker_docker_data_root}" -mindepth 1 -maxdepth 1 | read -r _; then
      cp -a "${docker_source_root}/." "${worker_docker_data_root}/"
    fi
  fi
fi

if [[ "${#proxy_env_keys[@]}" -gt 0 ]]; then
  install -d -m 0755 /etc/systemd/system/docker.service.d
  {
    echo "[Service]"
    for key in "${proxy_env_keys[@]}"; do
      if [[ "${key}" == "${key^^}" ]]; then
        printf 'Environment="%s=%s"\n' "${key}" "${!key}"
      fi
    done
  } > /etc/systemd/system/docker.service.d/http-proxy.conf
else
  rm -f /etc/systemd/system/docker.service.d/http-proxy.conf
fi

if ! getent group "${worker_runtime_user}" >/dev/null 2>&1; then
  groupadd --system "${worker_runtime_user}"
fi
if ! id -u "${worker_runtime_user}" >/dev/null 2>&1; then
  useradd \
    --system \
    --gid "${worker_runtime_user}" \
    --home-dir "${worker_runtime_state_root}" \
    --no-create-home \
    --shell /usr/sbin/nologin \
    "${worker_runtime_user}"
fi

install -d -m 0755 /etc/aiyolo
install -d -m 0755 /usr/local/lib/aiyolo
install -d -o "${worker_runtime_user}" -g "${worker_runtime_user}" -m 0755 "${worker_data_root}"
install -d -o "${worker_runtime_user}" -g "${worker_runtime_user}" -m 0755 "${worker_workspace_root}"
install -d -o "${worker_runtime_user}" -g "${worker_runtime_user}" -m 0755 "${worker_runtime_state_root}"

install -m 0755 "${runtime_script_template}" "${worker_runtime_script_path}"

export AIYOLO_LOCAL_BOOTSTRAP_CONFIG_PATH="${worker_runtime_config_path}"
export AIYOLO_LOCAL_BOOTSTRAP_WORKER_ID="${worker_id}"
export AIYOLO_LOCAL_BOOTSTRAP_DATA_ROOT="${worker_data_root}"
export AIYOLO_LOCAL_DOCKER_DATA_ROOT="${worker_docker_data_root}"
export AIYOLO_LOCAL_BOOTSTRAP_WORKSPACE_ROOT="${worker_workspace_root}"
export AIYOLO_LOCAL_BOOTSTRAP_LISTEN_HOST="${worker_runtime_listen_host}"
export AIYOLO_LOCAL_BOOTSTRAP_LISTEN_PORT="${worker_runtime_listen_port}"
export AIYOLO_LOCAL_BOOTSTRAP_SERVICE_NAME="${worker_runtime_service_name}"

python3 <<'PY'
import json
import os

payload = {
    "worker_id": os.environ["AIYOLO_LOCAL_BOOTSTRAP_WORKER_ID"],
    "data_root": os.environ["AIYOLO_LOCAL_BOOTSTRAP_DATA_ROOT"],
    "workspace_root": os.environ["AIYOLO_LOCAL_BOOTSTRAP_WORKSPACE_ROOT"],
    "listen_host": os.environ["AIYOLO_LOCAL_BOOTSTRAP_LISTEN_HOST"],
    "listen_port": int(os.environ["AIYOLO_LOCAL_BOOTSTRAP_LISTEN_PORT"]),
    "service_name": os.environ["AIYOLO_LOCAL_BOOTSTRAP_SERVICE_NAME"],
}
with open(os.environ["AIYOLO_LOCAL_BOOTSTRAP_CONFIG_PATH"], "w", encoding="utf-8") as handle:
    json.dump(payload, handle, ensure_ascii=True, indent=2)
    handle.write("\n")
PY

cat > "/etc/systemd/system/${worker_runtime_service_name}.service" <<EOF
[Unit]
Description=AIYolo local worker runtime
After=network-online.target docker.service
Wants=network-online.target docker.service

[Service]
Type=simple
User=${worker_runtime_user}
Group=${worker_runtime_user}
WorkingDirectory=${worker_runtime_state_root}
ExecStart=/usr/bin/python3 ${worker_runtime_script_path} --config ${worker_runtime_config_path}
Restart=on-failure
RestartSec=2
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now docker
systemctl restart docker
if [[ "$(docker info --format '{{.DockerRootDir}}')" != "${worker_docker_data_root}" ]]; then
  echo "docker data root 没有切到 ${worker_docker_data_root}" >&2
  exit 1
fi
if [[ "${docker_source_root}" != "${worker_docker_data_root}" ]] && [[ -d "${docker_source_root}" ]]; then
  find "${docker_source_root}" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
fi
systemctl enable --now "${worker_runtime_service_name}"
systemctl restart "${worker_runtime_service_name}"

systemctl is-active --quiet docker
systemctl is-active --quiet "${worker_runtime_service_name}"
docker version --format '{{.Server.Version}}'

ready=0
for _ in $(seq 1 20); do
  if curl --fail --silent --show-error "${worker_runtime_health_url}" >/tmp/aiyolo-local-worker-ready.json; then
    ready=1
    break
  fi
  sleep 1
done

if [[ "${ready}" != "1" ]]; then
  echo "worker health check 失败: ${worker_runtime_health_url}" >&2
  exit 1
fi

cat /tmp/aiyolo-local-worker-ready.json
