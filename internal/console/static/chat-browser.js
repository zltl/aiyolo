(() => {
  if (window.__aiyoloChatBrowserBound) {
    return;
  }
  window.__aiyoloChatBrowserBound = true;

  const t = (zh, _en) => zh;

  const currentRoot = () => document.getElementById("chat-content");
  const currentForm = () => currentRoot()?.querySelector(".chat-shell[data-chat-stream-url]") || null;

  const browserReadyURL = (form) => String(form?.dataset.chatBrowserReadyUrl || "").trim();
  const browserViewURL = (form) => String(form?.dataset.chatBrowserViewUrl || "").trim();
  const browserNavigateURL = (form) => String(form?.dataset.chatBrowserNavigateUrl || "").trim();
  const browserPanel = (form) => form?.querySelector("[data-chat-browser-panel]") || null;
  const browserFrame = (form) => form?.querySelector("[data-chat-browser-frame]") || null;
  const browserToolbar = (form) => form?.querySelector("[data-chat-browser-toolbar]") || null;
  const browserURLInput = (form) => form?.querySelector("[data-chat-browser-url-input]") || null;
  const browserToggleButton = (form) => form?.querySelector(".chat-activitybar-browser-toggle") || null;
  const browserMCPPanel = (form) => form?.querySelector("[data-chat-browser-mcp]") || null;
  const browserMCPEnabledInput = (form) => form?.querySelector("[data-chat-browser-mcp-enabled]") || null;
  const browserMCPStatusNode = (form) => form?.querySelector("[data-chat-browser-mcp-status]") || null;
  const browserMCPStatusURL = (form) => String(form?.dataset.chatBrowserMcpStatusUrl || "").trim();
  const browserMCPConfigURL = (form) => String(form?.dataset.chatBrowserMcpConfigUrl || "").trim();

  const isCloudAgentEnvironment = (value) => String(value || "").trim().startsWith("cloud-agent:");

  const readClientSessionID = (form) => {
    const field = form?.querySelector("input[name=\"chat_client_session_id\"]");
    return field instanceof HTMLInputElement ? field.value.trim() : "";
  };

  const currentSelectedEnvironment = (form) => {
    const field = form?.querySelector("select[name=\"chat_environment\"], input[name=\"chat_environment\"]");
    if (field instanceof HTMLSelectElement || field instanceof HTMLInputElement) {
      return String(field.value || "").trim();
    }
    return "";
  };

  let browserOpen = false;
  let browserLoading = false;
  let loadedSessionID = "";
  let mcpStatusTimer = 0;
  let mcpConfigInFlight = false;
  let mcpStatusRequest = 0;

  const mcpStatusLabel = (payload) => {
    if (!payload || typeof payload !== "object") {
      return t("未知", "Unknown");
    }
    if (!payload.enabled) {
      return t("已关闭", "Disabled");
    }
    const status = String(payload.status || "").trim().toLowerCase();
    const toolCount = Number(payload.toolCount || 0);
    if (status === "ready" && payload.connected) {
      return toolCount > 0
        ? t(`已连接 · ${toolCount} 工具`, `Connected · ${toolCount} tools`)
        : t("已连接", "Connected");
    }
    if (status === "pending") {
      return t("等待连接", "Waiting");
    }
    if (status === "unavailable") {
      return t("不可用", "Unavailable");
    }
    if (status === "error") {
      return t("异常", "Error");
    }
    return t("检查中…", "Checking…");
  };

  const applyMCPStatus = (form, payload, options = {}) => {
    const panel = browserMCPPanel(form);
    const statusNode = browserMCPStatusNode(form);
    const enabledInput = browserMCPEnabledInput(form);
    if (!(panel instanceof HTMLElement) || !(statusNode instanceof HTMLElement)) {
      return;
    }
    const enabled = Boolean(payload?.enabled);
    const status = String(payload?.status || "idle").trim().toLowerCase() || "idle";
    if (enabledInput instanceof HTMLInputElement && !options.skipToggleSync) {
      enabledInput.checked = enabled;
      enabledInput.disabled = mcpConfigInFlight;
    }
    statusNode.textContent = mcpStatusLabel(payload);
    statusNode.dataset.status = !enabled ? "disabled" : status;
    statusNode.title = String(payload?.notice || payload?.error || "").trim();
    panel.classList.toggle("is-disabled", !enabled);
    panel.classList.toggle("is-connected", enabled && Boolean(payload?.connected));
    panel.classList.toggle("is-pending", enabled && status === "pending");
    panel.classList.toggle("is-error", status === "error");
  };

  const refreshMCPStatus = async (form = currentForm()) => {
    if (!(form instanceof HTMLFormElement)) {
      return null;
    }
    const endpoint = browserMCPStatusURL(form);
    const sessionID = readClientSessionID(form);
    if (endpoint === "" || sessionID === "" || !isCloudAgentEnvironment(currentSelectedEnvironment(form))) {
      return null;
    }
    const requestID = ++mcpStatusRequest;
    applyMCPStatus(form, { enabled: browserMCPEnabledInput(form) instanceof HTMLInputElement ? browserMCPEnabledInput(form).checked : true, status: "checking" }, { skipToggleSync: true });
    try {
      const url = new URL(endpoint, window.location.href);
      url.searchParams.set("session", sessionID);
      const response = await fetch(url.toString(), {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      });
      const payload = await response.json();
      if (requestID !== mcpStatusRequest) {
        return null;
      }
      if (!response.ok) {
        throw new Error(String(payload?.error || t("无法读取 MCP 状态。", "Unable to read MCP status.")));
      }
      applyMCPStatus(form, payload);
      return payload;
    } catch (error) {
      if (requestID !== mcpStatusRequest) {
        return null;
      }
      applyMCPStatus(form, {
        enabled: browserMCPEnabledInput(form) instanceof HTMLInputElement ? browserMCPEnabledInput(form).checked : true,
        status: "error",
        error: String(error?.message || t("无法读取 MCP 状态。", "Unable to read MCP status.")),
      }, { skipToggleSync: true });
      return null;
    }
  };

  const setMCPEnabled = async (form, enabled) => {
    const endpoint = browserMCPConfigURL(form);
    const sessionID = readClientSessionID(form);
    if (!(form instanceof HTMLFormElement) || endpoint === "" || sessionID === "" || mcpConfigInFlight) {
      return false;
    }
    mcpConfigInFlight = true;
    syncMCPControls(form);
    try {
      const response = await fetch(endpoint, {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ sessionId: sessionID, enabled: Boolean(enabled) }),
      });
      const payload = await response.json();
      if (!response.ok || String(payload.status || "") !== "ok") {
        throw new Error(String(payload?.error || t("更新 MCP 设置失败。", "Failed to update MCP settings.")));
      }
      await refreshMCPStatus(form);
      return true;
    } catch (error) {
      await refreshMCPStatus(form);
      return false;
    } finally {
      mcpConfigInFlight = false;
      syncMCPControls(form);
    }
  };

  const scheduleMCPStatusRefresh = (form = currentForm()) => {
    window.clearInterval(mcpStatusTimer);
    mcpStatusTimer = 0;
    if (!(form instanceof HTMLFormElement) || browserMCPStatusURL(form) === "" || !isCloudAgentEnvironment(currentSelectedEnvironment(form))) {
      return;
    }
    void refreshMCPStatus(form);
    mcpStatusTimer = window.setInterval(() => {
      void refreshMCPStatus(currentForm());
    }, 20000);
  };

  const syncMCPControls = (form = currentForm()) => {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    const panel = browserMCPPanel(form);
    const enabled = browserMCPStatusURL(form) !== "" && isCloudAgentEnvironment(currentSelectedEnvironment(form));
    if (panel instanceof HTMLElement) {
      panel.hidden = !enabled;
    }
    const enabledInput = browserMCPEnabledInput(form);
    if (enabledInput instanceof HTMLInputElement) {
      enabledInput.disabled = mcpConfigInFlight || !enabled;
    }
    if (enabled) {
      scheduleMCPStatusRefresh(form);
    } else {
      window.clearInterval(mcpStatusTimer);
      mcpStatusTimer = 0;
      mcpStatusRequest += 1;
    }
  };

  const setBrowserStatus = (form, message, isError = false) => {
    const status = form?.querySelector("[data-chat-editor-status]");
    if (!(status instanceof HTMLElement)) {
      return;
    }
    const text = String(message || "").trim();
    status.hidden = text === "";
    status.textContent = text;
    status.classList.toggle("is-error", Boolean(isError) && text !== "");
  };

  const syncBrowserSurface = (form) => {
    const panel = browserPanel(form);
    const toolbar = browserToolbar(form);
    const preview = form?.querySelector("[data-chat-editor-preview]");
    const markdown = form?.querySelector("[data-chat-editor-markdown]");
    const code = form?.querySelector("[data-chat-editor-code]");
    if (panel instanceof HTMLElement) {
      panel.hidden = !browserOpen;
    }
    if (toolbar instanceof HTMLElement) {
      toolbar.hidden = !browserOpen;
    }
    if (preview instanceof HTMLElement) {
      preview.hidden = browserOpen || preview.hidden;
    }
    if (markdown instanceof HTMLElement) {
      markdown.hidden = browserOpen || markdown.hidden;
    }
    if (code instanceof HTMLElement) {
      code.hidden = browserOpen;
    }
    const heading = form?.querySelector(".chat-editor-heading");
    if (heading instanceof HTMLElement) {
      heading.hidden = browserOpen;
    }
    const pathNode = form?.querySelector("[data-chat-editor-path]");
    if (pathNode instanceof HTMLElement && browserOpen) {
      pathNode.textContent = t("容器浏览器", "Container browser");
    }
    const directoryNode = form?.querySelector("[data-chat-editor-directory]");
    if (directoryNode instanceof HTMLElement) {
      directoryNode.hidden = browserOpen ? true : directoryNode.hidden;
    }
    form?.classList.toggle("is-browser-open", browserOpen);
    window.dispatchEvent(new CustomEvent("aiyolo:chat-browser-layout", { detail: { open: browserOpen } }));
  };

  const buildBrowserViewURL = (form, sessionID) => {
    const base = browserViewURL(form);
    if (base === "" || sessionID === "") {
      return "";
    }
    const url = new URL(base, window.location.href);
    url.searchParams.set("session", sessionID);
    return url.toString();
  };

  const loadBrowserFrame = async (form) => {
    const frame = browserFrame(form);
    const sessionID = readClientSessionID(form);
    const readyEndpoint = browserReadyURL(form);
    if (!(frame instanceof HTMLIFrameElement) || sessionID === "" || readyEndpoint === "") {
      return false;
    }
    if (loadedSessionID === sessionID && frame.src !== "") {
      syncBrowserSurface(form);
      return true;
    }
    browserLoading = true;
    setBrowserStatus(form, t("正在连接容器浏览器…", "Connecting to the container browser..."), false);
    try {
      const url = new URL(readyEndpoint, window.location.href);
      url.searchParams.set("session", sessionID);
      const response = await fetch(url.toString(), {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      });
      const payload = await response.json();
      if (!response.ok || String(payload.status || "") !== "ready") {
        throw new Error(String(payload.error || t("容器浏览器不可用。", "Container browser is unavailable.")));
      }
      const viewURL = String(payload.viewUrl || buildBrowserViewURL(form, sessionID) || "").trim();
      if (viewURL === "") {
        throw new Error(t("容器浏览器视图地址不可用。", "Container browser view URL is unavailable."));
      }
      frame.src = viewURL;
      loadedSessionID = sessionID;
      setBrowserStatus(form, "", false);
      syncBrowserSurface(form);
      return true;
    } catch (error) {
      setBrowserStatus(form, String(error?.message || t("连接容器浏览器失败。", "Failed to connect to the container browser.")), true);
      return false;
    } finally {
      browserLoading = false;
    }
  };

  const openBrowser = async (form) => {
    if (!(form instanceof HTMLFormElement)) {
      return false;
    }
    browserOpen = true;
    layoutStateBridge(form, true);
    syncBrowserSurface(form);
    return loadBrowserFrame(form);
  };

  const closeBrowser = (form) => {
    browserOpen = false;
    syncBrowserSurface(form);
    layoutStateBridge(form, false);
    setBrowserStatus(form, "", false);
  };

  const toggleBrowser = async (form) => {
    if (browserOpen) {
      closeBrowser(form);
      return;
    }
    await openBrowser(form);
  };

  const layoutStateBridge = (form, open) => {
    if (typeof window.AIYoloChatWorkspace?.setBrowserOpen === "function") {
      window.AIYoloChatWorkspace.setBrowserOpen(form, open);
      return;
    }
    if (open) {
      form.classList.remove("is-editor-collapsed");
    }
  };

  const navigateBrowser = async (form, rawURL) => {
    const endpoint = browserNavigateURL(form);
    const sessionID = readClientSessionID(form);
    const targetURL = String(rawURL || "").trim();
    if (endpoint === "" || sessionID === "" || targetURL === "") {
      return false;
    }
    setBrowserStatus(form, t("正在打开页面…", "Opening page..."), false);
    try {
      const response = await fetch(endpoint, {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ sessionId: sessionID, url: targetURL }),
      });
      const payload = await response.json();
      if (!response.ok || String(payload.status || "") !== "ok") {
        throw new Error(String(payload.error || t("打开页面失败。", "Failed to open the page.")));
      }
      if (!browserOpen) {
        await openBrowser(form);
      } else {
        await loadBrowserFrame(form);
      }
      setBrowserStatus(form, String(payload.notice || t("已在容器浏览器中打开页面。", "Opened the page in the container browser.")), false);
      return true;
    } catch (error) {
      setBrowserStatus(form, String(error?.message || t("打开页面失败。", "Failed to open the page.")), true);
      return false;
    }
  };

  const syncBrowserControls = (form = currentForm()) => {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    const button = browserToggleButton(form);
    const enabled = browserReadyURL(form) !== "" && isCloudAgentEnvironment(currentSelectedEnvironment(form));
    if (button instanceof HTMLButtonElement) {
      button.hidden = !enabled;
      button.classList.toggle("is-active", browserOpen);
      button.disabled = browserLoading;
    }
    if (!enabled && browserOpen) {
      closeBrowser(form);
      loadedSessionID = "";
      const frame = browserFrame(form);
      if (frame instanceof HTMLIFrameElement) {
        frame.removeAttribute("src");
      }
    }
    syncBrowserSurface(form);
    syncMCPControls(form);
  };

  window.AIYoloChatBrowser = {
    isOpen: () => browserOpen,
    open: openBrowser,
    close: closeBrowser,
    toggle: toggleBrowser,
    navigate: navigateBrowser,
    sync: syncBrowserControls,
    refreshMCPStatus,
    setMCPEnabled,
    syncMCP: syncMCPControls,
    cdpURL: () => String(currentForm()?.dataset.chatBrowserCdpUrl || "").trim(),
    mcpURL: () => String(currentForm()?.dataset.chatBrowserMcpUrl || "").trim(),
    screenshotURL: () => String(currentForm()?.dataset.chatBrowserScreenshotUrl || "").trim(),
    resetSession() {
      loadedSessionID = "";
      const frame = browserFrame(currentForm());
      if (frame instanceof HTMLIFrameElement) {
        frame.removeAttribute("src");
      }
    },
  };

  const handleChatOperation = (event) => {
    const detail = event instanceof CustomEvent && event.detail && typeof event.detail === "object" ? event.detail : {};
    const operation = detail.operation && typeof detail.operation === "object" ? detail.operation : null;
    const form = detail.form instanceof HTMLFormElement ? detail.form : currentForm();
    if (!(form instanceof HTMLFormElement) || !operation) {
      return;
    }
    if (String(operation.category || "").trim().toLowerCase() !== "browser") {
      return;
    }
    const targetURL = String(operation.url || operation.detail || "").trim();
    if (targetURL === "") {
      if (String(operation.status || "").trim().toLowerCase() === "started") {
        void openBrowser(form);
      }
      return;
    }
    if (String(operation.status || "").trim().toLowerCase() === "started") {
      void navigateBrowser(form, targetURL);
    }
  };

  window.addEventListener("aiyolo:chat-operation", handleChatOperation);

  document.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    const actionTarget = target.closest("[data-chat-action]");
    if (!(actionTarget instanceof HTMLElement)) {
      return;
    }
    const form = currentForm();
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    switch (String(actionTarget.dataset.chatAction || "").trim()) {
      case "toggle-browser": {
        event.preventDefault();
        void toggleBrowser(form);
        return;
      }
      case "navigate-browser": {
        event.preventDefault();
        const input = browserURLInput(form);
        void navigateBrowser(form, input instanceof HTMLInputElement ? input.value : "");
        return;
      }
      default:
        return;
    }
  });

  document.addEventListener("keydown", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLInputElement) || !target.matches("[data-chat-browser-url-input]")) {
      return;
    }
    if (event.key !== "Enter") {
      return;
    }
    event.preventDefault();
    void navigateBrowser(currentForm(), target.value);
  });

  document.addEventListener("change", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLInputElement) || !target.matches("[data-chat-browser-mcp-enabled]")) {
      return;
    }
    const form = currentForm();
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    void setMCPEnabled(form, target.checked);
  });

  window.addEventListener("aiyolo:chat-state", (event) => {
    const detail = event instanceof CustomEvent && event.detail && typeof event.detail === "object" ? event.detail : {};
    if (String(detail.source || "") === "ensure-environment") {
      syncMCPControls(currentForm());
    }
  });

  window.addEventListener("aiyolo:chat-environment-ready", () => {
    syncBrowserControls(currentForm());
    syncMCPControls(currentForm());
  });

  const restoreBrowserFromLayout = () => {
    const form = currentForm();
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    if (typeof window.AIYoloChatWorkspace?.isBrowserOpen !== "function" || !window.AIYoloChatWorkspace.isBrowserOpen()) {
      return;
    }
    if (browserOpen) {
      return;
    }
    void openBrowser(form);
  };

  window.addEventListener("aiyolo:chat-state", () => {
    restoreBrowserFromLayout();
  });

  syncBrowserControls(currentForm());
  syncMCPControls(currentForm());
  restoreBrowserFromLayout();
})();
