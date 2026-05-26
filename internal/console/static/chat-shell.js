(function () {
  const page = document.querySelector("[data-chat-shell-page]");
  if (!(page instanceof HTMLElement)) {
    return;
  }

  const terminalHost = page.querySelector("[data-chat-shell-terminal]");
  const statusNode = page.querySelector("[data-chat-shell-status]");
  const reconnectButton = page.querySelector("[data-chat-shell-action=\"reconnect\"]");
  const locale = String(document.documentElement.lang || "").toLowerCase();
  const useChinese = locale.startsWith("zh");
  const t = (zh, en) => (useChinese ? zh : en);
  const socketPath = String(page.dataset.chatShellSocketUrl || "").trim();

  const setStatus = (message, isError = false) => {
    if (!(statusNode instanceof HTMLElement)) {
      return;
    }
    statusNode.textContent = String(message || "").trim();
    statusNode.classList.toggle("is-error", Boolean(isError));
  };

  if (!(terminalHost instanceof HTMLElement) || socketPath === "" || typeof window.Terminal !== "function") {
    setStatus(t("Shell 依赖加载失败。", "Shell dependencies failed to load."), true);
    return;
  }

  const term = new window.Terminal({
    cursorBlink: true,
    fontFamily: '"IBM Plex Mono", "SFMono-Regular", Consolas, "Liberation Mono", monospace',
    fontSize: 14,
    lineHeight: 1.35,
    theme: {
      background: "#0d131b",
      foreground: "#eef2f6",
      cursor: "#f4f7fb",
      cursorAccent: "#0d131b",
      selectionBackground: "rgba(128, 175, 255, 0.28)",
      black: "#11161d",
      red: "#ff7b72",
      green: "#9fd089",
      yellow: "#e6c15c",
      blue: "#73a8ff",
      magenta: "#c792ea",
      cyan: "#67d7c4",
      white: "#e8edf3",
      brightBlack: "#5e6873",
      brightRed: "#ff9b8b",
      brightGreen: "#b9e394",
      brightYellow: "#f1d98e",
      brightBlue: "#9fc1ff",
      brightMagenta: "#deb4ff",
      brightCyan: "#9ee9db",
      brightWhite: "#ffffff",
    },
  });
  const fitAddon = window.FitAddon && typeof window.FitAddon.FitAddon === "function"
    ? new window.FitAddon.FitAddon()
    : null;
  if (fitAddon) {
    term.loadAddon(fitAddon);
  }
  term.open(terminalHost);

  let socket = null;

  const buildSocketURL = () => {
    const nextURL = new URL(socketPath, window.location.href);
    nextURL.protocol = nextURL.protocol === "https:" ? "wss:" : "ws:";
    return nextURL.toString();
  };

  const fitTerminal = () => {
    if (fitAddon) {
      fitAddon.fit();
      return;
    }
    const cols = Math.max(80, Math.floor(terminalHost.clientWidth / 9));
    const rows = Math.max(24, Math.floor(terminalHost.clientHeight / 21));
    term.resize(cols, rows);
  };

  const send = (payload) => {
    if (!(socket instanceof WebSocket) || socket.readyState !== WebSocket.OPEN) {
      return;
    }
    socket.send(JSON.stringify(payload));
  };

  const sendResize = () => {
    send({ type: "resize", cols: term.cols || 120, rows: term.rows || 32 });
  };

  const connect = () => {
    if (socket instanceof WebSocket) {
      socket.close();
    }
    term.reset();
    fitTerminal();
    setStatus(t("正在连接 shell…", "Connecting shell…"));
    socket = new WebSocket(buildSocketURL());
    socket.addEventListener("open", () => {
      setStatus(t("Shell 已连接", "Shell connected"));
      sendResize();
      term.focus();
    });
    socket.addEventListener("message", (event) => {
      let payload = null;
      try {
        payload = JSON.parse(String(event.data || "{}"));
      } catch (_error) {
        return;
      }
      switch (String(payload?.type || "")) {
        case "ready":
          if (payload.message) {
            setStatus(payload.message, false);
          }
          break;
        case "output":
          if (payload.data) {
            term.write(String(payload.data));
          }
          break;
        case "error":
          if (payload.message) {
            setStatus(payload.message, true);
            term.write(`\r\n${String(payload.message)}\r\n`);
          }
          break;
        case "closed":
          if (payload.message) {
            setStatus(payload.message, true);
          }
          break;
        default:
          if (payload && payload.data) {
            term.write(String(payload.data));
          }
          break;
      }
    });
    socket.addEventListener("close", () => {
      setStatus(t("Shell 已断开", "Shell disconnected"), true);
    });
    socket.addEventListener("error", () => {
      setStatus(t("Shell 连接失败。", "Shell connection failed."), true);
    });
  };

  term.onData((data) => {
    send({ type: "input", data });
  });

  if (reconnectButton instanceof HTMLButtonElement) {
    reconnectButton.addEventListener("click", () => {
      connect();
    });
  }

  window.addEventListener("resize", () => {
    fitTerminal();
    sendResize();
  });

  fitTerminal();
  connect();
})();