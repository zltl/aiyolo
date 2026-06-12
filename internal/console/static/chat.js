(() => {
  if (window.__aiyoloConsoleChatBound) {
    return;
  }
  window.__aiyoloConsoleChatBound = true;

  const sessionLimit = 24;
  let persistTimer = 0;

  const supportsStreaming = () => Boolean(window.fetch && window.FormData && window.TextDecoder && window.ReadableStream);

  const currentRoot = () => document.getElementById("chat-content");
  const currentForm = () => currentRoot()?.querySelector(".chat-shell[data-chat-stream-url]") || null;
  const t = (zh, _en) => zh;
  const emitChatState = (detail = {}) => {
    if (typeof window === "undefined" || typeof window.dispatchEvent !== "function" || typeof window.CustomEvent !== "function") {
      return;
    }
    const form = currentForm();
    window.dispatchEvent(new CustomEvent("aiyolo:chat-state", {
      detail: {
        sessionId: readClientSessionID(form),
        environment: currentSelectedEnvironment(form),
        ...detail,
      },
    }));
  };
  const syncLucideIcons = () => {
    if (!window.lucide || typeof window.lucide.createIcons !== "function") {
      return;
    }
    window.lucide.createIcons();
  };
  const pickerViewportMargin = 8;
  const pickerViewportGap = 6;
  const isChatPicker = (node) => node instanceof HTMLDetailsElement && node.classList.contains("chat-model-picker");
  let pickerIDCounter = 0;
  const pickerMenuPortals = new WeakMap();
  const ensurePickerID = (picker) => {
    if (!isChatPicker(picker)) {
      return "";
    }
    if (!picker.dataset.chatPickerId) {
      pickerIDCounter += 1;
      picker.dataset.chatPickerId = `chat-picker-${pickerIDCounter}`;
    }
    return picker.dataset.chatPickerId;
  };
  const pickerMenu = (picker) => {
    if (!isChatPicker(picker)) {
      return null;
    }
    const localMenu = picker.querySelector(".chat-model-picker-menu");
    if (localMenu instanceof HTMLElement) {
      return localMenu;
    }
    const pickerID = picker.dataset.chatPickerId || "";
    if (pickerID === "") {
      return null;
    }
    return document.body.querySelector(`.chat-model-picker-menu[data-chat-picker-owner="${pickerID}"]`);
  };
  const pickerFromMenu = (menu) => {
    if (!(menu instanceof HTMLElement)) {
      return null;
    }
    const pickerID = menu.dataset.chatPickerOwner || "";
    if (pickerID === "") {
      return null;
    }
    return Array.from(currentForm()?.querySelectorAll(".chat-model-picker") || []).find((picker) => picker instanceof HTMLDetailsElement && picker.dataset.chatPickerId === pickerID) || null;
  };
  const pickerFromTarget = (target) => {
    if (!(target instanceof Element)) {
      return null;
    }
    const localPicker = target.closest(".chat-model-picker");
    if (localPicker instanceof HTMLDetailsElement) {
      return localPicker;
    }
    return pickerFromMenu(target.closest(".chat-model-picker-menu"));
  };
  const pickerFormControls = (form, selector) => {
    const controls = [];
    const seen = new Set();
    const add = (node) => {
      if (!(node instanceof HTMLElement) || seen.has(node)) {
        return;
      }
      seen.add(node);
      controls.push(node);
    };
    form?.querySelectorAll(selector).forEach(add);
    document.body.querySelectorAll(`.chat-model-picker-menu[data-chat-picker-owner] ${selector}`).forEach((node) => {
      const picker = pickerFromMenu(node.closest(".chat-model-picker-menu"));
      if (!form || !picker || !form.contains(picker)) {
        return;
      }
      add(node);
    });
    return controls;
  };
  const portalPickerMenu = (picker, menu) => {
    if (!isChatPicker(picker) || !(menu instanceof HTMLElement) || menu.parentElement === document.body) {
      return;
    }
    const pickerID = ensurePickerID(picker);
    const form = picker.closest("form");
    if (form instanceof HTMLFormElement) {
      if (!form.id) {
        form.id = "chat-form";
      }
      menu.querySelectorAll("input, select, textarea, button").forEach((control) => {
        if (control instanceof HTMLElement) {
          control.setAttribute("form", form.id);
        }
      });
    }
    const placeholder = document.createComment("chat-picker-menu");
    pickerMenuPortals.set(picker, placeholder);
    menu.dataset.chatPickerOwner = pickerID;
    menu.after(placeholder);
    document.body.appendChild(menu);
  };
  const restorePickerMenu = (picker) => {
    if (!isChatPicker(picker)) {
      return;
    }
    const menu = pickerMenu(picker);
    const placeholder = pickerMenuPortals.get(picker);
    if (!(menu instanceof HTMLElement) || !placeholder?.parentNode) {
      return;
    }
    placeholder.parentNode.insertBefore(menu, placeholder);
    placeholder.remove();
    pickerMenuPortals.delete(picker);
  };
  const setPickerExpanded = (picker) => {
    if (!isChatPicker(picker)) {
      return;
    }
    const summary = picker.querySelector("summary");
    if (summary instanceof HTMLElement) {
      summary.setAttribute("aria-expanded", picker.open ? "true" : "false");
    }
  };
  const clearPickerMenuPlacement = (picker) => {
    if (!isChatPicker(picker)) {
      return;
    }
    const menu = pickerMenu(picker);
    picker.classList.remove("is-floating-menu", "is-menu-above", "is-menu-below");
    setPickerExpanded(picker);
    if (!(menu instanceof HTMLElement)) {
      return;
    }
    menu.classList.remove("is-floating-menu", "is-menu-above", "is-menu-below");
    menu.style.removeProperty("top");
    menu.style.removeProperty("left");
    menu.style.removeProperty("width");
    menu.style.removeProperty("height");
    menu.style.removeProperty("max-height");
    menu.style.removeProperty("visibility");
    restorePickerMenu(picker);
  };
  const syncPickerMenuPlacement = (picker) => {
    if (!isChatPicker(picker) || !picker.open) {
      clearPickerMenuPlacement(picker);
      return;
    }
    const summary = picker.querySelector("summary");
    const menu = pickerMenu(picker);
    if (!(summary instanceof HTMLElement) || !(menu instanceof HTMLElement)) {
      return;
    }

    portalPickerMenu(picker, menu);
    picker.classList.add("is-floating-menu");
    menu.classList.add("is-floating-menu");
    setPickerExpanded(picker);
    menu.style.visibility = "hidden";
    menu.style.removeProperty("top");
    menu.style.removeProperty("left");
    menu.style.removeProperty("width");
    menu.style.removeProperty("height");
    menu.style.removeProperty("max-height");

    const triggerRect = summary.getBoundingClientRect();
    const visualViewport = window.visualViewport || null;
    const viewportWidth = Math.max(1, Math.floor(visualViewport?.width || window.innerWidth || document.documentElement.clientWidth || 0));
    const viewportHeight = Math.max(1, Math.floor(visualViewport?.height || window.innerHeight || document.documentElement.clientHeight || 0));
    const maxWidth = Math.max(180, viewportWidth - pickerViewportMargin * 2);
    const measuredWidth = Math.ceil(menu.getBoundingClientRect().width || menu.offsetWidth || 0);
    const triggerWidth = Math.ceil(triggerRect.width || 0);
    const menuWidth = Math.min(maxWidth, Math.max(triggerWidth, measuredWidth, 180));

    menu.style.width = `${menuWidth}px`;
    menu.style.maxHeight = "none";

    const naturalHeight = Math.ceil(menu.scrollHeight || menu.getBoundingClientRect().height || 0);
    const viewportCap = Math.max(48, viewportHeight - pickerViewportMargin * 2);
    const availableBelow = Math.max(0, viewportHeight - triggerRect.bottom - pickerViewportGap - pickerViewportMargin);
    const availableAbove = Math.max(0, triggerRect.top - pickerViewportGap - pickerViewportMargin);
    const neededHeight = Math.min(naturalHeight || viewportCap, viewportCap);
    const fitsBelow = availableBelow >= neededHeight;
    const fitsAbove = availableAbove >= neededHeight;
    const openBelow = fitsBelow || (!fitsAbove && availableBelow >= availableAbove);
    const availableHeight = Math.min(viewportCap, Math.max(48, openBelow ? availableBelow : availableAbove));
    const menuHeight = Math.min(neededHeight, availableHeight);

    const maxLeft = viewportWidth - pickerViewportMargin - menuWidth;
    const alignedLeft = triggerRect.right - menuWidth;
    const left = Math.round(Math.min(Math.max(pickerViewportMargin, alignedLeft), Math.max(pickerViewportMargin, maxLeft)));
    const rawTop = openBelow
      ? triggerRect.bottom + pickerViewportGap
      : triggerRect.top - pickerViewportGap - menuHeight;
    const maxTop = viewportHeight - pickerViewportMargin - menuHeight;
    const top = Math.round(Math.min(Math.max(pickerViewportMargin, rawTop), Math.max(pickerViewportMargin, maxTop)));

    picker.classList.toggle("is-menu-below", openBelow);
    picker.classList.toggle("is-menu-above", !openBelow);
  menu.classList.toggle("is-menu-below", openBelow);
  menu.classList.toggle("is-menu-above", !openBelow);
    menu.style.left = `${left}px`;
    menu.style.top = `${top}px`;
    menu.style.height = `${Math.max(48, Math.floor(menuHeight))}px`;
    menu.style.maxHeight = `${Math.max(48, Math.floor(menuHeight))}px`;
    menu.style.visibility = "";
  };
  const closeOtherPickers = (activePicker) => {
    currentForm()?.querySelectorAll(".chat-model-picker[open]").forEach((picker) => {
      if (picker !== activePicker && picker instanceof HTMLDetailsElement) {
        picker.open = false;
      }
    });
  };
  const syncOpenPickerMenus = () => {
    currentForm()?.querySelectorAll(".chat-model-picker[open]").forEach((picker) => {
      if (picker instanceof HTMLDetailsElement) {
        syncPickerMenuPlacement(picker);
      }
    });
  };
  const sidebarPreferenceKey = "aiyolo.console.chat.sidebarCollapsed.v2";
  const ownProperty = Object.prototype.hasOwnProperty;
  const sessionStreamStates = new Map();

  const normalizeSessionStreamID = (sessionID) => String(sessionID || "").trim();

  const getSessionStreamState = (sessionID) => {
    const id = normalizeSessionStreamID(sessionID);
    return id === "" ? null : sessionStreamStates.get(id) || null;
  };

  const ensureSessionStreamState = (sessionID) => {
    const id = normalizeSessionStreamID(sessionID);
    if (id === "") {
      return null;
    }
    let state = sessionStreamStates.get(id);
    if (!state) {
      state = {
        sessionID: id,
        controller: null,
        stopRequested: false,
        preemptTurn: null,
        queuedTurns: [],
        active: false,
        assistantHistoryMessage: null,
        baseMessages: [],
        userHistoryMessage: null,
        promptValue: "",
        draftValue: "",
        attachments: [],
        ui: null,
        streamOpened: false,
        sawStreamEvent: false,
        streamCompleted: false,
        streamErrored: false,
        streamInterrupted: false,
        reconnectAttempt: 0,
        allowReconnect: false,
        resumeURL: "",
        publicName: "",
        environment: "",
        preserveLocalTranscript: false,
        replaced: false,
      };
      sessionStreamStates.set(id, state);
    }
    return state;
  };

  const isSessionStreamActive = (sessionID) => {
    const state = getSessionStreamState(sessionID);
    return Boolean(state?.active && !state.streamCompleted && !state.streamErrored && !state.streamInterrupted);
  };

  const isSessionVisible = (sessionID) => readClientSessionID(currentForm()) === normalizeSessionStreamID(sessionID);

  const syncFormStreamingUI = (form = currentForm()) => {
    if (!form) {
      return;
    }
    const streaming = isSessionStreamActive(readClientSessionID(form));
    form.dataset.streaming = streaming ? "true" : "false";
    form.classList.toggle("is-streaming", streaming);
    updateComposerControls(form);
  };

  const detachSessionStreamUI = (sessionID) => {
    const state = getSessionStreamState(sessionID);
    if (!state) {
      return;
    }
    state.ui = null;
  };

  const streamAssistantUI = (state) => state?.ui?.assistantMessage || null;

  const streamThreadUI = (state) => state?.ui?.thread || null;

  const updateStreamAssistantUI = (state, updateFn) => {
    const assistantMessage = streamAssistantUI(state);
    if (!assistantMessage) {
      return;
    }
    updateFn(assistantMessage);
    const thread = streamThreadUI(state);
    if (thread) {
      scrollThread(thread);
    }
  };

  const persistSessionStreamProgress = (state, metadata = {}) => {
    if (!state) {
      return;
    }
    const form = currentForm();
    if (!form) {
      return;
    }
    const routes = routeMap(form);
    let store = normalizeStore(loadStore(), form, routes);
    const existing = store.sessions.find((item) => item.id === state.sessionID);
    if (!existing) {
      return;
    }
    const history = [...state.baseMessages];
    if (state.userHistoryMessage) {
      history.push(normalizeMessage(state.userHistoryMessage));
    }
    if (state.assistantHistoryMessage) {
      history.push(normalizeMessage(state.assistantHistoryMessage));
    }
    const nextSession = normalizeSession({
      ...existing,
      ...metadata,
      messages: history.filter(Boolean),
      status: String(metadata.status || existing.status || "streaming").trim(),
      updatedAt: nowISO(),
    }, form, routes, existing);
    store = upsertSession(store, nextSession);
    saveStore(store, true);
    const root = currentRoot();
    if (root) {
      renderSessionList(root, store);
    }
    if (isSessionVisible(state.sessionID)) {
      writeHiddenJSON(form, "chat_history_json", nextSession.messages);
    }
    void saveSessionToServer(nextSession);
  };

  const updateStreamSessionMetadata = (state, updates = {}) => {
    if (isSessionVisible(state.sessionID)) {
      updateCurrentSessionMetadata(updates);
      return;
    }
    persistSessionStreamProgress(state, updates);
  };

  const mountStreamUI = (state, form) => {
    const root = currentRoot();
    const thread = form?.querySelector("[data-chat-scroll]");
    if (!root || !thread || !state?.assistantHistoryMessage) {
      return;
    }
    const routes = routeMap(form);
    const route = routes.get(state.publicName || currentSelectedModel(form)) || null;
    const history = [...state.baseMessages];
    if (state.userHistoryMessage) {
      history.push(normalizeMessage(state.userHistoryMessage));
    }
    renderThread(root, history.filter(Boolean), route);
    const assistantNode = buildMessageNode(
      "assistant",
      roleLabel("assistant"),
      state.assistantHistoryMessage.content || "",
      form.dataset.chatStreamingLabel || "Streaming",
      [],
      state.assistantHistoryMessage.reasoning || "",
    );
    thread.appendChild(assistantNode.article);
    scrollThread(thread);
    state.ui = { form, thread, assistantMessage: assistantNode };
    syncFormStreamingUI(form);
  };

  const stopSessionStream = (sessionID) => {
    const state = getSessionStreamState(sessionID);
    if (!state?.controller) {
      sessionStreamStates.delete(normalizeSessionStreamID(sessionID));
      return false;
    }
    state.stopRequested = true;
    state.controller.abort();
    return true;
  };

  const cloneValue = (value) => {
    if (value === undefined) {
      return undefined;
    }
    if (typeof structuredClone === "function") {
      return structuredClone(value);
    }
    return JSON.parse(JSON.stringify(value));
  };

  const safeParseJSON = (raw, fallback) => {
    if (typeof raw !== "string" || raw.trim() === "") {
      return cloneValue(fallback);
    }
    try {
      const parsed = JSON.parse(raw);
      return parsed == null ? cloneValue(fallback) : parsed;
    } catch (_error) {
      return cloneValue(fallback);
    }
  };

  const readSidebarPreference = () => {
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
  };

  const writeSidebarPreference = (collapsed) => {
    if (typeof window === "undefined" || !window.localStorage) {
      return;
    }
    try {
      window.localStorage.setItem(sidebarPreferenceKey, collapsed ? "true" : "false");
    } catch (_error) {
      // Ignore storage failures and keep the in-memory layout state.
    }
  };

  const makeID = (prefix) => `${prefix}_${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`;
  const nowISO = () => new Date().toISOString();
  const markdownBreakToken = "@@AIOYOLO_MD_BR@@";
  const truncateText = (value, limit = 56) => {
    const compact = String(value || "").trim().replace(/\s+/g, " ");
    if (compact.length <= limit) {
      return compact;
    }
    return `${compact.slice(0, limit - 1)}…`;
  };

  const normalizeMarkdownSource = (value) => String(value || "")
    .replace(/\r\n?/g, "\n")
    .replace(/<br\s*\/?>/gi, markdownBreakToken);

  const escapeHTML = (value) => String(value || "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/\"/g, "&quot;")
    .replace(/'/g, "&#39;");

  const sanitizeMarkdownURL = (value) => {
    const trimmed = String(value || "").trim();
    if (trimmed === "") {
      return "";
    }
    if (/^data:image\/(png|jpe?g|gif|webp);base64,[a-z0-9+/=]+$/i.test(trimmed)) {
      return trimmed;
    }
    if (/^(#|\/|\.\.?\/|\?)/.test(trimmed)) {
      return trimmed;
    }
    try {
      const parsed = new URL(trimmed, window.location.origin);
      if (["http:", "https:", "mailto:", "tel:"].includes(parsed.protocol)) {
        return parsed.href;
      }
    } catch (_error) {
      return "";
    }
    return "";
  };

  const renderInlineMarkdown = (value, depth = 0) => {
    if (depth > 4) {
      return escapeHTML(String(value || "")).replace(new RegExp(markdownBreakToken, "g"), "<br>");
    }

    const tokens = [];
    const stashToken = (html) => {
      const index = tokens.push(html) - 1;
      return `@@MDTOKEN${index}@@`;
    };

    let rendered = String(value || "");
  rendered = rendered.replace(new RegExp(markdownBreakToken, "g"), () => stashToken("<br>"));
    rendered = rendered.replace(/\\([\\`*_[\]()#+\-.!|>~])/g, (_match, escaped) => stashToken(escapeHTML(escaped)));
    rendered = rendered.replace(/`([^`\n]+)`/g, (_match, code) => stashToken(`<code>${escapeHTML(code)}</code>`));
    rendered = rendered.replace(/!\[([^\]]*)\]\(([^)\s]+(?:\s+\"[^\"]*\")?)\)/g, (_match, alt, target) => {
      const source = sanitizeMarkdownURL(String(target).replace(/\s+\"[^\"]*\"$/, ""));
      if (source === "") {
        return stashToken(`${escapeHTML(alt)} (${escapeHTML(target)})`);
      }
      return stashToken(`<img src="${escapeHTML(source)}" alt="${escapeHTML(alt)}" loading="lazy" decoding="async">`);
    });
    rendered = rendered.replace(/\[([^\]]+)\]\(([^)\s]+(?:\s+\"[^\"]*\")?)\)/g, (_match, label, target) => {
      const href = sanitizeMarkdownURL(String(target).replace(/\s+\"[^\"]*\"$/, ""));
      if (href === "") {
        return stashToken(`${escapeHTML(label)} (${escapeHTML(target)})`);
      }
      return stashToken(`<a href="${escapeHTML(href)}" target="_blank" rel="noreferrer">${renderInlineMarkdown(label, depth + 1)}</a>`);
    });
    rendered = rendered.replace(/(^|[\s(])(https?:\/\/[^\s<)]+|mailto:[^\s<)]+|tel:[^\s<)]+)/g, (_match, prefix, rawURL) => {
      const href = sanitizeMarkdownURL(rawURL);
      if (href === "") {
        return `${prefix}${rawURL}`;
      }
      return `${prefix}${stashToken(`<a href="${escapeHTML(href)}" target="_blank" rel="noreferrer">${escapeHTML(rawURL)}</a>`)}`;
    });

    rendered = escapeHTML(rendered);
    rendered = rendered.replace(/\*\*([^\n]+?)\*\*/g, "<strong>$1</strong>");
    rendered = rendered.replace(/__([^\n]+?)__/g, "<strong>$1</strong>");
    rendered = rendered.replace(/~~([^\n]+?)~~/g, "<del>$1</del>");
    rendered = rendered.replace(/(^|[^*])\*([^*\n]+?)\*(?!\*)/g, "$1<em>$2</em>");
    rendered = rendered.replace(/(^|[^_])_([^_\n]+?)_(?!_)/g, "$1<em>$2</em>");

    return rendered.replace(/@@MDTOKEN(\d+)@@/g, (_match, index) => tokens[Number(index)] || "");
  };

  const splitMarkdownTableCells = (line) => {
    let trimmed = String(line || "").trim();
    if (trimmed.startsWith("|")) {
      trimmed = trimmed.slice(1);
    }
    if (trimmed.endsWith("|")) {
      trimmed = trimmed.slice(0, -1);
    }
    return trimmed.split("|").map((cell) => cell.trim());
  };

  const markdownTableAlignment = (cell) => {
    const trimmed = String(cell || "").trim();
    if (!/^:?-{3,}:?$/.test(trimmed)) {
      return "";
    }
    if (trimmed.startsWith(":")) {
      if (trimmed.endsWith(":")) {
        return "center";
      }
      return "left";
    }
    if (trimmed.endsWith(":")) {
      return "right";
    }
    return "";
  };

  const looksLikeMarkdownTable = (lines, index) => {
    if (index + 1 >= lines.length) {
      return false;
    }
    const header = String(lines[index] || "").trim();
    const divider = String(lines[index + 1] || "").trim();
    if (!header.includes("|") || !divider.includes("-")) {
      return false;
    }
    const headerCells = splitMarkdownTableCells(header);
    const dividerCells = splitMarkdownTableCells(divider);
    return headerCells.length > 1
      && headerCells.length === dividerCells.length
      && dividerCells.every((cell) => /^:?-{3,}:?$/.test(cell));
  };

  const renderMarkdownHTML = (value) => {
    const source = normalizeMarkdownSource(value);
    if (source.trim() === "") {
      return "";
    }

    const lines = source.split("\n");
    const fragments = [];
    let paragraph = [];

    const flushParagraph = () => {
      if (!paragraph.length) {
        return;
      }
      const raw = paragraph.join("\n").trim();
      paragraph = [];
      if (raw === "") {
        return;
      }
      fragments.push(`<p>${renderInlineMarkdown(raw).replace(/\n/g, "<br>")}</p>`);
    };

    let index = 0;
    while (index < lines.length) {
      const line = lines[index];
      const trimmed = String(line || "").trim();

      if (trimmed === "") {
        flushParagraph();
        index += 1;
        continue;
      }

      if (/^```/.test(trimmed)) {
        flushParagraph();
        const language = trimmed.slice(3).trim();
        index += 1;
        const codeLines = [];
        while (index < lines.length && !/^```/.test(String(lines[index] || "").trim())) {
          codeLines.push(lines[index]);
          index += 1;
        }
        if (index < lines.length) {
          index += 1;
        }
        const languageAttr = language ? ` data-language="${escapeHTML(language)}"` : "";
        fragments.push(`<pre class="chat-markdown-pre"><code${languageAttr}>${escapeHTML(codeLines.join("\n"))}</code></pre>`);
        continue;
      }

      if (/^\s*>/.test(line)) {
        flushParagraph();
        const quoteLines = [];
        while (index < lines.length && /^\s*>/.test(String(lines[index] || ""))) {
          quoteLines.push(String(lines[index] || "").replace(/^\s*>\s?/, ""));
          index += 1;
        }
        fragments.push(`<blockquote>${renderMarkdownHTML(quoteLines.join("\n"))}</blockquote>`);
        continue;
      }

      if (looksLikeMarkdownTable(lines, index)) {
        flushParagraph();
        const headerCells = splitMarkdownTableCells(lines[index]);
        const alignments = splitMarkdownTableCells(lines[index + 1]).map(markdownTableAlignment);
        const renderCells = (cells, tagName) => headerCells.map((_, cellIndex) => {
          const cellValue = cells[cellIndex] || "";
          const alignment = alignments[cellIndex] ? ` style="text-align:${alignments[cellIndex]}"` : "";
          return `<${tagName}${alignment}>${renderInlineMarkdown(cellValue)}</${tagName}>`;
        }).join("");

        index += 2;
        const bodyRows = [];
        while (index < lines.length) {
          const row = String(lines[index] || "").trim();
          if (row === "" || !row.includes("|")) {
            break;
          }
          bodyRows.push(splitMarkdownTableCells(lines[index]));
          index += 1;
        }

        const tableBody = bodyRows.length
          ? `<tbody>${bodyRows.map((row) => `<tr>${renderCells(row, "td")}</tr>`).join("")}</tbody>`
          : "";
        fragments.push(`<div class="chat-markdown-table"><table><thead><tr>${renderCells(headerCells, "th")}</tr></thead>${tableBody}</table></div>`);
        continue;
      }

      const heading = trimmed.match(/^(#{1,6})\s+(.*)$/);
      if (heading) {
        flushParagraph();
        const level = heading[1].length;
        fragments.push(`<h${level}>${renderInlineMarkdown(heading[2])}</h${level}>`);
        index += 1;
        continue;
      }

      if (/^([-*_])(?:\s*\1){2,}\s*$/.test(trimmed)) {
        flushParagraph();
        fragments.push("<hr>");
        index += 1;
        continue;
      }

      const listItem = String(line || "").match(/^\s*([-+*]|\d+\.)\s+(.+)$/);
      if (listItem) {
        flushParagraph();
        const ordered = /\d+\./.test(listItem[1]);
        const tagName = ordered ? "ol" : "ul";
        const items = [];
        while (index < lines.length) {
          const candidate = String(lines[index] || "").match(/^\s*([-+*]|\d+\.)\s+(.+)$/);
          if (!candidate || ordered !== /\d+\./.test(candidate[1])) {
            break;
          }
          items.push(`<li>${renderInlineMarkdown(candidate[2]).replace(/\n/g, "<br>")}</li>`);
          index += 1;
        }
        fragments.push(`<${tagName}>${items.join("")}</${tagName}>`);
        continue;
      }

      paragraph.push(line);
      index += 1;
    }

    flushParagraph();
    return fragments.join("");
  };

  const renderMarkdownInto = (element, value) => {
    if (!(element instanceof HTMLElement)) {
      return;
    }
    const source = String(value || "");
    element.dataset.chatMarkdownSource = source;
    element.innerHTML = renderMarkdownHTML(source);
  };

  const markdownToPlainText = (value) => {
    const source = String(value || "");
    if (source.trim() === "") {
      return "";
    }
    const sandbox = document.createElement("div");
    sandbox.innerHTML = renderMarkdownHTML(source);
    return String(sandbox.textContent || "").replace(/\s+/g, " ").trim();
  };

  window.AIYoloMarkdown = Object.freeze({
    renderHTML: renderMarkdownHTML,
    renderInto: renderMarkdownInto,
    toPlainText: markdownToPlainText,
    readSource(element) {
      if (!(element instanceof HTMLElement)) {
        return "";
      }
      return String(element.dataset.chatMarkdownSource || element.textContent || "").trim();
    },
  });

  const readMarkdownSource = (element) => window.AIYoloMarkdown.readSource(element);

  const copyTextToClipboard = async (text) => {
    const value = String(text || "");
    if (value.trim() === "") {
      return false;
    }
    if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
      await navigator.clipboard.writeText(value);
      return true;
    }
    const helper = document.createElement("textarea");
    helper.value = value;
    helper.setAttribute("readonly", "");
    helper.style.position = "fixed";
    helper.style.opacity = "0";
    document.body.appendChild(helper);
    helper.select();
    const copied = document.execCommand("copy");
    helper.remove();
    if (!copied) {
      throw new Error("copy failed");
    }
    return true;
  };

  const copyAssistantMessageMarkdown = async (bubble, options = {}) => {
    if (!(bubble instanceof HTMLElement)) {
      return false;
    }
    const contentNode = bubble.querySelector(".chat-message-copy");
    const markdown = readMarkdownSource(contentNode);
    if (markdown === "") {
      if (options.showToast !== false) {
        showEnvironmentToast(t("没有可复制的内容。", "Nothing to copy."), "warning");
      }
      return false;
    }
    try {
      await copyTextToClipboard(markdown);
      if (options.showToast !== false) {
        showEnvironmentToast(t("已复制 Markdown。", "Markdown copied."), "success");
      }
      return true;
    } catch (_error) {
      if (options.showToast !== false) {
        showEnvironmentToast(t("复制失败。", "Copy failed."), "error");
      }
      return false;
    }
  };

  const selectionNodeWithin = (element, node) => {
    if (!(element instanceof HTMLElement) || !node) {
      return false;
    }
    let current = node;
    while (current) {
      if (current === element) {
        return true;
      }
      current = current.parentNode;
    }
    return false;
  };

  const selectionRangeIntersectsElement = (element, range) => {
    if (!(element instanceof HTMLElement) || !range) {
      return false;
    }
    const elementRange = document.createRange();
    elementRange.selectNodeContents(element);
    return range.compareBoundaryPoints(Range.END_TO_START, elementRange) < 0
      && range.compareBoundaryPoints(Range.START_TO_END, elementRange) > 0;
  };

  const getSelectedPlainTextWithin = (element) => {
    const selection = window.getSelection();
    if (!selection || selection.isCollapsed || selection.rangeCount === 0) {
      return "";
    }
    const range = selection.getRangeAt(0);
    const intersects = selectionRangeIntersectsElement(element, range)
      || selectionNodeWithin(element, selection.anchorNode)
      || selectionNodeWithin(element, selection.focusNode);
    if (!intersects) {
      return "";
    }
    const selected = String(selection.toString() || "");
    if (selected.length > 0) {
      return selected;
    }
    const fragment = range.cloneContents();
    const sandbox = document.createElement("div");
    sandbox.appendChild(fragment);
    return String(sandbox.textContent || "");
  };

  const attachMarkdownCopyHandler = (element) => {
    if (!(element instanceof HTMLElement) || element.dataset.chatMarkdownCopyBound === "true") {
      return;
    }
    element.dataset.chatMarkdownCopyBound = "true";
    element.addEventListener("copy", (event) => {
      if (!(event.clipboardData instanceof DataTransfer)) {
        return;
      }
      const selectedText = getSelectedPlainTextWithin(element);
      if (selectedText.length > 0) {
        event.preventDefault();
        event.clipboardData.setData("text/plain", selectedText);
        return;
      }
      const markdown = readMarkdownSource(element);
      if (markdown === "") {
        return;
      }
      event.preventDefault();
      event.clipboardData.setData("text/plain", markdown);
    });
  };

  const messageSummaryText = (message) => {
    const summary = markdownToPlainText(message?.content || "");
    if (summary !== "") {
      return summary;
    }
    const reasoningSummary = markdownToPlainText(message?.reasoning || "");
    if (reasoningSummary !== "") {
      return reasoningSummary;
    }
    return message?.attachments?.[0]?.name || t("附件消息", "Attachment message");
  };

  const scrollThread = (thread) => {
    if (!thread) {
      return;
    }
    thread.scrollTop = thread.scrollHeight;
  };

  const setSidebarCollapsed = (form, collapsed, persist = false) => {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    form.classList.toggle("is-sidebar-collapsed", collapsed);
    const toggle = form.querySelector(".chat-sidebar-toggle");
    if (toggle instanceof HTMLButtonElement) {
      toggle.setAttribute("aria-expanded", collapsed ? "false" : "true");
    }
    if (persist) {
      writeSidebarPreference(collapsed);
    }
  };

  const sessionStoreField = (form = currentForm()) => form?.querySelector("#chat-session-store-json") || null;
  const hiddenField = (form, name) => form?.querySelector(`input[name="${name}"]`) || null;
  const readHiddenJSON = (form, name, fallback) => {
    const field = hiddenField(form, name);
    if (!(field instanceof HTMLInputElement)) {
      return cloneValue(fallback);
    }
    return safeParseJSON(field.value, fallback);
  };
  const writeHiddenJSON = (form, name, value) => {
    const field = hiddenField(form, name);
    if (field instanceof HTMLInputElement) {
      field.value = JSON.stringify(value);
    }
  };
  const readClientSessionID = (form) => {
    const field = hiddenField(form, "chat_client_session_id");
    if (!(field instanceof HTMLInputElement)) {
      return "";
    }
    return field.value.trim();
  };
  const writeClientSessionID = (form, value) => {
    const field = hiddenField(form, "chat_client_session_id");
    if (field instanceof HTMLInputElement) {
      field.value = String(value || "").trim();
    }
  };
  const environmentField = (form = currentForm()) => form?.querySelector("[data-chat-environment-input], select[name=\"chat_environment\"]") || null;
  const environmentPicker = (form = currentForm()) => form?.querySelector("[data-chat-environment-picker]") || null;
  const environmentPickerCopy = (form = currentForm()) => form?.querySelector("[data-chat-environment-picker-copy]") || null;
  const environmentOptionInputs = (form = currentForm()) => pickerFormControls(form, "[data-chat-environment-option]").filter((input) => input instanceof HTMLInputElement);
  const currentSelectedEnvironment = (form) => {
    const field = environmentField(form);
    if (!(field instanceof HTMLInputElement || field instanceof HTMLSelectElement)) {
      return "local";
    }
    return field.value.trim() || "local";
  };
  const environmentLabel = (form, value) => {
    const next = String(value || "").trim() || "local";
    const option = environmentOptionInputs(form).find((input) => input.value.trim() === next);
    if (option instanceof HTMLInputElement) {
      const label = String(option.dataset.chatEnvironmentLabel || "").trim();
      if (label !== "") {
        return label;
      }
    }
    return next === "local" ? t("聊天", "Chat") : next;
  };
  const refreshEnvironmentOptionStates = (form) => {
    const selected = currentSelectedEnvironment(form);
    environmentOptionInputs(form).forEach((input) => {
      const option = input.closest(".chat-environment-picker-option");
      const active = input.value.trim() === selected;
      input.checked = active;
      if (option instanceof HTMLElement) {
        option.classList.toggle("is-active", active);
      }
    });
  };
  const setEnvironmentPickerBusy = (form, busy) => {
    const picker = environmentPicker(form);
    if (!(picker instanceof HTMLDetailsElement)) {
      return;
    }
    if (busy) {
      picker.open = false;
    }
    picker.classList.toggle("is-disabled", busy);
    const summary = picker.querySelector("summary");
    if (summary instanceof HTMLElement) {
      summary.setAttribute("aria-disabled", busy ? "true" : "false");
      if (busy) {
        summary.tabIndex = -1;
      } else {
        summary.removeAttribute("tabindex");
      }
    }
    environmentOptionInputs(form).forEach((input) => {
      input.disabled = busy;
    });
  };
  const setSelectedEnvironment = (form, value) => {
    const field = environmentField(form);
    const next = String(value || "").trim() || "local";
    if (field instanceof HTMLSelectElement) {
      const hasOption = Array.from(field.options).some((option) => option.value === next);
      field.value = hasOption ? next : "local";
      return;
    }
    if (!(field instanceof HTMLInputElement)) {
      return;
    }
    const options = environmentOptionInputs(form);
    const selected = options.find((input) => input.value.trim() === next)
      || options.find((input) => input.value.trim() === "local")
      || null;
    field.value = selected ? selected.value.trim() : "local";
    const copy = environmentPickerCopy(form);
    if (copy instanceof HTMLElement) {
      copy.textContent = environmentLabel(form, field.value);
    }
    refreshEnvironmentOptionStates(form);
  };
  const environmentEnsureURL = (form = currentForm()) => String(form?.dataset.chatEnvironmentEnsureUrl || "").trim();
  const environmentEnsureStreamURL = (form = currentForm()) => {
    const explicit = String(form?.dataset.chatEnvironmentEnsureStreamUrl || "").trim();
    if (explicit !== "") {
      return explicit;
    }
    const base = environmentEnsureURL(form);
    return base === "" ? "" : `${base}/stream`;
  };
  let environmentToastTimer = null;

  const dismissEnvironmentToast = () => {
    if (environmentToastTimer) {
      window.clearTimeout(environmentToastTimer);
      environmentToastTimer = null;
    }
    document.querySelector("[data-chat-environment-toast]")?.remove();
  };

  const showEnvironmentToast = (message, tone = "info", options = {}) => {
    const text = String(message || "").trim();
    if (text === "") {
      dismissEnvironmentToast();
      return;
    }
    let host = document.querySelector("[data-chat-environment-toast-host]");
    if (!(host instanceof HTMLElement)) {
      host = document.createElement("div");
      host.className = "chat-environment-toast-host";
      host.dataset.chatEnvironmentToastHost = "";
      document.body.appendChild(host);
    }
    let toast = host.querySelector("[data-chat-environment-toast]");
    if (!(toast instanceof HTMLElement)) {
      toast = document.createElement("div");
      toast.className = "chat-environment-toast";
      toast.dataset.chatEnvironmentToast = "";
      toast.setAttribute("role", "status");
      toast.setAttribute("aria-live", "polite");
      host.appendChild(toast);
    }
    toast.className = `chat-environment-toast is-${tone}`;
    toast.textContent = text;
    if (environmentToastTimer) {
      window.clearTimeout(environmentToastTimer);
      environmentToastTimer = null;
    }
    if (!options.persistent) {
      const delay = tone === "error" ? 9000 : 4500;
      environmentToastTimer = window.setTimeout(() => dismissEnvironmentToast(), delay);
    }
  };

  const environmentStatusPhaseLabel = (phase, fallback = "") => {
    const key = String(phase || "").trim().toLowerCase();
    switch (key) {
      case "connect":
        return t("正在连接 Worker…", "Connecting to worker…");
      case "resolve_ass":
        return t("正在解析 aiyolo-ass 版本…", "Resolving published aiyolo-ass checksum…");
      case "starting":
        return t("正在准备 Cloud Agent…", "Preparing cloud agent…");
      case "ensure_remote":
        return t("正在远程确保 Cloud Agent…", "Ensuring cloud agent remotely…");
      case "prepare":
        return t("正在准备远程工作区…", "Preparing remote workspace…");
      case "lock":
        return t("Cloud Agent 正在启动或重建，请稍候…", "Cloud agent is starting or rebuilding, please wait…");
      case "download_ass":
        return t("正在下载 aiyolo-ass…", "Downloading aiyolo-ass…");
      case "download_rootfs":
        return t("正在下载基础 rootfs…", "Downloading base rootfs…");
      case "build_image":
        return t("正在构建 Cloud Agent 镜像…", "Building cloud agent image…");
      case "reuse_image":
        return t("镜像已是最新，跳过构建", "Image already matches release; skipping build");
      case "upgrade_container":
        return t("正在升级 Cloud Agent 容器…", "Upgrading cloud agent container…");
      case "create_container":
        return t("正在创建 Cloud Agent…", "Creating cloud agent…");
      case "start_container":
        return t("正在启动 Claude Code…", "Starting Claude Code…");
      case "reuse_container":
        return t("正在恢复 Cloud Agent 容器…", "Restoring cloud agent container…");
      case "sync_hosts":
        return t("正在同步内网 hosts…", "Syncing internal hosts…");
      case "wait_runtime":
        return t("正在等待 Claude Code 就绪…", "Waiting for Claude Code to become ready…");
      case "reuse":
        return t("正在恢复 Cloud Agent 会话…", "Restoring cloud agent session…");
      case "ready":
        return t("Claude Code 已就绪", "Claude Code is ready");
      case "sending":
        return t("正在发送消息…", "Sending message…");
      case "preparing":
        return t("正在准备发送…", "Preparing to send…");
      default:
        return String(fallback || "").trim();
    }
  };

  const applyEnvironmentEnsureEvent = (form, event, options = {}) => {
    const showToast = options.showToast !== false;
    const notifyPhase = (phase, fallback = "") => {
      const label = environmentStatusPhaseLabel(phase, fallback);
      if (typeof options.onPhase === "function") {
        options.onPhase(phase, label);
      }
      if (showToast) {
        showEnvironmentToast(label, "info", { persistent: true });
      }
      return label;
    };
    if (!event || typeof event !== "object") {
      return null;
    }
    const type = String(event.type || "").trim().toLowerCase();
    if (type === "log") {
      const phase = String(event.phase || "").trim().toLowerCase();
      if (phase === "build_image") {
        const message = String(event.message || "").trim();
        if (message) {
          const label = `${t("镜像构建中：", "Image build: ")}${message}`;
          if (typeof options.onPhase === "function") {
            options.onPhase(phase, label);
          }
          if (showToast) {
            showEnvironmentToast(label, "info", { persistent: true });
          }
        }
      }
      return null;
    }
    if (type === "phase") {
      const phase = String(event.phase || "").trim().toLowerCase();
      notifyPhase(phase, event.message);
      return null;
    }
    if (type === "local") {
      const message = String(event.message || event.notice || t("已切换到聊天", "Switched to chat"));
      if (showToast) {
        showEnvironmentToast(message, "info");
      }
      return {
        status: "local",
        sessionId: event.sessionId,
        environment: event.environment,
        notice: event.notice || event.message || "",
      };
    }
    if (type === "ready") {
      const notice = String(event.notice || event.message || environmentStatusPhaseLabel("ready")).trim();
      if (typeof options.onPhase === "function") {
        options.onPhase("ready", notice);
      }
      if (showToast) {
        showEnvironmentToast(notice, "success");
      }
      return {
        status: "ready",
        sessionId: event.sessionId,
        environment: event.environment,
        workerId: event.workerId,
        containerName: event.containerName,
        workspacePath: event.workspacePath,
        notice,
      };
    }
    if (type === "error") {
      const message = String(event.error || event.message || t("环境准备失败。", "Failed to prepare the selected environment."));
      if (showToast) {
        showEnvironmentToast(message, "error");
      }
      throw new Error(message);
    }
    return null;
  };

  const currentSelectedModel = (form) => {
    const checked = pickerFormControls(form, "input[name=\"chat_public_name\"]:checked")[0];
    if (checked instanceof HTMLInputElement) {
      return checked.value.trim();
    }
    const first = pickerFormControls(form, "input[name=\"chat_public_name\"]")[0];
    return first instanceof HTMLInputElement ? first.value.trim() : "";
  };

  const parseReasoningEfforts = (raw) => String(raw || "")
    .split(",")
    .map((value) => String(value || "").trim().toLowerCase())
    .filter((value, index, values) => value !== "" && values.indexOf(value) === index);

  const normalizeReasoningEffort = (value, allowedEfforts = []) => {
    const normalized = String(value || "").trim().toLowerCase();
    return allowedEfforts.includes(normalized) ? normalized : "";
  };

  const reasoningEffortDefaultLabel = () => "reasoning_effort · default";
  const reasoningEffortSummaryLabel = (value) => {
    const normalized = String(value || "").trim().toLowerCase();
    return normalized === "" ? t("思考 · 默认", "Thinking · default") : `${t("思考", "Thinking")} · ${reasoningEffortLabel(normalized)}`;
  };

  const reasoningEffortLabel = (value) => {
    switch (String(value || "").trim().toLowerCase()) {
      case "high":
        return "high";
      case "max":
        return "max";
      default:
        return String(value || "").trim();
    }
  };

  const reasoningEffortControl = (form = currentForm()) => form?.querySelector("[data-chat-reasoning-control]") || null;
  const reasoningEffortInput = (form = currentForm()) => form?.querySelector("[data-chat-reasoning-effort-input]") || null;
  const reasoningEffortPicker = (form = currentForm()) => form?.querySelector("[data-chat-reasoning-picker]") || null;
  const reasoningEffortPickerMenu = (form = currentForm()) => {
    const menu = pickerMenu(reasoningEffortPicker(form));
    return menu?.classList.contains("chat-reasoning-picker-menu") ? menu : null;
  };
  const reasoningEffortPickerCopy = (form = currentForm()) => form?.querySelector("[data-chat-reasoning-picker-copy]") || null;

  const refreshReasoningOptionStates = (form = currentForm()) => {
    pickerFormControls(form, "[data-chat-reasoning-option]").forEach((input) => {
      if (!(input instanceof HTMLInputElement)) {
        return;
      }
      const option = input.closest(".chat-reasoning-picker-option");
      option?.classList.toggle("is-active", input.checked);
    });
  };

  const setSelectedReasoningEffort = (form, value) => {
    const normalized = String(value || "").trim().toLowerCase();
    let matched = false;
    pickerFormControls(form, "[data-chat-reasoning-option]").forEach((input) => {
      if (!(input instanceof HTMLInputElement)) {
        return;
      }
      const checked = String(input.value || "").trim().toLowerCase() === normalized;
      input.checked = checked;
      matched = matched || checked;
    });
    if (!matched) {
      const fallback = pickerFormControls(form, "[data-chat-reasoning-option][value='']")[0];
      if (fallback instanceof HTMLInputElement) {
        fallback.checked = true;
      }
    }
    refreshReasoningOptionStates(form);
  };

  const currentSelectedReasoningEffort = (form) => {
    const input = reasoningEffortInput(form);
    if (!(input instanceof HTMLInputElement) || input.disabled) {
      return "";
    }
    return String(input.value || "").trim().toLowerCase();
  };

  const isImageGenerationRoute = (route) => Boolean(route?.imageGeneration);

  const syncComposerDraftPlaceholder = (form, route) => {
    const draftField = form?.querySelector("#chat-draft");
    if (!(draftField instanceof HTMLTextAreaElement)) {
      return;
    }
    draftField.placeholder = isImageGenerationRoute(route)
      ? t("描述你想生成的图片，例如：雨夜赛博朋克小巷", "Describe the image you want, e.g. a rainy cyberpunk alley")
      : t("输入消息", "Type a message");
  };

  const syncReasoningEffortControl = (form, route, preferredValue) => {
    const control = reasoningEffortControl(form);
    const input = reasoningEffortInput(form);
    const picker = reasoningEffortPicker(form);
    const menu = reasoningEffortPickerMenu(form);
    const copy = reasoningEffortPickerCopy(form);
    if (!(input instanceof HTMLInputElement) || !(menu instanceof HTMLElement)) {
      return;
    }

    if (isImageGenerationRoute(route)) {
      if (control instanceof HTMLElement) {
        control.hidden = true;
      }
      if (picker instanceof HTMLDetailsElement) {
        picker.open = false;
      }
      input.disabled = true;
      input.value = "";
      setSelectedReasoningEffort(form, "");
      syncComposerDraftPlaceholder(form, route);
      return;
    }

    const allowedEfforts = Array.isArray(route?.reasoningEfforts) ? route.reasoningEfforts : [];
    const nextValue = normalizeReasoningEffort(typeof preferredValue === "string" ? preferredValue : currentSelectedReasoningEffort(form), allowedEfforts);

    menu.replaceChildren();

    const appendOption = (value, label, detail) => {
      const option = document.createElement("label");
      option.className = "chat-model-picker-option chat-reasoning-picker-option";

      const radio = document.createElement("input");
      radio.className = "chat-route-control chat-reasoning-option-control";
      radio.type = "radio";
      radio.name = "chat_reasoning_effort_choice";
      radio.value = value;
      radio.setAttribute("data-chat-reasoning-option", "");

      const name = document.createElement("span");
      name.className = "chat-model-name";
      name.textContent = label;

      const description = document.createElement("span");
      description.className = "chat-model-detail";
      description.textContent = detail;

      option.appendChild(radio);
      option.appendChild(name);
      option.appendChild(description);
      menu.appendChild(option);
    };

    appendOption("", reasoningEffortDefaultLabel(), t("不显式传值，沿用 DeepSeek 默认思考强度。", "Leave reasoning_effort unset and let DeepSeek choose the default effort."));

    allowedEfforts.forEach((effort) => {
      appendOption(effort, reasoningEffortLabel(effort), `reasoning_effort=${reasoningEffortLabel(effort)}`);
    });

    const enabled = allowedEfforts.length > 0;
    if (control instanceof HTMLElement) {
      control.hidden = !enabled;
    }
    if (picker instanceof HTMLDetailsElement) {
      picker.open = false;
    }
    input.disabled = !enabled;
    input.value = enabled ? nextValue : "";
    setSelectedReasoningEffort(form, enabled ? nextValue : "");
    if (copy instanceof HTMLElement) {
      copy.textContent = reasoningEffortSummaryLabel(input.value);
    }
    syncComposerDraftPlaceholder(form, route);
  };

  const defaultSystemPrompt = (form) => {
    const field = form?.querySelector("textarea[name=\"chat_system_prompt\"]");
    if (!(field instanceof HTMLTextAreaElement)) {
      return "";
    }
    return String(field.defaultValue || field.value || "").trim();
  };

  const composerPrimaryButton = (form = currentForm()) => form?.querySelector("[data-chat-action=\"composer-primary\"]") || null;
  const composerPreemptButton = (form = currentForm()) => form?.querySelector("[data-chat-action=\"composer-preempt\"]") || null;
  const composerPrimaryStartGlyph = (form = currentForm()) => composerPrimaryButton(form)?.querySelector("[data-chat-primary-start]") || null;
  const composerPrimaryStopGlyph = (form = currentForm()) => composerPrimaryButton(form)?.querySelector("[data-chat-primary-stop]") || null;
  const composerQueuePanel = (form = currentForm()) => form?.querySelector("[data-chat-composer-queue]") || null;
  const composerQueueList = (form = currentForm()) => form?.querySelector("[data-chat-queue-list]") || null;
  const composerQueueIndicator = (form = currentForm()) => form?.querySelector("[data-chat-queue-indicator]") || null;
  const shellLaunchButton = (form = currentForm()) => form?.querySelector("[data-chat-action=\"open-shell\"]") || null;
  const shellPageBaseURL = (form = currentForm()) => String(form?.dataset.chatShellUrl || "").trim();
  const shellSocketBaseURL = (form = currentForm()) => String(form?.dataset.chatShellSocketUrl || "").trim();
  const shellStateURL = (form = currentForm()) => String(form?.dataset.chatShellStateUrl || "").trim();
  const shellDock = (form = currentForm()) => form?.querySelector("[data-chat-shell-dock]") || null;
  const shellPanelBar = (form = currentForm()) => form?.querySelector(".chat-shell-panel-bar") || null;
  const shellTabsHost = (form = currentForm()) => form?.querySelector("[data-chat-shell-tabs]") || null;
  const shellPanelsHost = (form = currentForm()) => form?.querySelector("[data-chat-shell-panels]") || null;
  const shellHeightPreferenceKey = "aiyolo.console.chat.shellHeight";
  const shellStatePreferenceKey = "aiyolo.console.chat.shellSessions.v1";
  const shellCwdOscPrefix = "\u001b]6973;AiyoloCwd=";
  const shellCwdOscMaxBuffer = 8192;
  const shellDefaultHeight = 360;
  const shellMinHeight = 240;
  const shellReadyProbeTimeoutMs = 3000;
  let shellInstances = [];
  let activeShellInstanceID = "";
  let shellResizeState = null;
  let shellInstanceCounter = 0;
  let shellOpenInFlight = false;
  let environmentEnsureInFlight = null;
  let environmentEnsureKey = "";
  let shellStatePersistTimer = null;

  const syncDraftFieldHeight = (field) => {
    if (!(field instanceof HTMLTextAreaElement)) {
      return;
    }
    field.style.height = "0px";
    const viewportMaxHeight = window.innerHeight > 0 ? Math.floor(window.innerHeight * 0.5) : 0;
    const nextHeight = viewportMaxHeight > 0 ? Math.min(field.scrollHeight, viewportMaxHeight) : field.scrollHeight;
    field.style.height = `${nextHeight}px`;
    field.style.overflowY = viewportMaxHeight > 0 && field.scrollHeight > viewportMaxHeight ? "auto" : "hidden";
  };

  const syncCurrentDraftHeight = (form = currentForm()) => {
    const draftField = form?.querySelector("#chat-draft");
    if (draftField instanceof HTMLTextAreaElement) {
      syncDraftFieldHeight(draftField);
    }
  };

  const readShellHeightPreference = () => {
    if (typeof window === "undefined" || !window.localStorage) {
      return shellDefaultHeight;
    }
    try {
      const parsed = Number.parseInt(window.localStorage.getItem(shellHeightPreferenceKey) || "", 10);
      return Number.isFinite(parsed) ? parsed : shellDefaultHeight;
    } catch (_error) {
      return shellDefaultHeight;
    }
  };

  const writeShellHeightPreference = (height) => {
    if (typeof window === "undefined" || !window.localStorage) {
      return;
    }
    try {
      window.localStorage.setItem(shellHeightPreferenceKey, String(height));
    } catch (_error) {
      // Ignore storage failures and keep the in-memory size.
    }
  };

  const currentShellStateKey = (form) => {
    const sessionID = readClientSessionID(form);
    const environment = currentSelectedEnvironment(form);
    return sessionID === "" ? "" : `${sessionID}|${environment}`;
  };

  const readShellStates = () => {
    if (typeof window === "undefined" || !window.localStorage) {
      return {};
    }
    try {
      const parsed = safeParseJSON(window.localStorage.getItem(shellStatePreferenceKey), {});
      return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {};
    } catch (_error) {
      return {};
    }
  };

  const normalizeShellWorkingDirectory = (value) => {
    const path = String(value || "").trim();
    if (path === "" || !path.startsWith("/") || /[\u0000\r\n]/.test(path)) {
      return "";
    }
    return path;
  };

  const normalizeShellSnapshot = (snapshot) => {
    if (!snapshot || typeof snapshot !== "object") {
      return null;
    }
    const terminalID = String(snapshot.terminalID || snapshot.terminalId || snapshot.id || "").trim();
    const sessionID = String(snapshot.sessionID || snapshot.sessionId || "").trim();
    if (terminalID === "" || sessionID === "") {
      return null;
    }
    return {
      terminalID,
      label: String(snapshot.label || "").trim(),
      sessionID,
      socketURL: String(snapshot.socketURL || snapshot.socketUrl || "").trim(),
      meta: {
        sessionID,
        workerID: String(snapshot.meta?.workerID || snapshot.workerID || "").trim(),
        containerName: String(snapshot.meta?.containerName || snapshot.containerName || "").trim(),
        workspacePath: String(snapshot.meta?.workspacePath || snapshot.workspacePath || "").trim(),
        currentWorkingDirectory: normalizeShellWorkingDirectory(snapshot.meta?.currentWorkingDirectory || snapshot.currentWorkingDirectory || snapshot.cwd || ""),
      },
    };
  };

  const normalizeShellState = (state) => {
    const parsed = state && typeof state === "object" ? state : {};
    const seen = new Set();
    const instances = (Array.isArray(parsed.instances) ? parsed.instances : [])
      .map(normalizeShellSnapshot)
      .filter(Boolean)
      .filter((snapshot) => {
        if (seen.has(snapshot.terminalID)) {
          return false;
        }
        seen.add(snapshot.terminalID);
        return true;
      })
      .slice(0, 8);
    const activeTerminalID = String(parsed.activeTerminalID || "").trim();
    return {
      activeTerminalID: instances.some((instance) => instance.terminalID === activeTerminalID)
        ? activeTerminalID
        : instances[0]?.terminalID || "",
      instances,
      hidden: parsed.hidden === true,
      updatedAt: String(parsed.updatedAt || "").trim(),
    };
  };

  const currentShellStateSnapshot = (form = currentForm()) => {
    const sessionID = readClientSessionID(form);
    const instances = shellInstances
      .filter((instance) => instance && String(instance.sessionID || "").trim() === sessionID)
      .map((instance) => ({
        terminalID: String(instance.terminalID || instance.id || "").trim(),
        label: String(instance.label || "").trim(),
        sessionID: String(instance.sessionID || "").trim(),
        socketURL: String(instance.socketURL || "").trim(),
        meta: {
          sessionID: String(instance.sessionID || "").trim(),
          workerID: String(instance.meta?.workerID || "").trim(),
          containerName: String(instance.meta?.containerName || "").trim(),
          workspacePath: String(instance.meta?.workspacePath || "").trim(),
          currentWorkingDirectory: normalizeShellWorkingDirectory(instance.meta?.currentWorkingDirectory || ""),
        },
      }))
      .filter((instance) => instance.terminalID !== "" && instance.sessionID !== "");
    return {
      activeTerminalID: activeShellInstance()?.terminalID || instances[0]?.terminalID || "",
      instances,
      hidden: shellDock(form)?.hidden === true,
      updatedAt: new Date().toISOString(),
    };
  };

  const persistShellStateToServer = (form, state) => {
    const sessionID = readClientSessionID(form);
    const endpoint = shellStateURL(form);
    if (!(form instanceof HTMLFormElement) || sessionID === "" || endpoint === "" || !isCloudAgentEnvironment(currentSelectedEnvironment(form))) {
      return;
    }
    if (shellStatePersistTimer) {
      window.clearTimeout(shellStatePersistTimer);
    }
    const body = JSON.stringify({
      sessionID,
      activeTerminalID: String(state?.activeTerminalID || "").trim(),
      instances: Array.isArray(state?.instances) ? state.instances : [],
      hidden: state?.hidden === true,
      updatedAt: String(state?.updatedAt || new Date().toISOString()).trim(),
    });
    shellStatePersistTimer = window.setTimeout(() => {
      shellStatePersistTimer = null;
      void fetch(endpoint, {
        method: "POST",
        credentials: "same-origin",
        keepalive: body.length < 60000,
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
        },
        body,
      }).catch(() => {});
    }, 120);
  };

  const writeShellState = (form = currentForm()) => {
    const stateKey = currentShellStateKey(form);
    if (stateKey === "") {
      return;
    }
    const nextState = currentShellStateSnapshot(form);
    persistShellStateToServer(form, nextState);
    if (typeof window === "undefined" || !window.localStorage) {
      return;
    }
    try {
      const states = readShellStates();
      if (nextState.instances.length === 0) {
        delete states[stateKey];
      } else {
        states[stateKey] = nextState;
      }
      const entries = Object.entries(states)
        .filter(([, value]) => value && typeof value === "object")
        .sort((left, right) => Date.parse(String(right[1].updatedAt || "")) - Date.parse(String(left[1].updatedAt || "")))
        .slice(0, 48);
      window.localStorage.setItem(shellStatePreferenceKey, JSON.stringify(Object.fromEntries(entries)));
    } catch (_error) {
      // Ignore storage failures and keep the current in-memory shell tabs.
    }
  };

  const decodeShellBase64UTF8 = (value) => {
    const encoded = String(value || "").trim();
    if (encoded === "" || typeof window.atob !== "function") {
      return "";
    }
    try {
      const binary = window.atob(encoded);
      const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
      if (typeof window.TextDecoder === "function") {
        return new window.TextDecoder("utf-8").decode(bytes);
      }
      return binary;
    } catch (_error) {
      return "";
    }
  };

  const setShellInstanceCurrentWorkingDirectory = (form, instance, value) => {
    const currentWorkingDirectory = normalizeShellWorkingDirectory(value);
    if (!(form instanceof HTMLFormElement) || !instance || currentWorkingDirectory === "") {
      return;
    }
    if (!instance.meta || typeof instance.meta !== "object") {
      instance.meta = {};
    }
    if (instance.meta.currentWorkingDirectory === currentWorkingDirectory) {
      return;
    }
    instance.meta.currentWorkingDirectory = currentWorkingDirectory;
    renderShellMeta(instance);
    writeShellState(form);
    rememberWorkdirPath(currentWorkingDirectory);
  };

  const processShellCwdReports = (form, instance, chunk) => {
    const value = String(chunk || "");
    if (!(form instanceof HTMLFormElement) || !instance || value === "") {
      return;
    }
    let buffer = `${String(instance.cwdReportBuffer || "")}${value}`;
    let consumedUntil = 0;
    while (true) {
      const start = buffer.indexOf(shellCwdOscPrefix, consumedUntil);
      if (start < 0) {
        break;
      }
      const payloadStart = start + shellCwdOscPrefix.length;
      const belEnd = buffer.indexOf("\u0007", payloadStart);
      const stEnd = buffer.indexOf("\u001b\\", payloadStart);
      const end = belEnd >= 0 && (stEnd < 0 || belEnd < stEnd) ? belEnd : stEnd;
      if (end < 0) {
        consumedUntil = start;
        break;
      }
      const terminatorLength = end === stEnd ? 2 : 1;
      const decoded = decodeShellBase64UTF8(buffer.slice(payloadStart, end));
      setShellInstanceCurrentWorkingDirectory(form, instance, decoded);
      consumedUntil = end + terminatorLength;
    }
    buffer = consumedUntil > 0 ? buffer.slice(consumedUntil) : buffer;
    if (buffer.length > shellCwdOscMaxBuffer) {
      buffer = buffer.slice(-shellCwdOscMaxBuffer);
    }
    instance.cwdReportBuffer = buffer;
  };

  const ensureHiddenFormField = (form, name) => {
    if (!(form instanceof HTMLFormElement) || String(name || "").trim() === "") {
      return null;
    }
    let field = form.querySelector(`input[type="hidden"][name="${name}"]`);
    if (!(field instanceof HTMLInputElement)) {
      field = document.createElement("input");
      field.type = "hidden";
      field.name = name;
      form.appendChild(field);
    }
    return field;
  };

  const currentShellChatContext = () => {
    const instance = activeShellInstance();
    return {
      terminalID: String(instance?.terminalID || "").trim(),
      currentWorkingDirectory: normalizeShellWorkingDirectory(instance?.meta?.currentWorkingDirectory || ""),
    };
  };

  const syncShellContextFields = (form = currentForm()) => {
    if (!(form instanceof HTMLFormElement)) {
      return { terminalID: "", currentWorkingDirectory: "" };
    }
    const context = currentShellChatContext();
    const overrideWorkingDirectory = normalizeShellWorkingDirectory(form.dataset.chatWorkdirOverride || "");
    const resolvedWorkingDirectory = overrideWorkingDirectory || context.currentWorkingDirectory || lastRememberedWorkdir();
    const terminalField = ensureHiddenFormField(form, "chat_shell_active_terminal_id");
    const cwdField = ensureHiddenFormField(form, "chat_shell_current_working_directory");
    if (terminalField instanceof HTMLInputElement) {
      terminalField.value = context.terminalID;
    }
    if (cwdField instanceof HTMLInputElement) {
      cwdField.value = resolvedWorkingDirectory;
    }
    rememberWorkdirPath(resolvedWorkingDirectory);
    return { terminalID: context.terminalID, currentWorkingDirectory: resolvedWorkingDirectory };
  };

  const workdirDefaultPath = "/workspace";
  const workdirHistoryPreferenceKey = "aiyolo.console.chat.workspaces.v1";
  const workdirHistoryLimit = 16;
  const workdirListCache = new Map();
  let workdirListToken = 0;
  const workdirGroup = (form = currentForm()) => form?.querySelector("[data-chat-workdir-group]") || null;
  const workdirPicker = (form = currentForm()) => form?.querySelector("[data-chat-workdir-picker]") || null;
  const workdirValueNode = (form = currentForm()) => form?.querySelector("[data-chat-workdir-value]") || null;
  const workdirMenuNode = (form = currentForm()) => {
    const picker = workdirPicker(form);
    return picker instanceof HTMLDetailsElement ? pickerMenu(picker) : null;
  };
  const workdirInput = (form = currentForm()) => workdirMenuNode(form)?.querySelector("[data-chat-workdir-input]") || null;
  const workdirSuggestionsNode = (form = currentForm()) => workdirMenuNode(form)?.querySelector("[data-chat-workdir-suggestions]") || null;
  const workdirStatusNode = (form = currentForm()) => workdirMenuNode(form)?.querySelector("[data-chat-workdir-status]") || null;

  const normalizeWorkdirHistoryPath = (value) => {
    let normalized = normalizeShellWorkingDirectory(String(value || "").trim().replace(/\\/g, "/"));
    if (normalized === "") {
      return "";
    }
    normalized = normalized.replace(/\/{2,}/g, "/");
    if (normalized.length > 1) {
      normalized = normalized.replace(/\/+$/, "");
    }
    return normalized || "/";
  };

  const emptyWorkdirHistory = () => ({ last: "", items: [] });

  const normalizeWorkdirHistoryItem = (item) => {
    const parsed = item && typeof item === "object" ? item : { path: item };
    const workspacePath = normalizeWorkdirHistoryPath(parsed.path || parsed.workspacePath || parsed.value || "");
    if (workspacePath === "") {
      return null;
    }
    const count = Number.parseInt(String(parsed.count || parsed.uses || 0), 10);
    return {
      path: workspacePath,
      count: Number.isFinite(count) && count > 0 ? count : 1,
      updatedAt: String(parsed.updatedAt || parsed.lastUsedAt || "").trim(),
    };
  };

  const normalizeWorkdirHistory = (value) => {
    const parsed = value && typeof value === "object" ? value : {};
    const sourceItems = Array.isArray(parsed.items)
      ? parsed.items
      : Array.isArray(parsed.workspaces)
        ? parsed.workspaces
        : Array.isArray(value)
          ? value
          : [];
    const byPath = new Map();
    sourceItems.map(normalizeWorkdirHistoryItem).filter(Boolean).forEach((item) => {
      const existing = byPath.get(item.path);
      if (!existing) {
        byPath.set(item.path, item);
        return;
      }
      existing.count = Math.max(existing.count, item.count);
      if (String(item.updatedAt || "") > String(existing.updatedAt || "")) {
        existing.updatedAt = item.updatedAt;
      }
    });
    const last = normalizeWorkdirHistoryPath(parsed.last || parsed.lastWorkspacePath || parsed.lastPath || "");
    if (last !== "" && !byPath.has(last)) {
      byPath.set(last, { path: last, count: 1, updatedAt: "" });
    }
    const items = Array.from(byPath.values())
      .sort((left, right) => String(right.updatedAt || "").localeCompare(String(left.updatedAt || "")) || left.path.localeCompare(right.path))
      .slice(0, workdirHistoryLimit);
    return { last, items };
  };

  const readWorkdirHistory = () => {
    if (typeof window === "undefined" || !window.localStorage) {
      return emptyWorkdirHistory();
    }
    try {
      return normalizeWorkdirHistory(safeParseJSON(window.localStorage.getItem(workdirHistoryPreferenceKey), emptyWorkdirHistory()));
    } catch (_error) {
      return emptyWorkdirHistory();
    }
  };

  const writeWorkdirHistory = (history) => {
    if (typeof window === "undefined" || !window.localStorage) {
      return;
    }
    try {
      window.localStorage.setItem(workdirHistoryPreferenceKey, JSON.stringify(normalizeWorkdirHistory(history)));
    } catch (_error) {
      // Ignore storage failures; the current form still carries the selected path.
    }
  };

  const rememberWorkdirPath = (value) => {
    const workspacePath = normalizeWorkdirHistoryPath(value);
    if (workspacePath === "") {
      return emptyWorkdirHistory();
    }
    const history = readWorkdirHistory();
    const now = new Date().toISOString();
    const byPath = new Map(history.items.map((item) => [item.path, { ...item }]));
    const item = byPath.get(workspacePath) || { path: workspacePath, count: 0, updatedAt: "" };
    item.count += 1;
    item.updatedAt = now;
    byPath.set(workspacePath, item);
    const next = {
      last: workspacePath,
      items: Array.from(byPath.values())
        .sort((left, right) => String(right.updatedAt || "").localeCompare(String(left.updatedAt || "")) || left.path.localeCompare(right.path))
        .slice(0, workdirHistoryLimit),
    };
    writeWorkdirHistory(next);
    return next;
  };

  const lastRememberedWorkdir = () => readWorkdirHistory().last;

  const effectiveWorkingDirectory = (form = currentForm()) => {
    if (!(form instanceof HTMLFormElement)) {
      return workdirDefaultPath;
    }
    const override = normalizeShellWorkingDirectory(form.dataset.chatWorkdirOverride || "");
    if (override !== "") {
      return override;
    }
    const shellCwd = normalizeShellWorkingDirectory(activeShellInstance()?.meta?.currentWorkingDirectory || "");
    if (shellCwd !== "") {
      return shellCwd;
    }
    const remembered = lastRememberedWorkdir();
    if (remembered !== "") {
      return remembered;
    }
    return workdirDefaultPath;
  };

  const renderWorkdirControl = (form = currentForm()) => {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    const current = effectiveWorkingDirectory(form);
    const valueNode = workdirValueNode(form);
    if (valueNode instanceof HTMLElement) {
      valueNode.textContent = current;
      valueNode.title = current;
    }
    const picker = workdirPicker(form);
    const overridden = normalizeShellWorkingDirectory(form.dataset.chatWorkdirOverride || "") !== "";
    if (picker instanceof HTMLElement) {
      picker.classList.toggle("is-overridden", overridden);
    }
  };

  const setWorkdirStatus = (form, message, isError = false) => {
    const node = workdirStatusNode(form);
    if (!(node instanceof HTMLElement)) {
      return;
    }
    const text = String(message || "").trim();
    node.textContent = text;
    node.hidden = text === "";
    node.classList.toggle("is-error", Boolean(isError) && text !== "");
  };

  const splitWorkdirInput = (raw) => {
    let value = String(raw || "").trim().replace(/\\/g, "/");
    if (value === "") {
      return { dir: workdirDefaultPath, prefix: "" };
    }
    if (!value.startsWith("/")) {
      value = "/" + value;
    }
    if (value.endsWith("/")) {
      const dir = value.length > 1 ? value.replace(/\/+$/, "") : "/";
      return { dir: dir === "" ? "/" : dir, prefix: "" };
    }
    const lastSlash = value.lastIndexOf("/");
    const dir = lastSlash <= 0 ? "/" : value.slice(0, lastSlash);
    const prefix = value.slice(lastSlash + 1);
    return { dir, prefix };
  };

  const joinWorkdirPath = (dir, name) => {
    const base = dir === "/" ? "" : String(dir || "").replace(/\/+$/, "");
    return base + "/" + String(name || "");
  };

  const workdirListDirURL = (form) => String(form?.dataset.chatWorkspaceListdirUrl || "").trim();

  const fetchWorkdirDirectories = async (form, dir) => {
    const sessionID = readClientSessionID(form);
    const endpoint = workdirListDirURL(form);
    if (sessionID === "" || endpoint === "") {
      return null;
    }
    const cacheKey = `${sessionID}|${dir}`;
    if (workdirListCache.has(cacheKey)) {
      return workdirListCache.get(cacheKey);
    }
    const url = new URL(endpoint, window.location.href);
    url.searchParams.set("session", sessionID);
    url.searchParams.set("path", dir);
    const response = await fetch(url.toString(), {
      method: "GET",
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok || payload?.status !== "ready") {
      const error = new Error(String(payload?.error || `HTTP ${response.status}`));
      throw error;
    }
    const result = {
      path: String(payload.path || dir || "").trim() || dir,
      directories: Array.isArray(payload.directories) ? payload.directories.map((name) => String(name || "").trim()).filter(Boolean) : [],
    };
    workdirListCache.set(cacheKey, result);
    return result;
  };

  const workdirInputMatchesPath = (rawInput, workspacePath) => {
    const pathValue = normalizeWorkdirHistoryPath(workspacePath).toLowerCase();
    const raw = String(rawInput || "").trim().replace(/\\/g, "/").toLowerCase();
    if (pathValue === "") {
      return false;
    }
    if (raw === "") {
      return true;
    }
    const normalizedRaw = raw.startsWith("/") ? raw : `/${raw}`;
    return pathValue.startsWith(normalizedRaw.replace(/\/{2,}/g, "/"));
  };

  const rememberedWorkdirEntries = (rawInput) => {
    const history = readWorkdirHistory();
    const entries = [];
    const seen = new Set();
    const add = (entry, label, icon) => {
      const workspacePath = normalizeWorkdirHistoryPath(entry?.path || "");
      if (workspacePath === "" || seen.has(workspacePath) || !workdirInputMatchesPath(rawInput, workspacePath)) {
        return;
      }
      seen.add(workspacePath);
      entries.push({
        path: workspacePath,
        label,
        icon,
        count: Number.isFinite(entry.count) ? entry.count : 1,
        updatedAt: String(entry.updatedAt || ""),
      });
    };
    const last = history.items.find((item) => item.path === history.last) || (history.last === "" ? null : { path: history.last, count: 1 });
    add(last, t("上次工作区", "Last workspace"), "history");
    history.items
      .filter((item) => item.path !== history.last)
      .sort((left, right) => (right.count - left.count) || String(right.updatedAt || "").localeCompare(String(left.updatedAt || "")) || left.path.localeCompare(right.path))
      .slice(0, 8)
      .forEach((item) => add(item, item.path, "folder-clock"));
    return entries;
  };

  const appendWorkdirOption = (host, options) => {
    const option = document.createElement("button");
    option.type = "button";
    option.className = "chat-workdir-option";
    option.dataset.chatWorkdirOption = options.path;
    if (options.apply) {
      option.dataset.chatWorkdirApply = "true";
    }
    option.setAttribute("role", "option");
    option.title = options.path;
    const icon = document.createElement("i");
    icon.className = "chat-control-icon";
    icon.dataset.lucide = options.icon || "folder";
    icon.setAttribute("aria-hidden", "true");
    const copy = document.createElement("span");
    copy.className = "chat-workdir-option-copy";
    const label = document.createElement("span");
    label.className = "chat-workdir-option-name";
    label.textContent = options.label || options.path;
    copy.appendChild(label);
    if (options.meta) {
      const meta = document.createElement("span");
      meta.className = "chat-workdir-option-meta";
      meta.textContent = options.meta;
      copy.appendChild(meta);
    }
    option.appendChild(icon);
    option.appendChild(copy);
    host.appendChild(option);
    return option;
  };

  const appendWorkdirSectionLabel = (host, label) => {
    const node = document.createElement("div");
    node.className = "chat-workdir-section-label";
    node.textContent = label;
    host.appendChild(node);
  };

  const renderWorkdirSuggestions = (form, dir, prefix, directories, rawInput = "") => {
    const host = workdirSuggestionsNode(form);
    if (!(host instanceof HTMLElement)) {
      return;
    }
    const remembered = rememberedWorkdirEntries(rawInput);
    const rememberedPaths = new Set(remembered.map((entry) => entry.path));
    const lowerPrefix = String(prefix || "").toLowerCase();
    const matches = directories
      .filter((name) => lowerPrefix === "" || name.toLowerCase().startsWith(lowerPrefix))
      .filter((name) => !rememberedPaths.has(normalizeWorkdirHistoryPath(joinWorkdirPath(dir, name))))
      .slice(0, 80);
    host.textContent = "";
    if (remembered.length > 0) {
      appendWorkdirSectionLabel(host, t("常用工作区", "Frequent workspaces"));
      remembered.forEach((entry) => {
        appendWorkdirOption(host, {
          path: entry.path,
          label: entry.label,
          meta: entry.label === entry.path ? (entry.count > 1 ? t(`使用 ${entry.count} 次`, `Used ${entry.count} times`) : "") : entry.path,
          icon: entry.icon,
          apply: true,
        });
      });
    }
    if (matches.length > 0) {
      appendWorkdirSectionLabel(host, t("子目录", "Subdirectories"));
    }
    if (remembered.length === 0 && matches.length === 0) {
      const empty = document.createElement("div");
      empty.className = "chat-workdir-empty";
      empty.textContent = t("没有匹配的子目录", "No matching subdirectories");
      host.appendChild(empty);
      return;
    }
    matches.forEach((name) => {
      const fullPath = joinWorkdirPath(dir, name);
      appendWorkdirOption(host, { path: fullPath, label: name, icon: "folder" });
    });
    syncLucideIcons();
  };

  const updateWorkdirSuggestions = async (form = currentForm()) => {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    const input = workdirInput(form);
    if (!(input instanceof HTMLInputElement)) {
      return;
    }
    const rawInput = input.value;
    const { dir, prefix } = splitWorkdirInput(rawInput);
    const token = ++workdirListToken;
    if (rememberedWorkdirEntries(rawInput).length > 0) {
      renderWorkdirSuggestions(form, dir, prefix, [], rawInput);
    }
    setWorkdirStatus(form, t("正在加载目录…", "Loading directories..."));
    try {
      const result = await fetchWorkdirDirectories(form, dir);
      if (token !== workdirListToken) {
        return;
      }
      if (!result) {
        setWorkdirStatus(form, t("请先打开 Claude Code 会话", "Open a Claude Code session first"), true);
        renderWorkdirSuggestions(form, dir, prefix, [], rawInput);
        return;
      }
      setWorkdirStatus(form, "");
      renderWorkdirSuggestions(form, dir, prefix, result.directories, rawInput);
    } catch (error) {
      if (token !== workdirListToken) {
        return;
      }
      setWorkdirStatus(form, String(error?.message || t("无法读取目录", "Unable to read the directory")), true);
      renderWorkdirSuggestions(form, dir, prefix, [], rawInput);
    }
  };

  let workdirSuggestionTimer = null;
  const scheduleWorkdirSuggestions = (form) => {
    if (workdirSuggestionTimer) {
      window.clearTimeout(workdirSuggestionTimer);
    }
    workdirSuggestionTimer = window.setTimeout(() => {
      workdirSuggestionTimer = null;
      void updateWorkdirSuggestions(form);
    }, 160);
  };

  const applyWorkdirOverride = (form, rawValue, { close = true } = {}) => {
    if (!(form instanceof HTMLFormElement)) {
      return false;
    }
    const normalized = normalizeShellWorkingDirectory(rawValue);
    if (normalized === "") {
      setWorkdirStatus(form, t("请输入以 / 开头的绝对路径", "Enter an absolute path starting with /"), true);
      return false;
    }
    form.dataset.chatWorkdirOverride = normalized;
    syncShellContextFields(form);
    renderWorkdirControl(form);
    setWorkdirStatus(form, "");
    if (close) {
      const picker = workdirPicker(form);
      if (picker instanceof HTMLDetailsElement) {
        picker.open = false;
      }
    }
    return true;
  };

  const resetWorkdirOverride = (form, { close = true } = {}) => {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    delete form.dataset.chatWorkdirOverride;
    workdirListCache.clear();
    syncShellContextFields(form);
    renderWorkdirControl(form);
    const input = workdirInput(form);
    if (input instanceof HTMLInputElement) {
      input.value = effectiveWorkingDirectory(form);
    }
    setWorkdirStatus(form, "");
    if (close) {
      const picker = workdirPicker(form);
      if (picker instanceof HTMLDetailsElement) {
        picker.open = false;
      }
    }
  };

  const openWorkdirPicker = (form = currentForm()) => {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    const input = workdirInput(form);
    if (input instanceof HTMLInputElement) {
      input.value = effectiveWorkingDirectory(form);
      window.requestAnimationFrame(() => {
        input.focus();
        input.select();
      });
    }
    void updateWorkdirSuggestions(form);
  };

  const normalizeShellHeight = (value) => {
    const numeric = Number.parseInt(String(value || ""), 10);
    const fallback = readShellHeightPreference();
    const candidate = Number.isFinite(numeric) ? numeric : fallback;
    const viewportCap = window.innerHeight > 0
      ? Math.max(shellMinHeight, Math.floor(window.innerHeight * 0.72))
      : shellDefaultHeight;
    return Math.min(viewportCap, Math.max(shellMinHeight, candidate));
  };

  const preferredShellOpenHeight = () => {
    const viewportDefault = window.innerHeight > 0 ? Math.floor(window.innerHeight * 0.38) : shellDefaultHeight;
    return normalizeShellHeight(Math.max(shellDefaultHeight, viewportDefault, readShellHeightPreference()));
  };

  const applyShellHeight = (form, height, persist = false) => {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    const nextHeight = normalizeShellHeight(height);
    form.style.setProperty("--chat-shell-height", `${nextHeight}px`);
    const dock = shellDock(form);
    if (dock instanceof HTMLElement) {
      dock.style.height = `${nextHeight}px`;
    }
    if (persist) {
      writeShellHeightPreference(nextHeight);
    }
  };

  const setShellMetaValue = (node, value) => {
    if (!(node instanceof HTMLElement)) {
      return;
    }
    node.textContent = String(value || "").trim() || t("未连接", "Not connected");
    node.title = node.textContent;
  };

  const shellInstanceByID = (instanceID) => shellInstances.find((instance) => instance.id === instanceID) || null;

  const activeShellInstance = () => shellInstanceByID(activeShellInstanceID);

  const isShellDockHidden = (form = currentForm()) => {
    const dock = shellDock(form);
    return shellInstances.length > 0 && dock instanceof HTMLElement && dock.hidden === true;
  };

  const generatedShellLabel = (number) => t(`终端 ${number}`, `Terminal ${number}`);

  const generatedShellLabelNumber = (label) => {
    const match = String(label || "").trim().match(/^(?:终端|Terminal)\s+(\d+)$/i);
    if (!match) {
      return 0;
    }
    const number = Number.parseInt(match[1], 10);
    return Number.isFinite(number) && number > 0 ? number : 0;
  };

  const syncShellInstanceCounterFromLabels = () => {
    let maxLabelNumber = 0;
    shellInstances.forEach((instance) => {
      maxLabelNumber = Math.max(maxLabelNumber, generatedShellLabelNumber(instance?.label));
    });
    shellInstanceCounter = Math.max(shellInstanceCounter, maxLabelNumber, shellInstances.length);
  };

  const nextShellLabel = () => {
    syncShellInstanceCounterFromLabels();
    const used = new Set(shellInstances.map((instance) => String(instance?.label || "").trim()).filter(Boolean));
    for (let index = 0; index < 64; index += 1) {
      shellInstanceCounter += 1;
      const label = generatedShellLabel(shellInstanceCounter);
      if (!used.has(label)) {
        return label;
      }
    }
    return generatedShellLabel(shellInstanceCounter);
  };

  const normalizeShellInstanceLabels = () => {
    if (shellInstances.length === 0) {
      shellInstanceCounter = 0;
      return;
    }
    const allGenerated = shellInstances.every((instance) => {
      const label = String(instance?.label || "").trim();
      return label === "" || generatedShellLabelNumber(label) > 0;
    });
    if (allGenerated) {
      shellInstances.forEach((instance, index) => {
        instance.label = generatedShellLabel(index + 1);
      });
      shellInstanceCounter = Math.max(shellInstanceCounter, shellInstances.length);
      return;
    }
    const used = new Set();
    shellInstances.forEach((instance) => {
      const currentLabel = String(instance?.label || "").trim();
      if (currentLabel !== "" && !used.has(currentLabel)) {
        instance.label = currentLabel;
        used.add(currentLabel);
        return;
      }
      let nextNumber = 1;
      let nextLabel = generatedShellLabel(nextNumber);
      while (used.has(nextLabel)) {
        nextNumber += 1;
        nextLabel = generatedShellLabel(nextNumber);
      }
      instance.label = nextLabel;
      used.add(nextLabel);
    });
    syncShellInstanceCounterFromLabels();
  };

  const shellTabMetaText = (instance) => truncateText(
    instance?.statusError && String(instance?.statusMessage || "").trim() !== ""
      ? instance.statusMessage
      : instance?.meta?.containerName
        || instance?.meta?.workerID
        || instance?.meta?.sessionID
        || t("未连接", "Not connected"),
    44,
  );

  const renderShellMeta = (instance) => {
    if (!instance || typeof instance !== "object") {
      return;
    }
    setShellMetaValue(instance.metaNodes?.session, instance.meta?.sessionID);
    setShellMetaValue(instance.metaNodes?.worker, instance.meta?.workerID);
    setShellMetaValue(instance.metaNodes?.container, instance.meta?.containerName);
    setShellMetaValue(instance.metaNodes?.workspace, instance.meta?.currentWorkingDirectory || instance.meta?.workspacePath);
  };

  const createShellInstance = (meta) => ({
    id: makeID("shell"),
    terminalID: String(meta?.terminalID || makeID("terminal")).trim(),
    label: String(meta?.label || "").trim() || nextShellLabel(),
    sessionID: String(meta?.sessionID || "").trim(),
    socketURL: String(meta?.socketURL || "").trim(),
    meta: {
      sessionID: String(meta?.sessionID || "").trim(),
      workerID: String(meta?.workerID || "").trim(),
      containerName: String(meta?.containerName || "").trim(),
      workspacePath: String(meta?.workspacePath || "").trim(),
      currentWorkingDirectory: normalizeShellWorkingDirectory(meta?.currentWorkingDirectory || meta?.cwd || ""),
    },
    statusMessage: t("未连接", "Not connected"),
    statusError: false,
    controller: null,
    panel: null,
    terminalHost: null,
    statusNode: null,
    metaNodes: null,
  });

  const ensureActiveShellInstance = () => {
    if (shellInstanceByID(activeShellInstanceID)) {
      return activeShellInstance();
    }
    activeShellInstanceID = shellInstances.length > 0 ? shellInstances[shellInstances.length - 1].id : "";
    return activeShellInstance();
  };

  const ensureShellPane = (form, instance) => {
    if (!(form instanceof HTMLFormElement) || !instance || typeof instance !== "object") {
      return null;
    }
    if (instance.panel instanceof HTMLElement && instance.terminalHost instanceof HTMLElement && document.contains(instance.panel)) {
      return instance.panel;
    }

    const pane = document.createElement("section");
    pane.className = "chat-shell-pane";
    pane.dataset.chatShellPaneId = instance.id;
    pane.id = `chat-shell-pane-${instance.id}`;
    pane.setAttribute("role", "tabpanel");

    const surface = document.createElement("div");
    surface.className = "chat-shell-panel-surface";

    const terminalHost = document.createElement("div");
    terminalHost.className = "chat-terminal-canvas";
    surface.appendChild(terminalHost);

    pane.appendChild(surface);

    instance.panel = pane;
    instance.terminalHost = terminalHost;
    instance.statusNode = null;
    instance.metaNodes = null;
    return pane;
  };

  const renderShellDockContents = (form) => {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    normalizeShellInstanceLabels();
    const tabsHost = shellTabsHost(form);
    const panelsHost = shellPanelsHost(form);
    if (!(tabsHost instanceof HTMLElement) || !(panelsHost instanceof HTMLElement)) {
      return;
    }

    const activeInstance = ensureActiveShellInstance();
    const tabs = document.createDocumentFragment();
    const panels = [];

    shellInstances.forEach((instance) => {
      const selected = activeInstance ? activeInstance.id === instance.id : false;
      const pane = ensureShellPane(form, instance);
      if (pane instanceof HTMLElement) {
        pane.hidden = !selected;
        pane.classList.toggle("is-active", selected);
        pane.setAttribute("aria-labelledby", `chat-shell-tab-${instance.id}`);
        panels.push(pane);
      }

      const tabItem = document.createElement("div");
      tabItem.className = "chat-shell-panel-tab-item";

      const tab = document.createElement("button");
      tab.className = "chat-shell-panel-tab";
      tab.type = "button";
      tab.dataset.chatShellTabId = instance.id;
      tab.id = `chat-shell-tab-${instance.id}`;
      tab.setAttribute("role", "tab");
      tab.setAttribute("aria-controls", `chat-shell-pane-${instance.id}`);
      tab.setAttribute("aria-selected", selected ? "true" : "false");
      tab.tabIndex = selected ? 0 : -1;
      tab.classList.toggle("is-active", selected);
      tab.classList.toggle("is-error", instance.statusError);
      tab.title = instance.label;

      const tabCopy = document.createElement("span");
      tabCopy.className = "chat-shell-panel-tab-copy";

      const tabTitle = document.createElement("strong");
      tabTitle.className = "chat-shell-panel-tab-title";
      tabTitle.textContent = instance.label;

      tabCopy.appendChild(tabTitle);
      tab.appendChild(tabCopy);

      const close = document.createElement("button");
      close.className = "chat-shell-panel-tab-close";
      close.type = "button";
      close.dataset.chatShellTabClose = instance.id;
      close.setAttribute("aria-label", `${t("关闭", "Close")} ${instance.label}`);
      close.textContent = "×";

      tabItem.appendChild(tab);
      tabItem.appendChild(close);
      tabs.appendChild(tabItem);
    });

    panelsHost.replaceChildren(...panels);

    tabsHost.replaceChildren(tabs);
    scheduleActiveShellLayout();
  };

  const refreshActiveShellController = () => {
    const instance = activeShellInstance();
    instance?.controller?.refresh?.();
  };

  const scheduleActiveShellLayout = () => {
    refreshActiveShellController();
    window.setTimeout(() => {
      refreshActiveShellController();
    }, 64);
  };

  const setShellDockVisible = (form, visible) => {
    const dock = shellDock(form);
    if (!(dock instanceof HTMLElement) || !(form instanceof HTMLFormElement)) {
      return;
    }
    const shouldShow = visible && shellInstances.length > 0;
    dock.hidden = !shouldShow;
    form.classList.toggle("is-shell-open", shouldShow);
    if (!shouldShow) {
      return;
    }
    applyShellHeight(form, preferredShellOpenHeight());
    scheduleActiveShellLayout();
  };

  const hideShellDock = (form) => {
    setShellDockVisible(form, false);
    writeShellState(form);
    updateComposerControls(form);
  };

  const ensureShellController = (form, instance) => {
    if (!(form instanceof HTMLFormElement) || !instance || typeof instance !== "object") {
      return null;
    }
    const pane = ensureShellPane(form, instance);
    if (!(pane instanceof HTMLElement) || !(instance.terminalHost instanceof HTMLElement)) {
      return null;
    }
    if (instance.controller && document.contains(instance.terminalHost)) {
      return instance.controller;
    }
    if (!window.AIYoloChatShell || typeof window.AIYoloChatShell.createController !== "function") {
      setInlineFlash(currentRoot(), t("Shell 依赖加载失败。", "Shell dependencies failed to load."), true);
      return null;
    }
    instance.controller = window.AIYoloChatShell.createController({
      terminalHost: instance.terminalHost,
      statusNode: instance.statusNode,
      getSocketPath: () => instance.socketURL,
      autoConnect: false,
      onStatusChange: ({ message, isError }) => {
        instance.statusMessage = String(message || "").trim() || t("未连接", "Not connected");
        instance.statusError = Boolean(isError);
        renderShellDockContents(form);
      },
      onOutput: (chunk) => {
        processShellCwdReports(form, instance, chunk);
      },
    });
    return instance.controller;
  };

  const selectShellInstance = (form, instanceID) => {
    if (!(form instanceof HTMLFormElement) || !shellInstanceByID(instanceID)) {
      return;
    }
    activeShellInstanceID = instanceID;
    renderShellDockContents(form);
    setShellDockVisible(form, true);
    writeShellState(form);
  };

  const disposeShellController = (instance, options = {}) => {
    if (!instance || typeof instance !== "object") {
      return;
    }
    instance.controller?.dispose?.({ terminate: options.terminate === true });
    instance.controller = null;
    instance.panel?.remove?.();
    instance.panel = null;
    instance.terminalHost = null;
    instance.statusNode = null;
    instance.metaNodes = null;
  };

  const disposeAllShellControllers = (options = {}) => {
    shellInstances.forEach((instance) => {
      disposeShellController(instance, { terminate: options.terminate === true });
    });
    shellInstances = [];
    activeShellInstanceID = "";
  };

  const closeShellInstance = (form, instanceID, options = {}) => {
    const index = shellInstances.findIndex((instance) => instance.id === instanceID);
    if (index < 0) {
      return;
    }
    const [instance] = shellInstances.splice(index, 1);
    disposeShellController(instance, { terminate: options.terminate !== false });
    if (activeShellInstanceID === instanceID) {
      const fallback = shellInstances[Math.max(0, index - 1)] || shellInstances[index] || null;
      activeShellInstanceID = fallback ? fallback.id : "";
    }
    renderShellDockContents(form);
    setShellDockVisible(form, shellInstances.length > 0);
    if (options.forget !== false) {
      writeShellState(form);
    }
  };

  const closeAllChatShells = (form, options = {}) => {
    disposeAllShellControllers({ terminate: options.terminate === true });
    renderShellDockContents(form);
    setShellDockVisible(form, false);
    if (options.forget !== false) {
      writeShellState(form);
    }
  };

  const closeChatShellDock = (form) => {
    const instance = activeShellInstance();
    if (!instance) {
      setShellDockVisible(form, false);
      return;
    }
    closeShellInstance(form, instance.id);
  };

  const shellSocketURLForTerminal = (form, sessionID, terminalID, fallback = "") => {
    const baseSocketURL = shellSocketBaseURL(form);
    if (baseSocketURL === "" || String(sessionID || "").trim() === "") {
      return String(fallback || "").trim();
    }
    const nextURL = new URL(baseSocketURL, window.location.href);
    nextURL.searchParams.set("session", String(sessionID || "").trim());
    nextURL.searchParams.set("terminal", String(terminalID || "").trim() || "default");
    return nextURL.toString();
  };

  const restoreShellInstancesFromState = (form, rawState, options = {}) => {
    if (!(form instanceof HTMLFormElement) || !canUseCloudAgentShell(form)) {
      return false;
    }
    const state = normalizeShellState(rawState);
    if (state.instances.length === 0) {
      return false;
    }
    disposeAllShellControllers({ terminate: false });
    shellInstances = state.instances.map((snapshot) => createShellInstance({
      terminalID: snapshot.terminalID,
      label: snapshot.label,
      sessionID: snapshot.sessionID,
      socketURL: shellSocketURLForTerminal(form, snapshot.sessionID, snapshot.terminalID, snapshot.socketURL),
      workerID: snapshot.meta.workerID,
      containerName: snapshot.meta.containerName,
      workspacePath: snapshot.meta.workspacePath,
      currentWorkingDirectory: snapshot.meta.currentWorkingDirectory,
    }));
    shellInstanceCounter = Math.max(shellInstanceCounter, shellInstances.length);
    const active = shellInstances.find((instance) => instance.terminalID === state.activeTerminalID) || shellInstances[0] || null;
    activeShellInstanceID = active ? active.id : "";
    renderShellDockContents(form);
    setShellDockVisible(form, state.hidden !== true);
    shellInstances.forEach((instance) => {
      ensureShellController(form, instance)?.connect?.({ resetTerminal: true });
    });
    if (options.persist !== false) {
      writeShellState(form);
    }
    return shellInstances.length > 0;
  };

  const restoreShellInstances = (form) => {
    if (!(form instanceof HTMLFormElement) || !canUseCloudAgentShell(form)) {
      return false;
    }
    const stateKey = currentShellStateKey(form);
    if (stateKey === "") {
      return false;
    }
    return restoreShellInstancesFromState(form, readShellStates()[stateKey]);
  };

  const loadShellStateFromServer = async (form) => {
    const sessionID = readClientSessionID(form);
    const endpoint = shellStateURL(form);
    if (!(form instanceof HTMLFormElement) || sessionID === "" || endpoint === "" || !canUseCloudAgentShell(form)) {
      return null;
    }
    try {
      const stateURL = new URL(endpoint, window.location.href);
      stateURL.searchParams.set("session", sessionID);
      const response = await fetchWithTimeout(stateURL.toString(), {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      }, shellReadyProbeTimeoutMs);
      if (!response.ok) {
        return null;
      }
      const parsed = safeParseJSON(await response.text(), null);
      if (!parsed || typeof parsed !== "object" || String(parsed.status || "").trim() !== "ready") {
        return null;
      }
      if (String(parsed.environment || "").trim() !== currentSelectedEnvironment(form)) {
        return null;
      }
      return normalizeShellState(parsed.shellState || parsed.state || null);
    } catch (_error) {
      return null;
    }
  };

  const restoreShellInstancesFromServer = async (form) => {
    if (shellInstances.length > 0) {
      return true;
    }
    const state = await loadShellStateFromServer(form);
    if (!state || state.instances.length === 0 || shellInstances.length > 0) {
      return false;
    }
    return restoreShellInstancesFromState(form, state);
  };

  const restoreShellInstancesOrLoadServer = (form) => {
    if (restoreShellInstances(form)) {
      return true;
    }
    void restoreShellInstancesFromServer(form);
    return false;
  };

  const syncChatShellSession = (form) => {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    applyShellHeight(form, preferredShellOpenHeight());
    const instance = activeShellInstance();
    if (!instance) {
      restoreShellInstancesOrLoadServer(form);
      return;
    }
    if (instance.sessionID === "" || instance.sessionID === readClientSessionID(form)) {
      return;
    }
    closeAllChatShells(form, { terminate: false, forget: false });
    restoreShellInstancesOrLoadServer(form);
  };

  const readDraftPayload = (form, options = {}) => {
    const draftField = form?.querySelector("#chat-draft");
    const rawVisibleText = ownProperty.call(options, "userVisibleText")
      ? options.userVisibleText
      : draftField instanceof HTMLTextAreaElement
        ? draftField.value
        : "";
    const rawPrompt = ownProperty.call(options, "prompt") ? options.prompt : rawVisibleText;
    const rawAttachments = ownProperty.call(options, "attachments")
      ? options.attachments
      : readHiddenJSON(form, "chat_draft_attachments_json", []);
    return {
      userVisibleText: String(rawVisibleText || "").trim(),
      prompt: String(rawPrompt || "").trim(),
      attachments: Array.isArray(rawAttachments) ? rawAttachments.map(normalizeAttachment).filter(Boolean) : [],
    };
  };

  const hasDraftPayload = (payload) => Boolean(
    String(payload?.prompt || "").trim()
    || (Array.isArray(payload?.attachments) && payload.attachments.length > 0),
  );

  const normalizeQueuedTurn = (turn = {}) => ({
    id: String(turn.id || makeID("queue")).trim(),
    prompt: String(turn.prompt || "").trim(),
    userVisibleText: String(turn.userVisibleText || turn.prompt || "").trim(),
    attachments: Array.isArray(turn.attachments) ? turn.attachments.map(normalizeAttachment).filter(Boolean) : [],
  });

  const queueTurnPreview = (turn) => {
    const text = String(turn?.userVisibleText || turn?.prompt || "").trim();
    if (text !== "") {
      return text.length > 120 ? `${text.slice(0, 117)}...` : text;
    }
    const count = Array.isArray(turn?.attachments) ? turn.attachments.length : 0;
    return count > 0 ? t(`已附带 ${count} 个附件`, `${count} attachment(s)`) : t("(空消息)", "(Empty message)");
  };

  const preserveSessionQueue = (sessionID) => {
    const state = getSessionStreamState(sessionID);
    if (!state?.queuedTurns?.length) {
      return [];
    }
    return state.queuedTurns.map(normalizeQueuedTurn);
  };

  const restoreSessionQueue = (sessionID, queuedTurns) => {
    if (!Array.isArray(queuedTurns) || queuedTurns.length === 0) {
      return;
    }
    ensureSessionStreamState(sessionID).queuedTurns = queuedTurns.map(normalizeQueuedTurn);
  };

  const releaseSessionStreamRuntime = (state) => {
    if (!state?.sessionID) {
      return;
    }
    const queuedTurns = preserveSessionQueue(state.sessionID);
    const preemptTurn = state.preemptTurn ? normalizeQueuedTurn(state.preemptTurn) : null;
    sessionStreamStates.delete(state.sessionID);
    restoreSessionQueue(state.sessionID, queuedTurns);
    if (preemptTurn) {
      ensureSessionStreamState(state.sessionID).preemptTurn = preemptTurn;
    }
  };

  const renderComposerQueue = (form = currentForm()) => {
    if (!form) {
      return;
    }
    const panel = composerQueuePanel(form);
    const list = composerQueueList(form);
    const sessionID = readClientSessionID(form);
    const streamState = getSessionStreamState(sessionID);
    const turns = streamState?.queuedTurns || [];
    if (panel instanceof HTMLElement) {
      panel.hidden = turns.length === 0;
      const clearButton = panel.querySelector("[data-chat-action=\"clear-queue\"]");
      if (clearButton instanceof HTMLButtonElement) {
        clearButton.hidden = turns.length === 0;
      }
    }
    const indicator = composerQueueIndicator(form);
    if (indicator instanceof HTMLElement) {
      indicator.textContent = turns.length > 0 ? t(`已排队 ${turns.length} 条`, `Queued ${turns.length}`) : "";
    }
    if (!(list instanceof HTMLElement)) {
      return;
    }
    list.replaceChildren();
    turns.forEach((turn, index) => {
      const item = document.createElement("li");
      item.className = "chat-composer-queue-item";
      item.dataset.chatQueueId = turn.id;

      const copy = document.createElement("div");
      copy.className = "chat-composer-queue-copy";
      const title = document.createElement("strong");
      title.textContent = t(`#${index + 1}`, `#${index + 1}`);
      const preview = document.createElement("span");
      preview.textContent = queueTurnPreview(turn);
      copy.appendChild(title);
      copy.appendChild(preview);

      const actions = document.createElement("div");
      actions.className = "chat-composer-queue-actions";

      const preemptButton = document.createElement("button");
      preemptButton.type = "button";
      preemptButton.className = "chat-composer-queue-action";
      preemptButton.dataset.chatAction = "preempt-queue-item";
      preemptButton.dataset.chatQueueId = turn.id;
      preemptButton.title = t("立即发送", "Send now");
      preemptButton.setAttribute("aria-label", t("立即发送", "Send now"));
      preemptButton.innerHTML = '<i class="chat-control-icon" data-lucide="zap" aria-hidden="true"></i>';

      const cancelButton = document.createElement("button");
      cancelButton.type = "button";
      cancelButton.className = "chat-composer-queue-action is-danger";
      cancelButton.dataset.chatAction = "cancel-queue-item";
      cancelButton.dataset.chatQueueId = turn.id;
      cancelButton.title = t("取消", "Cancel");
      cancelButton.setAttribute("aria-label", t("取消排队", "Cancel queued turn"));
      cancelButton.innerHTML = '<i class="chat-control-icon" data-lucide="x" aria-hidden="true"></i>';

      actions.appendChild(preemptButton);
      actions.appendChild(cancelButton);
      item.appendChild(copy);
      item.appendChild(actions);
      list.appendChild(item);
    });
    syncLucideIcons();
  };

  const cancelQueuedTurn = (sessionID, turnID) => {
    const normalizedSessionID = normalizeSessionStreamID(sessionID);
    const streamState = getSessionStreamState(normalizedSessionID);
    if (!streamState?.queuedTurns?.length) {
      return false;
    }
    const nextQueue = streamState.queuedTurns.filter((turn) => turn.id !== turnID);
    if (nextQueue.length === streamState.queuedTurns.length) {
      return false;
    }
    streamState.queuedTurns = nextQueue;
    renderComposerQueue(currentForm());
    updateComposerControls(currentForm());
    return true;
  };

  const clearQueuedTurns = (sessionID) => {
    const normalizedSessionID = normalizeSessionStreamID(sessionID);
    const streamState = getSessionStreamState(normalizedSessionID);
    if (!streamState?.queuedTurns?.length) {
      return false;
    }
    streamState.queuedTurns = [];
    renderComposerQueue(currentForm());
    updateComposerControls(currentForm());
    return true;
  };

  const requestStreamPreempt = (sessionID, turn) => {
    const normalizedSessionID = normalizeSessionStreamID(sessionID);
    const normalizedTurn = normalizeQueuedTurn(turn);
    const state = getSessionStreamState(normalizedSessionID);
    if (state && isSessionStreamActive(normalizedSessionID)) {
      state.preemptTurn = normalizedTurn;
      stopSessionStream(normalizedSessionID);
      return true;
    }
    const form = currentForm();
    if (form && isSessionVisible(normalizedSessionID)) {
      void streamConsoleChat(form, normalizedTurn);
      return true;
    }
    return false;
  };

  const preemptQueuedTurn = (sessionID, turnID) => {
    const normalizedSessionID = normalizeSessionStreamID(sessionID);
    const streamState = getSessionStreamState(normalizedSessionID);
    if (!streamState?.queuedTurns?.length) {
      return false;
    }
    const index = streamState.queuedTurns.findIndex((turn) => turn.id === turnID);
    if (index < 0) {
      return false;
    }
    const [turn] = streamState.queuedTurns.splice(index, 1);
    renderComposerQueue(currentForm());
    updateComposerControls(currentForm());
    return requestStreamPreempt(normalizedSessionID, turn);
  };

  const preemptDraftTurn = (form) => {
    if (!form) {
      return false;
    }
    const payload = readDraftPayload(form);
    if (!hasDraftPayload(payload)) {
      updateComposerControls(form);
      return false;
    }
    const sessionID = readClientSessionID(form);
    clearComposerDraft(form);
    return requestStreamPreempt(sessionID, payload);
  };

  const updateComposerControls = (form = currentForm()) => {
    if (!form) {
      return;
    }
    const button = composerPrimaryButton(form);
    const preemptButton = composerPreemptButton(form);
    const startGlyph = composerPrimaryStartGlyph(form);
    const stopGlyph = composerPrimaryStopGlyph(form);
    const shellButton = shellLaunchButton(form);
    const sessionID = readClientSessionID(form);
    const streaming = isSessionStreamActive(sessionID);
    const payload = readDraftPayload(form);
    const hasDraft = hasDraftPayload(payload);

    if (preemptButton instanceof HTMLButtonElement) {
      preemptButton.hidden = !(streaming && hasDraft);
      preemptButton.disabled = !hasDraft;
    }
    if (button instanceof HTMLButtonElement) {
      if (streaming) {
        button.hidden = false;
        button.disabled = false;
        button.classList.add("is-stop");
        button.setAttribute("aria-label", t("终止当前回复", "Stop current reply"));
      } else {
        const shouldShow = hasDraft;
        button.hidden = !shouldShow;
        button.disabled = !shouldShow || currentSelectedModel(form) === "";
        button.classList.remove("is-stop");
        button.setAttribute("aria-label", t("开始发送", "Start sending"));
      }
    }
    if (startGlyph instanceof HTMLElement) {
      startGlyph.hidden = streaming;
    }
    if (stopGlyph instanceof HTMLElement) {
      stopGlyph.hidden = !streaming;
    }
    renderComposerQueue(form);
    if (shellButton instanceof HTMLButtonElement) {
      const hasShellSocket = shellSocketBaseURL(form) !== "";
      const showShellButton = hasShellSocket && isCloudAgentEnvironment(currentSelectedEnvironment(form));
      const hiddenShellReady = isShellDockHidden(form);
      const environmentOpening = environmentEnsureInFlight !== null;
      const canOpenShell = showShellButton && (hiddenShellReady || (currentSelectedModel(form) !== "" && !shellOpenInFlight && !environmentOpening));
      shellButton.hidden = !showShellButton;
      shellButton.disabled = !canOpenShell;
      shellButton.title = hiddenShellReady
        ? t("显示 Claude Code 终端", "Show the Claude Code terminal")
        : environmentOpening
        ? t("正在启动 Claude Code…", "Starting the Claude Code…")
        : shellOpenInFlight
        ? t("正在打开 Claude Code…", "Opening the Claude Code…")
        : canOpenShell
        ? t("打开 Claude Code 终端", "Open the Claude Code terminal")
        : t("切换到 Cloud Agent 后即可打开 Claude Code 终端", "Switch to Cloud Agent to open the Claude Code terminal");
      shellButton.setAttribute("aria-label", hiddenShellReady
        ? t("显示 Claude Code 终端", "Show the Claude Code terminal")
        : t("打开 Claude Code 终端", "Open the Claude Code terminal"));
    }
    const activitybarTerminalButton = form.querySelector(".chat-activitybar-terminal-toggle");
    if (activitybarTerminalButton instanceof HTMLButtonElement) {
      const hasShellSocket = shellSocketBaseURL(form) !== "";
      const showTerminalToggle = hasShellSocket && isCloudAgentEnvironment(currentSelectedEnvironment(form));
      activitybarTerminalButton.hidden = !showTerminalToggle;
      const dockVisible = !isShellDockHidden(form);
      activitybarTerminalButton.classList.toggle("is-active", dockVisible);
    }
    if (typeof window.AIYoloChatBrowser?.sync === "function") {
      window.AIYoloChatBrowser.sync(form);
    }
    if (typeof window.AIYoloChatBrowser?.syncMCP === "function") {
      window.AIYoloChatBrowser.syncMCP(form);
    }
    const workdirGroupNode = workdirGroup(form);
    if (workdirGroupNode instanceof HTMLElement) {
      const showWorkdir = isCloudAgentEnvironment(currentSelectedEnvironment(form));
      workdirGroupNode.hidden = !showWorkdir;
      if (showWorkdir) {
        renderWorkdirControl(form);
      } else if (form.dataset.chatWorkdirOverride) {
        delete form.dataset.chatWorkdirOverride;
      }
    }
  };

  const routeMap = (form) => {
    const routes = new Map();
    pickerFormControls(form, "input[name=\"chat_public_name\"]").forEach((input) => {
      if (!(input instanceof HTMLInputElement)) {
        return;
      }
      const option = input.closest(".chat-model-picker-option");
      const publicName = input.value.trim();
      if (publicName === "") {
        return;
      }
      const detail = option?.querySelector(".chat-model-detail")?.textContent || "";
      const detailParts = detail.split("·");
      routes.set(publicName, {
        publicName,
        providerName: option?.dataset.chatProviderName?.trim() || detailParts[0]?.trim() || "",
        upstreamModel: option?.dataset.chatUpstreamModel?.trim() || detailParts.slice(1).join("·").trim() || publicName,
        reasoningEfforts: parseReasoningEfforts(option?.dataset.chatReasoningEfforts),
        imageGeneration: option?.dataset.chatImageGeneration === "true",
      });
    });
    return routes;
  };

  const refreshModelCardStates = (form) => {
    pickerFormControls(form, "input[name=\"chat_public_name\"]").forEach((input) => {
      if (!(input instanceof HTMLInputElement)) {
        return;
      }
      const option = input.closest(".chat-model-picker-option");
      option?.classList.toggle("is-active", input.checked);
    });
  };

  const setSelectedModel = (form, publicName) => {
    let matched = false;
    pickerFormControls(form, "input[name=\"chat_public_name\"]").forEach((input) => {
      if (!(input instanceof HTMLInputElement)) {
        return;
      }
      const active = input.value.trim() === publicName;
      input.checked = active;
      if (active) {
        matched = true;
      }
    });
    if (!matched) {
      const first = pickerFormControls(form, "input[name=\"chat_public_name\"]")[0];
      if (first instanceof HTMLInputElement) {
        first.checked = true;
      }
    }
    refreshModelCardStates(form);
  };

  const normalizeAttachment = (attachment) => {
    if (!attachment || typeof attachment !== "object") {
      return null;
    }
    const objectKey = String(attachment.objectKey || "").trim();
    const url = String(attachment.url || "").trim();
    if (!objectKey || !url) {
      return null;
    }
    return {
      id: String(attachment.id || makeID("att")).trim(),
      name: String(attachment.name || objectKey.split("/").pop() || t("附件", "Attachment")).trim(),
      objectKey,
      url,
      browserUrl: String(attachment.browserUrl || attachment.url || "").trim() || url,
      mediaType: String(attachment.mediaType || "application/octet-stream").trim(),
      sizeBytes: Number(attachment.sizeBytes) > 0 ? Number(attachment.sizeBytes) : 0,
    };
  };

  const normalizeMessage = (message) => {
    if (!message || typeof message !== "object") {
      return null;
    }
    const role = String(message.role || "").trim().toLowerCase();
    if (!["user", "assistant", "system"].includes(role)) {
      return null;
    }
    const attachments = Array.isArray(message.attachments)
      ? message.attachments.map(normalizeAttachment).filter(Boolean)
      : [];
    const operations = Array.isArray(message.operations)
      ? message.operations.map(normalizeOperation).filter(Boolean)
      : [];
    const content = String(message.content || "").trim();
    const reasoning = String(message.reasoning || "").trim();
    if (!content && !reasoning && attachments.length === 0 && operations.length === 0) {
      return null;
    }
    return {
      id: String(message.id || makeID("msg")).trim(),
      role,
      label: String(message.label || "").trim() || roleLabel(role),
      content,
      reasoning,
      operations,
      attachments,
    };
  };

  const normalizeOperation = (operation) => {
    if (!operation || typeof operation !== "object") {
      return null;
    }
    const id = String(operation.id || "").trim();
    const name = String(operation.name || "").trim();
    if (id === "" && name === "") {
      return null;
    }
    return {
      id: id || `${name}-${makeID("op")}`,
      name: name || "tool",
      status: String(operation.status || "started").trim().toLowerCase(),
      detail: String(operation.detail || "").trim(),
      category: String(operation.category || "tool").trim().toLowerCase(),
      url: String(operation.url || "").trim(),
      screenshotUrl: String(operation.screenshotUrl || "").trim(),
    };
  };

  const roleLabel = (role) => {
    switch (role) {
      case "assistant":
        return currentForm()?.dataset.chatAssistantLabel || "AIYolo";
      case "system":
        return t("系统", "System");
      default:
        return currentForm()?.dataset.chatUserLabel || "You";
    }
  };

  const normalizeSession = (session, form, routes, existingSession = null) => {
    const fallbackRoute = currentSelectedModel(form);
    const publicName = String(session.publicName || existingSession?.publicName || fallbackRoute || "").trim();
    const resolvedRoute = routes.has(publicName) ? publicName : fallbackRoute;
    const route = routes.get(resolvedRoute) || null;
    const fallbackReasoningEffort = String(session.id || existingSession?.id || "").trim() === readClientSessionID(form)
      ? currentSelectedReasoningEffort(form)
      : "";
    const draftAttachments = Array.isArray(session.draftAttachments)
      ? session.draftAttachments.map(normalizeAttachment).filter(Boolean)
      : Array.isArray(session.attachments)
        ? session.attachments.map(normalizeAttachment).filter(Boolean)
        : [];
    const messages = Array.isArray(session.messages)
      ? session.messages.map(normalizeMessage).filter(Boolean)
      : [];
    const createdAt = String(session.createdAt || existingSession?.createdAt || nowISO());
    const normalized = {
      id: String(session.id || existingSession?.id || readClientSessionID(form) || makeID("chat")).trim(),
      title: String(session.title || existingSession?.title || "").trim(),
      customTitle: Boolean(session.customTitle || existingSession?.customTitle),
      publicName: resolvedRoute,
      environment: String(session.environment || existingSession?.environment || currentSelectedEnvironment(form) || "local").trim() || "local",
      reasoningEffort: normalizeReasoningEffort(session.reasoningEffort || existingSession?.reasoningEffort || fallbackReasoningEffort, route?.reasoningEfforts || []),
      systemPrompt: String(session.systemPrompt || existingSession?.systemPrompt || defaultSystemPrompt(form) || "").trim(),
      draft: String(session.draft || "").trim(),
      draftAttachments,
      messages,
      status: String(session.status || existingSession?.status || "").trim(),
      lastError: String(session.lastError || existingSession?.lastError || "").trim(),
      createdAt,
      updatedAt: String(session.updatedAt || existingSession?.updatedAt || createdAt || nowISO()),
    };
    if (!normalized.customTitle || !normalized.title) {
      normalized.title = deriveSessionTitle(normalized);
    }
    return normalized;
  };

  const deriveSessionTitle = (session) => {
    if (session.customTitle && String(session.title || "").trim() !== "") {
      return String(session.title).trim();
    }
    const firstUserMessage = (session.messages || []).find((message) => message.role === "user" && String(message.content || "").trim() !== "");
    if (firstUserMessage) {
      return truncateText(markdownToPlainText(firstUserMessage.content) || firstUserMessage.content);
    }
    const firstAttachment = (session.messages || []).find((message) => message.role === "user" && Array.isArray(message.attachments) && message.attachments.length > 0);
    if (firstAttachment && firstAttachment.attachments[0]?.name) {
      return truncateText(firstAttachment.attachments[0].name);
    }
    return t("新会话", "New chat");
  };

  const sessionHasMessages = (session) => Array.isArray(session.messages)
    && session.messages.some((message) => String(message.content || "").trim() !== ""
      || Array.isArray(message.attachments) && message.attachments.length > 0);

  const sessionHasDraft = (session) => String(session.draft || "").trim() !== ""
    || Array.isArray(session.draftAttachments) && session.draftAttachments.length > 0;

  const isPersistedSession = (session) => Boolean(session)
    && (sessionHasMessages(session) || sessionHasDraft(session));

  const isCloudAgentEnvironment = (value) => String(value || "").trim().startsWith("cloud-agent:");

  const sessionIsStreaming = (session) => String(session?.status || "").trim() === "streaming";

  const sessionIsRecoverableCloudAgentStream = (session) => {
    if (!session || !isCloudAgentEnvironment(session.environment)) {
      return false;
    }
    const status = String(session?.status || "").trim();
    if (status === "completed" || status === "failed") {
      return false;
    }
    if (status === "streaming" || status === "interrupted") {
      return true;
    }
    const messages = Array.isArray(session.messages) ? session.messages : [];
    const lastMessage = messages[messages.length - 1];
    return lastMessage?.role === "user";
  };

  const sessionSortTime = (value) => {
    const timestamp = Date.parse(String(value || "").trim());
    return Number.isNaN(timestamp) ? 0 : timestamp;
  };

  const sessionMessageSignature = (messages) => JSON.stringify(
    Array.isArray(messages) ? messages.map(normalizeMessage).filter(Boolean) : [],
  );

  const sessionHasMessageActivity = (existingSession, messages) => {
    const nextSignature = sessionMessageSignature(messages);
    const previousSignature = sessionMessageSignature(existingSession?.messages);
    return nextSignature !== previousSignature;
  };

  const compareSessionsByRecency = (left, right) => {
    const updatedDelta = sessionSortTime(right?.updatedAt) - sessionSortTime(left?.updatedAt);
    if (updatedDelta !== 0) {
      return updatedDelta;
    }
    const createdDelta = sessionSortTime(right?.createdAt) - sessionSortTime(left?.createdAt);
    if (createdDelta !== 0) {
      return createdDelta;
    }
    return String(left?.id || "").localeCompare(String(right?.id || ""));
  };

  const sortSessionsByRecency = (sessions) => [...sessions].sort(compareSessionsByRecency);

  const displaySessions = (store) => sortSessionsByRecency(Array.isArray(store?.sessions) ? store.sessions : []);

  const emptyStore = () => ({ version: 1, activeSessionId: "", sessions: [] });

  const loadStore = () => {
    const field = sessionStoreField();
    if (!(field instanceof HTMLInputElement)) {
      return emptyStore();
    }
    return safeParseJSON(field.value, emptyStore());
  };

  const saveStore = (store) => {
    const field = sessionStoreField();
    if (!(field instanceof HTMLInputElement)) {
      return;
    }
    field.value = JSON.stringify(store);
  };

  const syncSessionURL = (session) => {
    if (typeof window === "undefined" || !window.history || typeof window.history.replaceState !== "function") {
      return;
    }
    const nextURL = new URL(window.location.href);
    const sessionID = isPersistedSession(session) ? String(session?.id || "").trim() : "";
    if (sessionID === "") {
      nextURL.searchParams.delete("session");
    } else {
      nextURL.searchParams.set("session", sessionID);
    }
    const currentValue = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    const nextValue = `${nextURL.pathname}${nextURL.search}${nextURL.hash}`;
    if (nextValue !== currentValue) {
      window.history.replaceState(window.history.state, "", nextValue);
    }
  };

  const mergeSavedSessionMetadata = (serverSession) => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form || !serverSession || typeof serverSession !== "object") {
      return;
    }
    const sessionID = String(serverSession.id || "").trim();
    if (sessionID === "") {
      return;
    }
    const routes = routeMap(form);
    let store = normalizeStore(loadStore(), form, routes);
    const existing = store.sessions.find((session) => session.id === sessionID);
    if (!existing) {
      return;
    }
    const merged = {
      ...existing,
      title: String(serverSession.title || existing.title || "").trim(),
      customTitle: Boolean(serverSession.customTitle || existing.customTitle),
      status: String(serverSession.status || existing.status || "").trim(),
      lastError: String(serverSession.lastError || existing.lastError || "").trim(),
      updatedAt: String(serverSession.updatedAt || existing.updatedAt || "").trim() || existing.updatedAt,
    };
    store = upsertSession(store, merged);
    saveStore(store);
    renderSessionList(root, store);
  };

  const saveSessionToServer = async (session) => {
    const form = currentForm();
    if (!form || !session || typeof session !== "object") {
      return;
    }
    const saveURL = String(form.dataset.chatSessionSaveUrl || "").trim();
    if (saveURL === "") {
      return;
    }
    try {
      const response = await fetch(saveURL, {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
        },
        body: JSON.stringify(session),
      });
      const responseBody = await response.text();
      const parsed = safeParseJSON(responseBody, null);
      if (!response.ok || !parsed || typeof parsed !== "object") {
        return;
      }
      mergeSavedSessionMetadata(parsed);
    } catch (_error) {
      // Keep the current in-page state even if the background sync fails.
    }
  };

  const deleteSessionOnServer = async (sessionID) => {
    const form = currentForm();
    if (!form || String(sessionID || "").trim() === "") {
      return;
    }
    const saveURL = String(form.dataset.chatSessionSaveUrl || "").trim();
    if (saveURL === "") {
      return;
    }
    try {
      await fetch(`${saveURL}/${encodeURIComponent(String(sessionID).trim())}`, {
        method: "DELETE",
        credentials: "same-origin",
      });
    } catch (_error) {
      // Keep the current in-page state even if the background sync fails.
    }
  };

  const normalizeStore = (rawStore, form, routes) => {
    const store = rawStore && typeof rawStore === "object" ? rawStore : emptyStore();
    const sessions = Array.isArray(store.sessions)
      ? store.sessions
        .map((session) => normalizeSession(session, form, routes))
        .filter((session) => isPersistedSession(session))
      : [];
    return {
      version: 1,
      activeSessionId: String(store.activeSessionId || "").trim(),
      sessions: sortSessionsByRecency(sessions),
    };
  };

  const upsertSession = (store, session) => {
    const next = {
      version: 1,
      activeSessionId: String(store.activeSessionId || session.id).trim() || session.id,
      sessions: store.sessions.filter((item) => item.id !== session.id),
    };
    next.sessions.unshift(session);
    next.sessions = sortSessionsByRecency(next.sessions);
    if (next.sessions.length > sessionLimit) {
      next.sessions = next.sessions.slice(0, sessionLimit);
    }
    return next;
  };

  const createBlankSession = (form, routes) => normalizeSession({
    id: makeID("chat"),
    title: t("新会话", "New chat"),
    customTitle: false,
    publicName: currentSelectedModel(form),
    environment: currentSelectedEnvironment(form),
    reasoningEffort: currentSelectedReasoningEffort(form),
    systemPrompt: defaultSystemPrompt(form),
    draft: "",
    draftAttachments: [],
    messages: [],
    createdAt: nowISO(),
    updatedAt: nowISO(),
  }, form, routes);

  const captureSessionFromDOM = (form, store, routes) => {
    const existingSession = store.sessions.find((session) => session.id === readClientSessionID(form)) || null;
    const draftField = form.querySelector("#chat-draft");
    const messages = readHiddenJSON(form, "chat_history_json", []).map(normalizeMessage).filter(Boolean);
    const createdAt = existingSession?.createdAt || nowISO();
    const updatedAt = sessionHasMessageActivity(existingSession, messages)
      ? nowISO()
      : String(existingSession?.updatedAt || createdAt).trim() || createdAt;
    const session = normalizeSession({
      id: readClientSessionID(form) || existingSession?.id || makeID("chat"),
      title: existingSession?.title || "",
      customTitle: existingSession?.customTitle || false,
      publicName: currentSelectedModel(form),
      environment: currentSelectedEnvironment(form),
      reasoningEffort: currentSelectedReasoningEffort(form),
      systemPrompt: String((form.querySelector("textarea[name=\"chat_system_prompt\"]")?.value || defaultSystemPrompt(form) || "")).trim(),
      draft: draftField instanceof HTMLTextAreaElement ? draftField.value : "",
      draftAttachments: readHiddenJSON(form, "chat_draft_attachments_json", []),
      messages,
      createdAt,
      updatedAt,
    }, form, routes, existingSession);
    writeClientSessionID(form, session.id);
    writeHiddenJSON(form, "chat_history_json", session.messages);
    writeHiddenJSON(form, "chat_draft_attachments_json", session.draftAttachments);
    return session;
  };

  const buildAttachmentChip = (attachment, removable = false) => {
    const element = removable ? document.createElement("div") : document.createElement("a");
    element.className = "chat-attachment-chip";
    if (!removable) {
      element.href = attachment.browserUrl || attachment.url;
      element.target = "_blank";
      element.rel = "noreferrer";
    }

    const copy = document.createElement("div");
    copy.className = "chat-attachment-chip-copy";

    const name = document.createElement("strong");
    name.className = "chat-attachment-name";
    name.textContent = attachment.name;

    const meta = document.createElement("span");
    meta.className = "chat-attachment-meta";
    meta.textContent = attachment.mediaType;

    copy.appendChild(name);
    copy.appendChild(meta);

    element.appendChild(copy);

    if (removable) {
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "chat-draft-attachment-remove";
      remove.dataset.chatAction = "remove-attachment";
      remove.dataset.chatAttachmentId = attachment.id;
      remove.textContent = t("移除", "Remove");
      element.appendChild(remove);
    }

    return element;
  };

  const buildMessageAttachmentNode = (attachment) => {
    if (!String(attachment?.mediaType || "").trim().toLowerCase().startsWith("image/")) {
      return buildAttachmentChip(attachment, false);
    }
    const card = document.createElement("a");
    card.className = "chat-message-image";
    card.href = attachment.browserUrl || attachment.url;
    card.target = "_blank";
    card.rel = "noreferrer";

    const preview = document.createElement("img");
    preview.className = "chat-message-image-preview";
    preview.src = attachment.browserUrl || attachment.url;
    preview.alt = attachment.name;
    preview.loading = "lazy";

    const meta = document.createElement("div");
    meta.className = "chat-message-image-meta";

    const name = document.createElement("strong");
    name.className = "chat-message-image-name";
    name.textContent = attachment.name;

    const type = document.createElement("span");
    type.className = "chat-message-image-type";
    type.textContent = attachment.mediaType;

    meta.appendChild(name);
    meta.appendChild(type);
    card.appendChild(preview);
    card.appendChild(meta);
    return card;
  };

  const buildReasoningPanel = (article, bubble, reasoning, open = false, anchor = null) => {
    const details = document.createElement("details");
    details.className = "chat-reasoning";

    const summary = document.createElement("summary");
    summary.className = "chat-reasoning-summary";
    const summaryLabel = document.createElement("span");
    summaryLabel.className = "chat-reasoning-summary-label";
    summaryLabel.textContent = t("思考过程", "Reasoning");
    const summaryIcon = document.createElement("span");
    summaryIcon.className = "chat-reasoning-summary-icon";
    summaryIcon.setAttribute("aria-hidden", "true");
    const summaryIconGlyph = document.createElement("i");
    summaryIconGlyph.className = "chat-control-icon";
    summaryIconGlyph.dataset.lucide = "chevron-down";
    summaryIcon.appendChild(summaryIconGlyph);
    summary.appendChild(summaryLabel);
    summary.appendChild(summaryIcon);

    const copy = document.createElement("div");
    copy.className = "chat-reasoning-copy";

    details.appendChild(summary);
    details.appendChild(copy);

    let mounted = false;

    const mount = () => {
      if (mounted || !(bubble instanceof HTMLElement)) {
        return;
      }
      bubble.insertBefore(details, anchor instanceof Node ? anchor : bubble.firstChild);
      mounted = true;
    };

    const unmount = () => {
      if (!mounted) {
        return;
      }
      details.remove();
      mounted = false;
    };

    const setReasoning = (nextReasoning, keepOpen = details.open) => {
      const raw = String(nextReasoning || "");
      if (raw.trim() === "") {
        article.classList.remove("is-reasoning-stream");
        details.hidden = true;
        copy.dataset.chatMarkdownSource = "";
        copy.innerHTML = "";
        unmount();
        return;
      }
      mount();
      details.hidden = false;
      article.classList.add("is-reasoning-stream");
      renderMarkdownInto(copy, raw);
      attachMarkdownCopyHandler(copy);
      details.open = keepOpen;
      syncLucideIcons();
    };

    setReasoning(reasoning, open);
    return { details, copy, setReasoning };
  };

  const operationCategoryIcon = (category) => {
    switch (String(category || "").trim().toLowerCase()) {
      case "browser":
        return "globe";
      case "shell":
        return "terminal";
      case "file":
        return "file-text";
      case "search":
        return "search";
      default:
        return "wrench";
    }
  };

  const operationStatusLabel = (status) => {
    switch (String(status || "").trim().toLowerCase()) {
      case "started":
        return t("运行中", "Running");
      case "completed":
        return t("完成", "Done");
      case "failed":
        return t("失败", "Failed");
      default:
        return String(status || "").trim();
    }
  };

  const buildOperationsPanel = (article, bubble, anchor = null) => {
    const details = document.createElement("details");
    details.className = "chat-operations";

    const summary = document.createElement("summary");
    summary.className = "chat-operations-summary";
    const summaryLabel = document.createElement("span");
    summaryLabel.className = "chat-operations-summary-label";
    summaryLabel.textContent = t("实时操作", "Live operations");
    const summaryIcon = document.createElement("span");
    summaryIcon.className = "chat-operations-summary-icon";
    summaryIcon.setAttribute("aria-hidden", "true");
    const summaryIconGlyph = document.createElement("i");
    summaryIconGlyph.className = "chat-control-icon";
    summaryIconGlyph.dataset.lucide = "chevron-down";
    summaryIcon.appendChild(summaryIconGlyph);
    summary.appendChild(summaryLabel);
    summary.appendChild(summaryIcon);

    const list = document.createElement("div");
    list.className = "chat-operations-list";
    details.appendChild(summary);
    details.appendChild(list);

    let mounted = false;
    const operationNodes = new Map();

    const mount = () => {
      if (mounted || !(bubble instanceof HTMLElement)) {
        return;
      }
      bubble.insertBefore(details, anchor instanceof Node ? anchor : bubble.firstChild);
      mounted = true;
    };

    const unmount = () => {
      if (!mounted) {
        return;
      }
      details.remove();
      mounted = false;
      operationNodes.clear();
    };

    const renderOperationNode = (operation) => {
      const id = String(operation?.id || "").trim() || `${String(operation?.name || "tool")}-${operationNodes.size}`;
      let item = operationNodes.get(id);
      if (!(item instanceof HTMLElement)) {
        item = document.createElement("article");
        item.className = "chat-operation-item";
        item.dataset.chatOperationId = id;
        const iconWrap = document.createElement("span");
        iconWrap.className = "chat-operation-icon";
        const icon = document.createElement("i");
        icon.className = "chat-control-icon";
        iconWrap.appendChild(icon);
        const body = document.createElement("div");
        body.className = "chat-operation-body";
        const name = document.createElement("div");
        name.className = "chat-operation-name";
        const detail = document.createElement("div");
        detail.className = "chat-operation-detail";
        body.appendChild(name);
        body.appendChild(detail);
        const status = document.createElement("span");
        status.className = "chat-operation-status";
        item.append(iconWrap, body, status);
        operationNodes.set(id, item);
      }

      const category = String(operation?.category || "tool").trim().toLowerCase();
      item.classList.toggle("is-browser", category === "browser");
      const icon = item.querySelector(".chat-operation-icon .chat-control-icon");
      if (icon instanceof HTMLElement) {
        icon.dataset.lucide = operationCategoryIcon(category);
      }
      const name = item.querySelector(".chat-operation-name");
      if (name instanceof HTMLElement) {
        name.textContent = String(operation?.name || "tool");
      }
      const detail = item.querySelector(".chat-operation-detail");
      if (detail instanceof HTMLElement) {
        const detailText = String(operation?.detail || operation?.url || "").trim();
        detail.hidden = detailText === "";
        detail.textContent = detailText;
      }
      let screenshot = item.querySelector(".chat-operation-screenshot");
      const screenshotURL = String(operation?.screenshotUrl || "").trim();
      if (screenshotURL !== "") {
        if (!(screenshot instanceof HTMLImageElement)) {
          screenshot = document.createElement("img");
          screenshot.className = "chat-operation-screenshot";
          screenshot.loading = "lazy";
          screenshot.alt = t("浏览器截图", "Browser screenshot");
          item.querySelector(".chat-operation-body")?.appendChild(screenshot);
        }
        screenshot.src = screenshotURL;
        screenshot.hidden = false;
      } else if (screenshot instanceof HTMLImageElement) {
        screenshot.hidden = true;
        screenshot.removeAttribute("src");
      }
      const status = item.querySelector(".chat-operation-status");
      if (status instanceof HTMLElement) {
        const statusValue = String(operation?.status || "started").trim().toLowerCase();
        status.className = `chat-operation-status is-${statusValue}`;
        status.textContent = operationStatusLabel(statusValue);
      }
      return item;
    };

    const setOperations = (nextOperations = [], keepOpen = details.open) => {
      const operations = Array.isArray(nextOperations) ? nextOperations.filter(Boolean) : [];
      if (operations.length === 0) {
        article.classList.remove("is-operations-stream");
        details.hidden = true;
        list.replaceChildren();
        unmount();
        return;
      }
      mount();
      details.hidden = false;
      article.classList.add("is-operations-stream");
      const seen = new Set();
      operations.forEach((operation) => {
        const node = renderOperationNode(operation);
        seen.add(node.dataset.chatOperationId || "");
        if (!node.isConnected) {
          list.appendChild(node);
        }
      });
      list.querySelectorAll("[data-chat-operation-id]").forEach((node) => {
        if (!(node instanceof HTMLElement)) {
          return;
        }
        if (!seen.has(node.dataset.chatOperationId || "")) {
          node.remove();
          operationNodes.delete(node.dataset.chatOperationId || "");
        }
      });
      details.open = keepOpen || operations.some((operation) => String(operation?.status || "").trim().toLowerCase() === "started");
      syncLucideIcons();
    };

    return { details, list, setOperations };
  };

  const buildMessageNode = (role, label, content, streamingLabel, attachments = [], reasoning = "", operations = []) => {
    const article = document.createElement("article");
    const roleClass = role === "user" ? "is-user" : role === "system" ? "is-system" : "is-assistant";
    article.className = `chat-message ${roleClass}`;
    if (streamingLabel) {
      article.classList.add("is-pending");
    }

    const bubble = document.createElement("div");
    bubble.className = "chat-bubble";

    const copy = document.createElement("div");
    copy.className = "chat-message-copy";
    const defaultCopy = attachments.length > 0 ? t("已附带附件。", "Attachments included.") : "";
    const setContent = (nextContent) => {
      const raw = String(nextContent || "").trim() !== "" ? String(nextContent) : defaultCopy;
      if (String(raw || "").trim() === "") {
        copy.hidden = true;
        copy.dataset.chatMarkdownSource = "";
        copy.innerHTML = "";
        return;
      }
      copy.hidden = false;
      renderMarkdownInto(copy, raw);
      if (roleClass === "is-assistant") {
        attachMarkdownCopyHandler(copy);
      }
    };

    const operationsPanel = buildOperationsPanel(article, bubble, copy);
    const reasoningPanel = buildReasoningPanel(article, bubble, reasoning, false, copy);
    setContent(content);
    bubble.appendChild(copy);

    if (attachments.length > 0) {
      const attachmentList = document.createElement("div");
      attachmentList.className = "chat-message-attachments";
      attachments.forEach((attachment) => {
        attachmentList.appendChild(buildMessageAttachmentNode(attachment));
      });
      bubble.appendChild(attachmentList);
    }

    let status = null;
    if (streamingLabel) {
      status = document.createElement("span");
      status.className = "chat-streaming-status";
      status.textContent = streamingLabel;
      bubble.appendChild(status);
    }

    const actionBar = document.createElement("div");
    actionBar.className = "chat-message-actions";
    actionBar.hidden = true;
    bubble.appendChild(actionBar);

    if (roleClass === "is-assistant") {
      const toolbar = document.createElement("div");
      toolbar.className = "chat-message-toolbar";
      const copyButton = document.createElement("button");
      copyButton.type = "button";
      copyButton.className = "chat-message-toolbar-button";
      copyButton.dataset.chatAction = "copy-message-markdown";
      copyButton.setAttribute("aria-label", t("复制 Markdown", "Copy markdown"));
      copyButton.title = t("复制 Markdown", "Copy markdown");
      const copyIcon = document.createElement("i");
      copyIcon.className = "chat-control-icon";
      copyIcon.dataset.lucide = "copy";
      copyButton.appendChild(copyIcon);
      const copyLabel = document.createElement("span");
      copyLabel.className = "chat-message-toolbar-label";
      copyLabel.textContent = t("复制", "Copy");
      copyButton.appendChild(copyLabel);
      toolbar.appendChild(copyButton);
      bubble.appendChild(toolbar);
    }

    const setStreamingStatus = (nextStatus, tone = "streaming") => {
      if (!(status instanceof HTMLElement)) {
        return;
      }
      const raw = String(nextStatus || "").trim();
      status.classList.remove("is-streaming", "is-reasoning", "is-heartbeat", "is-warning", "is-error", "is-done");
      status.hidden = raw === "";
      status.textContent = raw;
      article.classList.toggle("is-pending", raw !== "" && ["streaming", "reasoning", "heartbeat"].includes(tone));
      if (raw !== "") {
        status.classList.add(`is-${tone}`);
      }
    };

    const setActions = (nextActions = []) => {
      actionBar.replaceChildren();
      if (!Array.isArray(nextActions) || nextActions.length === 0) {
        actionBar.hidden = true;
        return;
      }
      nextActions.forEach((action) => {
        const button = document.createElement("button");
        button.type = "button";
        button.className = action.kind === "primary" ? "primary-button chat-message-action" : "ghost-button chat-message-action";
        button.textContent = String(action.label || "").trim();
        button.disabled = Boolean(action.disabled);
        if (typeof action.onClick === "function") {
          button.addEventListener("click", action.onClick);
        }
        actionBar.appendChild(button);
      });
      actionBar.hidden = false;
    };

    article.appendChild(bubble);
    operationsPanel.setOperations(operations, operations.some((operation) => String(operation?.status || "").trim().toLowerCase() === "started"));
    return {
      article,
      copy,
      reasoningCopy: reasoningPanel.copy,
      setContent,
      setReasoning: reasoningPanel.setReasoning,
      setOperations: operationsPanel.setOperations,
      setStreamingStatus,
      setActions,
    };
  };

  const buildEmptyState = (route) => {
    const empty = document.createElement("div");
    empty.className = "chat-empty-state";

    const mark = document.createElement("div");
    mark.className = "chat-empty-mark";
    mark.setAttribute("aria-hidden", "true");
    const markInner = document.createElement("span");
    mark.appendChild(markInner);

    const routeText = document.createElement("p");
    routeText.className = "chat-empty-route";
    routeText.textContent = route ? `${route.publicName} · ${route.providerName}` : t("先选一个模型开始对话", "Choose a model to start chatting");

    const title = document.createElement("h2");
    title.textContent = t("今天想聊点什么？", "Hello! How can I help you today?");

    const copy = document.createElement("p");
    copy.textContent = t(
      "附件上传和中断恢复会沿用当前模型与路由。",
      "Attachment uploads and resume behavior follow the current model and route."
    );

    empty.appendChild(mark);
    empty.appendChild(routeText);
    empty.appendChild(title);
    empty.appendChild(copy);
    return empty;
  };

  const renderThread = (root, messages, route) => {
    const thread = root.querySelector("[data-chat-scroll]");
    if (!thread) {
      return;
    }
    thread.replaceChildren();
    if (!messages.length) {
      thread.appendChild(buildEmptyState(route));
      syncLucideIcons();
      return;
    }
    messages.forEach((message) => {
      const node = buildMessageNode(message.role, message.label || roleLabel(message.role), message.content, "", message.attachments || [], message.reasoning || "", message.operations || []);
      thread.appendChild(node.article);
    });
    syncLucideIcons();
    scrollThread(thread);
  };

  const renderThreadSummary = (root, messages) => {
    const container = root.querySelector("[data-chat-thread-summary]");
    if (!container) {
      return;
    }
    container.replaceChildren();
    if (!messages.length) {
      const empty = document.createElement("div");
      empty.className = "chat-sidebar-empty";
      const copy = document.createElement("p");
      copy.textContent = t("这里会按时间顺序保留当前线程的消息摘要。", "This sidebar keeps a quick summary of the current thread in order.");
      empty.appendChild(copy);
      container.appendChild(empty);
      return;
    }
    const list = document.createElement("div");
    list.className = "chat-history-list";
    messages.forEach((message) => {
      const card = document.createElement("div");
      card.className = "chat-history-card";
      const role = document.createElement("span");
      role.className = "chat-history-role";
      role.textContent = message.label || roleLabel(message.role);
      const snippet = document.createElement("span");
      snippet.className = "chat-history-snippet";
      snippet.textContent = messageSummaryText(message);
      card.appendChild(role);
      card.appendChild(snippet);
      list.appendChild(card);
    });
    container.appendChild(list);
  };

  const renderDraftAttachments = (root, attachments) => {
    const tray = root.querySelector("[data-chat-draft-attachments]");
    if (!tray) {
      return;
    }
    tray.replaceChildren();
    if (!attachments.length) {
      tray.hidden = true;
      return;
    }
    attachments.forEach((attachment) => {
      const item = document.createElement("div");
      item.className = "chat-draft-attachment";
      if (attachment.mediaType.startsWith("image/")) {
        const preview = document.createElement("img");
        preview.className = "chat-draft-attachment-preview";
        preview.src = attachment.browserUrl || attachment.url;
        preview.alt = attachment.name;
        item.appendChild(preview);
      } else {
        const badge = document.createElement("span");
        badge.className = "chat-draft-attachment-badge";
        badge.textContent = (attachment.mediaType.split("/")[1] || "file").slice(0, 4).toUpperCase();
        item.appendChild(badge);
      }
      item.appendChild(buildAttachmentChip(attachment, true));
      tray.appendChild(item);
    });
    tray.hidden = false;
  };

  const setAttachmentStatus = (root, message, isError = false) => {
    const status = root.querySelector("[data-chat-attachment-status]");
    if (!status) {
      return;
    }
    const textValue = String(message || "").trim();
    if (textValue === "") {
      status.hidden = true;
      status.textContent = "";
      status.classList.remove("is-error");
      return;
    }
    status.hidden = false;
    status.textContent = textValue;
    status.classList.toggle("is-error", isError);
  };

  const hideResultMeta = (root) => {
    const meta = root.querySelector(".chat-response-meta");
    if (meta) {
      meta.hidden = true;
    }
  };

  const updateStageHeader = (root, session, routes) => {
    const route = routes.get(session.publicName) || null;
    const stageTitle = root.querySelector("[data-chat-session-title]");
    if (stageTitle) {
      stageTitle.textContent = session.title || t("新会话", "New chat");
    }
    const stageModel = root.querySelector(".chat-stage-model");
    if (stageModel) {
      if (route) {
        stageModel.textContent = route.publicName;
        stageModel.classList.remove("is-muted");
      } else {
        stageModel.textContent = t("未选择模型", "No model selected");
        stageModel.classList.add("is-muted");
      }
    }
    const modelPickerCopy = root.querySelector("[data-chat-model-picker-copy]");
    if (modelPickerCopy) {
      modelPickerCopy.textContent = route ? route.publicName : t("选择模型", "Choose model");
    }
    const composerNote = root.querySelector(".chat-composer-note");
    if (composerNote) {
      composerNote.textContent = route ? `${route.publicName} · ${route.upstreamModel}` : t("选择模型后即可开始对话", "Pick a model to start chatting");
    }
  };

  const applySession = (root, session, routes) => {
    const form = currentForm();
    if (!form) {
      return;
    }
    writeClientSessionID(form, session.id);
    setSelectedModel(form, session.publicName);
    setSelectedEnvironment(form, session.environment);
    syncReasoningEffortControl(form, routes.get(session.publicName) || null, session.reasoningEffort);
    updateStageHeader(root, session, routes);

    const systemPromptField = form.querySelector("textarea[name=\"chat_system_prompt\"]");
    if (systemPromptField instanceof HTMLTextAreaElement) {
      systemPromptField.value = session.systemPrompt;
    }
    const draftField = form.querySelector("#chat-draft");
    if (draftField instanceof HTMLTextAreaElement) {
      draftField.value = session.draft;
      syncDraftFieldHeight(draftField);
    }
    writeHiddenJSON(form, "chat_history_json", session.messages);
    writeHiddenJSON(form, "chat_draft_attachments_json", session.draftAttachments);
    renderDraftAttachments(root, session.draftAttachments);
    renderThread(root, session.messages, routes.get(session.publicName) || null);
    renderThreadSummary(root, session.messages);
    setAttachmentStatus(root, "", false);
    hideResultMeta(root);
    root.querySelector(".chat-inline-flash")?.remove();
    const fileInput = root.querySelector("#chat-attachment-input");
    if (fileInput instanceof HTMLInputElement) {
      fileInput.value = "";
    }
    const drawer = root.querySelector("#chat-prompt-drawer");
    if (drawer instanceof HTMLElement) {
      drawer.hidden = true;
    }
    syncSessionURL(session);
    scrollThread(root.querySelector("[data-chat-scroll]"));
    emitChatState({ source: "apply-session", session });
    syncFormStreamingUI(form);
    void bootstrapActiveSessionConnections(form, session);
  };

  const renderSessionList = (root, store) => {
    const container = root.querySelector("[data-chat-session-store]");
    const counter = root.querySelector("[data-chat-session-counter]");
    const sessions = displaySessions(store);
    if (!container) {
      return;
    }
    if (counter) {
      counter.textContent = String(sessions.length);
    }
    container.replaceChildren();
    if (!sessions.length) {
      syncLucideIcons();
      return;
    }
    sessions.forEach((session) => {
      const entry = document.createElement("div");
      entry.className = "chat-session-entry";
      if (session.id === store.activeSessionId) {
        entry.classList.add("is-active");
      }

      const select = document.createElement("button");
      select.type = "button";
      select.className = "chat-session-select";
      select.dataset.chatAction = "activate-session";
      select.dataset.chatSessionId = session.id;

      const title = document.createElement("strong");
      title.className = "chat-session-title";
      title.textContent = session.title || t("新会话", "New chat");

      select.appendChild(title);

      if (sessionIsStreaming(session) || isSessionStreamActive(session.id)) {
        const badge = document.createElement("span");
        badge.className = "chat-session-streaming-badge";
        badge.textContent = t("生成中", "Streaming");
        select.appendChild(badge);
        entry.classList.add("is-streaming");
      }

      const actions = document.createElement("details");
      actions.className = "chat-session-menu";

      const toggle = document.createElement("summary");
      toggle.className = "chat-session-menu-toggle";
      toggle.setAttribute("aria-label", t("会话操作", "Session actions"));
      const toggleIcon = document.createElement("i");
      toggleIcon.className = "chat-control-icon";
      toggleIcon.dataset.lucide = "ellipsis";
      toggleIcon.setAttribute("aria-hidden", "true");
      toggle.appendChild(toggleIcon);

      const panel = document.createElement("div");
      panel.className = "chat-session-menu-panel";

      const rename = document.createElement("button");
      rename.type = "button";
      rename.className = "chat-session-action";
      rename.dataset.chatAction = "rename-session-item";
      rename.dataset.chatSessionId = session.id;
      rename.textContent = t("重命名", "Rename");

      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "chat-session-action is-danger";
      remove.dataset.chatAction = "delete-session";
      remove.dataset.chatSessionId = session.id;
      remove.textContent = t("删除", "Delete");

      panel.appendChild(rename);
      panel.appendChild(remove);
      actions.appendChild(toggle);
      actions.appendChild(panel);

      entry.appendChild(select);
      entry.appendChild(actions);
      container.appendChild(entry);
    });
    syncLucideIcons();
  };

  const closeSessionMenus = (exceptMenu = null) => {
    const root = currentRoot();
    if (!root) {
      return;
    }
    root.querySelectorAll(".chat-session-menu[open]").forEach((menu) => {
      if (menu === exceptMenu) {
        return;
      }
      menu.removeAttribute("open");
    });
  };

  const attachSessionStream = async (form, session) => {
    const sessionID = normalizeSessionStreamID(session?.id);
    if (sessionID === "" || !form) {
      return;
    }
    const state = getSessionStreamState(sessionID);
    syncFormStreamingUI(form);
    if (state && isSessionStreamActive(sessionID)) {
      mountStreamUI(state, form);
      return;
    }
    if (!sessionIsRecoverableCloudAgentStream(session)) {
      startQueuedTurnForSession(sessionID);
      return;
    }
    const resumeURL = String(form.dataset.chatStreamResumeUrl || "").trim();
    if (resumeURL === "") {
      startQueuedTurnForSession(sessionID);
      return;
    }
    await resumeDetachedSessionStream(form, session);
    startQueuedTurnForSession(sessionID);
  };

  const resumeDetachedSessionStream = async (form, session) => {
    const sessionID = normalizeSessionStreamID(session?.id);
    if (sessionID === "" || isSessionStreamActive(sessionID)) {
      return;
    }
    const messages = Array.isArray(session.messages) ? session.messages.map(normalizeMessage).filter(Boolean) : [];
    let baseMessages = [...messages];
    let userHistoryMessage = null;
    let assistantHistoryMessage = null;
    const lastMessage = baseMessages[baseMessages.length - 1];
    if (lastMessage?.role === "assistant") {
      assistantHistoryMessage = { ...lastMessage };
      baseMessages = baseMessages.slice(0, -1);
      const previousMessage = baseMessages[baseMessages.length - 1];
      if (previousMessage?.role === "user") {
        userHistoryMessage = { ...previousMessage };
        baseMessages = baseMessages.slice(0, -1);
      }
    }
    if (!assistantHistoryMessage) {
      assistantHistoryMessage = {
        id: makeID("msg"),
        role: "assistant",
        label: roleLabel("assistant"),
        content: "",
        reasoning: "",
        operations: [],
        attachments: [],
      };
    }
    const state = ensureSessionStreamState(sessionID);
    state.active = true;
    state.stopRequested = false;
    state.streamCompleted = false;
    state.streamErrored = false;
    state.streamInterrupted = false;
    state.streamOpened = false;
    state.sawStreamEvent = false;
    state.reconnectAttempt = 1;
    state.replaced = false;
    state.preserveLocalTranscript = false;
    state.baseMessages = baseMessages;
    state.userHistoryMessage = userHistoryMessage;
    state.assistantHistoryMessage = assistantHistoryMessage;
    state.promptValue = String(userHistoryMessage?.content || "").trim();
    state.draftValue = state.promptValue;
    state.attachments = Array.isArray(userHistoryMessage?.attachments) ? userHistoryMessage.attachments : [];
    state.allowReconnect = true;
    state.resumeURL = String(form.dataset.chatStreamResumeUrl || "").trim();
    state.publicName = session.publicName;
    state.environment = session.environment;
    const abortController = new AbortController();
    state.controller = abortController;
    mountStreamUI(state, form);
    await runSessionStreamFetchLoop(state, form, { resumeOnly: true });
  };

  const bootstrapActiveSessionConnections = async (form, session) => {
    if (!(form instanceof HTMLFormElement) || !session) {
      return;
    }
    const environment = currentSelectedEnvironment(form);
    if (isCloudAgentEnvironment(environment)) {
      try {
        await ensureChatEnvironment(form, {
          suppressSuccessNotice: true,
          forceRuntime: true,
          showToast: true,
        });
      } catch (error) {
        showEnvironmentToast(String(error?.message || t("Claude Code 启动失败。", "Claude Code startup failed.")), "error");
      }
    }
    syncChatShellSession(form);
    await attachSessionStream(form, { ...session, environment: currentSelectedEnvironment(form) });
  };

  const activateSession = (sessionID, broadcast = true) => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form) {
      return;
    }
    const routes = routeMap(form);
    let store = normalizeStore(loadStore(), form, routes);
    const session = store.sessions.find((item) => item.id === sessionID);
    if (!session) {
      return;
    }
    persistCurrentSession(broadcast);
    detachSessionStreamUI(readClientSessionID(form));
    store.activeSessionId = sessionID;
    saveStore(store, broadcast);
    renderSessionList(root, store);
    applySession(root, session, routes);
  };

  const renameSession = (sessionID) => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form) {
      return;
    }
    const routes = routeMap(form);
    let store = normalizeStore(loadStore(), form, routes);
    const session = store.sessions.find((item) => item.id === sessionID);
    if (!session) {
      return;
    }
    const nextTitle = window.prompt(t("输入新的会话名称", "Enter a new session name"), session.title);
    if (nextTitle == null) {
      return;
    }
    const trimmed = String(nextTitle).trim();
    if (trimmed === "") {
      return;
    }
    const renamed = { ...session, title: trimmed, customTitle: true };
    store = upsertSession(store, renamed);
    store.activeSessionId = renamed.id;
    saveStore(store, true);
    renderSessionList(root, store);
    applySession(root, renamed, routes);
    void saveSessionToServer(renamed);
  };

  const deleteSession = (sessionID) => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form) {
      return;
    }
    const routes = routeMap(form);
    let store = normalizeStore(loadStore(), form, routes);
    const session = store.sessions.find((item) => item.id === sessionID);
    if (!session) {
      return;
    }
    const confirmed = window.confirm(t("删除这个会话？", "Delete this chat?"));
    if (!confirmed) {
      return;
    }
    stopSessionStream(sessionID);
    store.sessions = store.sessions.filter((item) => item.id !== sessionID);
    void deleteSessionOnServer(sessionID);
    if (!store.sessions.length) {
      const blank = createBlankSession(form, routes);
      store = emptyStore();
      store.activeSessionId = blank.id;
      saveStore(store, true);
      renderSessionList(root, store);
      applySession(root, blank, routes);
      return;
    }
    if (store.activeSessionId === sessionID) {
      store.activeSessionId = store.sessions[0].id;
    }
    saveStore(store, true);
    renderSessionList(root, store);
    const active = store.sessions.find((item) => item.id === store.activeSessionId) || store.sessions[0];
    applySession(root, active, routes);
  };

  const createNewSession = () => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form) {
      return;
    }
    persistCurrentSession(true);
    detachSessionStreamUI(readClientSessionID(form));
    const routes = routeMap(form);
    let store = normalizeStore(loadStore(), form, routes);
    const session = createBlankSession(form, routes);
    store = upsertSession(store, session);
    store.activeSessionId = session.id;
    saveStore(store, true);
    renderSessionList(root, store);
    applySession(root, session, routes);
  };

  const persistCurrentSession = (broadcast = true) => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form) {
      return null;
    }
    const routes = routeMap(form);
    let store = normalizeStore(loadStore(), form, routes);
    const session = captureSessionFromDOM(form, store, routes);
    if (!isPersistedSession(session)) {
      if (store.activeSessionId === session.id) {
        store.activeSessionId = "";
      }
      saveStore(store, broadcast);
      renderSessionList(root, store);
      void deleteSessionOnServer(session.id);
      syncSessionURL(null);
      return { store, session, routes };
    }
    store = upsertSession(store, session);
    store.activeSessionId = session.id;
    saveStore(store, broadcast);
    renderSessionList(root, store);
    void saveSessionToServer(session);
    syncSessionURL(session);
    return { store, session, routes };
  };

  const updateCurrentSessionMetadata = (updates = {}, broadcast = false) => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form) {
      return null;
    }
    const routes = routeMap(form);
    let store = normalizeStore(loadStore(), form, routes);
    const session = captureSessionFromDOM(form, store, routes);
    const nextSession = normalizeSession({ ...session, ...updates }, form, routes, session);
    store = upsertSession(store, nextSession);
    store.activeSessionId = nextSession.id;
    saveStore(store, broadcast);
    renderSessionList(root, store);
    void saveSessionToServer(nextSession);
    return nextSession;
  };

  const queuePersist = () => {
    window.clearTimeout(persistTimer);
    persistTimer = window.setTimeout(() => {
      persistCurrentSession(true);
    }, 180);
  };

  const reconcileActiveSessionEnvironmentFromDOM = (store, active, form, routes) => {
    if (!form || !active) {
      return { store, active };
    }
    const domSessionID = readClientSessionID(form);
    const domEnvironment = currentSelectedEnvironment(form);
    if (domSessionID === "" || domSessionID !== active.id || !isCloudAgentEnvironment(domEnvironment)) {
      return { store, active };
    }
    if (isCloudAgentEnvironment(active.environment)) {
      return { store, active };
    }
    const nextActive = normalizeSession({ ...active, environment: domEnvironment }, form, routes, active);
    const nextStore = upsertSession(store, nextActive);
    nextStore.activeSessionId = nextActive.id;
    saveStore(nextStore, false);
    return { store: nextStore, active: nextActive };
  };

  const hydrateWorkspace = (preferServerState) => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form) {
      return;
    }
    applyShellHeight(form, readShellHeightPreference());
    renderShellMeta(form, {});
    const sidebarCollapsed = readSidebarPreference();
    setSidebarCollapsed(form, sidebarCollapsed == null ? true : sidebarCollapsed);
    const routes = routeMap(form);
    let store = normalizeStore(loadStore(), form, routes);
    const serverSession = captureSessionFromDOM(form, store, routes);
    const shouldMergeServer = (preferServerState || !store.sessions.length || readClientSessionID(form) !== "")
      && isPersistedSession(serverSession);
    if (shouldMergeServer) {
      store = upsertSession(store, serverSession);
      store.activeSessionId = serverSession.id;
      saveStore(store, false);
    }
    if (!store.sessions.some((session) => session.id === store.activeSessionId)) {
      store.activeSessionId = store.sessions[0]?.id || "";
    }
    let active = store.sessions.find((session) => session.id === store.activeSessionId) || store.sessions[0] || serverSession;
    ({ store, active } = reconcileActiveSessionEnvironmentFromDOM(store, active, form, routes));
    renderSessionList(root, store);
    applySession(root, active, routes);
  };

  const applyStoredSessions = () => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form) {
      return;
    }
    const routes = routeMap(form);
    let store = normalizeStore(loadStore(), form, routes);
    if (!store.sessions.length) {
      hydrateWorkspace(false);
      return;
    }
    if (!store.sessions.some((session) => session.id === store.activeSessionId)) {
      store.activeSessionId = store.sessions[0].id;
    }
    renderSessionList(root, store);
    let active = store.sessions.find((session) => session.id === store.activeSessionId) || store.sessions[0];
    ({ store, active } = reconcileActiveSessionEnvironmentFromDOM(store, active, form, routes));
    applySession(root, active, routes);
  };

  const decodeStreamEvents = async (response, onEvent) => {
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    while (true) {
      const { value, done } = await reader.read();
      buffer += decoder.decode(value || new Uint8Array(), { stream: !done });

      let newlineIndex = buffer.indexOf("\n");
      while (newlineIndex >= 0) {
        const rawLine = buffer.slice(0, newlineIndex).trim();
        buffer = buffer.slice(newlineIndex + 1);
        if (rawLine) {
          onEvent(JSON.parse(rawLine));
        }
        newlineIndex = buffer.indexOf("\n");
      }

      if (done) {
        const tail = buffer.trim();
        if (tail) {
          onEvent(JSON.parse(tail));
        }
        return;
      }
    }
  };

  const restoreDraftAndSubmit = (form, draftValue, attachments) => {
    const draftField = form.querySelector("#chat-draft");
    if (draftField instanceof HTMLTextAreaElement) {
      draftField.value = draftValue;
      syncDraftFieldHeight(draftField);
    }
    writeHiddenJSON(form, "chat_draft_attachments_json", attachments);
    renderDraftAttachments(currentRoot(), attachments);
    syncShellContextFields(form);
    form.submit();
  };

  const clearComposerDraft = (form) => {
    const root = currentRoot();
    const draftField = form?.querySelector("#chat-draft");
    if (draftField instanceof HTMLTextAreaElement) {
      draftField.value = "";
      syncDraftFieldHeight(draftField);
    }
    writeHiddenJSON(form, "chat_draft_attachments_json", []);
    if (root) {
      renderDraftAttachments(root, []);
      setAttachmentStatus(root, "", false);
    }
    updateComposerControls(form);
  };

  const queuePendingTurn = (form, options = {}) => {
    const root = currentRoot();
    if (!root || !form) {
      return false;
    }
    const sessionID = readClientSessionID(form);
    const streamState = ensureSessionStreamState(sessionID);
    if (!streamState) {
      return false;
    }
    const payload = readDraftPayload(form, options);
    if (!hasDraftPayload(payload)) {
      updateComposerControls(form);
      return false;
    }
    streamState.queuedTurns.push(normalizeQueuedTurn({
      prompt: payload.prompt,
      userVisibleText: payload.userVisibleText,
      attachments: payload.attachments,
    }));
    clearComposerDraft(form);
    const queuedCount = streamState.queuedTurns.length;
    setAttachmentStatus(
      root,
      queuedCount === 1
        ? t("已加入队列，当前回复结束后自动发送。", "Queued and will send after the current reply finishes.")
        : t(`已加入队列，当前共 ${queuedCount} 条待发送。`, `Queued ${queuedCount} turns.`),
      false,
    );
    queuePersist();
    updateComposerControls(form);
    return true;
  };

  const stopActiveStream = () => stopSessionStream(readClientSessionID(currentForm()));

  const startQueuedTurnForSession = (sessionID) => {
    const form = currentForm();
    const normalizedSessionID = normalizeSessionStreamID(sessionID);
    const streamState = getSessionStreamState(normalizedSessionID);
    const queue = streamState?.queuedTurns;
    if (!form || !isSessionVisible(normalizedSessionID) || isSessionStreamActive(normalizedSessionID) || !queue || queue.length === 0) {
      updateComposerControls(form);
      return false;
    }
    const nextTurn = queue.shift();
    updateComposerControls(form);
    window.setTimeout(() => {
      const nextForm = currentForm();
      if (!nextForm || !isSessionVisible(normalizedSessionID) || isSessionStreamActive(normalizedSessionID)) {
        queue.unshift(nextTurn);
        updateComposerControls(currentForm());
        return;
      }
      void streamConsoleChat(nextForm, nextTurn);
    }, 0);
    return true;
  };

  const startQueuedTurn = () => startQueuedTurnForSession(readClientSessionID(currentForm()));

  const setInlineFlash = (root, message, isError = false) => {
    const stage = root?.querySelector(".chat-stage");
    if (!(stage instanceof HTMLElement)) {
      return;
    }
    const textValue = String(message || "").trim();
    let flash = stage.querySelector(".chat-inline-flash");
    if (textValue === "") {
      flash?.remove();
      return;
    }
    if (!(flash instanceof HTMLElement)) {
      flash = document.createElement("div");
      const topbar = stage.querySelector(".chat-stage-topbar");
      const stageMain = stage.querySelector(".chat-stage-main");
      if (topbar instanceof HTMLElement) {
        topbar.insertAdjacentElement("afterend", flash);
      } else if (stageMain instanceof HTMLElement) {
        stageMain.prepend(flash);
      } else {
        stage.prepend(flash);
      }
    }
    flash.className = `flash ${isError ? "flash-error" : "flash-success"} chat-inline-flash`;
    flash.setAttribute("role", isError ? "alert" : "status");
    flash.textContent = textValue;
  };

  const buildHistoryMessage = (role, content, attachments = [], reasoning = "") => normalizeMessage({
    id: makeID("msg"),
    role,
    label: roleLabel(role),
    content,
    attachments,
    reasoning,
  });

  const syncStreamHistory = (form, baseMessages, userMessage, assistantMessage) => {
    const history = Array.isArray(baseMessages) ? [...baseMessages] : [];
    const normalizedUser = normalizeMessage(userMessage);
    const normalizedAssistant = normalizeMessage(assistantMessage);
    if (normalizedUser) {
      history.push(normalizedUser);
    }
    if (normalizedAssistant) {
      history.push(normalizedAssistant);
    }
    writeHiddenJSON(form, "chat_history_json", history);
    writeHiddenJSON(form, "chat_draft_attachments_json", []);
    return history;
  };

  const continuationPrompt = () => t(
    "请从刚才中断的位置继续回答，不要重复已经输出的内容。",
    "Continue exactly from where you stopped without repeating the text already shown.",
  );

  const enableForm = (form) => {
    syncFormStreamingUI(form);
  };

  const replaceChatContent = (html) => {
    const root = currentRoot();
    if (!root) {
      return;
    }
    disposeAllShellControllers();
    root.outerHTML = html;
    hydrateWorkspace(true);
    scrollThread(currentRoot()?.querySelector("[data-chat-scroll]"));
  };

  const uploadAttachments = async (files) => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form || !files.length) {
      return;
    }
    if (form.dataset.chatAttachmentUploadEnabled !== "true") {
      return;
    }
    const uploadURL = String(form.dataset.chatAttachmentUploadUrl || "").trim();
    if (uploadURL === "") {
      return;
    }
    setAttachmentStatus(root, t("正在上传附件…", "Uploading attachments…"), false);
    const payload = new FormData();
    files.forEach((file) => payload.append("files", file));
    try {
      const response = await fetch(uploadURL, {
        method: "POST",
        body: payload,
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      });
      const responseBody = await response.text();
      const parsed = safeParseJSON(responseBody, {});
      if (!response.ok || !Array.isArray(parsed.attachments)) {
        setAttachmentStatus(root, String(parsed.error || t("附件上传失败。", "Attachment upload failed.")), true);
        return;
      }
      const existing = readHiddenJSON(form, "chat_draft_attachments_json", []).map(normalizeAttachment).filter(Boolean);
      const deduped = new Map(existing.map((attachment) => [attachment.objectKey, attachment]));
      parsed.attachments.map(normalizeAttachment).filter(Boolean).forEach((attachment) => {
        deduped.set(attachment.objectKey, attachment);
      });
      const attachments = Array.from(deduped.values());
      writeHiddenJSON(form, "chat_draft_attachments_json", attachments);
      renderDraftAttachments(root, attachments);
      setAttachmentStatus(root, t("附件已加入当前草稿。", "Attachments added to the current draft."), false);
      queuePersist();
      updateComposerControls(form);
    } catch (_error) {
      setAttachmentStatus(root, t("附件上传失败。", "Attachment upload failed."), true);
    }
  };

  const ensureChatEnvironment = async (form, options = {}) => {
    const root = currentRoot();
    const field = environmentField(form);
    const ensureURL = environmentEnsureURL(form);
    const ensureStreamURL = environmentEnsureStreamURL(form);
    const suppressSuccessNotice = Boolean(options && options.suppressSuccessNotice);
    const showToast = options.showToast !== false;
    if (!root || !(field instanceof HTMLInputElement || field instanceof HTMLSelectElement) || ensureURL === "") {
      return null;
    }
    const environment = currentSelectedEnvironment(form);
    if (environment !== "local" && readClientSessionID(form) === "") {
      writeClientSessionID(form, makeID("chat"));
    }
    const requestKey = [
      readClientSessionID(form),
      environment,
      currentSelectedModel(form),
      currentSelectedReasoningEffort(form),
      options && options.forceRuntime ? "force-runtime" : "reuse",
    ].join("\u001f");
    if (environmentEnsureInFlight && environmentEnsureKey === requestKey) {
      return environmentEnsureInFlight;
    }
    const payload = new FormData();
    if (readClientSessionID(form) !== "") {
      payload.set("chat_client_session_id", readClientSessionID(form));
    }
    payload.set("chat_environment", environment);
    payload.set("chat_public_name", currentSelectedModel(form));
    payload.set("chat_reasoning_effort", currentSelectedReasoningEffort(form));
    if (options && options.forceRuntime) {
      payload.set("chat_environment_force_runtime", "1");
    }
    const previousDisabled = field.disabled;
    field.disabled = true;
    setEnvironmentPickerBusy(form, true);
    dismissEnvironmentToast();
    if (showToast) {
      if (environment === "local") {
        showEnvironmentToast(t("正在切换到聊天…", "Switching to chat…"), "info", { persistent: true });
      } else {
        showEnvironmentToast(t("正在连接 Cloud Agent…", "Connecting Cloud Agent…"), "info", { persistent: true });
      }
    } else if (typeof options.onPhase === "function") {
      options.onPhase("preparing", environmentStatusPhaseLabel("preparing"));
    }
    setAttachmentStatus(root, "", false);
    const ensureChatEnvironmentViaJSON = async () => {
      const response = await fetch(ensureURL, {
        method: "POST",
        body: payload,
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      });
      const raw = await response.text();
      const jsonParsed = safeParseJSON(raw, null);
      if (!response.ok || !jsonParsed || typeof jsonParsed !== "object") {
        throw new Error(String(jsonParsed?.error || t("环境准备失败。", "Failed to prepare the selected environment.")));
      }
      const notice = String(jsonParsed.notice || "").trim();
      if (environment === "local") {
        if (showToast) {
          showEnvironmentToast(notice || t("已切换到聊天", "Switched to chat"), "info");
        }
      } else if (showToast) {
        showEnvironmentToast(notice || t("Cloud Agent 已就绪", "Cloud Agent is ready"), "success");
      } else if (typeof options.onPhase === "function") {
        options.onPhase("ready", notice || environmentStatusPhaseLabel("ready"));
      }
      return jsonParsed;
    };
    const ensureChatEnvironmentViaStream = async () => {
      const response = await fetch(ensureStreamURL, {
        method: "POST",
        body: payload,
        credentials: "same-origin",
        headers: { Accept: "application/x-ndjson" },
      });
      if (!response.ok || !response.body) {
        const raw = await response.text();
        const fallback = safeParseJSON(raw, null);
        throw new Error(String(fallback?.error || t("环境准备失败。", "Failed to prepare the selected environment.")));
      }
      const contentType = response.headers.get("content-type") || "";
      if (!contentType.includes("application/x-ndjson")) {
        throw new Error(t("环境准备流不可用。", "Environment ensure stream is unavailable."));
      }
      let streamParsed = null;
      await decodeStreamEvents(response, (event) => {
        const result = applyEnvironmentEnsureEvent(form, event, options);
        if (result) {
          streamParsed = result;
        }
      });
      return streamParsed;
    };
    let ensurePromise = null;
    ensurePromise = (async () => {
      try {
        let parsed = null;
        if (environment !== "local" && ensureStreamURL !== "") {
          try {
            parsed = await ensureChatEnvironmentViaStream();
          } catch (_streamError) {
            if (showToast) {
              showEnvironmentToast(t("正在准备 Claude Code…", "Preparing Claude Code…"), "info", { persistent: true });
            } else if (typeof options.onPhase === "function") {
              options.onPhase("starting", environmentStatusPhaseLabel("starting"));
            }
            parsed = await ensureChatEnvironmentViaJSON();
          }
        } else {
          parsed = await ensureChatEnvironmentViaJSON();
        }
        if (!parsed || typeof parsed !== "object") {
          throw new Error(t("环境准备失败。", "Failed to prepare the selected environment."));
        }
        if (typeof parsed.environment === "string" && parsed.environment.trim() !== "") {
          setSelectedEnvironment(form, parsed.environment.trim());
        }
        if (typeof parsed.sessionId === "string" && parsed.sessionId.trim() !== "") {
          writeClientSessionID(form, parsed.sessionId.trim());
        }
        setInlineFlash(root, "", false);
        const notice = String(parsed.notice || "").trim();
        if (notice !== "" && environment !== "local" && suppressSuccessNotice && showToast) {
          showEnvironmentToast(notice, "success");
        }
        queuePersist();
        emitChatState({ source: "ensure-environment", ensured: parsed });
        return parsed;
      } catch (error) {
        const message = String(error?.message || t("环境准备失败。", "Failed to prepare the selected environment."));
        if (showToast) {
          showEnvironmentToast(message, "error");
          setInlineFlash(root, "", false);
        }
        throw error;
      } finally {
        if (environmentEnsureInFlight === ensurePromise) {
          field.disabled = previousDisabled;
          setEnvironmentPickerBusy(form, false);
          updateComposerControls(form);
        }
      }
    })();
    environmentEnsureInFlight = ensurePromise;
    environmentEnsureKey = requestKey;
    updateComposerControls(form);
    try {
      return await ensurePromise;
    } finally {
      if (environmentEnsureInFlight === ensurePromise) {
        environmentEnsureInFlight = null;
        environmentEnsureKey = "";
        updateComposerControls(form);
      }
    }
  };

  const switchChatEnvironment = async (form, value) => {
    if (!(form instanceof HTMLFormElement)) {
      return null;
    }
    if (typeof value === "string") {
      setSelectedEnvironment(form, value);
    }
    if (shellInstances.length > 0) {
      closeAllChatShells(form, { terminate: false, forget: false });
    }
    updateComposerControls(form);
    queuePersist();
    const ensured = await ensureChatEnvironment(form);
    if (isCloudAgentEnvironment(currentSelectedEnvironment(form))) {
      restoreShellInstancesOrLoadServer(form);
    }
    updateComposerControls(form);
    return ensured;
  };

  const fetchWithTimeout = async (url, options, timeoutMs) => {
    if (typeof AbortController !== "function") {
      return fetch(url, options);
    }
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      controller.abort();
    }, Math.max(0, timeoutMs));
    try {
      return await fetch(url, { ...options, signal: controller.signal });
    } finally {
      window.clearTimeout(timer);
    }
  };

  const probeReadyChatShell = async (form, terminalID = "") => {
    const sessionID = readClientSessionID(form);
    const environment = currentSelectedEnvironment(form);
    const shellPageURL = shellPageBaseURL(form);
    if (!(form instanceof HTMLFormElement) || sessionID === "" || shellPageURL === "" || !isCloudAgentEnvironment(environment)) {
      return null;
    }
    try {
      const readyURL = new URL(`${shellPageURL.replace(/\/$/, "")}/ready`, window.location.href);
      readyURL.searchParams.set("session", sessionID);
      if (String(terminalID || "").trim() !== "") {
        readyURL.searchParams.set("terminal", String(terminalID || "").trim());
      }
      const response = await fetchWithTimeout(readyURL.toString(), {
        method: "GET",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      }, shellReadyProbeTimeoutMs);
      if (!response.ok) {
        return null;
      }
      const raw = await response.text();
      const parsed = safeParseJSON(raw, null);
      if (!parsed || typeof parsed !== "object" || String(parsed.status || "").trim() !== "ready") {
        return null;
      }
      if (String(parsed.environment || "").trim() !== environment) {
        return null;
      }
      return parsed;
    } catch (_error) {
      return null;
    }
  };

  const shellQuote = (value) => `'${String(value || "").replace(/'/g, `'"'"'`)}'`;

  const resolveNewTerminalWorkingDirectory = (form, options = {}) => {
    const explicitCwd = normalizeShellWorkingDirectory(options.cwd || "");
    if (explicitCwd !== "") {
      return explicitCwd;
    }
    if (String(options.command || "").trim() !== "") {
      return "";
    }
    const override = normalizeShellWorkingDirectory(form?.dataset?.chatWorkdirOverride || "");
    if (override !== "") {
      return override;
    }
    return "~";
  };

  const buildShellInitialCdCommand = (cwd) => {
    const target = String(cwd || "").trim();
    if (target === "") {
      return "";
    }
    if (target === "~") {
      return "cd ~";
    }
    return `cd -- ${shellQuote(target)}`;
  };

  const buildShellInitialInput = (form, options = {}) => {
    const explicitCommand = ownProperty.call(options, "command") ? String(options.command || "").trim() : "";
    let initialInput = String(options.initialInput || "").trim();
    if (explicitCommand !== "") {
      initialInput = initialInput === "" ? explicitCommand : `${explicitCommand}\n${initialInput}`;
    } else {
      const cdCommand = buildShellInitialCdCommand(resolveNewTerminalWorkingDirectory(form, options));
      if (cdCommand !== "") {
        initialInput = initialInput === "" ? cdCommand : `${cdCommand}\n${initialInput}`;
      }
    }
    if (initialInput === "") {
      return "";
    }
    return initialInput.endsWith("\n") ? initialInput : `${initialInput}\n`;
  };

  const openChatShell = async (form, options = {}) => {
    const root = currentRoot();
    const baseSocketURL = shellSocketBaseURL(form);
    const initialInput = buildShellInitialInput(form, options);
    if (!root || baseSocketURL === "") {
      return;
    }
    const showProgress = options.showProgress !== false;
    if (currentSelectedEnvironment(form) === "local") {
      setInlineFlash(root, t("先切换到 Cloud Agent，再打开 Claude Code 终端。", "Switch to Cloud Agent before opening the Claude Code terminal."), true);
      return;
    }
    if (currentSelectedModel(form) === "") {
      setInlineFlash(root, t("先选择一个模型，再打开 Claude Code 终端。", "Choose a model before opening the Claude Code terminal."), true);
      return;
    }
    if (shellOpenInFlight) {
      if (showProgress) {
        setInlineFlash(root, t("Claude Code 正在打开…", "Claude Code is already opening…"), false);
      }
      return;
    }
    shellOpenInFlight = true;
    updateComposerControls(form);
    if (showProgress) {
      setInlineFlash(root, t("正在打开 Claude Code…", "Opening the Claude Code…"), false);
    }
    try {
      const terminalID = String(options.terminalID || makeID("terminal")).trim();
      let shellState = await probeReadyChatShell(form, terminalID);
      let ensured = null;
      if (!shellState) {
        try {
          ensured = await ensureChatEnvironment(form, { suppressSuccessNotice: true });
        } catch (_error) {
          return;
        }
      }
      const sessionID = String(shellState?.sessionId || ensured?.sessionId || readClientSessionID(form)).trim();
      if (sessionID === "") {
        setInlineFlash(root, t("Shell 会话未生成，请重试。", "The shell session could not be created. Please retry."), true);
        return;
      }
      let nextSocketURL = String(shellState?.socketUrl || "").trim();
      if (nextSocketURL === "") {
        nextSocketURL = shellSocketURLForTerminal(form, sessionID, terminalID);
      }
      const initialWorkingDirectory = resolveNewTerminalWorkingDirectory(form, options);
      const trackedWorkingDirectory = initialWorkingDirectory === "~"
        ? ""
        : normalizeShellWorkingDirectory(initialWorkingDirectory);
      const instance = createShellInstance({
        terminalID: String(shellState?.terminalId || terminalID).trim() || terminalID,
        sessionID,
        workerID: shellState?.workerId || ensured?.workerId,
        containerName: shellState?.containerName || ensured?.containerName,
        workspacePath: shellState?.workspacePath || ensured?.workspacePath,
        socketURL: nextSocketURL,
        currentWorkingDirectory: trackedWorkingDirectory || normalizeShellWorkingDirectory(shellState?.workspacePath || ensured?.workspacePath || ""),
      });

      shellInstances = [...shellInstances, instance];
      activeShellInstanceID = instance.id;
      renderShellDockContents(form);
      setShellDockVisible(form, true);
      renderShellMeta(instance);
      writeShellState(form);
      if (showProgress) {
        setInlineFlash(root, "", false);
      }
      const controller = ensureShellController(form, instance);
      if (!controller) {
        closeShellInstance(form, instance.id);
        return;
      }
      controller.connect({ resetTerminal: true });
      if (initialInput !== "") {
        controller.sendInput?.(initialInput);
      }
      window.setTimeout(() => {
        controller.refresh?.();
        controller.focus?.();
      }, 0);
    } finally {
      shellOpenInFlight = false;
      updateComposerControls(form);
    }
  };

  window.addEventListener("aiyolo:chat-open-shell", (event) => {
    const detail = event instanceof CustomEvent && event.detail && typeof event.detail === "object" ? event.detail : {};
    void openChatShell(currentForm(), {
      showProgress: detail.showProgress !== false,
      terminalID: detail.terminalID,
      command: detail.command,
      cwd: detail.cwd,
      initialInput: detail.initialInput,
    });
  });

  const canUseCloudAgentShell = (form) => (
    form instanceof HTMLFormElement
    && isCloudAgentEnvironment(currentSelectedEnvironment(form))
    && currentSelectedModel(form) !== ""
    && shellSocketBaseURL(form) !== ""
  );

  const delayWithAbort = (signal, timeoutMs) => new Promise((resolve, reject) => {
    const delay = Number.isFinite(timeoutMs) ? Math.max(0, timeoutMs) : 0;
    if (signal?.aborted) {
      reject(new DOMException("Aborted", "AbortError"));
      return;
    }
    const timer = window.setTimeout(() => {
      if (signal) {
        signal.removeEventListener("abort", onAbort);
      }
      resolve();
    }, delay);
    const onAbort = () => {
      window.clearTimeout(timer);
      signal?.removeEventListener("abort", onAbort);
      reject(new DOMException("Aborted", "AbortError"));
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });

  const runSessionStreamFetchLoop = async (state, form, options = {}) => {
    const resumeOnly = Boolean(options.resumeOnly);
    const streamURL = String(form.dataset.chatStreamUrl || "").trim();
    const formData = options.formData || null;
    const promptValue = state.promptValue;
    const draftValue = state.draftValue;
    const attachments = state.attachments;
    const assistantHistoryMessage = state.assistantHistoryMessage;

    const syncCurrentHistory = () => {
      if (isSessionVisible(state.sessionID) && state.ui?.form) {
        syncStreamHistory(state.ui.form, state.baseMessages, state.userHistoryMessage, assistantHistoryMessage);
        queuePersist();
        return;
      }
      persistSessionStreamProgress(state, { status: "streaming", lastError: "" });
    };

    const restartStream = (nextPrompt, nextVisibleText, nextAttachments = []) => {
      if (!isSessionVisible(state.sessionID)) {
        return;
      }
      const nextForm = currentForm();
      const nextDraftField = nextForm?.querySelector("#chat-draft");
      if (!(nextDraftField instanceof HTMLTextAreaElement)) {
        return;
      }
      nextDraftField.value = nextVisibleText;
      syncDraftFieldHeight(nextDraftField);
      writeHiddenJSON(nextForm, "chat_draft_attachments_json", nextAttachments);
      renderDraftAttachments(currentRoot(), nextAttachments);
      setInlineFlash(currentRoot(), "", false);
      void streamConsoleChat(nextForm, { prompt: nextPrompt, userVisibleText: nextVisibleText, attachments: nextAttachments });
    };
    const continueStream = () => restartStream(continuationPrompt(), t("继续生成", "Continue"));
    const retryStream = () => restartStream(promptValue, draftValue || promptValue, attachments);

    const finalizeInterruptedStream = (messageText, finalizeOptions = {}) => {
      if (state.streamInterrupted) {
        return;
      }
      state.streamInterrupted = true;
      const hasPartial = Boolean(String(assistantHistoryMessage.content || "").trim() || String(assistantHistoryMessage.reasoning || "").trim());
      syncCurrentHistory();
      updateStreamSessionMetadata(state, {
        status: hasPartial ? "interrupted" : "failed",
        lastError: String(messageText || "").trim(),
      });
      updateStreamAssistantUI(state, (assistantMessage) => {
        assistantMessage.setStreamingStatus(String(finalizeOptions.statusText || t("输出已中断", "Interrupted")), String(finalizeOptions.statusTone || "error"));
        assistantMessage.setActions(Array.isArray(finalizeOptions.actions)
          ? finalizeOptions.actions
          : hasPartial
            ? [{ label: t("继续生成", "Continue"), onClick: continueStream }]
            : [{ label: t("重试", "Retry"), onClick: retryStream }]);
      });
      if (isSessionVisible(state.sessionID)) {
        setInlineFlash(currentRoot(), messageText, finalizeOptions.isError !== false);
      }
    };

    const applyStreamMessage = (message) => {
      if (!message || typeof message !== "object") {
        return;
      }
      if (typeof message.content === "string" && message.content.trim() !== "") {
        assistantHistoryMessage.content = message.content;
        updateStreamAssistantUI(state, (assistantMessage) => {
          assistantMessage.setContent(assistantHistoryMessage.content);
        });
      }
      if (typeof message.reasoning === "string" && message.reasoning.trim() !== "") {
        assistantHistoryMessage.reasoning = message.reasoning;
        updateStreamAssistantUI(state, (assistantMessage) => {
          assistantMessage.setReasoning(assistantHistoryMessage.reasoning, true);
        });
      }
      if (Array.isArray(message.operations) && message.operations.length > 0) {
        assistantHistoryMessage.operations = message.operations.map(normalizeOperation).filter(Boolean);
        updateStreamAssistantUI(state, (assistantMessage) => {
          assistantMessage.setOperations(assistantHistoryMessage.operations, true);
        });
      }
    };

    const hasStreamProgress = () => Boolean(
      state.streamOpened
      || state.sawStreamEvent
      || String(assistantHistoryMessage.content || "").trim() !== ""
      || String(assistantHistoryMessage.reasoning || "").trim() !== "",
    );

    const waitForReconnect = async () => {
      state.reconnectAttempt += 1;
      updateStreamAssistantUI(state, (assistantMessage) => {
        assistantMessage.setStreamingStatus(t("连接已断开，正在重连…", "Connection lost, reconnecting..."), "heartbeat");
        assistantMessage.setActions([]);
      });
      await delayWithAbort(state.controller?.signal, Math.min(4000, 400 * state.reconnectAttempt));
    };

    const handleStreamEvent = (event) => {
      state.sawStreamEvent = true;
      if (event.type === "sync") {
        applyStreamMessage(event.message);
        updateStreamSessionMetadata(state, { status: "streaming", lastError: "" });
        updateStreamAssistantUI(state, (assistantMessage) => {
          assistantMessage.setStreamingStatus(t("已重新连接，继续等待输出…", "Reconnected, waiting for more output..."), "heartbeat");
          assistantMessage.setActions([]);
        });
        return;
      }
      if (event.type === "delta") {
        assistantHistoryMessage.content += String(event.delta || "");
        updateStreamAssistantUI(state, (assistantMessage) => {
          assistantMessage.setContent(assistantHistoryMessage.content);
          assistantMessage.setStreamingStatus(form.dataset.chatStreamingLabel || "Streaming", "streaming");
          assistantMessage.setActions([]);
        });
        syncCurrentHistory();
        return;
      }
      if (event.type === "reasoning") {
        assistantHistoryMessage.reasoning += String(event.reasoning || "");
        updateStreamAssistantUI(state, (assistantMessage) => {
          assistantMessage.setReasoning(assistantHistoryMessage.reasoning, true);
          assistantMessage.setStreamingStatus(t("正在思考", "Reasoning"), "reasoning");
          assistantMessage.setActions([]);
        });
        syncCurrentHistory();
        return;
      }
      if (event.type === "operation") {
        const operation = event.operation && typeof event.operation === "object" ? event.operation : null;
        if (!operation) {
          return;
        }
        if (!Array.isArray(assistantHistoryMessage.operations)) {
          assistantHistoryMessage.operations = [];
        }
        const operationID = String(operation.id || "").trim();
        const status = String(operation.status || "started").trim().toLowerCase();
        const existingIndex = assistantHistoryMessage.operations.findIndex((item) => String(item?.id || "").trim() === operationID);
        const normalizedOperation = normalizeOperation(operation);
        if (!normalizedOperation) {
          return;
        }
        if (existingIndex >= 0) {
          assistantHistoryMessage.operations[existingIndex] = {
            ...assistantHistoryMessage.operations[existingIndex],
            ...normalizedOperation,
          };
        } else {
          assistantHistoryMessage.operations.push(normalizedOperation);
        }
        updateStreamAssistantUI(state, (assistantMessage) => {
          assistantMessage.setOperations(assistantHistoryMessage.operations, true);
          assistantMessage.setStreamingStatus(
            status === "started" ? t("正在执行工具", "Running tools") : t("正在处理结果", "Processing tool results"),
            "reasoning",
          );
          assistantMessage.setActions([]);
        });
        window.dispatchEvent(new CustomEvent("aiyolo:chat-operation", {
          detail: {
            operation,
            sessionId: state.sessionID,
            form: state.ui?.form || currentForm(),
          },
        }));
        syncCurrentHistory();
        return;
      }
      if (event.type === "heartbeat") {
        updateStreamAssistantUI(state, (assistantMessage) => {
          assistantMessage.setStreamingStatus(t("连接保持中，等待更多输出…", "Connection alive, waiting for more output..."), "heartbeat");
        });
        return;
      }
      if (event.type === "done") {
        state.streamCompleted = true;
        applyStreamMessage(event.message);
        syncCurrentHistory();
        updateStreamSessionMetadata(state, { status: "completed", lastError: "" });
        if (String(event?.result?.finishReason || "").trim().toLowerCase() === "length") {
          state.preserveLocalTranscript = true;
          updateStreamAssistantUI(state, (assistantMessage) => {
            assistantMessage.setStreamingStatus(t("已达到输出上限", "Reached output limit"), "warning");
            assistantMessage.setActions([{ label: t("继续生成", "Continue"), onClick: continueStream, kind: "primary" }]);
          });
        } else {
          updateStreamAssistantUI(state, (assistantMessage) => {
            assistantMessage.setStreamingStatus(t("已完成", "Completed"), "done");
            assistantMessage.setActions([]);
          });
          if (isSessionVisible(state.sessionID)) {
            setInlineFlash(currentRoot(), "", false);
          }
        }
        return;
      }
      if (event.type === "error") {
        state.streamErrored = true;
        applyStreamMessage(event.message);
        finalizeInterruptedStream(String(event.error || t("连接中断，最后一段回复没有完整结束。", "The stream was interrupted before the answer finished.")).trim());
        return;
      }
      if (event.type === "reconnect") {
        applyStreamMessage(event.message);
        syncCurrentHistory();
        updateStreamSessionMetadata(state, { status: "streaming", lastError: "" });
        updateStreamAssistantUI(state, (assistantMessage) => {
          assistantMessage.setStreamingStatus(
            String(event.error || t("服务已重启，后台任务继续运行，正在重连…", "Server restarted; background task still running, reconnecting...")).trim(),
            "heartbeat",
          );
          assistantMessage.setActions([]);
        });
        return;
      }
      if (event.type === "replace") {
        if (state.streamErrored || state.preserveLocalTranscript) {
          return;
        }
        state.replaced = true;
        if (isSessionVisible(state.sessionID)) {
          replaceChatContent(event.html || "");
        }
      }
    };

    const openNDJSONResponse = async (response) => {
      if (response.redirected && response.url) {
        if (isSessionVisible(state.sessionID)) {
          window.location.href = response.url;
        }
        return false;
      }
      if (!response.ok || !response.body) {
        throw new Error("stream_unavailable");
      }
      const contentType = response.headers.get("content-type") || "";
      if (!contentType.includes("application/x-ndjson")) {
        throw new Error("stream_unavailable");
      }
      state.streamOpened = true;
      if (!resumeOnly) {
        state.reconnectAttempt = 0;
      }
      await decodeStreamEvents(response, handleStreamEvent);
      return true;
    };

    try {
      while (true) {
        try {
          const useResume = state.allowReconnect && state.reconnectAttempt > 0;
          const response = await fetch(useResume
            ? `${state.resumeURL}?session=${encodeURIComponent(state.sessionID)}`
            : streamURL, useResume
            ? {
              method: "GET",
              signal: state.controller?.signal,
              credentials: "same-origin",
              headers: { Accept: "application/x-ndjson" },
            }
            : {
              method: "POST",
              body: formData,
              signal: state.controller?.signal,
              credentials: "same-origin",
              headers: { Accept: "application/x-ndjson" },
            });
          const handled = await openNDJSONResponse(response);
          if (handled === false || state.streamCompleted || state.streamErrored || state.streamInterrupted || state.replaced) {
            break;
          }
          if (!state.allowReconnect) {
            break;
          }
          await waitForReconnect();
        } catch (error) {
          if (state.stopRequested && error?.name === "AbortError") {
            finalizeInterruptedStream(t("已终止当前回复。", "Stopped current reply."), {
              statusText: t("已终止", "Stopped"),
              statusTone: "warning",
              actions: [],
              isError: false,
            });
            return;
          }
          if (error?.name === "AbortError") {
            if (state.allowReconnect) {
              detachSessionStreamUI(state.sessionID);
              if (hasStreamProgress()) {
                updateStreamSessionMetadata(state, { status: "streaming", lastError: "" });
              }
              return;
            }
          }
          if (state.allowReconnect && !state.streamCompleted && !state.streamErrored && !state.replaced && hasStreamProgress()) {
            await waitForReconnect();
            continue;
          }
          if (!state.streamCompleted && !state.streamErrored && hasStreamProgress()) {
            finalizeInterruptedStream(t("连接异常，回复在完成前中断了。", "The connection dropped before the answer finished."));
            return;
          }
          if (isSessionVisible(state.sessionID)) {
            restoreDraftAndSubmit(form, promptValue, attachments);
          }
          return;
        }
      }
    } finally {
      const stoppedByUser = state.stopRequested;
      const preemptTurn = state.preemptTurn ? normalizeQueuedTurn(state.preemptTurn) : null;
      state.controller = null;
      state.stopRequested = false;
      state.preemptTurn = null;
      state.active = false;
      if (state.streamOpened && !state.replaced && !state.streamCompleted && !state.streamErrored && !stoppedByUser && !state.allowReconnect) {
        finalizeInterruptedStream(t("连接中断，最后一段回复没有完整结束。", "The stream was interrupted before the answer finished."));
      }
      if (!state.replaced) {
        syncFormStreamingUI(isSessionVisible(state.sessionID) ? form : currentForm());
      } else if (isSessionVisible(state.sessionID)) {
        updateComposerControls(currentForm());
      }
      const root = currentRoot();
      if (root) {
        renderSessionList(root, normalizeStore(loadStore(), form, routeMap(form)));
      }
      if (state.streamCompleted || state.streamErrored || state.streamInterrupted) {
        releaseSessionStreamRuntime(state);
      }
      if (preemptTurn && isSessionVisible(state.sessionID)) {
        const nextForm = currentForm();
        if (nextForm) {
          void streamConsoleChat(nextForm, preemptTurn);
        }
      } else if (!stoppedByUser) {
        startQueuedTurnForSession(state.sessionID);
      } else {
        updateComposerControls(currentForm());
      }
    }
  };

  const streamConsoleChat = async (form, options = {}) => {
    const sessionID = readClientSessionID(form);
    if (isSessionStreamActive(sessionID)) {
      queuePendingTurn(form, options);
      return;
    }
    const draftField = form.querySelector("#chat-draft");
    if (!(draftField instanceof HTMLTextAreaElement)) {
      return;
    }
    const payload = readDraftPayload(form, options);
    const draftValue = payload.userVisibleText;
    const promptValue = payload.prompt;
    const attachments = payload.attachments;
    if (!hasDraftPayload(payload)) {
      return;
    }

    const baseMessages = readHiddenJSON(form, "chat_history_json", []).map(normalizeMessage).filter(Boolean);
    const userHistoryMessage = buildHistoryMessage("user", draftValue || promptValue, attachments);
    const assistantHistoryMessage = {
      id: makeID("msg"),
      role: "assistant",
      label: roleLabel("assistant"),
      content: "",
      reasoning: "",
      operations: [],
      attachments: [],
    };

    const root = currentRoot();
    const thread = form.querySelector("[data-chat-scroll]");
    const streamURL = form.dataset.chatStreamUrl;
    const resumeURL = form.dataset.chatStreamResumeUrl;
    const allowReconnect = isCloudAgentEnvironment(currentSelectedEnvironment(form)) && String(resumeURL || "").trim() !== "";
    if (!thread || !streamURL) {
      restoreDraftAndSubmit(form, promptValue, attachments);
      return;
    }

    persistCurrentSession(false);

    const state = ensureSessionStreamState(sessionID);
    state.active = true;
    state.stopRequested = false;
    state.preemptTurn = null;
    state.streamCompleted = false;
    state.streamErrored = false;
    state.streamInterrupted = false;
    state.streamOpened = false;
    state.sawStreamEvent = false;
    state.reconnectAttempt = 0;
    state.replaced = false;
    state.preserveLocalTranscript = promptValue !== draftValue;
    state.baseMessages = baseMessages;
    state.userHistoryMessage = userHistoryMessage;
    state.assistantHistoryMessage = assistantHistoryMessage;
    state.promptValue = promptValue;
    state.draftValue = draftValue;
    state.attachments = attachments;
    state.allowReconnect = allowReconnect;
    state.resumeURL = String(resumeURL || "").trim();
    state.publicName = currentSelectedModel(form);
    state.environment = currentSelectedEnvironment(form);
    state.controller = new AbortController();

    setInlineFlash(root, "", false);
    if (thread.querySelector(".chat-empty-state")) {
      thread.replaceChildren();
    }
    const userMessage = buildMessageNode("user", roleLabel("user"), draftValue || t("已附带附件。", "Attachments included."), "", attachments);
    const initialAssistantStatus = currentSelectedEnvironment(form) === "local"
      ? (form.dataset.chatStreamingLabel || "Streaming")
      : environmentStatusPhaseLabel("preparing");
    const assistantMessage = buildMessageNode(
      "assistant",
      roleLabel("assistant"),
      "",
      initialAssistantStatus,
      [],
      "",
    );
    thread.appendChild(userMessage.article);
    thread.appendChild(assistantMessage.article);
    scrollThread(thread);
    state.ui = { form, thread, assistantMessage };
    clearComposerDraft(form);
    syncFormStreamingUI(form);
    syncStreamHistory(form, baseMessages, userHistoryMessage, assistantHistoryMessage);
    updateCurrentSessionMetadata({ status: "streaming", lastError: "" });

    const updatePrepareStatus = (_phase, label) => {
      assistantMessage.setStreamingStatus(label, "heartbeat");
      scrollThread(thread);
    };

    if (currentSelectedEnvironment(form) !== "local") {
      try {
        await ensureChatEnvironment(form, {
          showToast: false,
          suppressSuccessNotice: true,
          onPhase: updatePrepareStatus,
        });
      } catch (error) {
        const message = String(error?.message || t("Claude Code 启动失败。", "Claude Code startup failed."));
        assistantMessage.setStreamingStatus(message, "error");
        state.active = false;
        state.controller = null;
        releaseSessionStreamRuntime(state);
        syncFormStreamingUI(form);
        updateCurrentSessionMetadata({ status: "failed", lastError: message });
        return;
      }
      assistantMessage.setStreamingStatus(environmentStatusPhaseLabel("sending"), "heartbeat");
    }

    const formData = new FormData(form);
    formData.set("chat_draft", promptValue);
    formData.set("chat_draft_attachments_json", JSON.stringify(attachments));
    const shellContext = syncShellContextFields(form);
    formData.set("chat_shell_active_terminal_id", shellContext.terminalID);
    formData.set("chat_shell_current_working_directory", shellContext.currentWorkingDirectory);

    assistantMessage.setStreamingStatus(form.dataset.chatStreamingLabel || "Streaming", "streaming");
    await runSessionStreamFetchLoop(state, form, { formData });
  };

  document.addEventListener("click", (event) => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form) {
      return;
    }

    const promptCard = event.target.closest(".chat-prompt-card");
    if (promptCard && form.contains(promptCard)) {
      window.setTimeout(() => queuePersist(), 0);
      return;
    }

    const newSessionLink = event.target.closest(".chat-sidebar-new, .chat-reset-button, .chat-new-session-button");
    if (newSessionLink && form.contains(newSessionLink)) {
      event.preventDefault();
      createNewSession();
      return;
    }

    const sessionMenu = event.target.closest(".chat-session-menu");
    if (sessionMenu instanceof HTMLElement) {
      closeSessionMenus(sessionMenu);
    } else {
      closeSessionMenus();
    }

    const shellTabCloseTarget = event.target.closest("[data-chat-shell-tab-close]");
    if (shellTabCloseTarget instanceof HTMLElement && form.contains(shellTabCloseTarget)) {
      event.preventDefault();
      closeShellInstance(form, String(shellTabCloseTarget.dataset.chatShellTabClose || "").trim());
      return;
    }

    const shellTabTarget = event.target.closest("[data-chat-shell-tab-id]");
    if (shellTabTarget instanceof HTMLElement && form.contains(shellTabTarget)) {
      event.preventDefault();
      selectShellInstance(form, String(shellTabTarget.dataset.chatShellTabId || "").trim());
      return;
    }

    const shellActionTarget = event.target.closest("[data-chat-shell-action]");
    if (shellActionTarget instanceof HTMLElement && form.contains(shellActionTarget)) {
      switch (shellActionTarget.dataset.chatShellAction) {
        case "new":
          event.preventDefault();
          void openChatShell(form);
          return;
        case "clear":
          event.preventDefault();
          activeShellInstance()?.controller?.clear?.();
          return;
        case "reconnect":
          event.preventDefault();
          if (!activeShellInstance()) {
            void openChatShell(form);
            return;
          }
          setShellDockVisible(form, true);
          ensureShellController(form, activeShellInstance())?.connect?.({ resetTerminal: true });
          return;
        case "hide":
          event.preventDefault();
          hideShellDock(form);
          return;
        case "close":
          event.preventDefault();
          closeChatShellDock(form);
          return;
        default:
          break;
      }
    }

    const actionTarget = event.target.closest("[data-chat-action]");
    if (!actionTarget || !form.contains(actionTarget) && !root.contains(actionTarget)) {
      return;
    }
    const sessionID = String(actionTarget.dataset.chatSessionId || "").trim();
    switch (actionTarget.dataset.chatAction) {
      case "toggle-sidebar": {
        event.preventDefault();
        setSidebarCollapsed(form, !form.classList.contains("is-sidebar-collapsed"), true);
        return;
      }
      case "composer-primary": {
        event.preventDefault();
        if (isSessionStreamActive(readClientSessionID(form))) {
          stopActiveStream();
          return;
        }
        const payload = readDraftPayload(form);
        if (!hasDraftPayload(payload)) {
          updateComposerControls(form);
          return;
        }
        if (typeof form.requestSubmit === "function") {
          form.requestSubmit();
        } else {
          form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
        }
        return;
      }
      case "composer-preempt": {
        event.preventDefault();
        preemptDraftTurn(form);
        return;
      }
      case "clear-queue": {
        event.preventDefault();
        clearQueuedTurns(readClientSessionID(form));
        return;
      }
      case "cancel-queue-item": {
        event.preventDefault();
        cancelQueuedTurn(readClientSessionID(form), String(actionTarget.dataset.chatQueueId || "").trim());
        return;
      }
      case "preempt-queue-item": {
        event.preventDefault();
        preemptQueuedTurn(readClientSessionID(form), String(actionTarget.dataset.chatQueueId || "").trim());
        return;
      }
      case "copy-message-markdown": {
        event.preventDefault();
        const messageNode = actionTarget.closest(".chat-message");
        const bubbleNode = messageNode?.querySelector(".chat-bubble");
        void copyAssistantMessageMarkdown(bubbleNode);
        return;
      }
      case "open-shell": {
        event.preventDefault();
        if (isShellDockHidden(form)) {
          setShellDockVisible(form, true);
          writeShellState(form);
          updateComposerControls(form);
          return;
        }
        void openChatShell(form);
        return;
      }
      case "toggle-terminal": {
        event.preventDefault();
        if (!isShellDockHidden(form)) {
          hideShellDock(form);
          return;
        }
        if (shellInstances.length > 0) {
          setShellDockVisible(form, true);
          writeShellState(form);
          updateComposerControls(form);
          return;
        }
        void openChatShell(form);
        return;
      }
      case "pick-attachments": {
        event.preventDefault();
        const input = root.querySelector("#chat-attachment-input");
        if (input instanceof HTMLInputElement) {
          input.click();
        }
        return;
      }
      case "activate-session": {
        event.preventDefault();
        activateSession(sessionID, true);
        return;
      }
      case "rename-session": {
        event.preventDefault();
        renameSession(readClientSessionID(form));
        return;
      }
      case "rename-session-item": {
        event.preventDefault();
        renameSession(sessionID);
        return;
      }
      case "delete-session": {
        event.preventDefault();
        deleteSession(sessionID);
        return;
      }
      case "remove-attachment": {
        event.preventDefault();
        const attachments = readHiddenJSON(form, "chat_draft_attachments_json", []).map(normalizeAttachment).filter(Boolean);
        const nextAttachments = attachments.filter((attachment) => attachment.id !== String(actionTarget.dataset.chatAttachmentId || ""));
        writeHiddenJSON(form, "chat_draft_attachments_json", nextAttachments);
        renderDraftAttachments(root, nextAttachments);
        setAttachmentStatus(root, "", false);
        queuePersist();
        updateComposerControls(form);
        return;
      }
      default:
        return;
    }
  });

  document.addEventListener("change", (event) => {
    const target = event.target;
    const form = currentForm();
    const root = currentRoot();
    if (!form || !root) {
      return;
    }
    if (target instanceof HTMLInputElement && target.id === "chat-attachment-input") {
      const files = Array.from(target.files || []);
      if (files.length > 0) {
        void uploadAttachments(files);
      }
      target.value = "";
      return;
    }
    if (target instanceof HTMLInputElement && target.name === "chat_public_name") {
      refreshModelCardStates(form);
      const routes = routeMap(form);
      syncReasoningEffortControl(form, routes.get(target.value.trim()) || null);
      const session = captureSessionFromDOM(form, normalizeStore(loadStore(), form, routes), routes);
      updateStageHeader(root, session, routes);
      renderThread(root, session.messages, routes.get(session.publicName) || null);
      const picker = pickerFromTarget(target);
      if (picker instanceof HTMLDetailsElement) {
        picker.open = false;
      }
      queuePersist();
      updateComposerControls(form);
      return;
    }
    if (target instanceof HTMLInputElement && target.matches("[data-chat-environment-option]")) {
      const picker = pickerFromTarget(target);
      if (picker instanceof HTMLDetailsElement) {
        picker.open = false;
      }
      void switchChatEnvironment(form, target.value)
        .catch(() => {});
      return;
    }
    if (target instanceof HTMLSelectElement && target.name === "chat_environment") {
      void switchChatEnvironment(form)
        .catch(() => {});
      return;
    }
    if (target instanceof HTMLInputElement && target.matches("[data-chat-reasoning-option]")) {
      const input = reasoningEffortInput(form);
      const copy = reasoningEffortPickerCopy(form);
      if (input instanceof HTMLInputElement) {
        input.value = String(target.value || "").trim().toLowerCase();
      }
      refreshReasoningOptionStates(form);
      if (copy instanceof HTMLElement) {
        copy.textContent = reasoningEffortSummaryLabel(input?.value);
      }
      const picker = pickerFromTarget(target);
      if (picker instanceof HTMLDetailsElement) {
        picker.open = false;
      }
      queuePersist();
    }
  });

  document.addEventListener("input", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLTextAreaElement || target instanceof HTMLInputElement)) {
      return;
    }
    if (target.id === "chat-draft") {
      syncDraftFieldHeight(target);
      queuePersist();
      updateComposerControls(target.form instanceof HTMLFormElement ? target.form : currentForm());
      return;
    }
    if (target.name === "chat_system_prompt") {
      queuePersist();
      return;
    }
    if (target.matches("[data-chat-workdir-input]")) {
      const form = currentForm();
      scheduleWorkdirSuggestions(form);
      return;
    }
  });

  document.addEventListener("keydown", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLTextAreaElement) || target.id !== "chat-draft") {
      return;
    }
    if (event.defaultPrevented || event.isComposing || event.key !== "Enter" || event.shiftKey || event.altKey) {
      return;
    }
    const form = target.form;
    if (!(form instanceof HTMLFormElement) || !form.matches(".chat-shell[data-chat-stream-url]") || target.disabled) {
      return;
    }
    event.preventDefault();
    if (isSessionStreamActive(readClientSessionID(form))) {
      if (event.ctrlKey || event.metaKey) {
        preemptDraftTurn(form);
        return;
      }
      queuePendingTurn(form);
      return;
    }
    if (event.ctrlKey || event.metaKey) {
      return;
    }
    if (typeof form.requestSubmit === "function") {
      form.requestSubmit();
      return;
    }
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });

  document.addEventListener("toggle", (event) => {
    const picker = event.target;
    if (!isChatPicker(picker)) {
      return;
    }
    if (picker.open) {
      closeOtherPickers(picker);
      syncPickerMenuPlacement(picker);
      window.requestAnimationFrame(() => syncPickerMenuPlacement(picker));
      return;
    }
    clearPickerMenuPlacement(picker);
  }, true);

  document.addEventListener("toggle", (event) => {
    const picker = event.target;
    if (!(picker instanceof HTMLDetailsElement) || !picker.matches("[data-chat-workdir-picker]")) {
      return;
    }
    if (picker.open) {
      openWorkdirPicker(picker.closest("form") instanceof HTMLFormElement ? picker.closest("form") : currentForm());
    }
  }, true);

  document.addEventListener("scroll", syncOpenPickerMenus, true);
  window.addEventListener("resize", syncOpenPickerMenus);

  document.addEventListener("click", (event) => {
    const node = event.target;
    if (!(node instanceof Element)) {
      return;
    }
    const optionTarget = node.closest("[data-chat-workdir-option]");
    if (optionTarget instanceof HTMLElement) {
      event.preventDefault();
      const form = currentForm();
      const input = workdirInput(form);
      const target = String(optionTarget.dataset.chatWorkdirOption || "").trim();
      if (optionTarget.dataset.chatWorkdirApply === "true") {
        applyWorkdirOverride(form, target);
        return;
      }
      if (input instanceof HTMLInputElement && target !== "") {
        input.value = target.replace(/\/+$/, "") + "/";
        input.focus();
        void updateWorkdirSuggestions(form);
      }
      return;
    }
    const applyTarget = node.closest("[data-chat-workdir-apply]");
    if (applyTarget instanceof HTMLElement) {
      event.preventDefault();
      const form = currentForm();
      const input = workdirInput(form);
      applyWorkdirOverride(form, input instanceof HTMLInputElement ? input.value : "");
      return;
    }
    const resetTarget = node.closest("[data-chat-workdir-reset]");
    if (resetTarget instanceof HTMLElement) {
      event.preventDefault();
      resetWorkdirOverride(currentForm());
      return;
    }
  });

  document.addEventListener("keydown", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLInputElement) || !target.matches("[data-chat-workdir-input]")) {
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      applyWorkdirOverride(currentForm(), target.value);
    }
  });

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") {
      return;
    }
    const form = currentForm();
    const openPickers = Array.from(form?.querySelectorAll(".chat-model-picker[open]") || []).filter((picker) => picker instanceof HTMLDetailsElement);
    if (openPickers.length === 0) {
      return;
    }
    openPickers.forEach((picker) => {
      picker.open = false;
    });
    event.preventDefault();
  });

  document.addEventListener("click", (event) => {
    const form = currentForm();
    if (!form) {
      return;
    }
    form.querySelectorAll(".chat-model-picker[open]").forEach((picker) => {
      if (!(picker instanceof HTMLDetailsElement)) {
        return;
      }
      const menu = pickerMenu(picker);
      if (event.target instanceof Node && (picker.contains(event.target) || menu?.contains(event.target))) {
        return;
      }
      picker.open = false;
    });
  });

  document.addEventListener("submit", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLFormElement)) {
      return;
    }
    if (!target.matches(".chat-shell[data-chat-stream-url]")) {
      return;
    }
    syncShellContextFields(target);
    if (!supportsStreaming()) {
      persistCurrentSession(false);
      return;
    }
    event.preventDefault();
    void streamConsoleChat(target);
  });

  document.addEventListener("pointerdown", (event) => {
    const form = currentForm();
    const handle = event.target.closest("[data-chat-shell-resize-handle]");
    const dock = shellDock(form);
    if (!(form instanceof HTMLFormElement) || !(handle instanceof HTMLElement) || !(dock instanceof HTMLElement) || dock.hidden) {
      return;
    }
    event.preventDefault();
    shellResizeState = {
      startY: event.clientY,
      startHeight: dock.getBoundingClientRect().height,
      previousUserSelect: document.body.style.userSelect,
    };
    form.classList.add("is-shell-resizing");
    document.body.style.userSelect = "none";
  });

  document.addEventListener("pointermove", (event) => {
    const form = currentForm();
    if (!(form instanceof HTMLFormElement) || !shellResizeState) {
      return;
    }
    applyShellHeight(form, shellResizeState.startHeight + (shellResizeState.startY - event.clientY));
    activeShellInstance()?.controller?.refresh?.();
  });

  const finishShellResize = () => {
    const form = currentForm();
    if (!shellResizeState) {
      return;
    }
    document.body.style.userSelect = shellResizeState.previousUserSelect;
    if (form instanceof HTMLFormElement) {
      form.classList.remove("is-shell-resizing");
      const dock = shellDock(form);
      if (dock instanceof HTMLElement) {
        writeShellHeightPreference(dock.getBoundingClientRect().height);
      }
      activeShellInstance()?.controller?.refresh?.();
    }
    shellResizeState = null;
  };

  document.addEventListener("pointerup", finishShellResize);
  document.addEventListener("pointercancel", finishShellResize);

  window.addEventListener("resize", () => {
    syncCurrentDraftHeight();
    activeShellInstance()?.controller?.refresh?.();
  });

  hydrateWorkspace(false);
  syncLucideIcons();
})();
