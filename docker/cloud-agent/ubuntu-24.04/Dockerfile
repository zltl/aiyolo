FROM scratch

ADD rootfs.tar.gz /

ARG UBUNTU_RELEASE=noble
ARG APT_MIRROR=https://mirrors.aliyun.com/ubuntu
ARG CHROME_DEB_URL=https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb

ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8
ENV DISPLAY=:99
ENV XDG_RUNTIME_DIR=/tmp/runtime-root
ENV CHROME_BIN=/usr/bin/google-chrome-stable

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
    printf 'Acquire::Retries "5";\nAcquire::http::No-Cache "true";\nAcquire::https::No-Cache "true";\n' >/etc/apt/apt.conf.d/99aiyolo

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash bash-completion ca-certificates curl dbus-x11 file fluxbox fonts-dejavu-core fonts-liberation \
    git iproute2 iptables iputils-ping jq less libasound2t64 libatk-bridge2.0-0 libcups2t64 libgbm1 libgtk-3-0 \
    libnss3 libu2f-udev libxss1 libxtst6 mesa-utils netcat-openbsd procps psmisc python3 python3-dev python3-pip \
    python3-venv rsync sudo tini tzdata uidmap unzip wget x11vnc xauth xdg-utils xterm xvfb zip \
    docker.io fuse-overlayfs \
 && wget -nv --tries=3 --timeout=30 -O /tmp/google-chrome.deb "${CHROME_DEB_URL}" \
 && apt-get install -y /tmp/google-chrome.deb \
 && rm -f /tmp/google-chrome.deb \
 && install -d -m 0755 /workspace /etc/aiyolo /usr/local/bin /tmp/runtime-root \
 && rm -rf /var/lib/apt/lists/*

COPY cloud-agent-base.json /etc/aiyolo/cloud-agent-base.json
COPY aiyolo-cloud-agent-entrypoint /usr/local/bin/aiyolo-cloud-agent-entrypoint
COPY aiyolo-cloud-agent-info /usr/local/bin/aiyolo-cloud-agent-info
COPY aiyolo-cloud-agent-start-display /usr/local/bin/aiyolo-cloud-agent-start-display
COPY aiyolo-cloud-agent-open-chrome /usr/local/bin/aiyolo-cloud-agent-open-chrome
COPY aiyolo-cloud-agent-start-docker /usr/local/bin/aiyolo-cloud-agent-start-docker
COPY aiyolo-cloud-agent-start-services /usr/local/bin/aiyolo-cloud-agent-start-services

RUN chmod 0755 \
    /usr/local/bin/aiyolo-cloud-agent-entrypoint \
    /usr/local/bin/aiyolo-cloud-agent-info \
    /usr/local/bin/aiyolo-cloud-agent-start-display \
    /usr/local/bin/aiyolo-cloud-agent-open-chrome \
    /usr/local/bin/aiyolo-cloud-agent-start-docker \
    /usr/local/bin/aiyolo-cloud-agent-start-services

WORKDIR /workspace

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/aiyolo-cloud-agent-entrypoint"]
CMD ["/usr/local/bin/aiyolo-cloud-agent-start-services"]
