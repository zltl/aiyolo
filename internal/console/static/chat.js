(() => {
  if (window.__aiyoloConsoleChatBound) {
    return;
  }
  window.__aiyoloConsoleChatBound = true;

  const storageKey = "aiyolo.console.chat.v1";
  const sessionLimit = 24;
  const syncChannel = typeof BroadcastChannel === "function" ? new BroadcastChannel(storageKey) : null;
  let persistTimer = 0;

  const supportsStreaming = () => Boolean(window.fetch && window.FormData && window.TextDecoder && window.ReadableStream);
  const supportsStorage = (() => {
    try {
      window.localStorage.setItem("__aiyolo_console_chat_probe", "1");
      window.localStorage.removeItem("__aiyolo_console_chat_probe");
      return true;
    } catch (_error) {
      return false;
    }
  })();

  const currentRoot = () => document.getElementById("chat-content");
  const currentForm = () => currentRoot()?.querySelector(".chat-shell[data-chat-stream-url]") || null;
  const localeIsZH = () => String(currentForm()?.dataset.chatLocale || "").toLowerCase().startsWith("zh");
  const t = (zh, en) => (localeIsZH() ? zh : en);
  const assistantAvatarText = "AI";
  const userAvatarText = "U";
  const systemAvatarText = "S";

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

  const currentSelectedModel = (form) => {
    const checked = form?.querySelector("input[name=\"chat_public_name\"]:checked");
    if (checked instanceof HTMLInputElement) {
      return checked.value.trim();
    }
    const first = form?.querySelector("input[name=\"chat_public_name\"]");
    return first instanceof HTMLInputElement ? first.value.trim() : "";
  };

  const defaultSystemPrompt = (form) => {
    const field = form?.querySelector("textarea[name=\"chat_system_prompt\"]");
    if (!(field instanceof HTMLTextAreaElement)) {
      return "";
    }
    return String(field.defaultValue || field.value || "").trim();
  };

  const routeMap = (form) => {
    const routes = new Map();
    form?.querySelectorAll("input[name=\"chat_public_name\"]").forEach((input) => {
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
      });
    });
    return routes;
  };

  const refreshModelCardStates = (form) => {
    form?.querySelectorAll("input[name=\"chat_public_name\"]").forEach((input) => {
      if (!(input instanceof HTMLInputElement)) {
        return;
      }
      const option = input.closest(".chat-model-picker-option");
      option?.classList.toggle("is-active", input.checked);
    });
  };

  const setSelectedModel = (form, publicName) => {
    let matched = false;
    form?.querySelectorAll("input[name=\"chat_public_name\"]").forEach((input) => {
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
      const first = form?.querySelector("input[name=\"chat_public_name\"]");
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
    const content = String(message.content || "").trim();
    const reasoning = String(message.reasoning || "").trim();
    if (!content && !reasoning && attachments.length === 0) {
      return null;
    }
    return {
      id: String(message.id || makeID("msg")).trim(),
      role,
      label: String(message.label || "").trim() || roleLabel(role),
      content,
      reasoning,
      attachments,
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
    const draftAttachments = Array.isArray(session.draftAttachments)
      ? session.draftAttachments.map(normalizeAttachment).filter(Boolean)
      : Array.isArray(session.attachments)
        ? session.attachments.map(normalizeAttachment).filter(Boolean)
        : [];
    const messages = Array.isArray(session.messages)
      ? session.messages.map(normalizeMessage).filter(Boolean)
      : [];
    const normalized = {
      id: String(session.id || existingSession?.id || readClientSessionID(form) || makeID("chat")).trim(),
      title: String(session.title || existingSession?.title || "").trim(),
      customTitle: Boolean(session.customTitle || existingSession?.customTitle),
      publicName: resolvedRoute,
      systemPrompt: String(session.systemPrompt || existingSession?.systemPrompt || defaultSystemPrompt(form) || "").trim(),
      draft: String(session.draft || "").trim(),
      draftAttachments,
      messages,
      createdAt: String(session.createdAt || existingSession?.createdAt || nowISO()),
      updatedAt: String(session.updatedAt || nowISO()),
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
    if (String(session.draft || "").trim() !== "") {
      return truncateText(markdownToPlainText(session.draft) || session.draft);
    }
    if (Array.isArray(session.draftAttachments) && session.draftAttachments[0]?.name) {
      return truncateText(session.draftAttachments[0].name);
    }
    if (String(session.publicName || "").trim() !== "") {
      return String(session.publicName).trim();
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

  const emptyStore = () => ({ version: 1, activeSessionId: "", sessions: [] });

  const loadStore = () => {
    if (!supportsStorage) {
      return emptyStore();
    }
    return safeParseJSON(window.localStorage.getItem(storageKey), emptyStore());
  };

  const saveStore = (store, broadcast = true) => {
    if (!supportsStorage) {
      return;
    }
    try {
      window.localStorage.setItem(storageKey, JSON.stringify(store));
      if (broadcast && syncChannel) {
        syncChannel.postMessage(store);
      }
    } catch (_error) {
      // Ignore storage quota or privacy mode failures.
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
      sessions,
    };
  };

  const upsertSession = (store, session) => {
    const next = {
      version: 1,
      activeSessionId: String(store.activeSessionId || session.id).trim() || session.id,
      sessions: store.sessions.filter((item) => item.id !== session.id),
    };
    next.sessions.unshift(session);
    next.sessions.sort((left, right) => String(right.updatedAt || "").localeCompare(String(left.updatedAt || "")));
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
    const session = normalizeSession({
      id: readClientSessionID(form) || existingSession?.id || makeID("chat"),
      title: existingSession?.title || "",
      customTitle: existingSession?.customTitle || false,
      publicName: currentSelectedModel(form),
      systemPrompt: String((form.querySelector("textarea[name=\"chat_system_prompt\"]")?.value || defaultSystemPrompt(form) || "")).trim(),
      draft: draftField instanceof HTMLTextAreaElement ? draftField.value : "",
      draftAttachments: readHiddenJSON(form, "chat_draft_attachments_json", []),
      messages: readHiddenJSON(form, "chat_history_json", []),
      createdAt: existingSession?.createdAt || nowISO(),
      updatedAt: nowISO(),
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
      element.href = attachment.url;
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

  const buildReasoningPanel = (reasoning, open = false) => {
    const details = document.createElement("details");
    details.className = "chat-reasoning";

    const summary = document.createElement("summary");
    summary.className = "chat-reasoning-summary";
    summary.textContent = t("思考过程", "Reasoning");

    const copy = document.createElement("div");
    copy.className = "chat-reasoning-copy";

    details.appendChild(summary);
    details.appendChild(copy);

    const setReasoning = (nextReasoning, keepOpen = details.open) => {
      const raw = String(nextReasoning || "");
      if (raw.trim() === "") {
        details.hidden = true;
        copy.dataset.chatMarkdownSource = "";
        copy.innerHTML = "";
        return;
      }
      details.hidden = false;
      renderMarkdownInto(copy, raw);
      details.open = keepOpen;
    };

    setReasoning(reasoning, open);
    return { details, copy, setReasoning };
  };

  const buildMessageNode = (role, label, content, streamingLabel, attachments = [], reasoning = "") => {
    const article = document.createElement("article");
    const roleClass = role === "user" ? "is-user" : role === "system" ? "is-system" : "is-assistant";
    article.className = `chat-message ${roleClass}`;
    if (streamingLabel) {
      article.classList.add("is-pending");
    }

    const meta = document.createElement("div");
    meta.className = "chat-message-meta";

    const avatar = document.createElement("div");
    avatar.className = "chat-avatar";
    avatar.setAttribute("aria-hidden", "true");
    avatar.textContent = role === "user" ? userAvatarText : role === "system" ? systemAvatarText : assistantAvatarText;

    const messageLabel = document.createElement("span");
    messageLabel.className = "chat-message-label";
    messageLabel.textContent = label;

    meta.appendChild(avatar);
    meta.appendChild(messageLabel);

    const bubble = document.createElement("div");
    bubble.className = "chat-bubble";

    const reasoningPanel = buildReasoningPanel(reasoning, false);
    bubble.appendChild(reasoningPanel.details);

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
    };
    setContent(content);
    bubble.appendChild(copy);

    if (attachments.length > 0) {
      const attachmentList = document.createElement("div");
      attachmentList.className = "chat-message-attachments";
      attachments.forEach((attachment) => {
        attachmentList.appendChild(buildAttachmentChip(attachment, false));
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

    const setStreamingStatus = (nextStatus) => {
      if (!(status instanceof HTMLElement)) {
        return;
      }
      const raw = String(nextStatus || "").trim();
      status.hidden = raw === "";
      status.textContent = raw;
    };

    article.appendChild(meta);
    article.appendChild(bubble);
    return {
      article,
      copy,
      reasoningCopy: reasoningPanel.copy,
      setContent,
      setReasoning: reasoningPanel.setReasoning,
      setStreamingStatus,
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
    routeText.textContent = route ? `${route.publicName} · ${route.providerName}` : t("先从左侧选一个模型", "Choose a model from the sidebar first");

    const title = document.createElement("h2");
    title.textContent = t("Hello! How can I help you today?", "Hello! How can I help you today?");

    const copy = document.createElement("p");
    copy.textContent = t(
      "本地会话、附件上传和多标签页同步都会围绕当前模型与路由工作。",
      "Local sessions, attachment upload, and multi-tab sync all run against the currently selected route."
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
      return;
    }
    messages.forEach((message) => {
      const node = buildMessageNode(message.role, message.label || roleLabel(message.role), message.content, "", message.attachments || [], message.reasoning || "");
      thread.appendChild(node.article);
    });
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
        preview.src = attachment.url;
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
    form.dataset.streaming = "false";
    form.classList.remove("is-streaming");
    writeClientSessionID(form, session.id);
    setSelectedModel(form, session.publicName);
    updateStageHeader(root, session, routes);

    const systemPromptField = form.querySelector("textarea[name=\"chat_system_prompt\"]");
    if (systemPromptField instanceof HTMLTextAreaElement) {
      systemPromptField.value = session.systemPrompt;
    }
    const draftField = form.querySelector("#chat-draft");
    if (draftField instanceof HTMLTextAreaElement) {
      draftField.value = session.draft;
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
    scrollThread(root.querySelector("[data-chat-scroll]"));
  };

  const renderSessionList = (root, store) => {
    const container = root.querySelector("[data-chat-session-store]");
    const counter = root.querySelector("[data-chat-session-counter]");
    if (!container) {
      return;
    }
    if (counter) {
      counter.textContent = String(store.sessions.length);
    }
    container.replaceChildren();
    if (!store.sessions.length) {
      const empty = document.createElement("div");
      empty.className = "chat-sidebar-empty";
      const copy = document.createElement("p");
      copy.textContent = t("本地会话会在这里保留，并自动同步到其他已打开的 chat 标签页。", "Local conversations will appear here and sync to other open chat tabs.");
      empty.appendChild(copy);
      container.appendChild(empty);
      return;
    }
    store.sessions.forEach((session) => {
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
      title.textContent = session.title;

      const meta = document.createElement("span");
      meta.className = "chat-session-meta";
      meta.textContent = session.publicName
        ? `${session.publicName} · ${session.messages.length} ${t("条消息", "messages")}`
        : `${session.messages.length} ${t("条消息", "messages")}`;

      select.appendChild(title);
      select.appendChild(meta);

      const actions = document.createElement("div");
      actions.className = "chat-session-actions";

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

      actions.appendChild(rename);
      actions.appendChild(remove);

      entry.appendChild(select);
      entry.appendChild(actions);
      container.appendChild(entry);
    });
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
    const renamed = { ...session, title: trimmed, customTitle: true, updatedAt: nowISO() };
    store = upsertSession(store, renamed);
    store.activeSessionId = renamed.id;
    saveStore(store, true);
    renderSessionList(root, store);
    applySession(root, renamed, routes);
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
    const confirmed = window.confirm(t("删除这个本地会话？", "Delete this local session?"));
    if (!confirmed) {
      return;
    }
    store.sessions = store.sessions.filter((item) => item.id !== sessionID);
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
    const routes = routeMap(form);
    let store = normalizeStore(loadStore(), form, routes);
    const session = createBlankSession(form, routes);
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
      return { store, session, routes };
    }
    store = upsertSession(store, session);
    store.activeSessionId = session.id;
    saveStore(store, broadcast);
    renderSessionList(root, store);
    return { store, session, routes };
  };

  const queuePersist = () => {
    window.clearTimeout(persistTimer);
    persistTimer = window.setTimeout(() => {
      persistCurrentSession(true);
    }, 180);
  };

  const hydrateWorkspace = (preferServerState) => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form) {
      return;
    }
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
    const active = store.sessions.find((session) => session.id === store.activeSessionId) || store.sessions[0] || serverSession;
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
    const active = store.sessions.find((session) => session.id === store.activeSessionId) || store.sessions[0];
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
    }
    writeHiddenJSON(form, "chat_draft_attachments_json", attachments);
    renderDraftAttachments(currentRoot(), attachments);
    form.submit();
  };

  const enableForm = (form) => {
    form.dataset.streaming = "false";
    form.classList.remove("is-streaming");
    form.querySelectorAll("button, input, textarea, select").forEach((element) => {
      if (!(element instanceof HTMLButtonElement || element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement)) {
        return;
      }
      if (element.name === "chat_message_role" || element.name === "chat_message_content") {
        return;
      }
      element.disabled = false;
    });
  };

  const replaceChatContent = (html) => {
    const root = currentRoot();
    if (!root) {
      return;
    }
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
    } catch (_error) {
      setAttachmentStatus(root, t("附件上传失败。", "Attachment upload failed."), true);
    }
  };

  const streamConsoleChat = async (form) => {
    if (form.dataset.streaming === "true") {
      return;
    }
    const draftField = form.querySelector("#chat-draft");
    if (!(draftField instanceof HTMLTextAreaElement)) {
      return;
    }
    const draftValue = draftField.value.trim();
    const attachments = readHiddenJSON(form, "chat_draft_attachments_json", []).map(normalizeAttachment).filter(Boolean);
    if (!draftValue && attachments.length === 0) {
      return;
    }

    persistCurrentSession(false);

    const thread = form.querySelector("[data-chat-scroll]");
    const streamURL = form.dataset.chatStreamUrl;
    if (!thread || !streamURL) {
      restoreDraftAndSubmit(form, draftValue, attachments);
      return;
    }

    const formData = new FormData(form);
    formData.set("chat_draft", draftValue);

    form.dataset.streaming = "true";
    form.classList.add("is-streaming");
    form.querySelectorAll("button, input, textarea, select").forEach((element) => {
      if (!(element instanceof HTMLButtonElement || element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement)) {
        return;
      }
      if (element.name === "chat_message_role" || element.name === "chat_message_content") {
        return;
      }
      element.disabled = true;
    });

    if (thread.querySelector(".chat-empty-state")) {
      thread.replaceChildren();
    }
    const userMessage = buildMessageNode("user", roleLabel("user"), draftValue || t("已附带附件。", "Attachments included."), "", attachments);
    const assistantMessage = buildMessageNode("assistant", roleLabel("assistant"), "", form.dataset.chatStreamingLabel || "Streaming", [], "");
    thread.appendChild(userMessage.article);
    thread.appendChild(assistantMessage.article);
    scrollThread(thread);
    draftField.value = "";
    writeHiddenJSON(form, "chat_draft_attachments_json", []);
    renderDraftAttachments(currentRoot(), []);
    setAttachmentStatus(currentRoot(), "", false);

    let replaced = false;
    try {
      const response = await fetch(streamURL, {
        method: "POST",
        body: formData,
        credentials: "same-origin",
        headers: { Accept: "application/x-ndjson" },
      });

      if (response.redirected && response.url) {
        window.location.href = response.url;
        return;
      }
      if (!response.ok || !response.body) {
        restoreDraftAndSubmit(form, draftValue, attachments);
        return;
      }

      const contentType = response.headers.get("content-type") || "";
      if (!contentType.includes("application/x-ndjson")) {
        restoreDraftAndSubmit(form, draftValue, attachments);
        return;
      }

      await decodeStreamEvents(response, (event) => {
        if (event.type === "delta") {
          assistantMessage.setContent(`${assistantMessage.copy.dataset.chatMarkdownSource || ""}${event.delta || ""}`);
          assistantMessage.setStreamingStatus(form.dataset.chatStreamingLabel || "Streaming");
          scrollThread(thread);
          return;
        }
        if (event.type === "reasoning") {
          assistantMessage.setReasoning(`${assistantMessage.reasoningCopy.dataset.chatMarkdownSource || ""}${event.reasoning || ""}`, true);
          assistantMessage.setStreamingStatus(t("正在思考", "Reasoning"));
          scrollThread(thread);
          return;
        }
        if (event.type === "replace") {
          replaced = true;
          replaceChatContent(event.html || "");
        }
      });
    } catch (_error) {
      restoreDraftAndSubmit(form, draftValue, attachments);
      return;
    } finally {
      if (!replaced) {
        enableForm(form);
      }
    }
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

    const newSessionLink = event.target.closest(".chat-sidebar-new, .chat-reset-button");
    if (newSessionLink && form.contains(newSessionLink)) {
      event.preventDefault();
      createNewSession();
      return;
    }

    const actionTarget = event.target.closest("[data-chat-action]");
    if (!actionTarget || !form.contains(actionTarget) && !root.contains(actionTarget)) {
      return;
    }
    const sessionID = String(actionTarget.dataset.chatSessionId || "").trim();
    switch (actionTarget.dataset.chatAction) {
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
      const session = captureSessionFromDOM(form, normalizeStore(loadStore(), form, routes), routes);
      updateStageHeader(root, session, routes);
      renderThread(root, session.messages, routes.get(session.publicName) || null);
      const picker = target.closest(".chat-model-picker");
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
    if (target.id === "chat-draft" || target.name === "chat_system_prompt") {
      queuePersist();
    }
  });

  document.addEventListener("submit", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLFormElement)) {
      return;
    }
    if (!target.matches(".chat-shell[data-chat-stream-url]")) {
      return;
    }
    if (!supportsStreaming()) {
      persistCurrentSession(false);
      return;
    }
    event.preventDefault();
    void streamConsoleChat(target);
  });

  window.addEventListener("storage", (event) => {
    if (event.key !== storageKey) {
      return;
    }
    applyStoredSessions();
  });

  if (syncChannel) {
    syncChannel.addEventListener("message", () => {
      applyStoredSessions();
    });
  }

  hydrateWorkspace(false);
})();
