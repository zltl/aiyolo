(() => {
  if (window.__aiyoloChatWorkspaceBound) {
    return;
  }
  window.__aiyoloChatWorkspaceBound = true;

  const t = (zh, _en) => zh;
  const ownProperty = Object.prototype.hasOwnProperty;
  const currentRoot = () => document.getElementById("chat-content");
  const currentForm = () => currentRoot()?.querySelector(".chat-shell[data-chat-stream-url]") || null;
  const layoutPreferenceKey = "aiyolo.console.chat.workspaceLayout";
  const sidebarPreferenceKey = "aiyolo.console.chat.sidebarCollapsed.v2";
  const imagePreviewBackgroundPreferenceKey = "aiyolo.console.chat.imagePreviewBackground";
  const sidebarDefaultWidth = 288;
  const sidebarMinWidth = 220;
  const editorDefaultWidth = 520;
  const editorMinWidth = 320;
  const workspaceDesktopBreakpoint = 1100;
  const workspaceMinChatWidth = 420;
  const workspaceMaxPreferredChatWidth = 860;

  let layoutState = readLayoutPreference();
  let workspaceSessionKey = "";
  let attachmentTreeRootLabel = "";
  let attachmentTreeCache = new Map();
  let attachmentOpenPaths = new Set([""]);
  let attachmentLoadingPaths = new Set();
  let attachmentActiveFilePath = "";
  let attachmentTreeStatusMessage = "";
  let attachmentTreeStatusError = false;
  let workspaceRootPath = "";
  let workspaceTreeCache = new Map();
  let workspaceOpenPaths = new Set([""]);
  let workspaceLoadingPaths = new Set();
  let workspaceActiveFilePath = "";
  let workspaceEditorKind = "text";
  let workspaceEditorMediaType = "";
  let workspaceEditorPreviewURL = "";
  let workspaceEditorContent = "";
  let workspaceEditorSavedContent = "";
  let workspaceEditorSize = 0;
  let workspaceEditorBusy = "";
  let workspaceEditorStatusMessage = "";
  let workspaceEditorStatusError = false;
  let workspaceTreeStatusMessage = "";
  let workspaceTreeStatusError = false;
  let workspaceImagePreviewBackground = readImagePreviewBackgroundPreference();
  let paneResizeState = null;

  function safeParseJSON(raw, fallback) {
    if (typeof raw !== "string" || raw.trim() === "") {
      return fallback;
    }
    try {
      const parsed = JSON.parse(raw);
      return parsed == null ? fallback : parsed;
    } catch (_error) {
      return fallback;
    }
  }

  function hiddenField(form, name) {
    return form?.querySelector(`input[name="${name}"]`) || null;
  }

  function readClientSessionID(form) {
    const field = hiddenField(form, "chat_client_session_id");
    return field instanceof HTMLInputElement ? field.value.trim() : "";
  }

  function environmentField(form) {
    return form?.querySelector("[data-chat-environment-input], select[name=\"chat_environment\"]") || null;
  }

  function currentSelectedEnvironment(form) {
    const field = environmentField(form);
    if (!(field instanceof HTMLInputElement || field instanceof HTMLSelectElement)) {
      return "local";
    }
    return field.value.trim() || "local";
  }

  function isCloudAgentEnvironment(value) {
    return String(value || "").trim().startsWith("cloud-agent:");
  }

  function attachmentTreeURL(form) {
    return String(form?.dataset.chatAttachmentTreeUrl || "").trim();
  }

  function attachmentTreeEnabled(form) {
    return String(form?.dataset.chatAttachmentTreeEnabled || "").trim() === "true";
  }

  function workspaceTreeURL(form) {
    return String(form?.dataset.chatWorkspaceTreeUrl || "").trim();
  }

  function workspaceFileURL(form) {
    return String(form?.dataset.chatWorkspaceFileUrl || "").trim();
  }

  function sidebarViewButtons(form) {
    return Array.from(form?.querySelectorAll("[data-chat-action=\"switch-sidebar-view\"]") || []).filter((node) => node instanceof HTMLButtonElement);
  }

  function paneToggleButtons(form) {
    return Array.from(form?.querySelectorAll("[data-chat-action=\"toggle-pane\"]") || []).filter((node) => node instanceof HTMLButtonElement);
  }

  function sidebarPanels(form) {
    return Array.from(form?.querySelectorAll("[data-chat-sidebar-panel]") || []).filter((node) => node instanceof HTMLElement);
  }

  function attachmentTreeHost(form) {
    return form?.querySelector("[data-chat-attachment-tree]") || null;
  }

  function attachmentRootCopyNode(form) {
    return form?.querySelector("[data-chat-attachment-root-copy]") || null;
  }

  function attachmentStatusNode(form) {
    return form?.querySelector("[data-chat-attachment-status]") || null;
  }

  function workspaceSection(form) {
    return form?.querySelector("[data-chat-workspace-section]") || null;
  }

  function workspaceTreeHost(form) {
    return form?.querySelector("[data-chat-workspace-tree]") || null;
  }

  function workspaceRootCopyNode(form) {
    return form?.querySelector("[data-chat-workspace-root-copy]") || null;
  }

  function workspaceStatusNode(form) {
    return form?.querySelector("[data-chat-workspace-status]") || null;
  }

  function workspaceEditorPathNode(form) {
    return form?.querySelector("[data-chat-editor-path]") || null;
  }

  function workspaceEditorTabNameNode(form) {
    return form?.querySelector("[data-chat-editor-tab-name]") || null;
  }

  function workspaceEditorDirectoryNode(form) {
    return form?.querySelector("[data-chat-editor-directory]") || null;
  }

  function workspaceEditorStatusNode(form) {
    return form?.querySelector("[data-chat-editor-status]") || null;
  }

  function workspaceEditorPreviewHost(form) {
    return form?.querySelector("[data-chat-editor-preview]") || null;
  }

  function workspaceEditorPreviewImage(form) {
    return form?.querySelector("[data-chat-editor-preview-image]") || null;
  }

  function workspaceEditorPreviewBackgroundButtons(form) {
    return Array.from(form?.querySelectorAll("[data-chat-action=\"set-image-preview-background\"]") || []).filter((node) => node instanceof HTMLButtonElement);
  }

  function workspaceEditorDirtyNode(form) {
    return form?.querySelector("[data-chat-editor-dirty]") || null;
  }

  function workspaceEditorInput(form) {
    return form?.querySelector("[data-chat-editor-input]") || null;
  }

  function workspaceSaveButton(form) {
    return form?.querySelector("[data-chat-action=\"save-workspace-file\"]") || null;
  }

  function currentWorkspaceKey(form) {
    return `${readClientSessionID(form)}|${currentSelectedEnvironment(form)}`;
  }

  function readSidebarPreference() {
    if (typeof window === "undefined" || !window.localStorage) {
      return null;
    }
    try {
      const raw = window.localStorage.getItem(sidebarPreferenceKey);
      if (raw === "true") {
        return true;
      }
      if (raw === "false") {
        return false;
      }
    } catch (_error) {
      return null;
    }
    return null;
  }

  function normalizeImagePreviewBackground(value) {
    const normalized = String(value || "").trim().toLowerCase();
    if (normalized === "light" || normalized === "dark") {
      return normalized;
    }
    return "grid";
  }

  function readImagePreviewBackgroundPreference() {
    if (typeof window === "undefined" || !window.localStorage) {
      return "grid";
    }
    try {
      return normalizeImagePreviewBackground(window.localStorage.getItem(imagePreviewBackgroundPreferenceKey));
    } catch (_error) {
      return "grid";
    }
  }

  function writeImagePreviewBackgroundPreference() {
    if (typeof window === "undefined" || !window.localStorage) {
      return;
    }
    try {
      window.localStorage.setItem(imagePreviewBackgroundPreferenceKey, workspaceImagePreviewBackground);
    } catch (_error) {
      // Ignore storage write failures and keep the in-page selection.
    }
  }

  function normalizeSidebarWidth(value) {
    const numeric = Number.parseInt(String(value || ""), 10);
    const candidate = Number.isFinite(numeric) ? numeric : sidebarDefaultWidth;
    const viewportCap = Math.max(sidebarMinWidth, Math.floor(window.innerWidth * 0.4));
    return Math.min(viewportCap, Math.max(sidebarMinWidth, candidate));
  }

  function normalizeEditorWidth(value) {
    const numeric = Number.parseInt(String(value || ""), 10);
    const candidate = Number.isFinite(numeric) ? numeric : editorDefaultWidth;
    const viewportCap = Math.max(editorMinWidth, Math.floor(window.innerWidth * 0.56));
    return Math.min(viewportCap, Math.max(editorMinWidth, candidate));
  }

  function applyImagePreviewBackground(form, background, persist = false) {
    const nextBackground = normalizeImagePreviewBackground(background);
    workspaceImagePreviewBackground = nextBackground;
    if (form instanceof HTMLFormElement) {
      form.dataset.chatImagePreviewBackground = nextBackground;
      workspaceEditorPreviewBackgroundButtons(form).forEach((button) => {
        const active = String(button.dataset.chatPreviewBackground || "").trim() === nextBackground;
        button.classList.toggle("is-active", active);
        button.setAttribute("aria-pressed", active ? "true" : "false");
      });
    }
    if (persist) {
      writeImagePreviewBackgroundPreference();
    }
  }

  function readPixelSize(value, fallback = 0) {
    const numeric = Number.parseFloat(String(value || "").trim());
    return Number.isFinite(numeric) ? numeric : fallback;
  }

  function desktopWorkbenchWidth(form) {
    const workbench = form?.querySelector(".chat-workbench");
    return workbench instanceof HTMLElement ? workbench.clientWidth : 0;
  }

  function desktopActivitybarWidth(form) {
    const activitybar = form?.querySelector(".chat-activitybar");
    if (activitybar instanceof HTMLElement) {
      const measured = activitybar.getBoundingClientRect().width;
      if (measured > 0) {
        return measured;
      }
      return readPixelSize(window.getComputedStyle(activitybar).width, 56);
    }
    return 56;
  }

  function desktopSidebarGap(form) {
    const sidebarColumn = form?.querySelector(".chat-sidebar-column");
    return sidebarColumn instanceof HTMLElement
      ? readPixelSize(window.getComputedStyle(sidebarColumn).gap, 8.8)
      : 8.8;
  }

  function desktopDividerWidth(form, pane) {
    const divider = form?.querySelector(`[data-chat-pane-divider="${pane}"]`);
    if (divider instanceof HTMLElement) {
      const measured = divider.getBoundingClientRect().width;
      if (measured > 0) {
        return measured;
      }
      return readPixelSize(window.getComputedStyle(divider).width, 6.08);
    }
    return 6.08;
  }

  function preferredChatWidth(availableWidth) {
    if (!Number.isFinite(availableWidth) || availableWidth <= 0) {
      return workspaceMinChatWidth;
    }
    const responsive = Math.floor(availableWidth * 0.42);
    return Math.min(workspaceMaxPreferredChatWidth, Math.max(workspaceMinChatWidth, responsive));
  }

  function reservedWorkbenchWidth(form) {
    const activitybarWidth = desktopActivitybarWidth(form);
    const sidebarGap = desktopSidebarGap(form);
    const editorVisible = workspaceEditorVisible();
    let total = activitybarWidth;
    if (!layoutState.sidebarCollapsed) {
      total += sidebarGap + layoutState.sidebarWidth;
      if (editorVisible || layoutState.chatOpen) {
        total += desktopDividerWidth(form, "sidebar");
      }
    }
    if (editorVisible) {
      total += layoutState.editorWidth;
      if (layoutState.chatOpen) {
        total += desktopDividerWidth(form, "editor");
      }
    }
    return total;
  }

  function rebalanceDesktopLayout(form) {
    if (!(form instanceof HTMLFormElement) || !layoutState.chatOpen || window.innerWidth <= workspaceDesktopBreakpoint) {
      return;
    }
    const editorVisible = workspaceEditorVisible();
    const availableWidth = desktopWorkbenchWidth(form);
    if (availableWidth <= 0) {
      return;
    }

    let overflow = reservedWorkbenchWidth(form) + preferredChatWidth(availableWidth) - availableWidth;
    if (overflow <= 0) {
      return;
    }

    if (editorVisible) {
      const nextWidth = Math.max(editorMinWidth, layoutState.editorWidth - overflow);
      overflow -= layoutState.editorWidth - nextWidth;
      layoutState.editorWidth = nextWidth;
    }

    if (overflow > 0 && !layoutState.sidebarCollapsed) {
      const nextWidth = Math.max(sidebarMinWidth, layoutState.sidebarWidth - overflow);
      overflow -= layoutState.sidebarWidth - nextWidth;
      layoutState.sidebarWidth = nextWidth;
    }

    if (overflow > 0 && editorVisible) {
      overflow -= layoutState.editorWidth + desktopDividerWidth(form, "editor");
      layoutState.editorOpen = false;
      layoutState.editorWidth = normalizeEditorWidth(layoutState.editorWidth);
    }

    if (overflow > 0 && !layoutState.sidebarCollapsed) {
      layoutState.sidebarCollapsed = true;
    }
  }

  function normalizeLayoutState(raw) {
    const parsed = raw && typeof raw === "object" ? raw : {};
    const sidebarPreference = readSidebarPreference();
    return {
      sidebarCollapsed: sidebarPreference == null ? true : sidebarPreference,
      editorOpen: ownProperty.call(parsed, "editorOpen") ? parsed.editorOpen === true : false,
      chatOpen: !ownProperty.call(parsed, "chatOpen") || parsed.chatOpen !== false,
      sidebarView: parsed.sidebarView === "sessions" ? "sessions" : "files",
      sidebarWidth: normalizeSidebarWidth(parsed.sidebarWidth),
      editorWidth: normalizeEditorWidth(parsed.editorWidth),
    };
  }

  function readLayoutPreference() {
    if (typeof window === "undefined" || !window.localStorage) {
      return normalizeLayoutState(null);
    }
    try {
      return normalizeLayoutState(safeParseJSON(window.localStorage.getItem(layoutPreferenceKey), null));
    } catch (_error) {
      return normalizeLayoutState(null);
    }
  }

  function writeLayoutPreference() {
    if (typeof window === "undefined" || !window.localStorage) {
      return;
    }
    try {
      window.localStorage.setItem(layoutPreferenceKey, JSON.stringify(layoutState));
      window.localStorage.setItem(sidebarPreferenceKey, layoutState.sidebarCollapsed ? "true" : "false");
    } catch (_error) {
      // Ignore storage failures and keep the current in-memory layout.
    }
  }

  function formatBytes(value) {
    const size = Number(value || 0);
    if (!Number.isFinite(size) || size <= 0) {
      return "0 B";
    }
    if (size < 1024) {
      return `${size} B`;
    }
    if (size < 1024 * 1024) {
      return `${(size / 1024).toFixed(size >= 10 * 1024 ? 0 : 1)} KB`;
    }
    return `${(size / (1024 * 1024)).toFixed(1)} MB`;
  }

  function measureTextBytes(value) {
    const content = String(value || "");
    if (typeof TextEncoder === "function") {
      try {
        return new TextEncoder().encode(content).length;
      } catch (_error) {
        return content.length;
      }
    }
    return content.length;
  }

  function workspacePathParts(path) {
    const normalized = String(path || "").trim().replace(/^\/+/, "");
    if (normalized === "") {
      return {
        name: t("尚未打开文件", "No file open"),
        directory: t("工作区根目录", "Workspace root"),
      };
    }
    const segments = normalized.split("/").filter(Boolean);
    const name = segments.pop() || normalized;
    return {
      name,
      directory: segments.length > 0 ? segments.join(" / ") : t("工作区根目录", "Workspace root"),
    };
  }

  function workspaceEditorVisible() {
    return layoutState.editorOpen && workspaceActiveFilePath !== "";
  }

  function ensureVisiblePane() {
    if (layoutState.sidebarCollapsed && !workspaceEditorVisible() && !layoutState.chatOpen) {
      layoutState.chatOpen = true;
    }
  }

  function applyLayout(form, persist = false) {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    layoutState.sidebarWidth = normalizeSidebarWidth(layoutState.sidebarWidth);
    layoutState.editorWidth = normalizeEditorWidth(layoutState.editorWidth);
    ensureVisiblePane();
    rebalanceDesktopLayout(form);
    const editorVisible = workspaceEditorVisible();
    form.classList.toggle("is-sidebar-collapsed", layoutState.sidebarCollapsed);
    form.classList.toggle("is-editor-collapsed", !editorVisible);
    form.classList.toggle("is-chat-collapsed", !layoutState.chatOpen);
    form.style.setProperty("--chat-sidebar-width", layoutState.sidebarCollapsed ? "0px" : `${layoutState.sidebarWidth}px`);
    form.style.setProperty("--chat-editor-width", editorVisible ? `${layoutState.editorWidth}px` : "0px");
    const sidebarDivider = form.querySelector("[data-chat-pane-divider=\"sidebar\"]");
    const editorDivider = form.querySelector("[data-chat-pane-divider=\"editor\"]");
    if (sidebarDivider instanceof HTMLElement) {
      sidebarDivider.hidden = layoutState.sidebarCollapsed || (!editorVisible && !layoutState.chatOpen);
    }
    if (editorDivider instanceof HTMLElement) {
      editorDivider.hidden = !editorVisible || !layoutState.chatOpen;
    }
    paneToggleButtons(form).forEach((button) => {
      const pane = String(button.dataset.chatPane || "").trim();
      let active = false;
      if (pane === "sidebar") {
        active = !layoutState.sidebarCollapsed;
      } else if (pane === "editor") {
        active = editorVisible;
      } else if (pane === "chat") {
        active = layoutState.chatOpen;
      }
      button.classList.toggle("is-active", active);
      button.setAttribute("aria-pressed", active ? "true" : "false");
    });
    if (persist) {
      writeLayoutPreference();
    }
  }

  function applySidebarView(form, view, persist = false) {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    layoutState.sidebarView = view === "sessions" ? "sessions" : "files";
    form.dataset.chatSidebarView = layoutState.sidebarView;
    sidebarPanels(form).forEach((panel) => {
      panel.hidden = String(panel.dataset.chatSidebarPanel || "") !== layoutState.sidebarView;
    });
    sidebarViewButtons(form).forEach((button) => {
      const active = String(button.dataset.chatSidebarView || "") === layoutState.sidebarView;
      button.classList.toggle("is-active", active);
      button.setAttribute("aria-pressed", active ? "true" : "false");
    });
    if (persist) {
      writeLayoutPreference();
    }
  }

  function resetWorkspaceState(nextKey) {
    workspaceSessionKey = nextKey;
    attachmentTreeRootLabel = "";
    attachmentTreeCache = new Map();
    attachmentOpenPaths = new Set([""]);
    attachmentLoadingPaths = new Set();
    attachmentActiveFilePath = "";
    attachmentTreeStatusMessage = "";
    attachmentTreeStatusError = false;
    workspaceRootPath = "";
    workspaceTreeCache = new Map();
    workspaceOpenPaths = new Set([""]);
    workspaceLoadingPaths = new Set();
    workspaceActiveFilePath = "";
    workspaceEditorKind = "text";
    workspaceEditorMediaType = "";
    workspaceEditorPreviewURL = "";
    workspaceEditorContent = "";
    workspaceEditorSavedContent = "";
    workspaceEditorSize = 0;
    workspaceEditorBusy = "";
    workspaceEditorStatusMessage = "";
    workspaceEditorStatusError = false;
    workspaceTreeStatusMessage = "";
    workspaceTreeStatusError = false;
  }

  function setWorkspaceTreeStatus(message, isError = false) {
    workspaceTreeStatusMessage = String(message || "").trim();
    workspaceTreeStatusError = Boolean(isError);
  }

  function setAttachmentTreeStatus(message, isError = false) {
    attachmentTreeStatusMessage = String(message || "").trim();
    attachmentTreeStatusError = Boolean(isError);
  }

  function setWorkspaceEditorStatus(message, isError = false) {
    workspaceEditorStatusMessage = String(message || "").trim();
    workspaceEditorStatusError = Boolean(isError);
  }

  function editorIsDirty() {
    return workspaceActiveFilePath !== "" && workspaceEditorKind !== "image" && workspaceEditorContent !== workspaceEditorSavedContent;
  }

  function workspaceEditorIsImage() {
    return workspaceActiveFilePath !== "" && workspaceEditorKind === "image";
  }

  function attachmentImageMediaType(path, url) {
    const candidates = [path, url];
    for (const candidate of candidates) {
      const value = String(candidate || "").trim();
      if (value === "") {
        continue;
      }
      const dataMatch = value.match(/^data:(image\/[^;,]+)/i);
      if (dataMatch) {
        return String(dataMatch[1] || "").trim().toLowerCase();
      }
      const clean = value.split("#")[0].split("?")[0];
      const extension = clean.includes(".") ? clean.slice(clean.lastIndexOf(".") + 1).toLowerCase() : "";
      switch (extension) {
        case "png":
          return "image/png";
        case "jpg":
        case "jpeg":
          return "image/jpeg";
        case "gif":
          return "image/gif";
        case "webp":
          return "image/webp";
        case "bmp":
          return "image/bmp";
        case "svg":
          return "image/svg+xml";
        case "avif":
          return "image/avif";
        default:
          break;
      }
    }
    return "";
  }

  function syncWorkspaceEditorSurface(input, previewHost, previewImage, options = {}) {
    const mode = String(options.mode || "text").trim() === "image" ? "image" : "text";
    if (previewHost instanceof HTMLElement) {
      previewHost.hidden = mode !== "image";
    }
    if (previewImage instanceof HTMLImageElement) {
      const nextSrc = mode === "image" ? String(options.src || "").trim() : "";
      if (nextSrc === "") {
        previewImage.removeAttribute("src");
      } else if (previewImage.getAttribute("src") !== nextSrc) {
        previewImage.setAttribute("src", nextSrc);
      }
      previewImage.alt = mode === "image" ? String(options.alt || "").trim() : "";
    }
    if (!(input instanceof HTMLTextAreaElement)) {
      return;
    }
    input.hidden = mode === "image";
    if (mode === "image") {
      if (input.value !== "") {
        input.value = "";
      }
      input.disabled = true;
      return;
    }
    const nextValue = String(options.value || "");
    if (input.value !== nextValue) {
      input.value = nextValue;
    }
    input.disabled = Boolean(options.disabled);
  }

  function normalizeWorkspaceEntry(entry) {
    if (!entry || typeof entry !== "object") {
      return null;
    }
    const name = String(entry.name || "").trim();
    const pathValue = String(entry.path || "").trim();
    const type = String(entry.type || "file").trim() === "directory" ? "directory" : "file";
    if (name === "" && pathValue === "") {
      return null;
    }
    return {
      name: name || pathValue.split("/").pop() || t("未命名", "Untitled"),
      path: pathValue,
      type,
      url: String(entry.url || "").trim(),
      modifiedAt: String(entry.modifiedAt || "").trim(),
      size: Number.isFinite(Number(entry.size)) ? Number(entry.size) : 0,
      hasChildren: Boolean(entry.hasChildren),
    };
  }

  function renderWorkspacePlaceholder(host, message) {
    if (!(host instanceof HTMLElement)) {
      return;
    }
    const empty = document.createElement("div");
    empty.className = "chat-workspace-empty";
    empty.textContent = String(message || "").trim();
    host.replaceChildren(empty);
  }

  function entryMeta(entry) {
    return entry.type === "directory" ? t("目录", "Directory") : formatBytes(entry.size);
  }

  function expandWorkspaceAncestors(path) {
    const segments = String(path || "").split("/").filter(Boolean);
    if (segments.length <= 1) {
      workspaceOpenPaths.add("");
      return;
    }
    let current = "";
    workspaceOpenPaths.add("");
    segments.slice(0, -1).forEach((segment) => {
      current = current ? `${current}/${segment}` : segment;
      workspaceOpenPaths.add(current);
    });
  }

  function collapseWorkspaceDescendants(path) {
    Array.from(workspaceOpenPaths).forEach((value) => {
      if (value !== path && value.startsWith(`${path}/`)) {
        workspaceOpenPaths.delete(value);
      }
    });
  }

  function expandAttachmentAncestors(path) {
    const segments = String(path || "").split("/").filter(Boolean);
    if (segments.length <= 1) {
      attachmentOpenPaths.add("");
      return;
    }
    let current = "";
    attachmentOpenPaths.add("");
    segments.slice(0, -1).forEach((segment) => {
      current = current ? `${current}/${segment}` : segment;
      attachmentOpenPaths.add(current);
    });
  }

  function collapseAttachmentDescendants(path) {
    Array.from(attachmentOpenPaths).forEach((value) => {
      if (value !== path && value.startsWith(`${path}/`)) {
        attachmentOpenPaths.delete(value);
      }
    });
  }

  function renderAttachmentEntries(container, entries, depth) {
    entries.forEach((entry) => {
      const item = document.createElement("div");
      item.className = "chat-workspace-item";

      const row = document.createElement("button");
      row.type = "button";
      row.className = "chat-workspace-row";
      row.style.setProperty("--chat-workspace-depth", String(depth));
      row.dataset.chatAction = entry.type === "directory" ? "toggle-attachment-directory" : "open-attachment-file";
      row.dataset.chatAttachmentPath = entry.path;
      row.dataset.chatWorkspaceType = entry.type;
      if (entry.url) {
        row.dataset.chatAttachmentUrl = entry.url;
      }
      row.classList.toggle("is-active", attachmentActiveFilePath === entry.path);

      const caret = document.createElement("span");
      caret.className = "chat-workspace-row-caret";
      caret.textContent = entry.type === "directory" ? (attachmentOpenPaths.has(entry.path) ? "v" : ">") : "";

      const kind = document.createElement("span");
      kind.className = "chat-workspace-row-kind";
      kind.textContent = entry.type === "directory" ? "DIR" : "TXT";

      const name = document.createElement("span");
      name.className = "chat-workspace-row-name";
      name.textContent = entry.name;

      const meta = document.createElement("span");
      meta.className = "chat-workspace-row-meta";
      meta.textContent = entryMeta(entry);

      row.appendChild(caret);
      row.appendChild(kind);
      row.appendChild(name);
      row.appendChild(meta);
      item.appendChild(row);

      if (entry.type === "directory" && attachmentOpenPaths.has(entry.path)) {
        const children = document.createElement("div");
        children.className = "chat-workspace-children";
        const childEntries = attachmentTreeCache.get(entry.path);
        if (Array.isArray(childEntries)) {
          if (childEntries.length === 0) {
            renderWorkspacePlaceholder(children, t("这个目录当前为空。", "This directory is empty."));
          } else {
            renderAttachmentEntries(children, childEntries, depth + 1);
          }
        } else if (attachmentLoadingPaths.has(entry.path)) {
          renderWorkspacePlaceholder(children, t("正在加载目录…", "Loading directory..."));
        } else {
          renderWorkspacePlaceholder(children, t("展开目录后将在这里显示子项。", "Expand the folder to show its children."));
        }
        item.appendChild(children);
      }

      container.appendChild(item);
    });
  }

  function renderAttachmentTree(form) {
    const host = attachmentTreeHost(form);
    const rootCopy = attachmentRootCopyNode(form);
    const status = attachmentStatusNode(form);
    if (!(host instanceof HTMLElement) || !(form instanceof HTMLFormElement)) {
      return;
    }
    const enabled = attachmentTreeEnabled(form);
    if (rootCopy instanceof HTMLElement) {
      rootCopy.textContent = attachmentTreeRootLabel || t("等待加载附件目录", "Waiting for the attachment tree");
    }
    if (status instanceof HTMLElement) {
      const fallback = enabled
        ? (attachmentTreeRootLabel === "" ? t("对象存储目录加载后，这里会显示 chat 附件 bucket 的内容。", "The chat attachment bucket will appear here after the directory tree loads.") : t("对象存储目录已连接。", "Chat attachment storage connected."))
        : t("当前没有配置可浏览的 chat 附件目录。", "Chat attachment browsing is not configured.");
      status.textContent = attachmentTreeStatusMessage || fallback;
      status.classList.toggle("is-error", attachmentTreeStatusError);
    }
    if (!enabled) {
      renderWorkspacePlaceholder(host, t("当前没有配置可浏览 chat 附件目录的对象存储。", "Chat attachment storage is not configured for directory browsing."));
      return;
    }
    const rootEntries = attachmentTreeCache.get("");
    if (!Array.isArray(rootEntries)) {
      if (attachmentLoadingPaths.has("")) {
        renderWorkspacePlaceholder(host, t("正在加载附件目录…", "Loading attachment tree..."));
      } else {
        renderWorkspacePlaceholder(host, t("打开文件资源管理器后，这里会显示 chat 附件 bucket 的目录树。", "Open the file explorer to load the chat attachment bucket tree here."));
      }
      return;
    }
    if (rootEntries.length === 0) {
      renderWorkspacePlaceholder(host, t("当前附件目录为空。", "The current attachment directory is empty."));
      return;
    }
    const fragment = document.createDocumentFragment();
    renderAttachmentEntries(fragment, rootEntries, 0);
    host.replaceChildren(fragment);
  }

  function renderWorkspaceEntries(container, entries, depth) {
    entries.forEach((entry) => {
      const item = document.createElement("div");
      item.className = "chat-workspace-item";

      const row = document.createElement("button");
      row.type = "button";
      row.className = "chat-workspace-row";
      row.style.setProperty("--chat-workspace-depth", String(depth));
      row.dataset.chatAction = entry.type === "directory" ? "toggle-workspace-directory" : "open-workspace-file";
      row.dataset.chatWorkspacePath = entry.path;
      row.dataset.chatWorkspaceType = entry.type;
      row.classList.toggle("is-active", workspaceActiveFilePath === entry.path);

      const caret = document.createElement("span");
      caret.className = "chat-workspace-row-caret";
      caret.textContent = entry.type === "directory" ? (workspaceOpenPaths.has(entry.path) ? "v" : ">") : "";

      const kind = document.createElement("span");
      kind.className = "chat-workspace-row-kind";
      kind.textContent = entry.type === "directory" ? "DIR" : "TXT";

      const name = document.createElement("span");
      name.className = "chat-workspace-row-name";
      name.textContent = entry.name;

      const meta = document.createElement("span");
      meta.className = "chat-workspace-row-meta";
      meta.textContent = entryMeta(entry);

      row.appendChild(caret);
      row.appendChild(kind);
      row.appendChild(name);
      row.appendChild(meta);
      item.appendChild(row);

      if (entry.type === "directory" && workspaceOpenPaths.has(entry.path)) {
        const children = document.createElement("div");
        children.className = "chat-workspace-children";
        const childEntries = workspaceTreeCache.get(entry.path);
        if (Array.isArray(childEntries)) {
          if (childEntries.length === 0) {
            renderWorkspacePlaceholder(children, t("这个目录当前为空。", "This directory is empty."));
          } else {
            renderWorkspaceEntries(children, childEntries, depth + 1);
          }
        } else if (workspaceLoadingPaths.has(entry.path)) {
          renderWorkspacePlaceholder(children, t("正在加载目录…", "Loading directory..."));
        } else {
          renderWorkspacePlaceholder(children, t("展开目录后将在这里显示子项。", "Expand the folder to show its children."));
        }
        item.appendChild(children);
      }

      container.appendChild(item);
    });
  }

  function renderWorkspaceTree(form) {
    const host = workspaceTreeHost(form);
    const rootCopy = workspaceRootCopyNode(form);
    const status = workspaceStatusNode(form);
    if (!(host instanceof HTMLElement) || !(form instanceof HTMLFormElement)) {
      return;
    }
    const environment = currentSelectedEnvironment(form);
    const section = workspaceSection(form);
    const sessionID = readClientSessionID(form);
    if (section instanceof HTMLElement) {
      section.hidden = !isCloudAgentEnvironment(environment);
    }
    if (rootCopy instanceof HTMLElement) {
      rootCopy.textContent = isCloudAgentEnvironment(environment)
        ? (workspaceRootPath || t("等待 Cloud Agent 工作区", "Waiting for the Cloud Agent workspace"))
        : t("当前是本地环境", "The current environment is local");
    }
    if (status instanceof HTMLElement) {
      const fallback = isCloudAgentEnvironment(environment)
        ? (workspaceRootPath === "" ? t("Cloud Agent 工作区准备好后，这里会显示目录树。", "The directory tree will appear here when the Cloud Agent workspace is ready.") : t("容器工作区已连接。", "Cloud Agent workspace connected."))
        : t("切换到 Cloud Agent 环境后，可浏览容器里的目录与文件。", "Switch to a Cloud Agent environment to browse files inside the container.");
      status.textContent = workspaceTreeStatusMessage || fallback;
      status.classList.toggle("is-error", workspaceTreeStatusError);
    }
    if (!isCloudAgentEnvironment(environment)) {
      renderWorkspacePlaceholder(host, t("本地对话没有容器工作区。切换到 Cloud Agent 环境后，这里会显示目录树。", "Local chat does not expose a container workspace. Switch to a Cloud Agent environment to load the directory tree."));
      return;
    }
    if (sessionID === "") {
      renderWorkspacePlaceholder(host, t("Cloud Agent 还没有为当前会话生成工作区。", "The Cloud Agent workspace is not ready for this chat session yet."));
      return;
    }
    const rootEntries = workspaceTreeCache.get("");
    if (!Array.isArray(rootEntries)) {
      if (workspaceLoadingPaths.has("")) {
        renderWorkspacePlaceholder(host, t("正在加载工作区目录…", "Loading the workspace tree..."));
      } else {
        renderWorkspacePlaceholder(host, t("点击刷新或重新选择 Cloud Agent 环境后，将在这里加载目录树。", "Refresh or re-select the Cloud Agent environment to load the directory tree here."));
      }
      return;
    }
    if (rootEntries.length === 0) {
      renderWorkspacePlaceholder(host, t("当前工作区为空。", "The current workspace is empty."));
      return;
    }
    const fragment = document.createDocumentFragment();
    renderWorkspaceEntries(fragment, rootEntries, 0);
    host.replaceChildren(fragment);
  }

  function renderWorkspaceEditor(form) {
    const pathNode = workspaceEditorPathNode(form);
    const tabNameNode = workspaceEditorTabNameNode(form);
    const directoryNode = workspaceEditorDirectoryNode(form);
    const statusNode = workspaceEditorStatusNode(form);
    const previewHost = workspaceEditorPreviewHost(form);
    const previewImage = workspaceEditorPreviewImage(form);
    const dirtyNode = workspaceEditorDirtyNode(form);
    const input = workspaceEditorInput(form);
    const saveButton = workspaceSaveButton(form);
    if (!(form instanceof HTMLFormElement) || !(pathNode instanceof HTMLElement) || !(statusNode instanceof HTMLElement) || !(input instanceof HTMLTextAreaElement)) {
      return;
    }
    applyImagePreviewBackground(form, workspaceImagePreviewBackground, false);
    const setEditorChrome = (name, directory) => {
      pathNode.textContent = name;
      if (tabNameNode instanceof HTMLElement) {
        tabNameNode.textContent = name;
      }
      if (directoryNode instanceof HTMLElement) {
        directoryNode.textContent = directory;
      }
      if (dirtyNode instanceof HTMLElement) {
        dirtyNode.hidden = !editorIsDirty();
      }
    };
    const setEditorMode = (mode) => {
      form.dataset.chatEditorMode = mode;
    };
    const environment = currentSelectedEnvironment(form);
    const imagePreviewActive = workspaceEditorIsImage() && workspaceEditorPreviewURL !== "";
    if (!isCloudAgentEnvironment(environment) && !imagePreviewActive) {
      setEditorMode("empty");
      setEditorChrome(
        t("尚未打开文件", "No file open"),
        t("切换到 Cloud Agent 环境后，可在这里编辑文本或预览容器里的图片。", "Switch to a Cloud Agent environment to edit text or preview container images here.")
      );
      statusNode.textContent = t("切换到 Cloud Agent 环境后，可在这里编辑文本或预览容器里的图片。", "Switch to a Cloud Agent environment to edit text or preview container images here.");
      statusNode.classList.remove("is-error");
      syncWorkspaceEditorSurface(input, previewHost, previewImage, { mode: "text", value: "", disabled: true });
      if (saveButton instanceof HTMLButtonElement) {
        saveButton.disabled = true;
      }
      return;
    }
    if (workspaceActiveFilePath === "") {
      setEditorMode("empty");
      setEditorChrome(
        t("尚未打开文件", "No file open"),
        t("从左侧目录树选择一个文件开始浏览。", "Choose a file from the left tree to start browsing.")
      );
      statusNode.textContent = t("从左侧目录树选择一个文件开始浏览。", "Choose a file from the left tree to start browsing.");
      statusNode.classList.remove("is-error");
      syncWorkspaceEditorSurface(input, previewHost, previewImage, { mode: "text", value: "", disabled: true });
      if (saveButton instanceof HTMLButtonElement) {
        saveButton.disabled = true;
      }
      return;
    }
    const pathInfo = workspacePathParts(workspaceActiveFilePath);
    const dirty = editorIsDirty();
    setEditorMode(imagePreviewActive ? "image" : "text");
    syncWorkspaceEditorSurface(input, previewHost, previewImage, imagePreviewActive
      ? { mode: "image", src: workspaceEditorPreviewURL, alt: pathInfo.name }
      : { mode: "text", value: workspaceEditorContent, disabled: workspaceEditorBusy !== "" });
    if (workspaceEditorBusy !== "") {
      setEditorChrome(
        pathInfo.name,
        pathInfo.directory
      );
      statusNode.textContent = workspaceEditorBusy === "save"
        ? t("正在保存文件…", "Saving file...")
        : t("正在加载文件…", "Loading file...");
      statusNode.classList.remove("is-error");
    } else if (workspaceEditorStatusMessage !== "") {
      setEditorChrome(
        pathInfo.name,
        pathInfo.directory
      );
      statusNode.textContent = workspaceEditorSize > 0 ? `${workspaceEditorStatusMessage} · ${formatBytes(workspaceEditorSize)}` : workspaceEditorStatusMessage;
      statusNode.classList.toggle("is-error", workspaceEditorStatusError);
    } else if (workspaceEditorIsImage()) {
      const statusParts = [t("图片预览", "Image preview")];
      if (workspaceEditorMediaType !== "") {
        statusParts.push(workspaceEditorMediaType);
      }
      if (workspaceEditorSize > 0) {
        statusParts.push(formatBytes(workspaceEditorSize));
      }
      setEditorChrome(
        pathInfo.name,
        pathInfo.directory
      );
      statusNode.textContent = statusParts.join(" · ");
      statusNode.classList.remove("is-error");
    } else {
      setEditorChrome(
        pathInfo.name,
        pathInfo.directory
      );
      statusNode.textContent = dirty
        ? `${t("已修改，尚未保存", "Unsaved changes")} · ${formatBytes(workspaceEditorSize)}`
        : `${t("已同步", "Synced")} · ${formatBytes(workspaceEditorSize)}`;
      statusNode.classList.remove("is-error");
    }
    if (saveButton instanceof HTMLButtonElement) {
      saveButton.disabled = workspaceEditorBusy !== "" || workspaceEditorIsImage() || !dirty;
    }
  }

  async function fetchWorkspaceJSON(url, options = {}) {
    const response = await fetch(url, {
      credentials: "same-origin",
      headers: { Accept: "application/json", ...(options.headers || {}) },
      method: options.method || "GET",
      body: options.body,
    });
    const raw = await response.text();
    const parsed = safeParseJSON(raw, null);
    if (!response.ok || !parsed || typeof parsed !== "object") {
      throw new Error(String(parsed?.error || t("工作区请求失败。", "Workspace request failed.")));
    }
    return parsed;
  }

  async function loadAttachmentTree(form, path = "", force = false) {
    if (!(form instanceof HTMLFormElement) || !attachmentTreeEnabled(form)) {
      return null;
    }
    const relativePath = String(path || "").trim();
    if (!force && attachmentTreeCache.has(relativePath)) {
      renderAttachmentTree(form);
      return attachmentTreeCache.get(relativePath);
    }
    const endpoint = attachmentTreeURL(form);
    if (endpoint === "") {
      return null;
    }
    attachmentLoadingPaths.add(relativePath);
    if (relativePath === "") {
      setAttachmentTreeStatus(t("正在加载附件目录…", "Loading attachment tree..."), false);
    }
    renderAttachmentTree(form);
    const requestKey = currentWorkspaceKey(form);
    try {
      const url = new URL(endpoint, window.location.href);
      if (relativePath !== "") {
        url.searchParams.set("path", relativePath);
      }
      const parsed = await fetchWorkspaceJSON(url.toString());
      if (requestKey !== workspaceSessionKey || requestKey !== currentWorkspaceKey(form)) {
        return null;
      }
      const nextPath = String(parsed.path || relativePath || "").trim();
      const entries = Array.isArray(parsed.entries) ? parsed.entries.map(normalizeWorkspaceEntry).filter(Boolean) : [];
      attachmentTreeCache.set(nextPath, entries);
      attachmentOpenPaths.add(nextPath);
      attachmentTreeRootLabel = String(parsed.rootLabel || attachmentTreeRootLabel || "").trim();
      setAttachmentTreeStatus("", false);
      return entries;
    } catch (error) {
      if (requestKey === workspaceSessionKey) {
        setAttachmentTreeStatus(String(error?.message || t("加载附件目录失败。", "Failed to load the attachment tree.")), true);
      }
      return null;
    } finally {
      attachmentLoadingPaths.delete(relativePath);
      if (requestKey === workspaceSessionKey) {
        renderAttachmentTree(form);
      }
    }
  }

  async function loadWorkspaceTree(form, path = "", force = false) {
    if (!(form instanceof HTMLFormElement) || !isCloudAgentEnvironment(currentSelectedEnvironment(form))) {
      return null;
    }
    const relativePath = String(path || "").trim();
    const sessionID = readClientSessionID(form);
    if (sessionID === "") {
      renderWorkspaceTree(form);
      return null;
    }
    if (!force && workspaceTreeCache.has(relativePath)) {
      renderWorkspaceTree(form);
      return workspaceTreeCache.get(relativePath);
    }
    const endpoint = workspaceTreeURL(form);
    if (endpoint === "") {
      return null;
    }
    workspaceLoadingPaths.add(relativePath);
    if (relativePath === "") {
      setWorkspaceTreeStatus(t("正在加载工作区目录…", "Loading the workspace tree..."), false);
    }
    renderWorkspaceTree(form);
    const requestKey = currentWorkspaceKey(form);
    try {
      const url = new URL(endpoint, window.location.href);
      url.searchParams.set("session", sessionID);
      if (relativePath !== "") {
        url.searchParams.set("path", relativePath);
      }
      const parsed = await fetchWorkspaceJSON(url.toString());
      if (requestKey !== workspaceSessionKey || requestKey !== currentWorkspaceKey(form)) {
        return null;
      }
      const nextPath = String(parsed.path || relativePath || "").trim();
      const entries = Array.isArray(parsed.entries) ? parsed.entries.map(normalizeWorkspaceEntry).filter(Boolean) : [];
      workspaceTreeCache.set(nextPath, entries);
      workspaceOpenPaths.add(nextPath);
      workspaceRootPath = String(parsed.workspacePath || workspaceRootPath || "").trim();
      setWorkspaceTreeStatus("", false);
      return entries;
    } catch (error) {
      if (requestKey === workspaceSessionKey) {
        setWorkspaceTreeStatus(String(error?.message || t("加载工作区失败。", "Failed to load the workspace.")), true);
      }
      return null;
    } finally {
      workspaceLoadingPaths.delete(relativePath);
      if (requestKey === workspaceSessionKey) {
        renderWorkspaceTree(form);
      }
    }
  }

  function openAttachmentFile(form, path, url) {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    const nextPath = String(path || "").trim();
    const targetURL = String(url || "").trim();
    const mediaType = attachmentImageMediaType(nextPath, targetURL);
    attachmentActiveFilePath = nextPath;
    expandAttachmentAncestors(nextPath);
    if (targetURL === "") {
      setAttachmentTreeStatus(t("这个对象目前没有可用的访问地址。", "This object does not have a usable URL right now."), true);
      renderAttachmentTree(form);
      return;
    }
    if (mediaType !== "") {
      const shouldRevealEditor = !workspaceEditorVisible();
      workspaceActiveFilePath = nextPath;
      workspaceEditorKind = "image";
      workspaceEditorMediaType = mediaType;
      workspaceEditorPreviewURL = targetURL;
      workspaceEditorContent = "";
      workspaceEditorSavedContent = "";
      workspaceEditorSize = 0;
      workspaceEditorBusy = "";
      setWorkspaceEditorStatus("", false);
      if (shouldRevealEditor) {
        layoutState.editorOpen = true;
        applyLayout(form, true);
      }
      setAttachmentTreeStatus("", false);
      renderAttachmentTree(form);
      renderWorkspaceEditor(form);
      return;
    }
    setAttachmentTreeStatus("", false);
    renderAttachmentTree(form);
    window.open(targetURL, "_blank", "noopener,noreferrer");
  }

  async function openWorkspaceFile(form, path, force = false) {
    if (!(form instanceof HTMLFormElement) || !isCloudAgentEnvironment(currentSelectedEnvironment(form))) {
      return;
    }
    const relativePath = String(path || "").trim();
    const sessionID = readClientSessionID(form);
    if (relativePath === "" || sessionID === "") {
      return;
    }
    if (!force && workspaceActiveFilePath === relativePath && workspaceEditorBusy === "") {
      renderWorkspaceEditor(form);
      return;
    }
    const endpoint = workspaceFileURL(form);
    if (endpoint === "") {
      return;
    }
    const shouldRevealEditor = !workspaceEditorVisible();
    workspaceActiveFilePath = relativePath;
    if (shouldRevealEditor) {
      layoutState.editorOpen = true;
      applyLayout(form, true);
    }
    workspaceEditorBusy = "open";
    expandWorkspaceAncestors(relativePath);
    setWorkspaceEditorStatus("", false);
    renderWorkspaceTree(form);
    renderWorkspaceEditor(form);
    const requestKey = currentWorkspaceKey(form);
    try {
      const url = new URL(endpoint, window.location.href);
      url.searchParams.set("session", sessionID);
      url.searchParams.set("path", relativePath);
      const parsed = await fetchWorkspaceJSON(url.toString());
      if (requestKey !== workspaceSessionKey || requestKey !== currentWorkspaceKey(form)) {
        return;
      }
      workspaceRootPath = String(parsed.workspacePath || workspaceRootPath || "").trim();
      workspaceActiveFilePath = String(parsed.path || relativePath).trim();
      workspaceEditorKind = String(parsed.kind || "").trim() === "image" ? "image" : "text";
      workspaceEditorMediaType = String(parsed.mediaType || "").trim();
      workspaceEditorPreviewURL = workspaceEditorKind === "image" ? String(parsed.previewURL || "").trim() : "";
      workspaceEditorContent = workspaceEditorKind === "image" ? "" : String(parsed.content || "");
      workspaceEditorSavedContent = workspaceEditorContent;
      workspaceEditorSize = Number.isFinite(Number(parsed.size)) ? Number(parsed.size) : (workspaceEditorKind === "image" ? 0 : measureTextBytes(workspaceEditorContent));
      if (workspaceEditorKind === "image" && workspaceEditorPreviewURL === "") {
        setWorkspaceEditorStatus(t("图片预览不可用。", "Image preview unavailable."), true);
      } else {
        setWorkspaceEditorStatus("", false);
      }
      applySidebarView(form, "files", true);
    } catch (error) {
      if (requestKey === workspaceSessionKey) {
        setWorkspaceEditorStatus(String(error?.message || t("读取文件失败。", "Failed to read the file.")), true);
      }
    } finally {
      if (requestKey === workspaceSessionKey) {
        workspaceEditorBusy = "";
        renderWorkspaceTree(form);
        renderWorkspaceEditor(form);
      }
    }
  }

  async function saveWorkspaceFile(form) {
    if (!(form instanceof HTMLFormElement) || workspaceActiveFilePath === "" || workspaceEditorBusy !== "" || !editorIsDirty()) {
      return;
    }
    const endpoint = workspaceFileURL(form);
    const sessionID = readClientSessionID(form);
    if (endpoint === "" || sessionID === "") {
      return;
    }
    workspaceEditorBusy = "save";
    setWorkspaceEditorStatus("", false);
    renderWorkspaceEditor(form);
    const requestKey = currentWorkspaceKey(form);
    try {
      const url = new URL(endpoint, window.location.href);
      url.searchParams.set("session", sessionID);
      const parsed = await fetchWorkspaceJSON(url.toString(), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: workspaceActiveFilePath, content: workspaceEditorContent }),
      });
      if (requestKey !== workspaceSessionKey || requestKey !== currentWorkspaceKey(form)) {
        return;
      }
      workspaceEditorSavedContent = workspaceEditorContent;
      workspaceEditorSize = Number.isFinite(Number(parsed.bytes)) ? Number(parsed.bytes) : measureTextBytes(workspaceEditorContent);
      setWorkspaceEditorStatus(String(parsed.notice || t("文件已保存。", "File saved.")), false);
    } catch (error) {
      if (requestKey === workspaceSessionKey) {
        setWorkspaceEditorStatus(String(error?.message || t("保存文件失败。", "Failed to save the file.")), true);
      }
    } finally {
      if (requestKey === workspaceSessionKey) {
        workspaceEditorBusy = "";
        renderWorkspaceEditor(form);
      }
    }
  }

  async function refreshWorkspace(form) {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    const activePath = workspaceActiveFilePath;
    const nextKey = currentWorkspaceKey(form);
    resetWorkspaceState(nextKey);
    applySidebarView(form, layoutState.sidebarView, false);
    renderAttachmentTree(form);
    renderWorkspaceTree(form);
    renderWorkspaceEditor(form);
    if (attachmentTreeEnabled(form)) {
      await loadAttachmentTree(form, "", true);
    }
    if (!isCloudAgentEnvironment(currentSelectedEnvironment(form)) || readClientSessionID(form) === "") {
      return;
    }
    await loadWorkspaceTree(form, "", true);
    if (activePath !== "") {
      await openWorkspaceFile(form, activePath, true);
    }
  }

  function syncWorkspaceSurface(form = currentForm(), options = {}) {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    const nextKey = currentWorkspaceKey(form);
    if (options.force || nextKey !== workspaceSessionKey) {
      resetWorkspaceState(nextKey);
    }
    applyLayout(form, false);
    applySidebarView(form, layoutState.sidebarView, false);
    renderAttachmentTree(form);
    renderWorkspaceTree(form);
    renderWorkspaceEditor(form);
    if (!layoutState.sidebarCollapsed && layoutState.sidebarView === "files") {
      if (attachmentTreeEnabled(form) && !attachmentTreeCache.has("")) {
        void loadAttachmentTree(form, "", Boolean(options.force));
      }
      if (isCloudAgentEnvironment(currentSelectedEnvironment(form)) && readClientSessionID(form) !== "" && !workspaceTreeCache.has("")) {
        void loadWorkspaceTree(form, "", Boolean(options.force));
      }
    }
  }

  document.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target.closest("[data-chat-action]") : null;
    const form = currentForm();
    if (!(target instanceof HTMLElement) || !(form instanceof HTMLFormElement) || !form.contains(target)) {
      return;
    }
    switch (String(target.dataset.chatAction || "").trim()) {
      case "toggle-pane": {
        event.preventDefault();
        const pane = String(target.dataset.chatPane || "").trim();
        if (pane === "sidebar") {
          layoutState.sidebarCollapsed = !layoutState.sidebarCollapsed;
        } else if (pane === "editor") {
          layoutState.editorOpen = !layoutState.editorOpen;
        } else if (pane === "chat") {
          layoutState.chatOpen = !layoutState.chatOpen;
        }
        ensureVisiblePane();
        applyLayout(form, true);
        syncWorkspaceSurface(form);
        return;
      }
      case "switch-sidebar-view": {
        event.preventDefault();
        layoutState.sidebarCollapsed = false;
        applyLayout(form, false);
        applySidebarView(form, String(target.dataset.chatSidebarView || "files").trim(), true);
        syncWorkspaceSurface(form);
        return;
      }
      case "refresh-workspace": {
        event.preventDefault();
        void refreshWorkspace(form);
        return;
      }
      case "toggle-attachment-directory": {
        event.preventDefault();
        const path = String(target.dataset.chatAttachmentPath || "").trim();
        if (attachmentOpenPaths.has(path)) {
          attachmentOpenPaths.delete(path);
          collapseAttachmentDescendants(path);
          renderAttachmentTree(form);
          return;
        }
        attachmentOpenPaths.add(path);
        renderAttachmentTree(form);
        if (!attachmentTreeCache.has(path)) {
          void loadAttachmentTree(form, path, false);
        }
        return;
      }
      case "open-attachment-file": {
        event.preventDefault();
        openAttachmentFile(form, String(target.dataset.chatAttachmentPath || "").trim(), String(target.dataset.chatAttachmentUrl || "").trim());
        return;
      }
      case "toggle-workspace-directory": {
        event.preventDefault();
        const path = String(target.dataset.chatWorkspacePath || "").trim();
        if (workspaceOpenPaths.has(path)) {
          workspaceOpenPaths.delete(path);
          collapseWorkspaceDescendants(path);
          renderWorkspaceTree(form);
          return;
        }
        workspaceOpenPaths.add(path);
        renderWorkspaceTree(form);
        if (!workspaceTreeCache.has(path)) {
          void loadWorkspaceTree(form, path, false);
        }
        return;
      }
      case "open-workspace-file": {
        event.preventDefault();
        void openWorkspaceFile(form, String(target.dataset.chatWorkspacePath || "").trim(), false);
        return;
      }
      case "save-workspace-file": {
        event.preventDefault();
        void saveWorkspaceFile(form);
        return;
      }
      case "set-image-preview-background": {
        event.preventDefault();
        applyImagePreviewBackground(form, String(target.dataset.chatPreviewBackground || "").trim(), true);
        return;
      }
      default:
        return;
    }
  });

  document.addEventListener("input", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLTextAreaElement) || !target.matches("[data-chat-editor-input]")) {
      return;
    }
    workspaceEditorContent = target.value;
    workspaceEditorSize = measureTextBytes(workspaceEditorContent);
    if (!editorIsDirty() && !workspaceEditorStatusError) {
      setWorkspaceEditorStatus("", false);
    }
    renderWorkspaceEditor(target.form instanceof HTMLFormElement ? target.form : currentForm());
  });

  document.addEventListener("keydown", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLTextAreaElement) || !target.matches("[data-chat-editor-input]")) {
      return;
    }
    if (event.key.toLowerCase() === "s" && (event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      void saveWorkspaceFile(target.form instanceof HTMLFormElement ? target.form : currentForm());
    }
  });

  document.addEventListener("change", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLInputElement || target instanceof HTMLSelectElement)) {
      return;
    }
    if (target.matches("[data-chat-environment-option], [data-chat-environment-input], select[name=\"chat_environment\"]")) {
      window.setTimeout(() => {
        syncWorkspaceSurface(currentForm(), { force: true });
      }, 0);
    }
  });

  document.addEventListener("pointerdown", (event) => {
    const form = currentForm();
    const handle = event.target instanceof Element ? event.target.closest("[data-chat-pane-divider]") : null;
    if (!(form instanceof HTMLFormElement) || !(handle instanceof HTMLElement)) {
      return;
    }
    const pane = String(handle.dataset.chatPaneDivider || "").trim();
    if (pane === "sidebar" && layoutState.sidebarCollapsed) {
      return;
    }
    if (pane === "editor" && !workspaceEditorVisible()) {
      return;
    }
    event.preventDefault();
    paneResizeState = {
      pane,
      startX: event.clientX,
      startWidth: pane === "sidebar" ? layoutState.sidebarWidth : layoutState.editorWidth,
      previousUserSelect: document.body.style.userSelect,
    };
    form.classList.add("is-pane-resizing");
    document.body.style.userSelect = "none";
  });

  document.addEventListener("pointermove", (event) => {
    const form = currentForm();
    if (!(form instanceof HTMLFormElement) || !paneResizeState) {
      return;
    }
    const delta = event.clientX - paneResizeState.startX;
    if (paneResizeState.pane === "sidebar") {
      layoutState.sidebarWidth = normalizeSidebarWidth(paneResizeState.startWidth + delta);
    } else if (paneResizeState.pane === "editor") {
      layoutState.editorWidth = normalizeEditorWidth(paneResizeState.startWidth + delta);
    }
    applyLayout(form, false);
  });

  function finishPaneResize() {
    const form = currentForm();
    if (!paneResizeState) {
      return;
    }
    document.body.style.userSelect = paneResizeState.previousUserSelect;
    if (form instanceof HTMLFormElement) {
      form.classList.remove("is-pane-resizing");
      applyLayout(form, true);
    }
    paneResizeState = null;
  }

  document.addEventListener("pointerup", finishPaneResize);
  document.addEventListener("pointercancel", finishPaneResize);

  window.addEventListener("resize", () => {
    const form = currentForm();
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    applyLayout(form, false);
    renderWorkspaceEditor(form);
  });

  window.addEventListener("aiyolo:chat-state", () => {
    syncWorkspaceSurface(currentForm());
  });

  syncWorkspaceSurface(currentForm(), { force: true });
})();