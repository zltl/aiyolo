FROM scratch

ADD rootfs.tar.gz /

ARG UBUNTU_RELEASE=resolute
ARG APT_MIRROR=http://mirrors.aliyun.com/ubuntu
ARG CHROME_DEB_URL=https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb
ARG AIYOLO_ASS_SHA256=
ARG AIYOLO_CLOUD_AGENT_BUILD_REVISION=dev
ARG NODE_VERSION=20.20.2
ARG CODEX_VERSION=latest

ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8
ENV DISPLAY=:99
ENV XDG_RUNTIME_DIR=/tmp/runtime-aiyolo
ENV CHROME_BIN=/usr/bin/google-chrome-stable
ENV AIYOLO_AGENT_USER=aiyolo
ENV AIYOLO_AGENT_HOME=/workspace
ENV CODEX_HOME=/workspace/.codex
ENV HOME=/workspace
ENV USER=aiyolo

LABEL aiyolo.ass.sha256="${AIYOLO_ASS_SHA256}"
LABEL aiyolo.cloud_agent.build_revision="${AIYOLO_CLOUD_AGENT_BUILD_REVISION}"

RUN set -eux; \
    mirror="${APT_MIRROR%/}"; \
    if [ -f /etc/apt/sources.list ]; then \
      sed -i \
        -e "s|http://archive.ubuntu.com/ubuntu|${mirror}|g" \
        -e "s|http://security.ubuntu.com/ubuntu|${mirror}|g" \
        -e "s|https://archive.ubuntu.com/ubuntu|${mirror}|g" \
        -e "s|https://security.ubuntu.com/ubuntu|${mirror}|g" \
        /etc/apt/sources.list; \
    else \
      printf 'deb %s %s main restricted universe multiverse\ndeb %s %s-updates main restricted universe multiverse\ndeb %s %s-security main restricted universe multiverse\n' \
        "${mirror}" "${UBUNTU_RELEASE}" "${mirror}" "${UBUNTU_RELEASE}" "${mirror}" "${UBUNTU_RELEASE}" >/etc/apt/sources.list; \
    fi; \
    if [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then \
      sed -i -E "s|^URIs: .*$|URIs: ${mirror}|g" /etc/apt/sources.list.d/ubuntu.sources; \
    fi; \
    printf 'Acquire::Retries "5";\nAcquire::http::No-Cache "true";\nAcquire::https::No-Cache "true";\n' >/etc/apt/apt.conf.d/99aiyolo

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash bash-completion build-essential ca-certificates cmake curl dbus-x11 dnsutils fd-find file fluxbox fonts-dejavu-core fonts-liberation \
    gdb git git-lfs gnupg htop iproute2 iptables iputils-ping jq less libasound2t64 libatk-bridge2.0-0 libcups2t64 libgbm1 libgtk-3-0 \
    libnss3 libxss1 libxtst6 lsof mesa-utils nano net-tools netcat-openbsd openssh-client pkg-config procps psmisc python3 python3-dev python3-pip \
    python3-venv ripgrep rsync strace sudo tini tmux tree tzdata uidmap unzip vim wget x11vnc xauth xdg-utils xterm xvfb zip \
    docker.io fuse-overlayfs \
 && groupadd --gid 1000 "${AIYOLO_AGENT_USER}" \
 && useradd --uid 1000 --gid 1000 --create-home --home-dir "${AIYOLO_AGENT_HOME}" --shell /bin/bash "${AIYOLO_AGENT_USER}" \
 && usermod -aG sudo,docker "${AIYOLO_AGENT_USER}" \
 && install -d -o "${AIYOLO_AGENT_USER}" -g "${AIYOLO_AGENT_USER}" -m 0755 /workspace "${CODEX_HOME}" "${XDG_RUNTIME_DIR}" /run/aiyolo \
 && install -d -m 0755 /etc/aiyolo /etc/sudoers.d /usr/local/bin /opt \
 && printf '%s ALL=(ALL) NOPASSWD:ALL\n' "${AIYOLO_AGENT_USER}" >/etc/sudoers.d/aiyolo \
 && chmod 0440 /etc/sudoers.d/aiyolo \
 && if [ -x /usr/bin/fdfind ]; then ln -sf /usr/bin/fdfind /usr/local/bin/fd; fi \
 && wget -nv --tries=3 --timeout=30 -O /tmp/google-chrome.deb "${CHROME_DEB_URL}" \
 && apt-get install -y /tmp/google-chrome.deb \
 && rm -f /tmp/google-chrome.deb \
 && curl -fsSL --retry 5 --connect-timeout 30 "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.gz" -o /tmp/node.tar.gz \
 && tar -xzf /tmp/node.tar.gz -C /opt \
 && ln -sf "/opt/node-v${NODE_VERSION}-linux-x64/bin/node" /usr/local/bin/node \
 && ln -sf "/opt/node-v${NODE_VERSION}-linux-x64/bin/npm" /usr/local/bin/npm \
 && ln -sf "/opt/node-v${NODE_VERSION}-linux-x64/bin/npx" /usr/local/bin/npx \
 && npm install -g --prefix /usr/local "@openai/codex@${CODEX_VERSION}" --cache /tmp/.npm-cache --fund=false --audit=false \
 && node --version \
 && npm --version \
 && codex --version \
 && rm -f /tmp/node.tar.gz \
 && rm -rf /tmp/.npm-cache \
 && rm -rf /var/lib/apt/lists/*

COPY cloud-agent-base.json /etc/aiyolo/cloud-agent-base.json
COPY aiyolo-ass /usr/local/bin/aiyolo-ass
COPY aiyolo-cloud-agent-entrypoint /usr/local/bin/aiyolo-cloud-agent-entrypoint
COPY aiyolo-cloud-agent-info /usr/local/bin/aiyolo-cloud-agent-info
COPY aiyolo-cloud-agent-start-display /usr/local/bin/aiyolo-cloud-agent-start-display
COPY aiyolo-cloud-agent-open-chrome /usr/local/bin/aiyolo-cloud-agent-open-chrome
COPY aiyolo-cloud-agent-start-docker /usr/local/bin/aiyolo-cloud-agent-start-docker
COPY aiyolo-cloud-agent-start-services /usr/local/bin/aiyolo-cloud-agent-start-services

RUN chmod 0755 \
  /usr/local/bin/aiyolo-ass \
    /usr/local/bin/aiyolo-cloud-agent-entrypoint \
    /usr/local/bin/aiyolo-cloud-agent-info \
    /usr/local/bin/aiyolo-cloud-agent-start-display \
    /usr/local/bin/aiyolo-cloud-agent-open-chrome \
    /usr/local/bin/aiyolo-cloud-agent-start-docker \
    /usr/local/bin/aiyolo-cloud-agent-start-services

WORKDIR /workspace

USER ${AIYOLO_AGENT_USER}

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/aiyolo-cloud-agent-entrypoint"]
CMD ["/usr/local/bin/aiyolo-cloud-agent-start-services"]
