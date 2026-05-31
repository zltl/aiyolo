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
  const workspaceOpenStatePreferenceKey = "aiyolo.console.chat.workspaceOpenState.v1";
  const sidebarDefaultWidth = 288;
  const sidebarMinWidth = 220;
  const editorDefaultWidth = 520;
  const editorMinWidth = 320;
  const workspaceDesktopBreakpoint = 1100;
  const workspaceMinChatWidth = 420;
  const workspaceMaxPreferredChatWidth = 860;
  const workspaceHighlightMaxChars = 240000;
  const workspaceEditorIndentText = "\t";
  const workspaceHighlightExactLanguages = new Map([
    [".env", "ini"],
    [".gitignore", "plaintext"],
    ["containerfile", "dockerfile"],
    ["dockerfile", "dockerfile"],
    ["gnumakefile", "makefile"],
    ["go.mod", "go"],
    ["go.sum", "plaintext"],
    ["makefile", "makefile"],
    ["package-lock.json", "json"],
    ["pnpm-lock.yaml", "yaml"],
    ["yarn.lock", "yaml"],
  ]);
  const workspaceHighlightExtensionLanguages = new Map([
    ["bash", "bash"],
    ["bat", "dos"],
    ["c", "c"],
    ["cc", "cpp"],
    ["cfg", "ini"],
    ["cmd", "dos"],
    ["conf", "nginx"],
    ["cpp", "cpp"],
    ["cs", "csharp"],
    ["css", "css"],
    ["csv", "plaintext"],
    ["diff", "diff"],
    ["go", "go"],
    ["graphql", "graphql"],
    ["h", "c"],
    ["hpp", "cpp"],
    ["html", "xml"],
    ["ini", "ini"],
    ["java", "java"],
    ["js", "javascript"],
    ["json", "json"],
    ["jsonc", "json"],
    ["jsx", "javascript"],
    ["kt", "kotlin"],
    ["kts", "kotlin"],
    ["less", "less"],
    ["lua", "lua"],
    ["mjs", "javascript"],
    ["md", "markdown"],
    ["markdown", "markdown"],
    ["mod", "go"],
    ["php", "php"],
    ["pl", "perl"],
    ["proto", "protobuf"],
    ["ps1", "powershell"],
    ["py", "python"],
    ["rb", "ruby"],
    ["rs", "rust"],
    ["sass", "scss"],
    ["scala", "scala"],
    ["scss", "scss"],
    ["sh", "bash"],
    ["sql", "sql"],
    ["sum", "plaintext"],
    ["svg", "xml"],
    ["swift", "swift"],
    ["toml", "ini"],
    ["ts", "typescript"],
    ["tsx", "typescript"],
    ["txt", "plaintext"],
    ["xml", "xml"],
    ["yaml", "yaml"],
    ["yml", "yaml"],
    ["zsh", "bash"],
  ]);

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
  let workspaceOpenFilePaths = [];
  let workspaceLoadingPaths = new Set();
  let workspaceUploadTargetPath = "";
  let workspaceUploadTargetRoot = false;
  let workspaceInlineEdit = null;
  let workspaceContextMenu = null;
  let workspacePathClipboard = null;
  let workspaceCompareBasePath = "";
  let workspaceActiveFilePath = "";
  let workspacePendingRestoreFilePath = "";
  let workspaceEditorKind = "text";
  let workspaceEditorMediaType = "";
  let workspaceEditorPreviewURL = "";
  let workspaceEditorContent = "";
  let workspaceEditorSavedContent = "";
  let workspaceEditorSize = 0;
  let workspaceEditorBusy = "";
  let workspaceEditorStatusMessage = "";
  let workspaceEditorStatusError = false;
  let workspaceEditorHighlightedContent = null;
  let workspaceEditorHighlightedLanguage = "";
  let workspaceEditorHighlightedPath = "";
  let workspaceEditorMarkdownMode = "preview";
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

  function workspaceDownloadURL(form) {
    return String(form?.dataset.chatWorkspaceDownloadUrl || "").trim();
  }

  function workspaceUploadURL(form) {
    return String(form?.dataset.chatWorkspaceUploadUrl || "").trim();
  }

  function workspaceUploadMaxBytes(form) {
    const value = Number(form?.dataset.chatWorkspaceUploadMaxBytes || "0");
    return Number.isFinite(value) && value > 0 ? value : 0;
  }

  function workspaceDirectoryURL(form) {
    return String(form?.dataset.chatWorkspaceDirectoryUrl || "").trim();
  }

  function workspaceCopyURL(form) {
    return String(form?.dataset.chatWorkspaceCopyUrl || "").trim();
  }

  function workspaceRenameURL(form) {
    return String(form?.dataset.chatWorkspaceRenameUrl || "").trim();
  }

  function workspaceDeleteURL(form) {
    return String(form?.dataset.chatWorkspaceDeleteUrl || "").trim();
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

  function workspaceFilesPanel(form) {
    return form?.querySelector("[data-chat-sidebar-panel=\"files\"]") || null;
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

  function workspaceEditorTabsNode(form) {
    return form?.querySelector("[data-chat-editor-tabs]") || form?.querySelector(".chat-editor-tabs") || null;
  }

  function workspaceEditorTabNameNode(form) {
    return form?.querySelector(".chat-editor-tab.is-active [data-chat-editor-tab-name], .chat-editor-tab.is-active[data-chat-editor-tab-name], [data-chat-editor-tab-name]") || null;
  }

  function workspaceEditorTabIconNode(form) {
    return form?.querySelector(".chat-editor-tab.is-active [data-chat-editor-tab-icon], .chat-editor-tab.is-active .chat-editor-tab-icon, [data-chat-editor-tab-icon]") || form?.querySelector(".chat-editor-tab-icon") || null;
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

  function workspaceEditorMarkdownControls(form) {
    return form?.querySelector("[data-chat-editor-markdown-controls]") || null;
  }

  function workspaceEditorMarkdownModeButtons(form) {
    return Array.from(form?.querySelectorAll("[data-chat-editor-markdown-mode]") || []).filter((node) => node instanceof HTMLButtonElement);
  }

  function workspaceEditorMarkdownHost(form) {
    return form?.querySelector("[data-chat-editor-markdown]") || null;
  }

  function workspaceEditorDirtyNode(form) {
    return form?.querySelector(".chat-editor-tab.is-active [data-chat-editor-dirty], .chat-editor-tab.is-active[data-chat-editor-dirty], [data-chat-editor-dirty]") || null;
  }

  function workspaceEditorInput(form) {
    return form?.querySelector("[data-chat-editor-input]") || null;
  }

  function workspaceEditorCodeHost(form) {
    return form?.querySelector("[data-chat-editor-code]") || null;
  }

  function workspaceEditorLineNumbersNode(form) {
    return form?.querySelector("[data-chat-editor-line-numbers]") || null;
  }

  function workspaceEditorHighlightNode(form) {
    return form?.querySelector("[data-chat-editor-highlight]") || null;
  }

  function workspaceSaveButton(form) {
    return form?.querySelector("[data-chat-action=\"save-workspace-file\"]") || null;
  }

  function currentWorkspaceKey(form) {
    return `${readClientSessionID(form)}|${currentSelectedEnvironment(form)}`;
  }

  function normalizeWorkspacePath(value) {
    return String(value || "").trim().replace(/\\/g, "/").replace(/^\/+/, "");
  }

  function normalizeWorkspaceCreatePath(value) {
    return normalizeWorkspacePath(value).replace(/\/+$/, "");
  }

  function workspaceParentPath(value) {
    const path = normalizeWorkspaceCreatePath(value);
    if (path === "") {
      return "";
    }
    const parts = path.split("/").filter(Boolean);
    parts.pop();
    return parts.join("/");
  }

  function workspaceAncestorDirectories(value, includeSelf = false) {
    const path = normalizeWorkspaceCreatePath(value);
    const parts = path.split("/").filter(Boolean);
    const limit = includeSelf ? parts.length : Math.max(0, parts.length - 1);
    const directories = [];
    let current = "";
    for (let index = 0; index < limit; index += 1) {
      current = current ? `${current}/${parts[index]}` : parts[index];
      if (current !== "") {
        directories.push(current);
      }
    }
    return directories;
  }

  function defaultWorkspaceCreateParent() {
    const parent = workspaceParentPath(workspaceActiveFilePath);
    return parent;
  }

  function workspaceBaseName(value) {
    const path = normalizeWorkspaceCreatePath(value);
    if (path === "") {
      return "";
    }
    const parts = path.split("/").filter(Boolean);
    return parts.length > 0 ? parts[parts.length - 1] : "";
  }

  function joinWorkspaceChildPath(parentPath, name) {
    const parent = normalizeWorkspaceCreatePath(parentPath);
    const childName = String(name || "").trim();
    return parent === "" ? childName : `${parent}/${childName}`;
  }

  function normalizeWorkspaceEntryName(value) {
    const name = String(value || "").trim();
    if (name === "") {
      return { name: "", error: t("请输入名称。", "Enter a name.") };
    }
    if (name === "." || name === ".." || name.includes("\0")) {
      return { name: "", error: t("这个名称不可用。", "This name cannot be used.") };
    }
    if (name.includes("/") || name.includes("\\")) {
      return { name: "", error: t("名称不能包含斜杠。", "Names cannot contain slashes.") };
    }
    return { name, error: "" };
  }

  function normalizeWorkspacePathList(values, limit = 24) {
    const seen = new Set();
    const next = [];
    (Array.isArray(values) ? values : []).forEach((value) => {
      const path = normalizeWorkspacePath(value);
      if (path === "" || seen.has(path)) {
        return;
      }
      seen.add(path);
      next.push(path);
    });
    return next.slice(0, limit);
  }

  function normalizeWorkspaceOpenState(raw) {
    const parsed = raw && typeof raw === "object" ? raw : {};
    const openFilePaths = normalizeWorkspacePathList(parsed.openFilePaths || parsed.files || []);
    let activeFilePath = normalizeWorkspacePath(parsed.activeFilePath || parsed.activePath || "");
    if (activeFilePath !== "" && !openFilePaths.includes(activeFilePath)) {
      openFilePaths.unshift(activeFilePath);
    }
    if (activeFilePath === "" && openFilePaths.length > 0) {
      activeFilePath = openFilePaths[0];
    }
    const expandedPaths = normalizeWorkspacePathList(parsed.expandedPaths || [], 96);
    if (!expandedPaths.includes("")) {
      expandedPaths.unshift("");
    }
    return { openFilePaths, activeFilePath, expandedPaths };
  }

  function readWorkspaceOpenStates() {
    if (typeof window === "undefined" || !window.localStorage) {
      return {};
    }
    try {
      const parsed = safeParseJSON(window.localStorage.getItem(workspaceOpenStatePreferenceKey), {});
      return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
    } catch (_error) {
      return {};
    }
  }

  function writeWorkspaceOpenStateForKey(key, state) {
    const stateKey = String(key || "").trim();
    if (stateKey === "" || typeof window === "undefined" || !window.localStorage) {
      return;
    }
    try {
      const states = readWorkspaceOpenStates();
      const normalized = normalizeWorkspaceOpenState(state);
      states[stateKey] = {
        openFilePaths: normalized.openFilePaths,
        activeFilePath: normalized.activeFilePath,
        expandedPaths: normalized.expandedPaths,
        updatedAt: new Date().toISOString(),
      };
      const entries = Object.entries(states)
        .filter(([, value]) => value && typeof value === "object")
        .sort((left, right) => Date.parse(String(right[1].updatedAt || "")) - Date.parse(String(left[1].updatedAt || "")))
        .slice(0, 48);
      window.localStorage.setItem(workspaceOpenStatePreferenceKey, JSON.stringify(Object.fromEntries(entries)));
    } catch (_error) {
      // Ignore storage failures and keep the current in-memory editor state.
    }
  }

  function writeWorkspaceOpenState(form = currentForm()) {
    const key = currentWorkspaceKey(form);
    if (String(key || "").trim() === "") {
      return;
    }
    const openFilePaths = normalizeWorkspacePathList(workspaceOpenFilePaths);
    if (workspaceActiveFilePath !== "" && !openFilePaths.includes(workspaceActiveFilePath)) {
      openFilePaths.unshift(workspaceActiveFilePath);
    }
    writeWorkspaceOpenStateForKey(key, {
      openFilePaths,
      activeFilePath: workspaceActiveFilePath,
      expandedPaths: Array.from(workspaceOpenPaths),
    });
  }

  function restoreWorkspaceOpenStateForKey(key) {
    const state = normalizeWorkspaceOpenState(readWorkspaceOpenStates()[String(key || "").trim()]);
    workspaceOpenFilePaths = state.openFilePaths;
    workspaceActiveFilePath = state.activeFilePath;
    workspacePendingRestoreFilePath = state.activeFilePath;
    workspaceOpenPaths = new Set(state.expandedPaths.length > 0 ? state.expandedPaths : [""]);
    workspaceOpenFilePaths.forEach(expandWorkspaceAncestors);
    if (workspaceActiveFilePath !== "") {
      expandWorkspaceAncestors(workspaceActiveFilePath);
    }
  }

  function rememberWorkspaceOpenFile(path, active = true, persist = true) {
    const nextPath = normalizeWorkspacePath(path);
    if (nextPath === "") {
      return;
    }
    workspaceOpenFilePaths = normalizeWorkspacePathList([nextPath, ...workspaceOpenFilePaths]);
    if (active) {
      workspaceActiveFilePath = nextPath;
    }
    expandWorkspaceAncestors(nextPath);
    if (persist) {
      writeWorkspaceOpenState();
    }
  }

  function forgetWorkspaceOpenFile(path, persist = true) {
    const targetPath = normalizeWorkspacePath(path);
    if (targetPath === "") {
      return "";
    }
    const previousIndex = workspaceOpenFilePaths.indexOf(targetPath);
    workspaceOpenFilePaths = workspaceOpenFilePaths.filter((value) => value !== targetPath);
    let nextActivePath = workspaceActiveFilePath;
    if (workspaceActiveFilePath === targetPath) {
      nextActivePath = workspaceOpenFilePaths[Math.max(0, previousIndex - 1)] || workspaceOpenFilePaths[previousIndex] || "";
      workspaceActiveFilePath = nextActivePath;
      workspacePendingRestoreFilePath = nextActivePath;
    }
    if (persist) {
      writeWorkspaceOpenState();
    }
    return nextActivePath;
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
    workspaceOpenFilePaths = [];
    workspaceLoadingPaths = new Set();
    workspaceUploadTargetPath = "";
    workspaceUploadTargetRoot = false;
    workspaceInlineEdit = null;
    hideWorkspaceContextMenu();
    workspaceActiveFilePath = "";
    workspacePendingRestoreFilePath = "";
    workspaceEditorKind = "text";
    workspaceEditorMediaType = "";
    workspaceEditorPreviewURL = "";
    workspaceEditorContent = "";
    workspaceEditorSavedContent = "";
    workspaceEditorSize = 0;
    workspaceEditorBusy = "";
    workspaceEditorStatusMessage = "";
    workspaceEditorStatusError = false;
    workspaceEditorHighlightedContent = null;
    workspaceEditorHighlightedLanguage = "";
    workspaceEditorHighlightedPath = "";
    workspaceEditorMarkdownMode = "preview";
    workspaceTreeStatusMessage = "";
    workspaceTreeStatusError = false;
    restoreWorkspaceOpenStateForKey(nextKey);
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

  function workspaceEditorPathIsMarkdown(path) {
    return workspaceHighlightLanguageForPath(path) === "markdown";
  }

  function workspaceEditorIsMarkdown() {
    const mediaType = String(workspaceEditorMediaType || "").trim().toLowerCase();
    return workspaceActiveFilePath !== "" && workspaceEditorKind !== "image" && (workspaceEditorPathIsMarkdown(workspaceActiveFilePath) || mediaType === "text/markdown" || mediaType === "text/x-markdown");
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

  function workspaceHighlightLanguageForPath(path) {
    const cleanPath = String(path || "").trim().split("#")[0].split("?")[0];
    const name = basenameForIcon(cleanPath).toLowerCase();
    if (name === "") {
      return "";
    }
    const prefixedDockerfileMatch = name.match(/(^|[._-])(dockerfile|containerfile)$/);
    if (prefixedDockerfileMatch) {
      return "dockerfile";
    }
    if (workspaceHighlightExactLanguages.has(name)) {
      return workspaceHighlightExactLanguages.get(name) || "";
    }
    const dotIndex = name.lastIndexOf(".");
    if (dotIndex < 0 || dotIndex === name.length - 1) {
      return "";
    }
    const extension = name.slice(dotIndex + 1);
    return workspaceHighlightExtensionLanguages.get(extension) || "";
  }

  function workspaceHighlightSupportedLanguage(language) {
    const candidate = String(language || "").trim();
    if (candidate === "") {
      return "";
    }
    const highlighter = window.hljs;
    if (!highlighter || typeof highlighter.getLanguage !== "function") {
      return "";
    }
    return highlighter.getLanguage(candidate) ? candidate : "";
  }

  function workspaceHighlightClass(language) {
    const normalized = String(language || "").trim().replace(/[^a-z0-9_-]/gi, "");
    return normalized === "" ? "" : `language-${normalized}`;
  }

  function resetWorkspaceEditorHighlightCache() {
    workspaceEditorHighlightedContent = null;
    workspaceEditorHighlightedLanguage = "";
    workspaceEditorHighlightedPath = "";
  }

  function setWorkspaceEditorHighlightReady(host, ready) {
    if (!(host instanceof HTMLElement)) {
      return;
    }
    if (ready) {
      host.dataset.chatEditorHighlightReady = "true";
    } else {
      delete host.dataset.chatEditorHighlightReady;
    }
  }

  function normalizeWorkspaceMarkdownCodeLanguage(value) {
    return String(value || "")
      .trim()
      .split(/\s+/)[0]
      .replace(/^language-/i, "")
      .replace(/[^a-z0-9_+-]/gi, "")
      .toLowerCase();
  }

  function workspaceMarkdownCodeLanguage(code) {
    if (!(code instanceof HTMLElement)) {
      return "";
    }
    const explicit = normalizeWorkspaceMarkdownCodeLanguage(code.dataset.language || "");
    if (explicit !== "") {
      return explicit;
    }
    const languageClass = Array.from(code.classList).find((className) => /^language-/i.test(className));
    return normalizeWorkspaceMarkdownCodeLanguage(languageClass || "");
  }

  function enhanceWorkspaceMarkdownCodeBlocks(host) {
    if (!(host instanceof HTMLElement)) {
      return;
    }
    const highlighter = window.hljs;
    host.querySelectorAll("pre.chat-markdown-pre").forEach((pre) => {
      if (!(pre instanceof HTMLElement)) {
        return;
      }
      const code = pre.querySelector("code");
      if (!(code instanceof HTMLElement)) {
        return;
      }
      const rawLanguage = workspaceMarkdownCodeLanguage(code);
      if (rawLanguage !== "") {
        pre.dataset.chatMarkdownLanguage = rawLanguage.toUpperCase();
      } else {
        delete pre.dataset.chatMarkdownLanguage;
      }
      const language = workspaceHighlightSupportedLanguage(rawLanguage);
      code.className = language === "" ? "hljs" : `hljs ${workspaceHighlightClass(language)}`;
      if (!highlighter || typeof highlighter.highlight !== "function" || language === "") {
        return;
      }
      try {
        code.innerHTML = highlighter.highlight(String(code.textContent || ""), { language, ignoreIllegals: true }).value;
      } catch (_error) {
        code.textContent = String(code.textContent || "");
      }
    });
  }

  function renderWorkspaceEditorMarkdown(form, content) {
    const host = workspaceEditorMarkdownHost(form);
    if (!(host instanceof HTMLElement)) {
      return;
    }
    const source = String(content || "");
    if (host.dataset.chatMarkdownSource === source) {
      return;
    }
    const markdown = window.AIYoloMarkdown;
    if (markdown && typeof markdown.renderInto === "function") {
      markdown.renderInto(host, source);
      enhanceWorkspaceMarkdownCodeBlocks(host);
      return;
    }
    host.dataset.chatMarkdownSource = source;
    host.textContent = source;
  }

  function syncWorkspaceEditorMarkdownControls(form, visible) {
    const controls = workspaceEditorMarkdownControls(form);
    if (controls instanceof HTMLElement) {
      controls.hidden = !visible;
    }
    workspaceEditorMarkdownModeButtons(form).forEach((button) => {
      const active = String(button.dataset.chatEditorMarkdownMode || "").trim() === workspaceEditorMarkdownMode;
      button.classList.toggle("is-active", active);
      button.setAttribute("aria-pressed", active ? "true" : "false");
    });
  }

  function workspaceEditorLineCount(content) {
    const value = String(content || "");
    if (value === "") {
      return 1;
    }
    let count = 1;
    for (let index = 0; index < value.length; index += 1) {
      if (value.charCodeAt(index) === 10) {
        count += 1;
      }
    }
    return count;
  }

  function renderWorkspaceEditorLineNumbers(form, content) {
    const host = workspaceEditorCodeHost(form);
    const lineNumbers = workspaceEditorLineNumbersNode(form);
    if (!(host instanceof HTMLElement) || !(lineNumbers instanceof HTMLElement)) {
      return;
    }
    const lineCount = workspaceEditorLineCount(content);
    const digits = String(lineCount).length;
    host.style.setProperty("--chat-editor-gutter-width", `${Math.max(4, digits + 2)}ch`);
    if (lineNumbers.dataset.chatEditorLineCount === String(lineCount)) {
      return;
    }
    let nextContent = "";
    for (let lineNumber = 1; lineNumber <= lineCount; lineNumber += 1) {
      nextContent += `${lineNumber}${lineNumber === lineCount ? "" : "\n"}`;
    }
    lineNumbers.textContent = nextContent;
    lineNumbers.dataset.chatEditorLineCount = String(lineCount);
  }

  function syncWorkspaceEditorLineNumberScroll(input) {
    if (!(input instanceof HTMLTextAreaElement)) {
      return;
    }
    const form = input.form instanceof HTMLFormElement ? input.form : currentForm();
    const lineNumbers = workspaceEditorLineNumbersNode(form);
    if (!(lineNumbers instanceof HTMLElement)) {
      return;
    }
    lineNumbers.style.transform = input.scrollTop > 0 ? `translateY(-${input.scrollTop}px)` : "";
  }

  function syncWorkspaceEditorHighlightScroll(input) {
    if (!(input instanceof HTMLTextAreaElement)) {
      return;
    }
    const form = input.form instanceof HTMLFormElement ? input.form : currentForm();
    const highlight = form?.querySelector(".chat-editor-highlight");
    if (!(highlight instanceof HTMLElement)) {
      return;
    }
    highlight.scrollTop = input.scrollTop;
    highlight.scrollLeft = input.scrollLeft;
    syncWorkspaceEditorLineNumberScroll(input);
  }

  function renderWorkspaceEditorHighlight(form, content) {
    const host = workspaceEditorCodeHost(form);
    const code = workspaceEditorHighlightNode(form);
    if (!(form instanceof HTMLFormElement) || !(host instanceof HTMLElement) || !(code instanceof HTMLElement)) {
      return;
    }
    renderWorkspaceEditorLineNumbers(form, content);
    const rawLanguage = workspaceHighlightLanguageForPath(workspaceActiveFilePath);
    const language = workspaceHighlightSupportedLanguage(rawLanguage);
    host.dataset.chatEditorLanguage = language;
    if (language === "") {
      delete host.dataset.chatEditorLanguage;
    }
    code.className = workspaceHighlightClass(language);
    const value = String(content || "");
    if (workspaceEditorHighlightedContent === value && workspaceEditorHighlightedLanguage === language && workspaceEditorHighlightedPath === workspaceActiveFilePath) {
      setWorkspaceEditorHighlightReady(host, code.textContent === value || value === "");
      syncWorkspaceEditorHighlightScroll(workspaceEditorInput(form));
      return;
    }
    workspaceEditorHighlightedContent = value;
    workspaceEditorHighlightedLanguage = language;
    workspaceEditorHighlightedPath = workspaceActiveFilePath;
    setWorkspaceEditorHighlightReady(host, false);
    const highlighter = window.hljs;
    if (language !== "" && highlighter && typeof highlighter.highlight === "function" && value.length <= workspaceHighlightMaxChars) {
      try {
        code.innerHTML = highlighter.highlight(value, { language, ignoreIllegals: true }).value;
        setWorkspaceEditorHighlightReady(host, true);
        syncWorkspaceEditorHighlightScroll(workspaceEditorInput(form));
        return;
      } catch (_error) {
        // Fall through to plain text when a CDN language definition is missing or rejects the file.
      }
    }
    code.textContent = value;
    setWorkspaceEditorHighlightReady(host, true);
    syncWorkspaceEditorHighlightScroll(workspaceEditorInput(form));
  }

  function dispatchWorkspaceEditorInput(input, inputType) {
    try {
      input.dispatchEvent(new InputEvent("input", { bubbles: true, inputType }));
    } catch (_error) {
      input.dispatchEvent(new Event("input", { bubbles: true }));
    }
  }

  function workspaceSelectedLineRange(value, start, end) {
    const lineStart = value.lastIndexOf("\n", Math.max(0, start - 1)) + 1;
    let effectiveEnd = end;
    if (effectiveEnd > start && value.charAt(effectiveEnd - 1) === "\n") {
      effectiveEnd -= 1;
    }
    const nextBreak = value.indexOf("\n", Math.max(lineStart, effectiveEnd));
    return {
      start: lineStart,
      end: nextBreak === -1 ? value.length : nextBreak,
    };
  }

  function indentWorkspaceEditorSelection(input) {
    const start = input.selectionStart;
    const end = input.selectionEnd;
    const value = input.value;
    if (start === end) {
      input.setRangeText(workspaceEditorIndentText, start, end, "end");
      dispatchWorkspaceEditorInput(input, "insertText");
      return;
    }
    const range = workspaceSelectedLineRange(value, start, end);
    const block = value.slice(range.start, range.end);
    const lineCount = workspaceEditorLineCount(block);
    const replacement = block.split("\n").map((line) => `${workspaceEditorIndentText}${line}`).join("\n");
    input.value = `${value.slice(0, range.start)}${replacement}${value.slice(range.end)}`;
    input.selectionStart = start + workspaceEditorIndentText.length;
    input.selectionEnd = end + (workspaceEditorIndentText.length * lineCount);
    dispatchWorkspaceEditorInput(input, "insertText");
  }

  function workspaceOutdentLength(line) {
    if (line.startsWith("\t")) {
      return 1;
    }
    if (line.startsWith("  ")) {
      return 2;
    }
    if (line.startsWith(" ")) {
      return 1;
    }
    return 0;
  }

  function outdentWorkspaceEditorSelection(input) {
    const start = input.selectionStart;
    const end = input.selectionEnd;
    const value = input.value;
    const range = workspaceSelectedLineRange(value, start, end);
    const block = value.slice(range.start, range.end);
    const lines = block.split("\n");
    let absoluteOffset = range.start;
    let nextStart = start;
    let nextEnd = end;
    const replacement = lines.map((line) => {
      const outdentLength = workspaceOutdentLength(line);
      if (outdentLength > 0) {
        if (absoluteOffset < start) {
          nextStart -= Math.min(outdentLength, start - absoluteOffset);
        }
        if (absoluteOffset < end) {
          nextEnd -= Math.min(outdentLength, end - absoluteOffset);
        }
      }
      absoluteOffset += line.length + 1;
      return line.slice(outdentLength);
    }).join("\n");
    if (replacement === block) {
      return;
    }
    input.value = `${value.slice(0, range.start)}${replacement}${value.slice(range.end)}`;
    input.selectionStart = Math.max(range.start, nextStart);
    input.selectionEnd = Math.max(input.selectionStart, nextEnd);
    dispatchWorkspaceEditorInput(input, "deleteContentBackward");
  }

  function syncWorkspaceEditorSurface(input, previewHost, previewImage, options = {}) {
    const mode = String(options.mode || "text").trim() === "image" ? "image" : "text";
    const form = input?.form instanceof HTMLFormElement ? input.form : currentForm();
    const codeHost = workspaceEditorCodeHost(form);
    const markdownHost = workspaceEditorMarkdownHost(form);
    const highlightNode = workspaceEditorHighlightNode(form);
    const markdownActive = mode !== "image" && Boolean(options.markdown);
    const markdownPreviewActive = markdownActive && workspaceEditorMarkdownMode === "preview";
    syncWorkspaceEditorMarkdownControls(form, markdownActive);
    if (previewHost instanceof HTMLElement) {
      previewHost.hidden = mode !== "image";
    }
    if (codeHost instanceof HTMLElement) {
      codeHost.hidden = mode === "image" || markdownPreviewActive;
    }
    if (markdownHost instanceof HTMLElement) {
      markdownHost.hidden = !markdownPreviewActive;
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
      setWorkspaceEditorHighlightReady(codeHost, false);
      if (highlightNode instanceof HTMLElement) {
        highlightNode.textContent = "";
      }
      resetWorkspaceEditorHighlightCache();
      if (input.value !== "") {
        input.value = "";
      }
      input.disabled = true;
      renderWorkspaceEditorMarkdown(form, "");
      return;
    }
    const nextValue = String(options.value || "");
    if (input.value !== nextValue) {
      input.value = nextValue;
    }
    input.disabled = Boolean(options.disabled) || markdownPreviewActive;
    renderWorkspaceEditorHighlight(form, nextValue);
    if (markdownActive) {
      renderWorkspaceEditorMarkdown(form, nextValue);
    } else {
      renderWorkspaceEditorMarkdown(form, "");
    }
    syncWorkspaceEditorHighlightScroll(input);
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

  function basenameForIcon(path) {
    const cleanPath = String(path || "").trim().split("#")[0].split("?")[0].replace(/\/+$/, "");
    if (cleanPath === "") {
      return "";
    }
    const segments = cleanPath.split("/").filter(Boolean);
    return segments.length > 0 ? segments[segments.length - 1] : cleanPath;
  }

  function fileIconLookupName(entry) {
    const explicitName = String(entry?.name || "").trim();
    return explicitName || basenameForIcon(entry?.path);
  }

  function fileIconClassName(entry) {
    if (!entry || entry.type === "directory") {
      return "";
    }
    const icons = window.FileIcons;
    if (!icons || typeof icons.getClassWithColor !== "function") {
      return "";
    }
    const lookupName = fileIconLookupName(entry);
    if (lookupName === "") {
      return "";
    }
    const className = String(icons.getClassWithColor(lookupName) || "").trim();
    return className || "text-icon medium-blue";
  }

  function applyWorkspaceEntryIcon(node, entry) {
    if (!(node instanceof HTMLElement)) {
      return;
    }
    const type = entry?.type === "directory" ? "directory" : "file";
    node.classList.remove("has-file-icon");
    node.replaceChildren();
    node.textContent = type === "directory" ? "DIR" : "TXT";
    if (type !== "file") {
      return;
    }
    const iconClassName = fileIconClassName(entry);
    if (iconClassName === "") {
      return;
    }
    const icon = document.createElement("i");
    icon.className = `icon ${iconClassName}`;
    icon.setAttribute("aria-hidden", "true");
    node.classList.add("has-file-icon");
    node.replaceChildren(icon);
  }

  function cacheWorkspaceChildTrees(children) {
    if (!children || typeof children !== "object") {
      return;
    }
    Object.entries(children).forEach(([path, entries]) => {
      const childPath = String(path || "").trim();
      if (childPath === "" || !Array.isArray(entries)) {
        return;
      }
      workspaceTreeCache.set(childPath, entries.map(normalizeWorkspaceEntry).filter(Boolean));
      workspaceLoadingPaths.delete(childPath);
    });
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

  function workspaceInlineCreateForParent(parentPath) {
    if (!workspaceInlineEdit || workspaceInlineEdit.mode !== "create") {
      return null;
    }
    return normalizeWorkspaceCreatePath(workspaceInlineEdit.parentPath) === normalizeWorkspaceCreatePath(parentPath) ? workspaceInlineEdit : null;
  }

  function workspaceInlineRenameForPath(path) {
    if (!workspaceInlineEdit || workspaceInlineEdit.mode !== "rename") {
      return null;
    }
    return normalizeWorkspaceCreatePath(workspaceInlineEdit.originalPath) === normalizeWorkspaceCreatePath(path) ? workspaceInlineEdit : null;
  }

  function renderWorkspaceInlineEdit(container, edit, depth) {
    if (!(container instanceof Node) || !edit) {
      return;
    }
    const row = document.createElement("div");
    row.className = "chat-workspace-inline-row";
    row.style.setProperty("--chat-workspace-depth", String(depth));
    row.dataset.chatWorkspaceInlineMode = edit.mode;
    row.dataset.chatWorkspaceType = edit.kind === "directory" ? "directory" : "file";

    const caret = document.createElement("span");
    caret.className = "chat-workspace-row-caret";
    caret.textContent = "";

    const kind = document.createElement("span");
    kind.className = "chat-workspace-row-kind";
    applyWorkspaceEntryIcon(kind, { name: edit.value || workspaceBaseName(edit.originalPath), path: edit.originalPath || "", type: edit.kind });

    const input = document.createElement("input");
    input.type = "text";
    input.className = "chat-workspace-inline-input";
    input.dataset.chatWorkspaceInlineInput = "";
    input.value = String(edit.value || "");
    input.disabled = Boolean(edit.busy);
    input.autocomplete = "off";
    input.spellcheck = false;
    input.placeholder = edit.kind === "directory" ? t("目录名", "Folder name") : t("文件名", "File name");
    input.setAttribute("aria-label", edit.mode === "rename" ? t("重命名", "Rename") : (edit.kind === "directory" ? t("新建目录", "New folder") : t("新建文件", "New file")));

    row.appendChild(caret);
    row.appendChild(kind);
    row.appendChild(input);
    container.appendChild(row);
  }

  function focusWorkspaceInlineInput(form) {
    if (!workspaceInlineEdit || !(form instanceof HTMLFormElement)) {
      return;
    }
    window.queueMicrotask(() => {
      const input = form.querySelector("[data-chat-workspace-inline-input]");
      if (!(input instanceof HTMLInputElement) || input.disabled) {
        return;
      }
      input.focus();
      input.select();
    });
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
      applyWorkspaceEntryIcon(kind, entry);

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

  function renderWorkspaceEntries(container, entries, depth, parentPath = "") {
    const inlineCreate = workspaceInlineCreateForParent(parentPath);
    if (inlineCreate) {
      renderWorkspaceInlineEdit(container, inlineCreate, depth);
    }
    entries.forEach((entry) => {
      const item = document.createElement("div");
      item.className = "chat-workspace-item";
      const inlineRename = workspaceInlineRenameForPath(entry.path);

      if (inlineRename) {
        renderWorkspaceInlineEdit(item, inlineRename, depth);
      } else {
        const row = document.createElement("button");
        row.type = "button";
        row.className = "chat-workspace-row";
        row.style.setProperty("--chat-workspace-depth", String(depth));
        row.dataset.chatAction = entry.type === "directory" ? "toggle-workspace-directory" : "open-workspace-file";
        row.dataset.chatWorkspacePath = entry.path;
        row.dataset.chatWorkspaceName = entry.name;
        row.dataset.chatWorkspaceType = entry.type;
        row.dataset.chatWorkspaceSize = String(entry.size || "");
        row.dataset.chatWorkspaceModifiedAt = String(entry.modifiedAt || "");
        row.classList.toggle("is-active", workspaceActiveFilePath === entry.path);
        row.classList.toggle("is-upload-target", !workspaceUploadTargetRoot && entry.type === "directory" && workspaceUploadTargetPath === entry.path);

        const caret = document.createElement("span");
        caret.className = "chat-workspace-row-caret";
        caret.textContent = entry.type === "directory" ? (workspaceOpenPaths.has(entry.path) ? "v" : ">") : "";

        const kind = document.createElement("span");
        kind.className = "chat-workspace-row-kind";
        applyWorkspaceEntryIcon(kind, entry);

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
      }

      if (entry.type === "directory" && workspaceOpenPaths.has(entry.path)) {
        const children = document.createElement("div");
        children.className = "chat-workspace-children";
        const childEntries = workspaceTreeCache.get(entry.path);
        const hasInlineCreate = Boolean(workspaceInlineCreateForParent(entry.path));
        if (Array.isArray(childEntries)) {
          if (childEntries.length === 0 && !hasInlineCreate) {
            renderWorkspacePlaceholder(children, t("这个目录当前为空。", "This directory is empty."));
          } else {
            renderWorkspaceEntries(children, childEntries, depth + 1, entry.path);
          }
        } else if (workspaceLoadingPaths.has(entry.path)) {
          if (hasInlineCreate) {
            renderWorkspaceInlineEdit(children, workspaceInlineEdit, depth + 1);
          }
          renderWorkspacePlaceholder(children, t("正在加载目录…", "Loading directory..."));
        } else {
          if (hasInlineCreate) {
            renderWorkspaceInlineEdit(children, workspaceInlineEdit, depth + 1);
          }
          renderWorkspacePlaceholder(children, t("展开目录后将在这里显示子项。", "Expand the folder to show its children."));
        }
        item.appendChild(children);
      }

      container.appendChild(item);
    });
    return Boolean(inlineCreate) || entries.length > 0;
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
      section.classList.toggle("is-upload-target", workspaceUploadTargetRoot);
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
      const inlineRootCreate = workspaceInlineCreateForParent("");
      if (inlineRootCreate) {
        const fragment = document.createDocumentFragment();
        renderWorkspaceInlineEdit(fragment, inlineRootCreate, 0);
        const empty = document.createElement("div");
        empty.className = "chat-workspace-empty";
        empty.textContent = workspaceLoadingPaths.has("")
          ? t("正在加载工作区目录…", "Loading the workspace tree...")
          : t("点击刷新或重新选择 Cloud Agent 环境后，将在这里加载目录树。", "Refresh or re-select the Cloud Agent environment to load the directory tree here.");
        fragment.appendChild(empty);
        host.replaceChildren(fragment);
        focusWorkspaceInlineInput(form);
        return;
      }
      if (workspaceLoadingPaths.has("")) {
        renderWorkspacePlaceholder(host, t("正在加载工作区目录…", "Loading the workspace tree..."));
      } else {
        renderWorkspacePlaceholder(host, t("点击刷新或重新选择 Cloud Agent 环境后，将在这里加载目录树。", "Refresh or re-select the Cloud Agent environment to load the directory tree here."));
      }
      return;
    }
    if (rootEntries.length === 0 && !workspaceInlineCreateForParent("")) {
      renderWorkspacePlaceholder(host, t("当前工作区为空。", "The current workspace is empty."));
      return;
    }
    const fragment = document.createDocumentFragment();
    renderWorkspaceEntries(fragment, rootEntries, 0, "");
    host.replaceChildren(fragment);
    focusWorkspaceInlineInput(form);
  }

  function renderWorkspaceEditorTabs(form) {
    const host = workspaceEditorTabsNode(form);
    if (!(host instanceof HTMLElement)) {
      return;
    }
    const paths = normalizeWorkspacePathList(workspaceOpenFilePaths);
    host.replaceChildren();
    if (paths.length === 0) {
      const emptyTab = document.createElement("div");
      emptyTab.className = "chat-editor-tab is-active";
      emptyTab.setAttribute("role", "tab");
      emptyTab.setAttribute("aria-selected", "true");

      const icon = document.createElement("span");
      icon.className = "chat-editor-tab-icon";
      icon.setAttribute("aria-hidden", "true");

      const name = document.createElement("span");
      name.className = "chat-editor-tab-name";
      name.dataset.chatEditorTabName = "";
      name.textContent = t("尚未打开文件", "No file open");

      const dirty = document.createElement("span");
      dirty.className = "chat-editor-tab-dirty";
      dirty.dataset.chatEditorDirty = "";
      dirty.hidden = true;

      emptyTab.appendChild(icon);
      emptyTab.appendChild(name);
      emptyTab.appendChild(dirty);
      host.appendChild(emptyTab);
      return;
    }

    paths.forEach((path) => {
      const active = path === workspaceActiveFilePath;
      const pathInfo = workspacePathParts(path);
      const item = document.createElement("div");
      item.className = "chat-editor-tab-item";
      item.classList.toggle("is-active", active);

      const tab = document.createElement("button");
      tab.type = "button";
      tab.className = "chat-editor-tab";
      tab.dataset.chatAction = "activate-workspace-tab";
      tab.dataset.chatWorkspacePath = path;
      tab.setAttribute("role", "tab");
      tab.setAttribute("aria-selected", active ? "true" : "false");
      tab.title = path;
      tab.classList.toggle("is-active", active);

      const icon = document.createElement("span");
      icon.className = "chat-editor-tab-icon";
      icon.dataset.chatEditorTabIcon = "";
      icon.setAttribute("aria-hidden", "true");
      applyWorkspaceEntryIcon(icon, { name: basenameForIcon(path), path, type: "file" });

      const name = document.createElement("span");
      name.className = "chat-editor-tab-name";
      name.dataset.chatEditorTabName = "";
      name.textContent = pathInfo.name;

      const dirty = document.createElement("span");
      dirty.className = "chat-editor-tab-dirty";
      dirty.dataset.chatEditorDirty = "";
      dirty.hidden = !(active && editorIsDirty());

      tab.appendChild(icon);
      tab.appendChild(name);
      tab.appendChild(dirty);

      const close = document.createElement("button");
      close.type = "button";
      close.className = "chat-editor-tab-close";
      close.dataset.chatAction = "close-workspace-tab";
      close.dataset.chatWorkspacePath = path;
      close.setAttribute("aria-label", `${t("关闭", "Close")} ${pathInfo.name}`);
      close.textContent = "x";

      item.appendChild(tab);
      item.appendChild(close);
      host.appendChild(item);
    });
  }

  function renderWorkspaceEditor(form) {
    const pathNode = workspaceEditorPathNode(form);
    const tabIconNode = workspaceEditorTabIconNode(form);
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
    renderWorkspaceEditorTabs(form);
    applyImagePreviewBackground(form, workspaceImagePreviewBackground, false);
    const setEditorChrome = (name, directory, isFile = false) => {
      pathNode.textContent = name;
      if (tabNameNode instanceof HTMLElement) {
        tabNameNode.textContent = name;
      }
      if (tabIconNode instanceof HTMLElement) {
        const iconName = workspaceActiveFilePath === "" ? name : basenameForIcon(workspaceActiveFilePath);
        applyWorkspaceEntryIcon(tabIconNode, { name: iconName, path: workspaceActiveFilePath || iconName, type: "file" });
      }
      if (directoryNode instanceof HTMLElement) {
        const showDirectory = isFile && directory !== "" && directory !== t("工作区根目录", "Workspace root");
        directoryNode.textContent = showDirectory ? directory : "";
        directoryNode.hidden = !showDirectory;
      }
      const heading = pathNode.closest(".chat-editor-heading");
      if (heading instanceof HTMLElement) {
        heading.classList.toggle("is-file", isFile);
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
      : { mode: "text", value: workspaceEditorContent, disabled: workspaceEditorBusy !== "", markdown: workspaceEditorIsMarkdown() });
    if (workspaceEditorBusy !== "") {
      setEditorChrome(
        pathInfo.name,
        pathInfo.directory,
        true
      );
      statusNode.textContent = workspaceEditorBusy === "save"
        ? t("正在保存文件…", "Saving file...")
        : t("正在加载文件…", "Loading file...");
      statusNode.classList.remove("is-error");
    } else if (workspaceEditorStatusMessage !== "") {
      setEditorChrome(
        pathInfo.name,
        pathInfo.directory,
        true
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
        pathInfo.directory,
        true
      );
      statusNode.textContent = statusParts.join(" · ");
      statusNode.classList.remove("is-error");
    } else {
      setEditorChrome(
        pathInfo.name,
        pathInfo.directory,
        true
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
      const error = new Error(String(parsed?.error || t("工作区请求失败。", "Workspace request failed.")));
      error.status = response.status;
      throw error;
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
      cacheWorkspaceChildTrees(parsed.children);
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
      workspaceEditorMarkdownMode = "source";
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
    if (attachmentActiveFilePath !== "") {
      attachmentActiveFilePath = "";
      setAttachmentTreeStatus("", false);
      renderAttachmentTree(form);
    }
    rememberWorkspaceOpenFile(relativePath, true, true);
    workspaceEditorKind = "text";
    workspaceEditorMediaType = "";
    workspaceEditorPreviewURL = "";
    workspaceEditorContent = "";
    workspaceEditorSavedContent = "";
    workspaceEditorSize = 0;
    workspaceEditorMarkdownMode = workspaceEditorPathIsMarkdown(relativePath) ? "preview" : "source";
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
      rememberWorkspaceOpenFile(workspaceActiveFilePath, true, true);
      workspaceEditorKind = String(parsed.kind || "").trim() === "image" ? "image" : "text";
      workspaceEditorMediaType = String(parsed.mediaType || "").trim();
      workspaceEditorPreviewURL = workspaceEditorKind === "image" ? String(parsed.previewURL || "").trim() : "";
      workspaceEditorContent = workspaceEditorKind === "image" ? "" : String(parsed.content || "");
      workspaceEditorSavedContent = workspaceEditorContent;
      workspaceEditorSize = Number.isFinite(Number(parsed.size)) ? Number(parsed.size) : (workspaceEditorKind === "image" ? 0 : measureTextBytes(workspaceEditorContent));
      workspaceEditorMarkdownMode = workspaceEditorIsMarkdown() ? "preview" : "source";
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

  async function refreshWorkspaceTreeForPath(form, path, includeSelf = false) {
    const directories = workspaceAncestorDirectories(path, includeSelf);
    workspaceOpenPaths.add("");
    directories.forEach((directory) => workspaceOpenPaths.add(directory));
    workspaceTreeCache.delete("");
    directories.forEach((directory) => workspaceTreeCache.delete(directory));
    writeWorkspaceOpenState(form);
    await loadWorkspaceTree(form, "", true);
    for (const directory of directories) {
      await loadWorkspaceTree(form, directory, true);
    }
  }

  function workspaceMutationReady(form, endpoint) {
    if (!(form instanceof HTMLFormElement) || !isCloudAgentEnvironment(currentSelectedEnvironment(form))) {
      setWorkspaceTreeStatus(t("先切换到 Cloud Agent 环境。", "Switch to a Cloud Agent environment first."), true);
      renderWorkspaceTree(form);
      return false;
    }
    if (readClientSessionID(form) === "") {
      setWorkspaceTreeStatus(t("Cloud Agent 工作区还没有准备好。", "The Cloud Agent workspace is not ready yet."), true);
      renderWorkspaceTree(form);
      return false;
    }
    if (String(endpoint || "").trim() === "") {
      setWorkspaceTreeStatus(t("工作区操作接口不可用。", "Workspace operation endpoint is not available."), true);
      renderWorkspaceTree(form);
      return false;
    }
    return true;
  }

  function workspaceDragContainsFiles(event) {
    const transfer = event?.dataTransfer;
    if (!transfer) {
      return false;
    }
    return Array.from(transfer.types || []).includes("Files");
  }

  function workspaceUploadTargetFromEvent(event, form) {
    if (!(form instanceof HTMLFormElement) || !(event?.target instanceof Element)) {
      return null;
    }
    const row = event.target.closest(".chat-workspace-row[data-chat-workspace-path]");
    if (row instanceof HTMLElement && form.contains(row)) {
      const info = workspaceRowInfo(row);
      if (!info) {
        return null;
      }
      if (info.type === "directory") {
        return { path: info.path, highlightRowPath: info.path, root: false };
      }
      const parentPath = workspaceParentPath(info.path);
      return { path: parentPath, highlightRowPath: parentPath, root: parentPath === "" };
    }
    const section = workspaceSection(form);
    if (section instanceof HTMLElement && section.contains(event.target)) {
      return { path: "", highlightRowPath: "", root: true };
    }
    const treeSection = event.target.closest(".chat-explorer-tree-section");
    if (treeSection instanceof HTMLElement && treeSection !== section) {
      return null;
    }
    const filesPanel = workspaceFilesPanel(form);
    if (section instanceof HTMLElement && !section.hidden && filesPanel instanceof HTMLElement && filesPanel.contains(event.target)) {
      return { path: "", highlightRowPath: "", root: true };
    }
    return null;
  }

  function setWorkspaceUploadTarget(form, path, root = false) {
    const nextPath = normalizeWorkspaceCreatePath(path);
    const nextRoot = Boolean(root);
    if (workspaceUploadTargetPath === nextPath && workspaceUploadTargetRoot === nextRoot) {
      return;
    }
    workspaceUploadTargetPath = nextPath;
    workspaceUploadTargetRoot = nextRoot;
    renderWorkspaceTree(form);
  }

  function clearWorkspaceUploadTarget(form = currentForm()) {
    if (workspaceUploadTargetPath === "" && !workspaceUploadTargetRoot) {
      return;
    }
    workspaceUploadTargetPath = "";
    workspaceUploadTargetRoot = false;
    renderWorkspaceTree(form);
  }

  function workspaceUploadFileName(file) {
    const raw = String(file?.name || "").replace(/\\/g, "/").split("/").filter(Boolean).pop() || "";
    const name = raw.trim();
    if (name === "" || name === "." || name === ".." || name.includes("\0")) {
      return "";
    }
    return name;
  }

  function joinWorkspaceUploadPath(directoryPath, fileName) {
    const directory = normalizeWorkspaceCreatePath(directoryPath);
    return directory === "" ? fileName : `${directory}/${fileName}`;
  }

  function workspaceDirectoryHasEntry(directoryPath, name) {
    const entries = workspaceTreeCache.get(normalizeWorkspaceCreatePath(directoryPath));
    return Array.isArray(entries) && entries.some((entry) => entry.name === name);
  }

  async function uploadWorkspaceFile(form, endpoint, sessionID, relativePath, file, overwrite) {
    const url = new URL(endpoint, window.location.href);
    url.searchParams.set("session", sessionID);
    const body = new FormData();
    body.append("path", relativePath);
    body.append("overwrite", overwrite ? "true" : "false");
    body.append("file", file, workspaceUploadFileName(file));
    return fetchWorkspaceJSON(url.toString(), { method: "POST", body });
  }

  async function uploadWorkspaceFilesToDirectory(form, directoryPath, files) {
    const endpoint = workspaceUploadURL(form);
    if (!workspaceMutationReady(form, endpoint)) {
      return;
    }
    const sessionID = readClientSessionID(form);
    const uploadFiles = Array.from(files || []).filter((file) => file && typeof file.name === "string");
    if (uploadFiles.length === 0) {
      return;
    }
    const targetDirectory = normalizeWorkspaceCreatePath(directoryPath);
    const maxBytes = workspaceUploadMaxBytes(form);
    const requestKey = currentWorkspaceKey(form);
    let uploaded = 0;
    let skipped = 0;
    let failed = 0;
    let lastError = "";
    workspaceOpenPaths.add(targetDirectory);
    setWorkspaceTreeStatus(t("正在上传文件…", "Uploading files..."), false);
    renderWorkspaceTree(form);
    for (let index = 0; index < uploadFiles.length; index += 1) {
      if (requestKey !== workspaceSessionKey || requestKey !== currentWorkspaceKey(form)) {
        return;
      }
      const file = uploadFiles[index];
      const fileName = workspaceUploadFileName(file);
      if (fileName === "") {
        skipped += 1;
        continue;
      }
      if (maxBytes > 0 && Number(file.size || 0) > maxBytes) {
        failed += 1;
        lastError = `${fileName} ${t("超过上传大小上限", "exceeds the upload size limit")} ${formatBytes(maxBytes)}`;
        continue;
      }
      const relativePath = joinWorkspaceUploadPath(targetDirectory, fileName);
      setWorkspaceTreeStatus(`${t("正在上传", "Uploading")} ${index + 1}/${uploadFiles.length}: ${fileName}`, false);
      renderWorkspaceTree(form);
      let overwrite = false;
      if (workspaceDirectoryHasEntry(targetDirectory, fileName)) {
        overwrite = window.confirm(t(`“${fileName}” 已存在。要替换它吗？`, `"${fileName}" already exists. Replace it?`));
        if (!overwrite) {
          skipped += 1;
          continue;
        }
      }
      try {
        await uploadWorkspaceFile(form, endpoint, sessionID, relativePath, file, overwrite);
        uploaded += 1;
      } catch (error) {
        if (Number(error?.status) === 409) {
          const shouldOverwrite = window.confirm(t(`“${fileName}” 已存在。要替换它吗？`, `"${fileName}" already exists. Replace it?`));
          if (shouldOverwrite) {
            try {
              await uploadWorkspaceFile(form, endpoint, sessionID, relativePath, file, true);
              uploaded += 1;
              continue;
            } catch (retryError) {
              failed += 1;
              lastError = String(retryError?.message || t("上传文件失败。", "Failed to upload the file."));
              continue;
            }
          }
          skipped += 1;
          continue;
        }
        failed += 1;
        lastError = String(error?.message || t("上传文件失败。", "Failed to upload the file."));
      }
    }
    if (requestKey !== workspaceSessionKey || requestKey !== currentWorkspaceKey(form)) {
      return;
    }
    if (uploaded > 0) {
      await refreshWorkspaceTreeForPath(form, targetDirectory, true);
    }
    const parts = [];
    if (uploaded > 0) {
      parts.push(`${t("已上传", "Uploaded")} ${uploaded} ${t("个文件", "file(s)")}`);
    }
    if (skipped > 0) {
      parts.push(`${t("已跳过", "Skipped")} ${skipped}`);
    }
    if (failed > 0) {
      parts.push(`${t("失败", "Failed")} ${failed}`);
    }
    setWorkspaceTreeStatus(parts.length > 0 ? `${parts.join(" · ")}${lastError ? ` · ${lastError}` : ""}` : t("没有文件被上传。", "No files were uploaded."), failed > 0 && uploaded === 0);
    renderWorkspaceTree(form);
  }

  function workspaceRowInfo(row) {
    if (!(row instanceof HTMLElement)) {
      return null;
    }
    const path = normalizeWorkspaceCreatePath(row.dataset.chatWorkspacePath || "");
    if (path === "") {
      return null;
    }
    return {
      path,
      name: String(row.dataset.chatWorkspaceName || workspaceBaseName(path) || "").trim(),
      type: String(row.dataset.chatWorkspaceType || "file") === "directory" ? "directory" : "file",
      size: String(row.dataset.chatWorkspaceSize || "").trim(),
      modifiedAt: String(row.dataset.chatWorkspaceModifiedAt || "").trim(),
    };
  }

  function workspaceAbsolutePath(relativePath) {
    const pathValue = normalizeWorkspaceCreatePath(relativePath);
    const root = String(workspaceRootPath || "").trim().replace(/\/+$/, "");
    if (root === "") {
      return pathValue;
    }
    return pathValue === "" ? root : `${root}/${pathValue}`;
  }

  function shellQuote(value) {
    return `'${String(value || "").replace(/'/g, `'"'"'`)}'`;
  }

  async function copyTextToClipboard(text, successMessage) {
    const value = String(text || "");
    try {
      if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
        await navigator.clipboard.writeText(value);
      } else {
        const input = document.createElement("textarea");
        input.value = value;
        input.setAttribute("readonly", "");
        input.style.position = "fixed";
        input.style.left = "-9999px";
        document.body.appendChild(input);
        input.select();
        document.execCommand("copy");
        input.remove();
      }
      setWorkspaceTreeStatus(successMessage, false);
    } catch (_error) {
      setWorkspaceTreeStatus(t("复制失败。", "Copy failed."), true);
    }
    renderWorkspaceTree(currentForm());
  }

  function workspaceFileRequestURL(form, endpoint, path) {
    const url = new URL(endpoint, window.location.href);
    url.searchParams.set("session", readClientSessionID(form));
    url.searchParams.set("path", normalizeWorkspaceCreatePath(path));
    return url;
  }

  async function fetchWorkspaceFilePayload(form, path) {
    const endpoint = workspaceFileURL(form);
    if (!workspaceMutationReady(form, endpoint)) {
      return null;
    }
    return fetchWorkspaceJSON(workspaceFileRequestURL(form, endpoint, path).toString());
  }

  function downloadBlob(blob, filename, openInNewTab = false) {
    const objectURL = URL.createObjectURL(blob);
    if (openInNewTab) {
      const opened = window.open(objectURL, "_blank", "noopener,noreferrer");
      window.setTimeout(() => URL.revokeObjectURL(objectURL), opened ? 30000 : 1000);
      if (opened) {
        return;
      }
    }
    const link = document.createElement("a");
    link.href = objectURL;
    link.download = filename || "download";
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(objectURL), 1000);
  }

  async function fetchWorkspaceDownloadBlob(form, path) {
    const endpoint = workspaceDownloadURL(form);
    if (!workspaceMutationReady(form, endpoint)) {
      return null;
    }
    const response = await fetch(workspaceFileRequestURL(form, endpoint, path).toString(), {
      credentials: "same-origin",
      headers: { Accept: "application/octet-stream, application/json" },
    });
    const contentType = String(response.headers.get("Content-Type") || "").toLowerCase();
    if (!response.ok) {
      let message = t("下载失败。", "Download failed.");
      if (contentType.includes("application/json")) {
        const parsed = safeParseJSON(await response.text(), null);
        message = String(parsed?.error || message);
      }
      const error = new Error(message);
      error.status = response.status;
      throw error;
    }
    return {
      blob: await response.blob(),
      filename: workspaceBaseName(path) || "download",
    };
  }

  async function openWorkspaceExternal(form, info) {
    if (!info || info.type !== "file") {
      setWorkspaceTreeStatus(t("只有文件可以用其他方式打开。", "Only files can be opened another way."), true);
      renderWorkspaceTree(form);
      return;
    }
    try {
      setWorkspaceTreeStatus(t("正在打开文件…", "Opening file..."), false);
      renderWorkspaceTree(form);
      const download = await fetchWorkspaceDownloadBlob(form, info.path);
      if (!download) {
        return;
      }
      downloadBlob(download.blob, download.filename, true);
      setWorkspaceTreeStatus(t("已在浏览器中打开。", "Opened in the browser."), false);
    } catch (error) {
      setWorkspaceTreeStatus(String(error?.message || t("打开文件失败。", "Failed to open the file.")), true);
    }
    renderWorkspaceTree(form);
  }

  async function downloadWorkspaceFile(form, info) {
    if (!info || info.type !== "file") {
      setWorkspaceTreeStatus(t("目录下载暂不支持。", "Folder download is not supported yet."), true);
      renderWorkspaceTree(form);
      return;
    }
    try {
      setWorkspaceTreeStatus(t("正在下载文件…", "Downloading file..."), false);
      renderWorkspaceTree(form);
      const download = await fetchWorkspaceDownloadBlob(form, info.path);
      if (!download) {
        return;
      }
      downloadBlob(download.blob, download.filename, false);
      setWorkspaceTreeStatus(t("已开始下载。", "Download started."), false);
    } catch (error) {
      setWorkspaceTreeStatus(String(error?.message || t("下载失败。", "Download failed.")), true);
    }
    renderWorkspaceTree(form);
  }

  function appendTextToChatDraft(form, text) {
    const draft = form?.querySelector("#chat-draft");
    if (!(draft instanceof HTMLTextAreaElement)) {
      setWorkspaceTreeStatus(t("找不到聊天输入框。", "The chat composer was not found."), true);
      renderWorkspaceTree(form);
      return false;
    }
    const addition = String(text || "").trim();
    if (addition === "") {
      return false;
    }
    const current = draft.value.trimEnd();
    draft.value = current === "" ? addition : `${current}\n\n${addition}`;
    draft.dispatchEvent(new Event("input", { bubbles: true }));
    draft.focus();
    return true;
  }

  async function addWorkspacePathToChat(form, info) {
    if (!info) {
      return;
    }
    try {
      if (info.type === "file") {
        const parsed = await fetchWorkspaceFilePayload(form, info.path);
        if (parsed && String(parsed.kind || "text") === "text") {
          const content = String(parsed.content || "");
          const truncated = content.length > 24000 ? `${content.slice(0, 24000)}\n...` : content;
          appendTextToChatDraft(form, `${t("请参考工作区文件", "Reference workspace file")} ${info.path}:\n\n\`\`\`\n${truncated}\n\`\`\``);
        } else {
          appendTextToChatDraft(form, `${t("请参考工作区文件", "Reference workspace file")} ${info.path}`);
        }
      } else {
        appendTextToChatDraft(form, `${t("请参考工作区目录", "Reference workspace folder")} ${info.path}`);
      }
      setWorkspaceTreeStatus(t("已添加到聊天输入框。", "Added to the chat composer."), false);
    } catch (_error) {
      appendTextToChatDraft(form, `${t("请参考工作区文件", "Reference workspace file")} ${info.path}`);
      setWorkspaceTreeStatus(t("已添加路径到聊天输入框。", "Added the path to the chat composer."), false);
    }
    renderWorkspaceTree(form);
  }

  function openWorkspacePathInTerminal(form, info) {
    if (!info) {
      return;
    }
    const cwd = info.type === "directory" ? info.path : workspaceParentPath(info.path);
    window.dispatchEvent(new CustomEvent("aiyolo:chat-open-shell", {
      detail: { cwd, command: cwd === "" ? "" : `cd -- ${shellQuote(cwd)}\n` },
    }));
    setWorkspaceTreeStatus(t("正在打开集成终端…", "Opening the integrated terminal..."), false);
    renderWorkspaceTree(form);
  }

  function revealWorkspacePath(form, info) {
    if (!info) {
      return;
    }
    layoutState.sidebarCollapsed = false;
    applyLayout(form, true);
    applySidebarView(form, "files", true);
    expandWorkspaceAncestors(info.path);
    writeWorkspaceOpenState(form);
    void loadWorkspaceTree(form, workspaceParentPath(info.path), true);
    setWorkspaceTreeStatus(t("已在资源管理器中显示。", "Revealed in the explorer."), false);
    renderWorkspaceTree(form);
  }

  function shareWorkspacePath(form, info) {
    if (!info) {
      return;
    }
    const url = new URL(window.location.href);
    const sessionID = readClientSessionID(form);
    if (sessionID !== "") {
      url.searchParams.set("session", sessionID);
    }
    url.hash = `workspace=${encodeURIComponent(info.path)}`;
    void copyTextToClipboard(url.toString(), t("已复制共享链接。", "Share link copied."));
  }

  function compareWindowHTML(leftLabel, leftContent, rightLabel, rightContent) {
    const escape = (value) => String(value || "").replace(/[&<>"]/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[char]));
    return `<!doctype html><html><head><meta charset="utf-8"><title>${escape(leftLabel)} ↔ ${escape(rightLabel)}</title><style>body{margin:0;background:#1e1e1e;color:#d4d4d4;font:13px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace}.bar{padding:8px 12px;border-bottom:1px solid #3c3c3c;background:#252526}.grid{display:grid;grid-template-columns:1fr 1fr;min-height:calc(100vh - 38px)}section{min-width:0;border-right:1px solid #3c3c3c}section:last-child{border-right:0}h2{margin:0;padding:8px 12px;font:600 13px system-ui,sans-serif;background:#2d2d2d;color:#e8e8e8}pre{margin:0;padding:12px;white-space:pre-wrap;word-break:break-word}</style></head><body><div class="bar">${escape(t("工作区文件比较", "Workspace file compare"))}</div><main class="grid"><section><h2>${escape(leftLabel)}</h2><pre>${escape(leftContent)}</pre></section><section><h2>${escape(rightLabel)}</h2><pre>${escape(rightContent)}</pre></section></main></body></html>`;
  }

  async function compareWorkspaceFiles(form, leftPath, rightPath, targetWindow) {
    const [left, right] = await Promise.all([
      fetchWorkspaceFilePayload(form, leftPath),
      fetchWorkspaceFilePayload(form, rightPath),
    ]);
    if (!left || !right || String(left.kind || "text") !== "text" || String(right.kind || "text") !== "text") {
      throw new Error(t("只能比较文本文件。", "Only text files can be compared."));
    }
    const html = compareWindowHTML(leftPath, String(left.content || ""), rightPath, String(right.content || ""));
    if (targetWindow && !targetWindow.closed) {
      targetWindow.document.open();
      targetWindow.document.write(html);
      targetWindow.document.close();
    }
  }

  function selectWorkspacePathForCompare(form, info) {
    if (!info || info.type !== "file") {
      setWorkspaceTreeStatus(t("只能选择文件进行比较。", "Only files can be selected for compare."), true);
      renderWorkspaceTree(form);
      return;
    }
    if (workspaceCompareBasePath === "" || workspaceCompareBasePath === info.path) {
      workspaceCompareBasePath = info.path;
      setWorkspaceTreeStatus(t("已选择进行比较。再右键另一个文件选择比较。", "Selected for compare. Right-click another file to compare."), false);
      renderWorkspaceTree(form);
      return;
    }
    const leftPath = workspaceCompareBasePath;
    const rightPath = info.path;
    workspaceCompareBasePath = "";
    const targetWindow = window.open("about:blank", "_blank", "noopener,noreferrer");
    if (targetWindow) {
      targetWindow.document.write(`<p style="font:14px system-ui;padding:16px">${t("正在加载比较…", "Loading compare...")}</p>`);
    }
    compareWorkspaceFiles(form, leftPath, rightPath, targetWindow).then(() => {
      setWorkspaceTreeStatus(t("已打开比较。", "Compare opened."), false);
      renderWorkspaceTree(form);
    }).catch((error) => {
      if (targetWindow && !targetWindow.closed) {
        targetWindow.close();
      }
      setWorkspaceTreeStatus(String(error?.message || t("比较失败。", "Compare failed.")), true);
      renderWorkspaceTree(form);
    });
  }

  function showWorkspaceTimeline(form, info) {
    if (!info) {
      return;
    }
    const modified = info.modifiedAt ? new Date(info.modifiedAt) : null;
    const display = modified && Number.isFinite(modified.getTime()) ? modified.toLocaleString() : t("暂无时间信息", "No timestamp available");
    setWorkspaceTreeStatus(`${t("时间线", "Timeline")}: ${info.path} · ${t("最后修改", "Last modified")} ${display}`, false);
    renderWorkspaceTree(form);
  }

  function setWorkspaceClipboard(form, info, mode) {
    if (!info) {
      return;
    }
    workspacePathClipboard = { mode: mode === "cut" ? "cut" : "copy", path: info.path, type: info.type, name: info.name || workspaceBaseName(info.path) };
    setWorkspaceTreeStatus(mode === "cut" ? t("已剪切，选择目标目录后粘贴。", "Cut. Choose a target folder and paste.") : t("已复制，选择目标目录后粘贴。", "Copied. Choose a target folder and paste."), false);
    renderWorkspaceTree(form);
  }

  function nextWorkspaceCopyPath(parentPath, name) {
    const parent = normalizeWorkspaceCreatePath(parentPath);
    const original = String(name || "copy").trim() || "copy";
    const dotIndex = original.lastIndexOf(".");
    const hasExt = dotIndex > 0;
    const stem = hasExt ? original.slice(0, dotIndex) : original;
    const ext = hasExt ? original.slice(dotIndex) : "";
    for (let index = 0; index < 100; index += 1) {
      const candidateName = index === 0 ? original : `${stem} copy${index === 1 ? "" : ` ${index}`}${ext}`;
      if (!workspaceDirectoryHasEntry(parent, candidateName)) {
        return joinWorkspaceChildPath(parent, candidateName);
      }
    }
    return joinWorkspaceChildPath(parent, `${stem} copy ${Date.now()}${ext}`);
  }

  function forgetDeletedWorkspacePath(form, deletedPath) {
    const targetPath = normalizeWorkspaceCreatePath(deletedPath);
    workspaceOpenFilePaths = workspaceOpenFilePaths.filter((value) => value !== targetPath && !value.startsWith(`${targetPath}/`));
    if (workspaceActiveFilePath === targetPath || workspaceActiveFilePath.startsWith(`${targetPath}/`)) {
      workspaceActiveFilePath = workspaceOpenFilePaths[0] || "";
      workspacePendingRestoreFilePath = workspaceActiveFilePath;
      workspaceEditorKind = "text";
      workspaceEditorMediaType = "";
      workspaceEditorPreviewURL = "";
      workspaceEditorContent = "";
      workspaceEditorSavedContent = "";
      workspaceEditorSize = 0;
      workspaceEditorBusy = "";
      workspaceEditorMarkdownMode = "preview";
      setWorkspaceEditorStatus("", false);
    }
    Array.from(workspaceOpenPaths).forEach((value) => {
      if (value === targetPath || value.startsWith(`${targetPath}/`)) {
        workspaceOpenPaths.delete(value);
      }
    });
    workspaceTreeCache.delete(targetPath);
    writeWorkspaceOpenState(form);
  }

  async function pasteWorkspaceClipboard(form, targetInfo) {
    if (!workspacePathClipboard) {
      return;
    }
    const targetDirectory = targetInfo?.type === "directory" ? targetInfo.path : workspaceParentPath(targetInfo?.path || "");
    const source = workspacePathClipboard;
    const endpoint = source.mode === "cut" ? workspaceRenameURL(form) : workspaceCopyURL(form);
    if (!workspaceMutationReady(form, endpoint)) {
      return;
    }
    const targetPath = nextWorkspaceCopyPath(targetDirectory, source.name || workspaceBaseName(source.path));
    const url = new URL(endpoint, window.location.href);
    url.searchParams.set("session", readClientSessionID(form));
    setWorkspaceTreeStatus(source.mode === "cut" ? t("正在移动…", "Moving...") : t("正在复制…", "Copying..."), false);
    renderWorkspaceTree(form);
    try {
      const parsed = await fetchWorkspaceJSON(url.toString(), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: source.mode === "cut"
          ? JSON.stringify({ path: source.path, new_path: targetPath })
          : JSON.stringify({ path: source.path, new_path: targetPath }),
      });
      const nextPath = normalizeWorkspaceCreatePath(parsed.path || targetPath);
      if (source.mode === "cut") {
        applyWorkspaceRenameState(form, source.path, nextPath);
        workspacePathClipboard = null;
      }
      await refreshWorkspaceTreeForPath(form, nextPath, source.type === "directory");
      setWorkspaceTreeStatus(String(parsed.notice || (source.mode === "cut" ? t("已移动。", "Moved.") : t("已复制。", "Copied."))), false);
      renderWorkspaceTree(form);
    } catch (error) {
      setWorkspaceTreeStatus(String(error?.message || t("粘贴失败。", "Paste failed.")), true);
      renderWorkspaceTree(form);
    }
  }

  async function deleteWorkspacePath(form, info) {
    const endpoint = workspaceDeleteURL(form);
    if (!info || !workspaceMutationReady(form, endpoint)) {
      return;
    }
    const confirmed = window.confirm(t(`永久删除“${info.name || info.path}”？`, `Permanently delete "${info.name || info.path}"?`));
    if (!confirmed) {
      return;
    }
    const url = new URL(endpoint, window.location.href);
    url.searchParams.set("session", readClientSessionID(form));
    setWorkspaceTreeStatus(t("正在删除…", "Deleting..."), false);
    renderWorkspaceTree(form);
    try {
      const parsed = await fetchWorkspaceJSON(url.toString(), {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: info.path }),
      });
      const deletedPath = normalizeWorkspaceCreatePath(parsed.path || info.path);
      forgetDeletedWorkspacePath(form, deletedPath);
      await refreshWorkspaceTreeForPath(form, workspaceParentPath(deletedPath), false);
      setWorkspaceTreeStatus(String(parsed.notice || t("已永久删除。", "Deleted permanently.")), false);
      renderWorkspaceTree(form);
      renderWorkspaceEditor(form);
    } catch (error) {
      setWorkspaceTreeStatus(String(error?.message || t("删除失败。", "Delete failed.")), true);
      renderWorkspaceTree(form);
    }
  }

  function startWorkspaceInlineCreate(form, kind, parentPath = defaultWorkspaceCreateParent()) {
    const normalizedKind = kind === "directory" ? "directory" : "file";
    const endpoint = normalizedKind === "directory" ? workspaceDirectoryURL(form) : workspaceFileURL(form);
    if (!workspaceMutationReady(form, endpoint)) {
      return;
    }
    const targetParent = normalizeWorkspaceCreatePath(parentPath);
    workspaceInlineEdit = {
      mode: "create",
      kind: normalizedKind,
      parentPath: targetParent,
      value: "",
      busy: false,
    };
    workspaceOpenPaths.add(targetParent);
    writeWorkspaceOpenState(form);
    setWorkspaceTreeStatus("", false);
    renderWorkspaceTree(form);
    if (targetParent !== "" && !workspaceTreeCache.has(targetParent) && !workspaceLoadingPaths.has(targetParent)) {
      void loadWorkspaceTree(form, targetParent, false);
    }
  }

  function startWorkspaceInlineRename(form, path, kind, name) {
    const endpoint = workspaceRenameURL(form);
    if (!workspaceMutationReady(form, endpoint)) {
      return;
    }
    const originalPath = normalizeWorkspaceCreatePath(path);
    if (originalPath === "") {
      return;
    }
    workspaceInlineEdit = {
      mode: "rename",
      kind: kind === "directory" ? "directory" : "file",
      parentPath: workspaceParentPath(originalPath),
      originalPath,
      value: String(name || workspaceBaseName(originalPath) || "").trim(),
      busy: false,
    };
    expandWorkspaceAncestors(originalPath);
    setWorkspaceTreeStatus("", false);
    renderWorkspaceTree(form);
  }

  function cancelWorkspaceInlineEdit(form = currentForm()) {
    if (!workspaceInlineEdit) {
      return;
    }
    workspaceInlineEdit = null;
    setWorkspaceTreeStatus("", false);
    renderWorkspaceTree(form);
  }

  function rewriteWorkspacePathPrefix(value, oldPath, newPath) {
    const current = normalizeWorkspacePath(value);
    const oldValue = normalizeWorkspaceCreatePath(oldPath);
    const newValue = normalizeWorkspaceCreatePath(newPath);
    if (current === "" || oldValue === "" || newValue === "") {
      return current;
    }
    if (current === oldValue) {
      return newValue;
    }
    if (current.startsWith(`${oldValue}/`)) {
      return `${newValue}${current.slice(oldValue.length)}`;
    }
    return current;
  }

  function applyWorkspaceRenameState(form, oldPath, newPath) {
    const oldValue = normalizeWorkspaceCreatePath(oldPath);
    const newValue = normalizeWorkspaceCreatePath(newPath);
    if (oldValue === "" || newValue === "") {
      return;
    }
    workspaceOpenFilePaths = normalizeWorkspacePathList(workspaceOpenFilePaths.map((value) => rewriteWorkspacePathPrefix(value, oldValue, newValue)));
    workspaceActiveFilePath = rewriteWorkspacePathPrefix(workspaceActiveFilePath, oldValue, newValue);
    workspacePendingRestoreFilePath = rewriteWorkspacePathPrefix(workspacePendingRestoreFilePath, oldValue, newValue);
    workspaceOpenPaths = new Set(Array.from(workspaceOpenPaths).map((value) => rewriteWorkspacePathPrefix(value, oldValue, newValue)));
    workspaceTreeCache.delete(oldValue);
    workspaceTreeCache.delete(newValue);
    writeWorkspaceOpenState(form);
  }

  async function refreshWorkspaceTreeForRename(form, oldPath, newPath, kind) {
    workspaceTreeCache.delete(workspaceParentPath(oldPath));
    workspaceTreeCache.delete(workspaceParentPath(newPath));
    workspaceTreeCache.delete(normalizeWorkspaceCreatePath(oldPath));
    await refreshWorkspaceTreeForPath(form, newPath, kind === "directory");
  }

  async function submitWorkspaceInlineEdit(form, input) {
    if (!(form instanceof HTMLFormElement) || !workspaceInlineEdit || workspaceInlineEdit.busy) {
      return;
    }
    const edit = workspaceInlineEdit;
    const normalized = normalizeWorkspaceEntryName(input instanceof HTMLInputElement ? input.value : edit.value);
    if (normalized.error) {
      edit.value = input instanceof HTMLInputElement ? input.value : edit.value;
      setWorkspaceTreeStatus(normalized.error, true);
      renderWorkspaceTree(form);
      return;
    }
    const targetPath = joinWorkspaceChildPath(edit.parentPath, normalized.name);
    if (edit.mode === "rename" && normalizeWorkspaceCreatePath(edit.originalPath) === targetPath) {
      cancelWorkspaceInlineEdit(form);
      return;
    }
    const requestKey = currentWorkspaceKey(form);
    edit.value = normalized.name;
    edit.busy = true;
    setWorkspaceTreeStatus(edit.mode === "rename" ? t("正在重命名…", "Renaming...") : (edit.kind === "directory" ? t("正在新建目录…", "Creating folder...") : t("正在新建文件…", "Creating file...")), false);
    renderWorkspaceTree(form);
    try {
      if (edit.mode === "rename") {
        const endpoint = workspaceRenameURL(form);
        const url = new URL(endpoint, window.location.href);
        url.searchParams.set("session", readClientSessionID(form));
        const parsed = await fetchWorkspaceJSON(url.toString(), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: edit.originalPath, new_path: targetPath }),
        });
        if (requestKey !== workspaceSessionKey || requestKey !== currentWorkspaceKey(form)) {
          return;
        }
        const oldPath = normalizeWorkspaceCreatePath(parsed.oldPath || edit.originalPath);
        const renamedPath = normalizeWorkspaceCreatePath(parsed.path || targetPath);
        const nextActivePath = rewriteWorkspacePathPrefix(workspaceActiveFilePath, oldPath, renamedPath);
        const reloadActiveImage = workspaceActiveFilePath !== "" && nextActivePath !== workspaceActiveFilePath && workspaceEditorKind === "image";
        workspaceInlineEdit = null;
        applyWorkspaceRenameState(form, oldPath, renamedPath);
        await refreshWorkspaceTreeForRename(form, oldPath, renamedPath, edit.kind);
        setWorkspaceTreeStatus(String(parsed.notice || t("已重命名。", "Renamed.")), false);
        if (reloadActiveImage) {
          await openWorkspaceFile(form, nextActivePath, true);
        } else {
          renderWorkspaceTree(form);
          renderWorkspaceEditor(form);
        }
        return;
      }

      const endpoint = edit.kind === "directory" ? workspaceDirectoryURL(form) : workspaceFileURL(form);
      const url = new URL(endpoint, window.location.href);
      url.searchParams.set("session", readClientSessionID(form));
      const parsed = await fetchWorkspaceJSON(url.toString(), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: edit.kind === "directory"
          ? JSON.stringify({ path: targetPath, mkdir_p: true })
          : JSON.stringify({ path: targetPath, content: "", create: true, mkdir_p: true }),
      });
      if (requestKey !== workspaceSessionKey || requestKey !== currentWorkspaceKey(form)) {
        return;
      }
      const createdPath = normalizeWorkspaceCreatePath(parsed.path || targetPath);
      workspaceInlineEdit = null;
      await refreshWorkspaceTreeForPath(form, createdPath, edit.kind === "directory");
      setWorkspaceTreeStatus(String(parsed.notice || (edit.kind === "directory" ? t("目录已创建。", "Directory created.") : t("文件已创建。", "File created."))), false);
      if (edit.kind === "file") {
        await openWorkspaceFile(form, createdPath, false);
      } else {
        renderWorkspaceTree(form);
      }
    } catch (error) {
      if (requestKey === workspaceSessionKey && workspaceInlineEdit === edit) {
        edit.busy = false;
        setWorkspaceTreeStatus(String(error?.message || (edit.mode === "rename" ? t("重命名失败。", "Failed to rename.") : t("新建失败。", "Failed to create."))), true);
        renderWorkspaceTree(form);
      }
    }
  }

  function hideWorkspaceContextMenu() {
    if (workspaceContextMenu instanceof HTMLElement) {
      workspaceContextMenu.remove();
    }
    workspaceContextMenu = null;
  }

  function addWorkspaceContextMenuSeparator(menu) {
    const separator = document.createElement("div");
    separator.className = "chat-workspace-context-separator";
    separator.setAttribute("role", "separator");
    menu.appendChild(separator);
  }

  function addWorkspaceContextMenuButton(menu, label, onClick, options = {}) {
    const button = document.createElement("button");
    button.type = "button";
    button.setAttribute("role", "menuitem");
    const text = document.createElement("span");
    text.className = "chat-workspace-context-label";
    text.textContent = label;
    button.appendChild(text);
    const shortcut = String(options.shortcut || "").trim();
    if (shortcut !== "") {
      const shortcutNode = document.createElement("span");
      shortcutNode.className = "chat-workspace-context-shortcut";
      shortcutNode.textContent = shortcut;
      button.appendChild(shortcutNode);
    }
    if (options.danger) {
      button.classList.add("is-danger");
    }
    if (options.disabled) {
      button.disabled = true;
      button.setAttribute("aria-disabled", "true");
    }
    button.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      if (button.disabled) {
        return;
      }
      hideWorkspaceContextMenu();
      onClick();
    });
    menu.appendChild(button);
  }

  function showWorkspaceContextMenu(form, row, event) {
    if (!(form instanceof HTMLFormElement) || !(row instanceof HTMLElement)) {
      return;
    }
    const info = workspaceRowInfo(row);
    if (!info) {
      return;
    }
    hideWorkspaceContextMenu();
    const menu = document.createElement("div");
    menu.className = "chat-workspace-context-menu";
    menu.setAttribute("role", "menu");
    const parentPath = info.type === "directory" ? info.path : workspaceParentPath(info.path);
    if (info.type === "directory") {
      addWorkspaceContextMenuButton(menu, t("新建文件", "New file"), () => startWorkspaceInlineCreate(form, "file", parentPath));
      addWorkspaceContextMenuButton(menu, t("新建目录", "New folder"), () => startWorkspaceInlineCreate(form, "directory", parentPath));
      if (workspacePathClipboard) {
        addWorkspaceContextMenuButton(menu, t("粘贴", "Paste"), () => void pasteWorkspaceClipboard(form, info));
      }
      addWorkspaceContextMenuSeparator(menu);
    }
    addWorkspaceContextMenuButton(menu, t("在侧边打开", "Open to the Side"), () => {
      if (info.type === "file") {
        void openWorkspaceFile(form, info.path, false);
      } else {
        workspaceOpenPaths.add(info.path);
        writeWorkspaceOpenState(form);
        void loadWorkspaceTree(form, info.path, false);
        renderWorkspaceTree(form);
      }
    }, { shortcut: "Ctrl+Enter" });
    addWorkspaceContextMenuButton(menu, t("打开方式...", "Open With..."), () => void openWorkspaceExternal(form, info), { disabled: info.type !== "file" });
    addWorkspaceContextMenuButton(menu, t("在文件资源管理器中显示", "Reveal in File Explorer"), () => revealWorkspacePath(form, info), { shortcut: "Shift+Alt+R" });
    addWorkspaceContextMenuButton(menu, t("在集成终端中打开", "Open in Integrated Terminal"), () => openWorkspacePathInTerminal(form, info));
    addWorkspaceContextMenuSeparator(menu);
    addWorkspaceContextMenuButton(menu, t("分享", "Share"), () => shareWorkspacePath(form, info), { shortcut: ">" });
    addWorkspaceContextMenuButton(menu, workspaceCompareBasePath && workspaceCompareBasePath !== info.path ? t("与已选项比较", "Compare with Selected") : t("选择以进行比较", "Select for Compare"), () => selectWorkspacePathForCompare(form, info), { disabled: info.type !== "file" });
    addWorkspaceContextMenuButton(menu, t("打开时间线", "Open Timeline"), () => showWorkspaceTimeline(form, info));
    addWorkspaceContextMenuButton(menu, t("将文件添加到聊天", "Add File to Chat"), () => void addWorkspacePathToChat(form, info));
    addWorkspaceContextMenuSeparator(menu);
    addWorkspaceContextMenuButton(menu, t("剪切", "Cut"), () => setWorkspaceClipboard(form, info, "cut"), { shortcut: "Ctrl+X" });
    addWorkspaceContextMenuButton(menu, t("复制", "Copy"), () => setWorkspaceClipboard(form, info, "copy"), { shortcut: "Ctrl+C" });
    if (workspacePathClipboard && info.type !== "directory") {
      addWorkspaceContextMenuButton(menu, t("粘贴", "Paste"), () => void pasteWorkspaceClipboard(form, info));
    }
    addWorkspaceContextMenuSeparator(menu);
    addWorkspaceContextMenuButton(menu, t("下载...", "Download..."), () => void downloadWorkspaceFile(form, info), { disabled: info.type !== "file" });
    addWorkspaceContextMenuButton(menu, t("复制路径", "Copy Path"), () => void copyTextToClipboard(workspaceAbsolutePath(info.path), t("已复制路径。", "Path copied.")), { shortcut: "Shift+Alt+C" });
    addWorkspaceContextMenuButton(menu, t("复制相对路径", "Copy Relative Path"), () => void copyTextToClipboard(info.path, t("已复制相对路径。", "Relative path copied.")), { shortcut: "Ctrl+K Ctrl+Shift+C" });
    addWorkspaceContextMenuSeparator(menu);
    addWorkspaceContextMenuButton(menu, t("重命名...", "Rename..."), () => startWorkspaceInlineRename(form, info.path, info.type, info.name), { shortcut: "F2" });
    addWorkspaceContextMenuButton(menu, t("永久删除", "Delete Permanently"), () => void deleteWorkspacePath(form, info), { shortcut: "Del", danger: true });
    document.body.appendChild(menu);
    const margin = 8;
    const rect = menu.getBoundingClientRect();
    const left = Math.max(margin, Math.min(Number(event.clientX || 0), window.innerWidth - rect.width - margin));
    const top = Math.max(margin, Math.min(Number(event.clientY || 0), window.innerHeight - rect.height - margin));
    menu.style.left = `${left}px`;
    menu.style.top = `${top}px`;
    workspaceContextMenu = menu;
  }

  async function createWorkspaceFile(form) {
    startWorkspaceInlineCreate(form, "file");
  }

  async function createWorkspaceDirectory(form) {
    startWorkspaceInlineCreate(form, "directory");
  }

  async function refreshWorkspace(form) {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    writeWorkspaceOpenState(form);
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
    if (isCloudAgentEnvironment(currentSelectedEnvironment(form)) && readClientSessionID(form) !== "" && workspacePendingRestoreFilePath !== "") {
      const restorePath = workspacePendingRestoreFilePath;
      workspacePendingRestoreFilePath = "";
      void openWorkspaceFile(form, restorePath, true);
    }
  }

  document.addEventListener("click", (event) => {
    if (!(event.target instanceof Node) || !(workspaceContextMenu instanceof HTMLElement) || !workspaceContextMenu.contains(event.target)) {
      hideWorkspaceContextMenu();
    }
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
        const nextView = String(target.dataset.chatSidebarView || "files").trim() === "sessions" ? "sessions" : "files";
        const togglesActiveView = !layoutState.sidebarCollapsed && layoutState.sidebarView === nextView;
        if (togglesActiveView) {
          layoutState.sidebarCollapsed = true;
          applyLayout(form, true);
          syncWorkspaceSurface(form);
          return;
        }
        layoutState.sidebarCollapsed = false;
        applyLayout(form, false);
        applySidebarView(form, nextView, true);
        syncWorkspaceSurface(form);
        return;
      }
      case "refresh-workspace": {
        event.preventDefault();
        void refreshWorkspace(form);
        return;
      }
      case "create-workspace-file": {
        event.preventDefault();
        void createWorkspaceFile(form);
        return;
      }
      case "create-workspace-directory": {
        event.preventDefault();
        void createWorkspaceDirectory(form);
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
          writeWorkspaceOpenState(form);
          renderWorkspaceTree(form);
          return;
        }
        workspaceOpenPaths.add(path);
        writeWorkspaceOpenState(form);
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
      case "activate-workspace-tab": {
        event.preventDefault();
        void openWorkspaceFile(form, String(target.dataset.chatWorkspacePath || "").trim(), false);
        return;
      }
      case "close-workspace-tab": {
        event.preventDefault();
        const nextPath = forgetWorkspaceOpenFile(String(target.dataset.chatWorkspacePath || "").trim(), true);
        if (nextPath !== "") {
          void openWorkspaceFile(form, nextPath, true);
        } else {
          workspaceActiveFilePath = "";
          workspaceEditorKind = "text";
          workspaceEditorMediaType = "";
          workspaceEditorPreviewURL = "";
          workspaceEditorContent = "";
          workspaceEditorSavedContent = "";
          workspaceEditorSize = 0;
          workspaceEditorBusy = "";
          workspaceEditorMarkdownMode = "preview";
          setWorkspaceEditorStatus("", false);
          renderWorkspaceTree(form);
          renderWorkspaceEditor(form);
          applyLayout(form, true);
        }
        return;
      }
      case "save-workspace-file": {
        event.preventDefault();
        void saveWorkspaceFile(form);
        return;
      }
      case "set-editor-markdown-mode": {
        event.preventDefault();
        const mode = String(target.dataset.chatEditorMarkdownMode || "").trim() === "source" ? "source" : "preview";
        workspaceEditorMarkdownMode = mode;
        renderWorkspaceEditor(form);
        if (mode === "source") {
          const input = workspaceEditorInput(form);
          if (input instanceof HTMLTextAreaElement && !input.disabled) {
            input.focus();
          }
        }
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

  document.addEventListener("contextmenu", (event) => {
    const form = currentForm();
    const row = event.target instanceof Element ? event.target.closest(".chat-workspace-row[data-chat-workspace-path]") : null;
    if (!(form instanceof HTMLFormElement) || !(row instanceof HTMLElement) || !form.contains(row)) {
      return;
    }
    if (!isCloudAgentEnvironment(currentSelectedEnvironment(form)) || readClientSessionID(form) === "") {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    showWorkspaceContextMenu(form, row, event);
  });

  document.addEventListener("dragover", (event) => {
    const form = currentForm();
    const target = workspaceUploadTargetFromEvent(event, form);
    if (!(form instanceof HTMLFormElement) || !target || !workspaceDragContainsFiles(event)) {
      return;
    }
    if (!isCloudAgentEnvironment(currentSelectedEnvironment(form)) || readClientSessionID(form) === "" || workspaceUploadURL(form) === "") {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    if (event.dataTransfer) {
      event.dataTransfer.dropEffect = "copy";
    }
    setWorkspaceUploadTarget(form, target.highlightRowPath, target.root);
  });

  document.addEventListener("dragleave", (event) => {
    const form = currentForm();
    const surface = workspaceFilesPanel(form) || workspaceSection(form) || workspaceTreeHost(form);
    if (!(surface instanceof HTMLElement)) {
      return;
    }
    const related = event.relatedTarget;
    if (related instanceof Node && surface.contains(related)) {
      return;
    }
    clearWorkspaceUploadTarget(form);
  });

  document.addEventListener("drop", (event) => {
    const form = currentForm();
    const target = workspaceUploadTargetFromEvent(event, form);
    if (!(form instanceof HTMLFormElement) || !target || !workspaceDragContainsFiles(event)) {
      clearWorkspaceUploadTarget(form);
      return;
    }
    if (!isCloudAgentEnvironment(currentSelectedEnvironment(form)) || readClientSessionID(form) === "" || workspaceUploadURL(form) === "") {
      clearWorkspaceUploadTarget(form);
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    const files = Array.from(event.dataTransfer?.files || []);
    const directoryPath = target.path;
    clearWorkspaceUploadTarget(form);
    void uploadWorkspaceFilesToDirectory(form, directoryPath, files);
  });

  document.addEventListener("dragend", () => {
    clearWorkspaceUploadTarget();
  });

  document.addEventListener("input", (event) => {
    const target = event.target;
    if (target instanceof HTMLInputElement && target.matches("[data-chat-workspace-inline-input]")) {
      if (workspaceInlineEdit) {
        workspaceInlineEdit.value = target.value;
      }
      return;
    }
    const form = currentForm();
    const row = target instanceof Element ? target.closest(".chat-workspace-row[data-chat-workspace-path]") : null;
    if (form instanceof HTMLFormElement && row instanceof HTMLElement && form.contains(row) && !(target instanceof HTMLTextAreaElement || target instanceof HTMLInputElement || target instanceof HTMLSelectElement)) {
      const info = workspaceRowInfo(row);
      const key = String(event.key || "").toLowerCase();
      if (info && event.key === "F2" && !event.metaKey && !event.ctrlKey && !event.altKey) {
        event.preventDefault();
        startWorkspaceInlineRename(form, info.path, info.type, info.name);
        return;
      }
      if (info && event.key === "Delete" && !event.metaKey && !event.ctrlKey && !event.altKey) {
        event.preventDefault();
        void deleteWorkspacePath(form, info);
        return;
      }
      if (info && key === "enter" && (event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey) {
        event.preventDefault();
        if (info.type === "file") {
          void openWorkspaceFile(form, info.path, false);
        } else {
          workspaceOpenPaths.add(info.path);
          writeWorkspaceOpenState(form);
          void loadWorkspaceTree(form, info.path, false);
          renderWorkspaceTree(form);
        }
        return;
      }
      if (info && key === "c" && (event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey) {
        event.preventDefault();
        setWorkspaceClipboard(form, info, "copy");
        return;
      }
      if (info && key === "x" && (event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey) {
        event.preventDefault();
        setWorkspaceClipboard(form, info, "cut");
        return;
      }
      if (info && key === "c" && event.shiftKey && event.altKey && !event.metaKey && !event.ctrlKey) {
        event.preventDefault();
        void copyTextToClipboard(workspaceAbsolutePath(info.path), t("已复制路径。", "Path copied."));
        return;
      }
      if (info && key === "r" && event.shiftKey && event.altKey && !event.metaKey && !event.ctrlKey) {
        event.preventDefault();
        revealWorkspacePath(form, info);
        return;
      }
    }
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

  document.addEventListener("scroll", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLTextAreaElement) || !target.matches("[data-chat-editor-input]")) {
      return;
    }
    syncWorkspaceEditorHighlightScroll(target);
  }, true);

  document.addEventListener("keydown", (event) => {
    const target = event.target;
    if (event.key === "Escape" && workspaceContextMenu instanceof HTMLElement) {
      event.preventDefault();
      hideWorkspaceContextMenu();
      return;
    }
    if (target instanceof HTMLInputElement && target.matches("[data-chat-workspace-inline-input]")) {
      if (event.key === "Enter" && !event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey) {
        event.preventDefault();
        void submitWorkspaceInlineEdit(target.form instanceof HTMLFormElement ? target.form : currentForm(), target);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        cancelWorkspaceInlineEdit(target.form instanceof HTMLFormElement ? target.form : currentForm());
      }
      return;
    }
    if (!(target instanceof HTMLTextAreaElement) || !target.matches("[data-chat-editor-input]")) {
      return;
    }
    if (event.key.toLowerCase() === "s" && (event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey) {
      event.preventDefault();
      void saveWorkspaceFile(target.form instanceof HTMLFormElement ? target.form : currentForm());
      return;
    }
    if (event.key === "Tab" && !event.metaKey && !event.ctrlKey && !event.altKey) {
      event.preventDefault();
      if (event.shiftKey) {
        outdentWorkspaceEditorSelection(target);
      } else {
        indentWorkspaceEditorSelection(target);
      }
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