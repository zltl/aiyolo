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
  const syncLucideIcons = () => {
    if (!window.lucide || typeof window.lucide.createIcons !== "function") {
      return;
    }
    window.lucide.createIcons();
  };
  const sidebarPreferenceKey = "aiyolo.console.chat.sidebarCollapsed";
  const ownProperty = Object.prototype.hasOwnProperty;
  let activeStreamController = null;
  let activeStreamStopRequested = false;
  const queuedTurns = [];

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
  const environmentOptionInputs = (form = currentForm()) => Array.from(form?.querySelectorAll("[data-chat-environment-option]") || []).filter((input) => input instanceof HTMLInputElement);
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
    return next === "local" ? "本地 Chat" : next;
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

  const currentSelectedModel = (form) => {
    const checked = form?.querySelector("input[name=\"chat_public_name\"]:checked");
    if (checked instanceof HTMLInputElement) {
      return checked.value.trim();
    }
    const first = form?.querySelector("input[name=\"chat_public_name\"]");
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
  const reasoningEffortPickerMenu = (form = currentForm()) => reasoningEffortPicker(form)?.querySelector(".chat-reasoning-picker-menu") || null;
  const reasoningEffortPickerCopy = (form = currentForm()) => form?.querySelector("[data-chat-reasoning-picker-copy]") || null;

  const refreshReasoningOptionStates = (form = currentForm()) => {
    form?.querySelectorAll("[data-chat-reasoning-option]").forEach((input) => {
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
    form?.querySelectorAll("[data-chat-reasoning-option]").forEach((input) => {
      if (!(input instanceof HTMLInputElement)) {
        return;
      }
      const checked = String(input.value || "").trim().toLowerCase() === normalized;
      input.checked = checked;
      matched = matched || checked;
    });
    if (!matched) {
      const fallback = form?.querySelector("[data-chat-reasoning-option][value='']");
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

  const syncReasoningEffortControl = (form, route, preferredValue) => {
    const control = reasoningEffortControl(form);
    const input = reasoningEffortInput(form);
    const picker = reasoningEffortPicker(form);
    const menu = reasoningEffortPickerMenu(form);
    const copy = reasoningEffortPickerCopy(form);
    if (!(input instanceof HTMLInputElement) || !(menu instanceof HTMLElement)) {
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
      copy.textContent = input.value ? reasoningEffortLabel(input.value) : reasoningEffortDefaultLabel();
    }
  };

  const defaultSystemPrompt = (form) => {
    const field = form?.querySelector("textarea[name=\"chat_system_prompt\"]");
    if (!(field instanceof HTMLTextAreaElement)) {
      return "";
    }
    return String(field.defaultValue || field.value || "").trim();
  };

  const composerPrimaryButton = (form = currentForm()) => form?.querySelector("[data-chat-action=\"composer-primary\"]") || null;
  const composerPrimaryStartGlyph = (form = currentForm()) => composerPrimaryButton(form)?.querySelector("[data-chat-primary-start]") || null;
  const composerPrimaryStopGlyph = (form = currentForm()) => composerPrimaryButton(form)?.querySelector("[data-chat-primary-stop]") || null;
  const composerQueueIndicator = (form = currentForm()) => form?.querySelector("[data-chat-queue-indicator]") || null;
  const shellLaunchButton = (form = currentForm()) => form?.querySelector("[data-chat-action=\"open-shell\"]") || null;
  const shellSocketBaseURL = (form = currentForm()) => String(form?.dataset.chatShellSocketUrl || "").trim();
  const shellDock = (form = currentForm()) => form?.querySelector("[data-chat-shell-dock]") || null;
  const shellTerminalHost = (form = currentForm()) => form?.querySelector("[data-chat-shell-terminal]") || null;
  const shellStatusNode = (form = currentForm()) => form?.querySelector("[data-chat-shell-status]") || null;
  const shellMetaNode = (form, key) => form?.querySelector(`[data-chat-shell-${key}]`) || null;
  const shellHeightPreferenceKey = "aiyolo.console.chat.shellHeight";
  const shellDefaultHeight = 360;
  const shellMinHeight = 240;
  let activeShellController = null;
  let activeShellForm = null;
  let activeShellSocketURL = "";
  let activeShellSessionID = "";
  let shellResizeState = null;

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

  const renderShellMeta = (form, meta = {}) => {
    setShellMetaValue(shellMetaNode(form, "session"), meta.sessionID || activeShellSessionID);
    setShellMetaValue(shellMetaNode(form, "worker"), meta.workerID);
    setShellMetaValue(shellMetaNode(form, "container"), meta.containerName);
    setShellMetaValue(shellMetaNode(form, "workspace"), meta.workspacePath);
  };

  const setShellDockVisible = (form, visible) => {
    const dock = shellDock(form);
    if (!(dock instanceof HTMLElement) || !(form instanceof HTMLFormElement)) {
      return;
    }
    dock.hidden = !visible;
    form.classList.toggle("is-shell-open", visible);
    if (!visible) {
      return;
    }
    applyShellHeight(form, preferredShellOpenHeight());
    window.requestAnimationFrame(() => {
      activeShellController?.refresh?.();
      activeShellController?.focus?.();
    });
  };

  const disposeShellController = () => {
    activeShellController?.dispose?.();
    activeShellController = null;
    activeShellForm = null;
  };

  const ensureShellController = (form) => {
    if (!(form instanceof HTMLFormElement)) {
      return null;
    }
    const terminalHost = shellTerminalHost(form);
    const status = shellStatusNode(form);
    if (!(terminalHost instanceof HTMLElement) || !(status instanceof HTMLElement)) {
      return null;
    }
    if (activeShellController && activeShellForm === form && document.contains(terminalHost)) {
      return activeShellController;
    }
    disposeShellController();
    if (!window.AIYoloChatShell || typeof window.AIYoloChatShell.createController !== "function") {
      setInlineFlash(currentRoot(), t("Shell 依赖加载失败。", "Shell dependencies failed to load."), true);
      return null;
    }
    activeShellController = window.AIYoloChatShell.createController({
      terminalHost,
      statusNode: status,
      getSocketPath: () => activeShellSocketURL,
      autoConnect: false,
    });
    activeShellForm = activeShellController ? form : null;
    return activeShellController;
  };

  const closeChatShellDock = (form, options = {}) => {
    activeShellController?.close?.();
    if (options.resetSession !== false) {
      activeShellSocketURL = "";
      activeShellSessionID = "";
    }
    renderShellMeta(form, {});
    setShellDockVisible(form, false);
  };

  const syncChatShellSession = (form) => {
    if (!(form instanceof HTMLFormElement)) {
      return;
    }
    applyShellHeight(form, preferredShellOpenHeight());
    if (activeShellSessionID === "" || activeShellSessionID === readClientSessionID(form)) {
      return;
    }
    closeChatShellDock(form);
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

  const updateComposerControls = (form = currentForm()) => {
    if (!form) {
      return;
    }
    const button = composerPrimaryButton(form);
    const startGlyph = composerPrimaryStartGlyph(form);
    const stopGlyph = composerPrimaryStopGlyph(form);
    const indicator = composerQueueIndicator(form);
    const shellButton = shellLaunchButton(form);
    const streaming = form.dataset.streaming === "true";
    const payload = readDraftPayload(form);

    if (button instanceof HTMLButtonElement) {
      if (streaming) {
        button.hidden = false;
        button.disabled = false;
        button.classList.add("is-stop");
        button.setAttribute("aria-label", t("终止当前回复", "Stop current reply"));
      } else {
        const shouldShow = hasDraftPayload(payload);
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
    if (indicator instanceof HTMLElement) {
      const queuedCount = queuedTurns.length;
      indicator.hidden = queuedCount === 0;
      indicator.textContent = queuedCount > 0 ? t(`已排队 ${queuedCount} 条`, `Queued ${queuedCount}`) : "";
    }
    if (shellButton instanceof HTMLButtonElement) {
      const hasShellSocket = shellSocketBaseURL(form) !== "";
      const canOpenShell = hasShellSocket && currentSelectedEnvironment(form) !== "local" && currentSelectedModel(form) !== "";
      shellButton.hidden = !hasShellSocket;
      shellButton.disabled = !canOpenShell;
      shellButton.title = canOpenShell
        ? t("打开 Claude Agent 容器 Shell", "Open the Claude Agent container shell")
        : t("切换到 Cloud Agent 环境后即可打开 shell", "Switch to a Cloud Agent environment to open the shell");
    }
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
        reasoningEfforts: parseReasoningEfforts(option?.dataset.chatReasoningEfforts),
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

  const buildReasoningPanel = (reasoning, open = false) => {
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
      syncLucideIcons();
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
    return {
      article,
      copy,
      reasoningCopy: reasoningPanel.copy,
      setContent,
      setReasoning: reasoningPanel.setReasoning,
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
      const node = buildMessageNode(message.role, message.label || roleLabel(message.role), message.content, "", message.attachments || [], message.reasoning || "");
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
    form.dataset.streaming = "false";
    form.classList.remove("is-streaming");
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
    updateComposerControls(form);
    syncChatShellSession(form);
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

  const hydrateWorkspace = (preferServerState) => {
    const root = currentRoot();
    const form = currentForm();
    if (!root || !form) {
      return;
    }
    applyShellHeight(form, readShellHeightPreference());
    renderShellMeta(form, {});
    setSidebarCollapsed(form, false);
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
      syncDraftFieldHeight(draftField);
    }
    writeHiddenJSON(form, "chat_draft_attachments_json", attachments);
    renderDraftAttachments(currentRoot(), attachments);
    form.submit();
  };

  const canInteractWhileStreaming = (element, draftField) => {
    if (element === draftField) {
      return true;
    }
    if (element instanceof HTMLInputElement && element.id === "chat-attachment-input") {
      return true;
    }
    if (!(element instanceof HTMLButtonElement)) {
      return false;
    }
    const action = String(element.dataset.chatAction || "").trim();
    return action === "composer-primary" || action === "pick-attachments" || action === "remove-attachment";
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
    const payload = readDraftPayload(form, options);
    if (!hasDraftPayload(payload)) {
      updateComposerControls(form);
      return false;
    }
    queuedTurns.push({
      prompt: payload.prompt,
      userVisibleText: payload.userVisibleText,
      attachments: payload.attachments,
    });
    clearComposerDraft(form);
    const queuedCount = queuedTurns.length;
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

  const stopActiveStream = () => {
    if (!(activeStreamController instanceof AbortController)) {
      return false;
    }
    activeStreamStopRequested = true;
    activeStreamController.abort();
    return true;
  };

  const startQueuedTurn = () => {
    const form = currentForm();
    if (!form || form.dataset.streaming === "true" || queuedTurns.length === 0) {
      updateComposerControls(form);
      return false;
    }
    const nextTurn = queuedTurns.shift();
    updateComposerControls(form);
    window.setTimeout(() => {
      const nextForm = currentForm();
      if (!nextForm || nextForm.dataset.streaming === "true") {
        queuedTurns.unshift(nextTurn);
        updateComposerControls(currentForm());
        return;
      }
      void streamConsoleChat(nextForm, nextTurn);
    }, 0);
    return true;
  };

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
    form.dataset.streaming = "false";
    form.classList.remove("is-streaming");
    form.querySelectorAll("button, input, textarea, select").forEach((element) => {
      if (!(element instanceof HTMLButtonElement || element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement)) {
        return;
      }
      if (element.dataset.chatStreamDisabled !== "true") {
        return;
      }
      element.disabled = false;
      delete element.dataset.chatStreamDisabled;
    });
    updateComposerControls(form);
  };

  const replaceChatContent = (html) => {
    const root = currentRoot();
    if (!root) {
      return;
    }
    disposeShellController();
    activeShellSocketURL = "";
    activeShellSessionID = "";
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

  const ensureChatEnvironment = async (form) => {
    const root = currentRoot();
    const field = environmentField(form);
    const ensureURL = environmentEnsureURL(form);
    if (!root || !(field instanceof HTMLInputElement || field instanceof HTMLSelectElement) || ensureURL === "") {
      return null;
    }
    const environment = currentSelectedEnvironment(form);
    if (environment !== "local" && readClientSessionID(form) === "") {
      writeClientSessionID(form, makeID("chat"));
    }
    const payload = new FormData();
    if (readClientSessionID(form) !== "") {
      payload.set("chat_client_session_id", readClientSessionID(form));
    }
    payload.set("chat_environment", environment);
    payload.set("chat_public_name", currentSelectedModel(form));
    payload.set("chat_reasoning_effort", currentSelectedReasoningEffort(form));
    const previousDisabled = field.disabled;
    field.disabled = true;
    setEnvironmentPickerBusy(form, true);
    setAttachmentStatus(root, environment === "local"
      ? t("正在切回本地环境…", "Switching back to local chat…")
      : t("正在启动 Cloud Agent…", "Starting cloud agent…"), false);
    try {
      const response = await fetch(ensureURL, {
        method: "POST",
        body: payload,
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      });
      const raw = await response.text();
      const parsed = safeParseJSON(raw, null);
      if (!response.ok || !parsed || typeof parsed !== "object") {
        throw new Error(String(parsed?.error || t("环境准备失败。", "Failed to prepare the selected environment.")));
      }
      if (typeof parsed.environment === "string" && parsed.environment.trim() !== "") {
        setSelectedEnvironment(form, parsed.environment.trim());
      }
      if (typeof parsed.sessionId === "string" && parsed.sessionId.trim() !== "") {
        writeClientSessionID(form, parsed.sessionId.trim());
      }
      setAttachmentStatus(root, "", false);
      setInlineFlash(root, String(parsed.notice || "").trim(), false);
      queuePersist();
      return parsed;
    } catch (error) {
      setAttachmentStatus(root, "", false);
      setInlineFlash(root, String(error?.message || t("环境准备失败。", "Failed to prepare the selected environment.")), true);
      throw error;
    } finally {
      field.disabled = previousDisabled;
      setEnvironmentPickerBusy(form, false);
      updateComposerControls(form);
    }
  };

  const openChatShell = async (form) => {
    const root = currentRoot();
    const baseSocketURL = shellSocketBaseURL(form);
    if (!root || baseSocketURL === "") {
      return;
    }
    if (currentSelectedEnvironment(form) === "local") {
      setInlineFlash(root, t("先选择 Cloud Agent 环境，再打开 shell。", "Select a Cloud Agent environment before opening the shell."), true);
      return;
    }
    if (currentSelectedModel(form) === "") {
      setInlineFlash(root, t("先选择一个模型，再打开 shell。", "Choose a model before opening the shell."), true);
      return;
    }
    let ensured = null;
    try {
      ensured = await ensureChatEnvironment(form);
    } catch (_error) {
      return;
    }
    const sessionID = String(ensured?.sessionId || readClientSessionID(form)).trim();
    if (sessionID === "") {
      setInlineFlash(root, t("Shell 会话未生成，请重试。", "The shell session could not be created. Please retry."), true);
      return;
    }
    const nextURL = new URL(baseSocketURL, window.location.href);
    nextURL.searchParams.set("session", sessionID);
    const nextSocketURL = nextURL.toString();
    if (activeShellSessionID === sessionID && activeShellSocketURL === nextSocketURL && activeShellController?.isConnected?.()) {
      setShellDockVisible(form, true);
      activeShellController.focus?.();
      return;
    }
    activeShellSocketURL = nextSocketURL;
    activeShellSessionID = sessionID;
    renderShellMeta(form, {
      sessionID,
      workerID: ensured?.workerId,
      containerName: ensured?.containerName,
      workspacePath: ensured?.workspacePath,
    });
    setShellDockVisible(form, true);
    const controller = ensureShellController(form);
    if (!controller) {
      activeShellSocketURL = "";
      activeShellSessionID = "";
      renderShellMeta(form, {});
      setShellDockVisible(form, false);
      return;
    }
    controller.connect({ resetTerminal: true });
    window.setTimeout(() => {
      controller.refresh?.();
      controller.focus?.();
    }, 0);
  };

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

  const streamConsoleChat = async (form, options = {}) => {
    if (form.dataset.streaming === "true") {
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
    let preserveLocalTranscript = promptValue !== draftValue;
    const assistantHistoryMessage = {
      id: makeID("msg"),
      role: "assistant",
      label: roleLabel("assistant"),
      content: "",
      reasoning: "",
      attachments: [],
    };

    if (currentSelectedEnvironment(form) !== "local") {
      try {
        await ensureChatEnvironment(form);
      } catch (_error) {
        return;
      }
    }

    persistCurrentSession(false);

    const root = currentRoot();
    const thread = form.querySelector("[data-chat-scroll]");
    const streamURL = form.dataset.chatStreamUrl;
    const resumeURL = form.dataset.chatStreamResumeUrl;
    const allowReconnect = isCloudAgentEnvironment(currentSelectedEnvironment(form)) && String(resumeURL || "").trim() !== "";
    if (!thread || !streamURL) {
      restoreDraftAndSubmit(form, promptValue, attachments);
      return;
    }

    const formData = new FormData(form);
    formData.set("chat_draft", promptValue);
    formData.set("chat_draft_attachments_json", JSON.stringify(attachments));

    form.dataset.streaming = "true";
    form.classList.add("is-streaming");
    const abortController = new AbortController();
    activeStreamController = abortController;
    activeStreamStopRequested = false;
    form.querySelectorAll("button, input, textarea, select").forEach((element) => {
      if (!(element instanceof HTMLButtonElement || element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement)) {
        return;
      }
      if (element.name === "chat_message_role" || element.name === "chat_message_content") {
        return;
      }
      if (canInteractWhileStreaming(element, draftField)) {
        delete element.dataset.chatStreamDisabled;
        return;
      }
      if (!element.disabled) {
        element.dataset.chatStreamDisabled = "true";
        element.disabled = true;
      }
    });
    setInlineFlash(root, "", false);

    if (thread.querySelector(".chat-empty-state")) {
      thread.replaceChildren();
    }
    const userMessage = buildMessageNode("user", roleLabel("user"), draftValue || t("已附带附件。", "Attachments included."), "", attachments);
    const assistantMessage = buildMessageNode("assistant", roleLabel("assistant"), "", form.dataset.chatStreamingLabel || "Streaming", [], "");
    thread.appendChild(userMessage.article);
    thread.appendChild(assistantMessage.article);
    scrollThread(thread);
    clearComposerDraft(form);
    updateComposerControls(form);

    const syncCurrentHistory = () => syncStreamHistory(form, baseMessages, userHistoryMessage, assistantHistoryMessage);
    syncCurrentHistory();
    updateCurrentSessionMetadata({ status: "streaming", lastError: "" });
    const restartStream = (nextPrompt, nextVisibleText, nextAttachments = []) => {
      const nextDraftField = form.querySelector("#chat-draft");
      if (!(nextDraftField instanceof HTMLTextAreaElement)) {
        return;
      }
      nextDraftField.value = nextVisibleText;
      syncDraftFieldHeight(nextDraftField);
      writeHiddenJSON(form, "chat_draft_attachments_json", nextAttachments);
      renderDraftAttachments(currentRoot(), nextAttachments);
      setInlineFlash(currentRoot(), "", false);
      void streamConsoleChat(form, { prompt: nextPrompt, userVisibleText: nextVisibleText });
    };
    const continueStream = () => restartStream(continuationPrompt(), t("继续生成", "Continue"));
    const retryStream = () => restartStream(promptValue, draftValue || promptValue, attachments);
    const finalizeInterruptedStream = (messageText, options = {}) => {
      if (streamInterrupted) {
        return;
      }
      streamInterrupted = true;
      const hasPartial = Boolean(String(assistantHistoryMessage.content || "").trim() || String(assistantHistoryMessage.reasoning || "").trim());
      syncCurrentHistory();
      queuePersist();
      updateCurrentSessionMetadata({
        status: hasPartial ? "interrupted" : "failed",
        lastError: String(messageText || "").trim(),
      });
      assistantMessage.setStreamingStatus(String(options.statusText || t("输出已中断", "Interrupted")), String(options.statusTone || "error"));
      assistantMessage.setActions(Array.isArray(options.actions)
        ? options.actions
        : hasPartial
          ? [{ label: t("继续生成", "Continue"), onClick: continueStream }]
          : [{ label: t("重试", "Retry"), onClick: retryStream }]);
      setInlineFlash(currentRoot(), messageText, options.isError !== false);
      scrollThread(thread);
    };
    const applyStreamMessage = (message) => {
      if (!message || typeof message !== "object") {
        return;
      }
      if (typeof message.content === "string" && message.content.trim() !== "") {
        assistantHistoryMessage.content = message.content;
        assistantMessage.setContent(assistantHistoryMessage.content);
      }
      if (typeof message.reasoning === "string" && message.reasoning.trim() !== "") {
        assistantHistoryMessage.reasoning = message.reasoning;
        assistantMessage.setReasoning(assistantHistoryMessage.reasoning, true);
      }
    };

    let replaced = false;
    let sawStreamEvent = false;
    let streamCompleted = false;
    let streamErrored = false;
    let streamInterrupted = false;
    let streamOpened = false;
    let reconnectAttempt = 0;
    const hasStreamProgress = () => Boolean(streamOpened || sawStreamEvent || String(assistantHistoryMessage.content || "").trim() !== "" || String(assistantHistoryMessage.reasoning || "").trim() !== "");
    const waitForReconnect = async () => {
      reconnectAttempt += 1;
      assistantMessage.setStreamingStatus(t("连接已断开，正在重连…", "Connection lost, reconnecting..."), "heartbeat");
      assistantMessage.setActions([]);
      scrollThread(thread);
      await delayWithAbort(abortController.signal, Math.min(4000, 400 * reconnectAttempt));
    };
    const handleStreamEvent = (event) => {
      sawStreamEvent = true;
      if (event.type === "sync") {
        applyStreamMessage(event.message);
        updateCurrentSessionMetadata({ status: "streaming", lastError: "" });
        assistantMessage.setStreamingStatus(t("已重新连接，继续等待输出…", "Reconnected, waiting for more output..."), "heartbeat");
        assistantMessage.setActions([]);
        scrollThread(thread);
        return;
      }
      if (event.type === "delta") {
        assistantHistoryMessage.content += String(event.delta || "");
        assistantMessage.setContent(assistantHistoryMessage.content);
        assistantMessage.setStreamingStatus(form.dataset.chatStreamingLabel || "Streaming", "streaming");
        assistantMessage.setActions([]);
        scrollThread(thread);
        return;
      }
      if (event.type === "reasoning") {
        assistantHistoryMessage.reasoning += String(event.reasoning || "");
        assistantMessage.setReasoning(assistantHistoryMessage.reasoning, true);
        assistantMessage.setStreamingStatus(t("正在思考", "Reasoning"), "reasoning");
        assistantMessage.setActions([]);
        scrollThread(thread);
        return;
      }
      if (event.type === "heartbeat") {
        assistantMessage.setStreamingStatus(t("连接保持中，等待更多输出…", "Connection alive, waiting for more output..."), "heartbeat");
        scrollThread(thread);
        return;
      }
      if (event.type === "done") {
        streamCompleted = true;
        applyStreamMessage(event.message);
        syncCurrentHistory();
        queuePersist();
        updateCurrentSessionMetadata({ status: "completed", lastError: "" });
        if (String(event?.result?.finishReason || "").trim().toLowerCase() === "length") {
          preserveLocalTranscript = true;
          assistantMessage.setStreamingStatus(t("已达到输出上限", "Reached output limit"), "warning");
          assistantMessage.setActions([{ label: t("继续生成", "Continue"), onClick: continueStream, kind: "primary" }]);
        } else {
          assistantMessage.setStreamingStatus(t("已完成", "Completed"), "done");
          assistantMessage.setActions([]);
          setInlineFlash(currentRoot(), "", false);
        }
        scrollThread(thread);
        return;
      }
      if (event.type === "error") {
        streamErrored = true;
        applyStreamMessage(event.message);
        finalizeInterruptedStream(String(event.error || t("连接中断，最后一段回复没有完整结束。", "The stream was interrupted before the answer finished.")).trim());
        return;
      }
      if (event.type === "replace") {
        if (streamErrored || preserveLocalTranscript) {
          return;
        }
        replaced = true;
        replaceChatContent(event.html || "");
      }
    };
    const openNDJSONResponse = async (response) => {
      if (response.redirected && response.url) {
        window.location.href = response.url;
        return false;
      }
      if (!response.ok || !response.body) {
        throw new Error("stream_unavailable");
      }
      const contentType = response.headers.get("content-type") || "";
      if (!contentType.includes("application/x-ndjson")) {
        throw new Error("stream_unavailable");
      }
      streamOpened = true;
      reconnectAttempt = 0;
      await decodeStreamEvents(response, handleStreamEvent);
      return true;
    };
    try {
      while (true) {
        try {
          const response = await fetch(allowReconnect && reconnectAttempt > 0
            ? `${resumeURL}?session=${encodeURIComponent(readClientSessionID(form))}`
            : streamURL, allowReconnect && reconnectAttempt > 0
            ? {
              method: "GET",
              signal: abortController.signal,
              credentials: "same-origin",
              headers: { Accept: "application/x-ndjson" },
            }
            : {
              method: "POST",
              body: formData,
              signal: abortController.signal,
              credentials: "same-origin",
              headers: { Accept: "application/x-ndjson" },
            });
          const handled = await openNDJSONResponse(response);
          if (handled === false || streamCompleted || streamErrored || replaced) {
            break;
          }
          if (!allowReconnect) {
            break;
          }
          await waitForReconnect();
        } catch (error) {
          if (activeStreamStopRequested || error?.name === "AbortError") {
            finalizeInterruptedStream(t("已终止当前回复。", "Stopped current reply."), {
              statusText: t("已终止", "Stopped"),
              statusTone: "warning",
              actions: [],
              isError: false,
            });
            return;
          }
          if (allowReconnect && !streamCompleted && !streamErrored && !replaced && hasStreamProgress()) {
            await waitForReconnect();
            continue;
          }
          if (!streamCompleted && !streamErrored && hasStreamProgress()) {
            finalizeInterruptedStream(t("连接异常，回复在完成前中断了。", "The connection dropped before the answer finished."));
            return;
          }
          restoreDraftAndSubmit(form, promptValue, attachments);
          return;
        }
      }
    } finally {
      const stoppedByUser = activeStreamStopRequested;
      activeStreamController = null;
      activeStreamStopRequested = false;
      if (streamOpened && !replaced && !streamCompleted && !streamErrored && !stoppedByUser && !allowReconnect) {
        finalizeInterruptedStream(t("连接中断，最后一段回复没有完整结束。", "The stream was interrupted before the answer finished."));
      }
      if (!replaced) {
        enableForm(form);
      } else {
        updateComposerControls(currentForm());
      }
      startQueuedTurn();
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

    const sessionMenu = event.target.closest(".chat-session-menu");
    if (sessionMenu instanceof HTMLElement) {
      closeSessionMenus(sessionMenu);
    } else {
      closeSessionMenus();
    }

    const shellActionTarget = event.target.closest("[data-chat-shell-action]");
    if (shellActionTarget instanceof HTMLElement && form.contains(shellActionTarget)) {
      switch (shellActionTarget.dataset.chatShellAction) {
        case "clear":
          event.preventDefault();
          ensureShellController(form)?.clear?.();
          return;
        case "reconnect":
          event.preventDefault();
          if (activeShellSocketURL === "") {
            void openChatShell(form);
            return;
          }
          setShellDockVisible(form, true);
          ensureShellController(form)?.connect?.({ resetTerminal: true });
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
        if (form.dataset.streaming === "true") {
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
      case "open-shell": {
        event.preventDefault();
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
      const picker = target.closest(".chat-model-picker");
      if (picker instanceof HTMLDetailsElement) {
        picker.open = false;
      }
      queuePersist();
      updateComposerControls(form);
      return;
    }
    if (target instanceof HTMLInputElement && target.matches("[data-chat-environment-option]")) {
      setSelectedEnvironment(form, target.value);
      const picker = target.closest(".chat-environment-picker");
      if (picker instanceof HTMLDetailsElement) {
        picker.open = false;
      }
      if (activeShellSessionID !== "") {
        closeChatShellDock(form);
      }
      queuePersist();
      void ensureChatEnvironment(form).catch(() => {});
      return;
    }
    if (target instanceof HTMLSelectElement && target.name === "chat_environment") {
      if (activeShellSessionID !== "") {
        closeChatShellDock(form);
      }
      queuePersist();
      void ensureChatEnvironment(form).catch(() => {});
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
        copy.textContent = input?.value ? reasoningEffortLabel(input.value) : reasoningEffortDefaultLabel();
      }
      const picker = target.closest(".chat-reasoning-picker");
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
    }
  });

  document.addEventListener("keydown", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLTextAreaElement) || target.id !== "chat-draft") {
      return;
    }
    if (event.defaultPrevented || event.isComposing || event.key !== "Enter" || event.shiftKey || event.altKey || event.ctrlKey || event.metaKey) {
      return;
    }
    const form = target.form;
    if (!(form instanceof HTMLFormElement) || !form.matches(".chat-shell[data-chat-stream-url]") || target.disabled) {
      return;
    }
    event.preventDefault();
    if (form.dataset.streaming === "true") {
      queuePendingTurn(form);
      return;
    }
    if (typeof form.requestSubmit === "function") {
      form.requestSubmit();
      return;
    }
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });

  document.addEventListener("click", (event) => {
    const form = currentForm();
    if (!form) {
      return;
    }
    form.querySelectorAll(".chat-model-picker[open], .chat-reasoning-picker[open]").forEach((picker) => {
      if (!(picker instanceof HTMLDetailsElement)) {
        return;
      }
      if (event.target instanceof Node && picker.contains(event.target)) {
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
    activeShellController?.refresh?.();
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
      activeShellController?.refresh?.();
    }
    shellResizeState = null;
  };

  document.addEventListener("pointerup", finishShellResize);
  document.addEventListener("pointercancel", finishShellResize);

  window.addEventListener("resize", () => {
    syncCurrentDraftHeight();
    activeShellController?.refresh?.();
  });

  hydrateWorkspace(false);
  syncLucideIcons();
})();
