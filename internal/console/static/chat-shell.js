(function () {
  const locale = String(document.documentElement.lang || "").toLowerCase();
  const useChinese = locale.startsWith("zh");
  const t = (zh, en) => (useChinese ? zh : en);

  const buildSocketURL = (socketPath) => {
    const nextURL = new URL(String(socketPath || "").trim(), window.location.href);
    nextURL.protocol = nextURL.protocol === "https:" ? "wss:" : "ws:";
    return nextURL.toString();
  };

  const createController = (options = {}) => {
    const terminalHost = options.terminalHost;
    const statusNode = options.statusNode;
    const getSocketPath = typeof options.getSocketPath === "function"
      ? options.getSocketPath
      : () => String(options.socketPath || "").trim();

    const setStatus = (message, isError = false) => {
      if (statusNode instanceof HTMLElement) {
        statusNode.textContent = String(message || "").trim();
        statusNode.classList.toggle("is-error", Boolean(isError));
      }
      if (typeof options.onStatusChange === "function") {
        options.onStatusChange({ message: String(message || "").trim(), isError: Boolean(isError) });
      }
    };

    if (!(terminalHost instanceof HTMLElement)) {
      setStatus(t("Shell 容器不存在。", "Shell host is missing."), true);
      return null;
    }
    if (typeof window.Terminal !== "function") {
      setStatus(t("Shell 依赖加载失败。", "Shell dependencies failed to load."), true);
      return null;
    }

    const term = new window.Terminal({
      allowTransparency: true,
      convertEol: true,
      cursorBlink: true,
      drawBoldTextInBrightColors: true,
      fontFamily: '"IBM Plex Mono", "SFMono-Regular", Consolas, "Liberation Mono", monospace',
      fontSize: 14,
      lineHeight: 1.35,
      minimumContrastRatio: 4.5,
      scrollback: 12000,
      tabStopWidth: 4,
      theme: {
        background: "#0d131b",
        foreground: "#eef2f6",
        cursor: "#f4f7fb",
        cursorAccent: "#0d131b",
        selectionBackground: "rgba(128, 175, 255, 0.28)",
        selectionForeground: "#ffffff",
        selectionInactiveBackground: "rgba(128, 175, 255, 0.16)",
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
    let manualClose = false;
    let disposed = false;
    let lastErrorMessage = "";
    let lastCloseMessage = "";
    let resizeObserver = null;
    let windowResizeHandler = null;
    let pendingInputs = [];

    const fitTerminal = () => {
      if (disposed) {
        return;
      }
      if (fitAddon) {
        fitAddon.fit();
        return;
      }
      const cols = Math.max(80, Math.floor(terminalHost.clientWidth / 9));
      const rows = Math.max(16, Math.floor(terminalHost.clientHeight / 21));
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

    const sendInput = (data) => {
      if (disposed) {
        return false;
      }
      const value = String(data || "");
      if (value === "") {
        return false;
      }
      if (socket instanceof WebSocket && socket.readyState === WebSocket.OPEN) {
        send({ type: "input", data: value });
      } else {
        pendingInputs.push(value);
      }
      term.focus();
      return true;
    };

    const handleOutput = (data) => {
      const value = String(data || "");
      if (value === "") {
        return;
      }
      if (typeof options.onOutput === "function") {
        try {
          options.onOutput(value);
        } catch (_error) {
          // Keep terminal rendering independent from page-level observers.
        }
      }
      term.write(value);
    };

    const connect = (connectOptions = {}) => {
      if (disposed) {
        return false;
      }
      const socketPath = String(getSocketPath() || "").trim();
      if (socketPath === "") {
        setStatus(t("Shell 会话未生成，请重试。", "The shell session could not be created. Please retry."), true);
        return false;
      }
      manualClose = false;
      lastErrorMessage = "";
      lastCloseMessage = "";
      if (socket instanceof WebSocket) {
        try {
          socket.close();
        } catch (_error) {
          // Ignore socket close races and continue with the new connection.
        }
        socket = null;
      }
      if (connectOptions.resetTerminal !== false) {
        term.reset();
      }
      fitTerminal();
      setStatus(
        connectOptions.retryCount > 0
          ? t("正在重新连接 shell…", "Reconnecting shell…")
          : t("正在连接 shell…", "Connecting shell…"),
      );
      const nextSocket = new WebSocket(buildSocketURL(socketPath));
      socket = nextSocket;
      nextSocket.addEventListener("open", () => {
        if (disposed || socket !== nextSocket) {
          return;
        }
        sendResize();
        pendingInputs.splice(0).forEach((data) => send({ type: "input", data }));
        term.focus();
      });
      nextSocket.addEventListener("message", (event) => {
        if (disposed || socket !== nextSocket) {
          return;
        }
        let payload = null;
        try {
          payload = JSON.parse(String(event.data || "{}"));
        } catch (_error) {
          return;
        }
        switch (String(payload?.type || "")) {
          case "ready":
            lastErrorMessage = "";
            lastCloseMessage = "";
            setStatus(String(payload.message || t("Claude Code 已连接", "Claude Code connected")), false);
            break;
          case "output":
            if (payload.data) {
              handleOutput(payload.data);
            }
            break;
          case "error": {
            lastErrorMessage = String(payload.message || t("Shell 连接失败。", "Shell connection failed.")).trim();
            if (lastErrorMessage !== "") {
              setStatus(lastErrorMessage, true);
              term.write(`\r\n${lastErrorMessage}\r\n`);
            }
            break;
          }
          case "closed":
            lastCloseMessage = String(payload.message || "").trim();
            if (lastCloseMessage !== "" && lastErrorMessage === "") {
              setStatus(lastCloseMessage, true);
            }
            break;
          default:
            if (payload && payload.data) {
              handleOutput(payload.data);
            }
            break;
        }
      });
      nextSocket.addEventListener("close", () => {
        if (socket !== nextSocket) {
          return;
        }
        socket = null;
        if (disposed) {
          return;
        }
        if (manualClose) {
          setStatus(t("Shell 已关闭", "Shell closed"), false);
          if (typeof options.onClosed === "function") {
            options.onClosed({ manual: true });
          }
          return;
        }
        const message = lastErrorMessage || lastCloseMessage || t("Shell 已断开", "Shell disconnected");
        setStatus(`${message} ${t("请手动点击重连。", "Use reconnect to try again.")}`, true);
        if (typeof options.onClosed === "function") {
          options.onClosed({ manual: false, message });
        }
      });
      nextSocket.addEventListener("error", () => {
        if (disposed || socket !== nextSocket) {
          return;
        }
        if (lastErrorMessage === "") {
          lastErrorMessage = t("Shell 连接失败。", "Shell connection failed.");
        }
        setStatus(lastErrorMessage, true);
      });
      return true;
    };

    const close = (closeOptions = {}) => {
      if (disposed) {
        return;
      }
      manualClose = true;
      if (socket instanceof WebSocket) {
        const activeSocket = socket;
        socket = null;
        if (closeOptions.terminate === true && activeSocket.readyState === WebSocket.OPEN) {
          try {
            activeSocket.send(JSON.stringify({ type: "close" }));
          } catch (_error) {
            // Continue closing the local socket even if the termination signal cannot be sent.
          }
        }
        try {
          activeSocket.close();
        } catch (_error) {
          setStatus(t("Shell 已关闭", "Shell closed"), false);
        }
        return;
      }
      setStatus(t("Shell 已关闭", "Shell closed"), false);
    };

    const clear = () => {
      if (disposed) {
        return;
      }
      term.clear();
      term.focus();
    };

    const refresh = () => {
      fitTerminal();
      sendResize();
    };

    const dispose = (disposeOptions = {}) => {
      if (disposed) {
        return;
      }
      disposed = true;
      manualClose = true;
      if (resizeObserver) {
        resizeObserver.disconnect();
        resizeObserver = null;
      }
      if (windowResizeHandler) {
        window.removeEventListener("resize", windowResizeHandler);
        windowResizeHandler = null;
      }
      if (socket instanceof WebSocket) {
        if (disposeOptions.terminate === true && socket.readyState === WebSocket.OPEN) {
          try {
            socket.send(JSON.stringify({ type: "close" }));
          } catch (_error) {
            // Ignore termination send failures during teardown.
          }
        }
        try {
          socket.close();
        } catch (_error) {
          // Ignore socket shutdown errors during teardown.
        }
        socket = null;
      }
      term.dispose();
    };

    if (typeof ResizeObserver === "function") {
      resizeObserver = new ResizeObserver(() => {
        refresh();
      });
      resizeObserver.observe(terminalHost);
    }
    windowResizeHandler = () => {
      refresh();
    };
    window.addEventListener("resize", windowResizeHandler);

    term.onData((data) => {
      send({ type: "input", data });
    });

    fitTerminal();
    if (options.autoConnect !== false) {
      connect({ resetTerminal: true });
    }

    return {
      connect,
      close,
      clear,
      sendInput,
      dispose,
      refresh,
      focus: () => term.focus(),
      isConnected: () => socket instanceof WebSocket && socket.readyState === WebSocket.OPEN,
    };
  };

  window.AIYoloChatShell = {
    createController,
  };

  const page = document.querySelector("[data-chat-shell-page]");
  if (!(page instanceof HTMLElement)) {
    return;
  }
  const terminalHost = page.querySelector("[data-chat-shell-terminal]");
  const statusNode = page.querySelector("[data-chat-shell-status]");
  const reconnectButton = page.querySelector("[data-chat-shell-action=\"reconnect\"]");
  const socketPath = String(page.dataset.chatShellSocketUrl || "").trim();
  const controller = createController({
    terminalHost,
    statusNode,
    getSocketPath: () => socketPath,
    autoConnect: true,
  });
  if (!controller) {
    return;
  }
  if (reconnectButton instanceof HTMLButtonElement) {
    reconnectButton.addEventListener("click", () => {
      controller.connect({ resetTerminal: true });
    });
  }
})();
