#!/usr/bin/env bash
set -euo pipefail

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少依赖命令: $1" >&2
    exit 1
  fi
}

need_cmd curl
need_cmd docker
need_cmd grep
need_cmd rsync
need_cmd sha256sum
need_cmd sort

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
template_context="${repo_root}/docker/cloud-agent/ubuntu-24.04"
build_context="$(mktemp -d -t aiyolo-cloud-agent-build-XXXXXX)"
release="${AIYOLO_CLOUD_AGENT_UBUNTU_RELEASE:-noble}"
series="${AIYOLO_CLOUD_AGENT_UBUNTU_SERIES:-24.04}"
mirror="${AIYOLO_CLOUD_AGENT_UBUNTU_MIRROR:-https://mirrors.aliyun.com/ubuntu}"
chrome_deb_url="${AIYOLO_CLOUD_AGENT_CHROME_DEB_URL:-https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb}"
rootfs_index_url="${AIYOLO_CLOUD_AGENT_ROOTFS_INDEX_URL:-https://mirrors.aliyun.com/ubuntu-cdimage/ubuntu-base/releases/${series}/release}"
rootfs_url="${AIYOLO_CLOUD_AGENT_ROOTFS_URL:-}"
ass_download_url="${AIYOLO_ASS_DOWNLOAD_URL:-}"
ass_sha256_url="${AIYOLO_ASS_SHA256_URL:-}"
ass_sha256="${AIYOLO_ASS_SHA256:-}"
image="${AIYOLO_CLOUD_AGENT_IMAGE:-aiyolo/local-cloud-agent:ubuntu-24.04-v2}"
skip_proxy_env="${AIYOLO_CLOUD_AGENT_SKIP_PROXY_ENV:-0}"
buildkit="${DOCKER_BUILDKIT:-1}"

if [[ "${buildkit}" == "1" ]] && ! docker buildx version >/dev/null 2>&1; then
  buildkit=0
fi

proxy_env=()
if [[ "${skip_proxy_env}" == "1" ]]; then
  for key in ALL_PROXY HTTP_PROXY HTTPS_PROXY NO_PROXY all_proxy http_proxy https_proxy no_proxy; do
    proxy_env+=(-u "${key}")
  done
fi

run_with_optional_proxy() {
  env "${proxy_env[@]}" "$@"
}

resolve_ass_sha256() {
  local resolved="${ass_sha256}"
  if [[ -z "${resolved}" && -n "${ass_sha256_url}" ]]; then
    resolved="$(run_with_optional_proxy curl -fsSL "${ass_sha256_url}" | awk 'NF { print $1; exit }')"
  fi
  printf '%s' "${resolved}" | tr '[:upper:]' '[:lower:]'
}

if [[ -n "${ass_download_url}" && -z "${ass_sha256_url}" ]]; then
  ass_sha256_url="${ass_download_url}.sha256"
fi

if [[ -z "${rootfs_url}" ]]; then
  rootfs_name="$(
    run_with_optional_proxy curl -fsSL "${rootfs_index_url%/}/" \
      | grep -o "ubuntu-base-${series//./\\.}\\.[0-9]\\+-base-amd64.tar.gz" \
      | sort -V \
      | tail -n 1
  )"
  if [[ -z "${rootfs_name}" ]]; then
    echo "无法从 ${rootfs_index_url} 解析 Ubuntu Base rootfs 包名" >&2
    exit 1
  fi
  rootfs_url="${rootfs_index_url%/}/${rootfs_name}"
fi

cleanup() {
  rm -rf "${build_context}"
}
trap cleanup EXIT

rsync -a "${template_context}/" "${build_context}/"
if [[ -n "${ass_download_url}" ]]; then
  run_with_optional_proxy curl -fL --retry 5 --connect-timeout 30 "${ass_download_url}" -o "${build_context}/aiyolo-ass"
  ass_sha256="$(resolve_ass_sha256)"
  if [[ -z "${ass_sha256}" ]]; then
    echo "缺少 AIYOLO_ASS_SHA256 或可访问的 AIYOLO_ASS_SHA256_URL" >&2
    exit 1
  fi
  actual_ass_sha256="$(sha256sum "${build_context}/aiyolo-ass" | cut -d' ' -f1)"
  if [[ "${actual_ass_sha256}" != "${ass_sha256}" ]]; then
    echo "下载的 aiyolo-ass 校验和不匹配: ${actual_ass_sha256}" >&2
    exit 1
  fi
elif command -v "${GO:-go}" >/dev/null 2>&1; then
  (cd "${repo_root}" && env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${GO:-go}" build -o "${build_context}/aiyolo-ass" ./cmd/aiyolo-ass)
  ass_sha256="$(sha256sum "${build_context}/aiyolo-ass" | cut -d' ' -f1)"
else
  echo "未找到 Go，且未提供 AIYOLO_ASS_DOWNLOAD_URL" >&2
  exit 1
fi
chmod 0755 "${build_context}/aiyolo-ass"
build_revision="sha256:$(printf '%s\n%s\n%s\n%s\n' "${release}" "${mirror}" "${chrome_deb_url}" "${ass_sha256}" | sha256sum | cut -d' ' -f1)"
run_with_optional_proxy curl -fL --retry 5 --connect-timeout 30 "${rootfs_url}" -o "${build_context}/rootfs.tar.gz"

build_args=(
  --pull
  --build-arg "UBUNTU_RELEASE=${release}"
  --build-arg "APT_MIRROR=${mirror}"
  --build-arg "CHROME_DEB_URL=${chrome_deb_url}"
  --build-arg "AIYOLO_ASS_SHA256=${ass_sha256}"
  --build-arg "AIYOLO_CLOUD_AGENT_BUILD_REVISION=${build_revision}"
  -t "${image}"
  -f "${build_context}/Dockerfile"
)

if [[ "${skip_proxy_env}" != "1" ]]; then
  for key in ALL_PROXY HTTP_PROXY HTTPS_PROXY NO_PROXY all_proxy http_proxy https_proxy no_proxy; do
    if [[ -n "${!key-}" ]]; then
      build_args+=(--build-arg "${key}=${!key}")
    fi
  done
fi

run_with_optional_proxy env DOCKER_BUILDKIT="${buildkit}" docker build "${build_args[@]}" "${build_context}"

printf 'image=%s\n' "${image}"
docker image inspect "${image}" --format 'image_id={{.Id}} size={{.Size}} bytes'
