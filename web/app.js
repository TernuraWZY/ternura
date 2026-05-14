const conversation = document.querySelector("#conversation");
const composer = document.querySelector("#composer");
const promptInput = document.querySelector("#promptInput");
const sendButton = document.querySelector("#sendButton");
const resetButton = document.querySelector("#resetButton");
const stopButton = document.querySelector("#stopButton");
const themeButton = document.querySelector("#themeButton");
const eventsList = document.querySelector("#eventsList");
const consoleStatus = document.querySelector("#consoleStatus");
const sessionState = document.querySelector("#sessionState");
const runState = document.querySelector("#runState");
const contextState = document.querySelector("#contextState");
const memoryState = document.querySelector("#memoryState");

const AUTO_SCROLL_THRESHOLD = 220;
const THEME_STORAGE_KEY = "ternura-theme";
const LIGHT_THEME = "light";
const DARK_THEME = "dark";

let controller = null;
let eventCount = 1;
let followConversation = true;
let scrollFrame = null;
let currentRun = null;
const reducedMotionQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
const colorSchemeQuery = window.matchMedia("(prefers-color-scheme: dark)");

applyTheme(getInitialTheme(), false);

themeButton.addEventListener("click", () => {
  const nextTheme = document.documentElement.dataset.theme === DARK_THEME ? LIGHT_THEME : DARK_THEME;
  applyTheme(nextTheme, true);
});

colorSchemeQuery.addEventListener("change", () => {
  if (!readStoredTheme()) {
    applyTheme(preferredSystemTheme(), false);
  }
});

conversation.addEventListener("scroll", () => {
  followConversation = isConversationNearBottom();
}, { passive: true });

composer.addEventListener("submit", async (event) => {
  event.preventDefault();

  const message = promptInput.value.trim();
  if (!message || controller) {
    return;
  }

  promptInput.value = "";
  const userMessage = addUserMessage(message);
  scrollToMessage(userMessage);
  addEvent("Prompt queued");
  setRunning(true);

  controller = new AbortController();
  const streamingMessage = addStreamingAssistantMessage();

  try {
    const response = await fetch("/api/chat/stream", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message }),
      signal: controller.signal,
    });

    if (!response.ok) {
      const text = await response.text();
      throw new Error(text || `Request failed: ${response.status}`);
    }

    await readEventStream(response, (payload) => {
      applyStreamEvent(streamingMessage, payload);
    });
    addEvent("Response complete");
  } catch (error) {
    if (error.name === "AbortError") {
      streamingMessage.setContent("Request stopped.");
      completeRunFromClient("cancelled");
      addEvent("Stopped");
    } else {
      streamingMessage.remove();
      completeRunFromClient("failed");
      addAssistantMessage(localFallback(message, error.message));
      addEvent("Local preview");
    }
  } finally {
    controller = null;
    setRunning(false);
  }
});

resetButton.addEventListener("click", async () => {
  if (scrollFrame) {
    cancelAnimationFrame(scrollFrame);
    scrollFrame = null;
  }
  conversation.replaceChildren();
  followConversation = true;
  eventCount = 0;
  currentRun = null;
  eventsList.replaceChildren();
  addEvent("Ready");
  setRunState(null);
  setState("Idle", "Ready", "Ready", "Ready");

  try {
    await fetch("/api/reset", { method: "POST" });
  } catch {
    addEvent("Preview reset");
  }
});

stopButton.addEventListener("click", () => {
  if (controller) {
    controller.abort();
  }
});

promptInput.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    composer.requestSubmit();
    return;
  }

  if (event.key === "Escape") {
    event.preventDefault();
    if (controller) {
      controller.abort();
      return;
    }
    promptInput.value = "";
  }
});

function getInitialTheme() {
  return readStoredTheme() || preferredSystemTheme();
}

function preferredSystemTheme() {
  return colorSchemeQuery.matches ? DARK_THEME : LIGHT_THEME;
}

function readStoredTheme() {
  try {
    const theme = window.localStorage.getItem(THEME_STORAGE_KEY);
    return theme === DARK_THEME || theme === LIGHT_THEME ? theme : "";
  } catch {
    return "";
  }
}

function writeStoredTheme(theme) {
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // Theme switching should keep working even when storage is blocked.
  }
}

function applyTheme(theme, persist) {
  const nextTheme = theme === DARK_THEME ? DARK_THEME : LIGHT_THEME;
  document.documentElement.dataset.theme = nextTheme;
  themeButton.setAttribute("aria-pressed", String(nextTheme === DARK_THEME));
  themeButton.setAttribute("aria-label", nextTheme === DARK_THEME ? "Switch to light mode" : "Switch to dark mode");
  themeButton.title = nextTheme === DARK_THEME ? "Switch to light mode" : "Switch to dark mode";

  if (persist) {
    writeStoredTheme(nextTheme);
  }
}

function startRun(event) {
  currentRun = {
    id: event.run_id || "",
    status: event.status || "running",
    startedAt: event.started_at || "",
    durationMS: 0,
  };
  setRunState(currentRun);
  setState("Running", "Active", "Writing", "Running");
  addEvent(`Run started · ${currentRun.id}`);
}

function finishRun(event) {
  currentRun = {
    id: event.run_id || currentRun?.id || "",
    status: event.status || "succeeded",
    startedAt: event.started_at || currentRun?.startedAt || "",
    finishedAt: event.finished_at || "",
    durationMS: event.duration_ms || 0,
  };
  setRunState(currentRun);

  const statusLabel = runStatusLabel(currentRun.status);
  const duration = formatDuration(currentRun.durationMS);
  setState("Idle", "Ready", "Ready", `${statusLabel} · ${duration}`);
  addEvent(`Run ${statusLabel.toLowerCase()} · ${currentRun.id} · ${duration}`);
}

function completeRunFromClient(status) {
  if (!currentRun || currentRun.status !== "running") {
    return;
  }

  const startedAt = currentRun.startedAt ? Date.parse(currentRun.startedAt) : Date.now();
  const durationMS = Math.max(0, Date.now() - startedAt);
  finishRun({
    run_id: currentRun.id,
    status,
    started_at: currentRun.startedAt,
    finished_at: new Date().toISOString(),
    duration_ms: durationMS,
  });
}

function setRunState(run) {
  if (!run || !run.id) {
    runState.textContent = "No run";
    runState.removeAttribute("title");
    return;
  }
  const duration = run.durationMS > 0 ? ` · ${formatDuration(run.durationMS)}` : "";
  const label = `${run.id} · ${runStatusLabel(run.status)}${duration}`;
  runState.textContent = label;
  runState.title = label;
}

function runStatusLabel(status) {
  switch (status) {
    case "running":
      return "Running";
    case "succeeded":
      return "Done";
    case "failed":
      return "Failed";
    case "cancelled":
      return "Cancelled";
    default:
      return status || "Unknown";
  }
}

function formatDuration(durationMS) {
  if (!Number.isFinite(durationMS) || durationMS <= 0) {
    return "<1s";
  }
  if (durationMS < 1000) {
    return `${durationMS}ms`;
  }
  return `${(durationMS / 1000).toFixed(durationMS < 10000 ? 1 : 0)}s`;
}

function setRunning(isRunning) {
  sendButton.disabled = isRunning;
  resetButton.disabled = isRunning;
  if (isRunning) {
    setState("Running", "Active", "Ready", "Writing");
    consoleStatus.textContent = "Running";
    addEvent("Thinking");
    return;
  }

  sessionState.textContent = "Idle";
  contextState.textContent = "Ready";
  memoryState.textContent = "Ready";
  if (!currentRun || currentRun.status === "running") {
    consoleStatus.textContent = "Ready";
  }
}

function setState(session, context, memory, label) {
  sessionState.textContent = session;
  contextState.textContent = context;
  memoryState.textContent = memory;
  if (label) {
    consoleStatus.textContent = label;
  }
}

function addEvent(label) {
  [...eventsList.children].forEach((item) => item.classList.remove("active"));
  const item = document.createElement("li");
  item.className = "event active";
  item.textContent = label;
  eventsList.prepend(item);
  eventCount += 1;

  while (eventsList.children.length > 9) {
    eventsList.lastElementChild.remove();
  }
}

function addUserMessage(text) {
  const wrapper = createMessage("user", "You");
  wrapper.append(renderMarkdown(text));
  conversation.append(wrapper);
  return wrapper;
}

function addAssistantMessage({ content, trace, pending = false }) {
  const wrapper = createMessage(pending ? "assistant pending" : "assistant", "Ternura");

  if (trace && trace.length > 0) {
    wrapper.append(createTrace(trace, pending));
  }

  const finalSection = document.createElement("section");
  finalSection.className = "final-answer";

  const finalLabel = document.createElement("span");
  finalLabel.className = "message-label secondary";
  finalLabel.textContent = "Final";

  finalSection.append(finalLabel, renderMarkdown(content || ""));
  wrapper.append(finalSection);

  conversation.append(wrapper);
  scrollConversation();
  return wrapper;
}

function addStreamingAssistantMessage() {
  const wrapper = createMessage("assistant streaming", "Ternura");
  let traceDetails = null;
  let traceList = null;
  const traceItems = new Map();
  let finalContent = "";

  const finalSection = document.createElement("section");
  finalSection.className = "final-answer";

  const finalLabel = document.createElement("span");
  finalLabel.className = "message-label secondary";
  finalLabel.textContent = "Final";

  const finalBody = renderMarkdown("");
  finalSection.append(finalLabel, finalBody);
  wrapper.append(finalSection);
  conversation.append(wrapper);

  function ensureTrace() {
    if (traceDetails) {
      return;
    }
    traceDetails = document.createElement("details");
    traceDetails.className = "trace-details";

    const summary = document.createElement("summary");
    summary.textContent = "Reasoning & tool use";
    traceDetails.append(summary);

    traceList = document.createElement("div");
    traceList.className = "trace-list";
    traceDetails.append(traceList);
    wrapper.insertBefore(traceDetails, finalSection);
  }

  function startTrace(id, type, title) {
    ensureTrace();
    const section = document.createElement("section");
    section.className = `trace-item trace-${type || "entry"}`;

    const heading = document.createElement("h3");
    heading.textContent = title || "Trace";

    const body = renderMarkdown("");
    section.append(heading, body);
    traceList.append(section);
    traceItems.set(id, { content: "", body });
    scrollConversation();
  }

  function appendTrace(id, delta) {
    if (!traceItems.has(id)) {
      startTrace(id, "entry", "Trace");
    }
    const item = traceItems.get(id);
    item.content += delta;
    item.body.innerHTML = markdownToHTML(item.content);
    scrollConversation();
  }

  return {
    remove() {
      wrapper.remove();
    },
    startTrace,
    appendTrace,
    setTrace(id, content) {
      if (!traceItems.has(id)) {
        startTrace(id, "entry", "Trace");
      }
      const item = traceItems.get(id);
      item.content = content;
      item.body.innerHTML = markdownToHTML(item.content);
      scrollConversation();
    },
    appendContent(delta) {
      finalContent += delta;
      finalBody.innerHTML = markdownToHTML(finalContent);
      scrollConversation();
    },
    setContent(content) {
      finalContent = content || "";
      finalBody.innerHTML = markdownToHTML(finalContent);
      scrollConversation();
    },
  };
}

function applyStreamEvent(message, event) {
  switch (event.type) {
    case "run_start":
      startRun(event);
      return;
    case "run_done":
    case "run_failed":
    case "run_cancelled":
      finishRun(event);
      return;
    case "start":
      return;
    case "trace_start":
      message.startTrace(event.id, event.trace_type, event.title);
      return;
    case "trace_delta":
      message.appendTrace(event.id, event.delta || "");
      return;
    case "trace_done":
      if (event.content) {
        message.setTrace(event.id, event.content);
      }
      return;
    case "content_delta":
      message.appendContent(event.delta || "");
      return;
    case "done":
      message.setContent(event.content || "");
      return;
    case "error":
      throw new Error(event.error || "stream error");
    default:
      return;
  }
}

async function readEventStream(response, onEvent) {
  if (!response.body) {
    throw new Error("Streaming response body is unavailable.");
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }

    buffer += decoder.decode(value, { stream: true });
    const parts = buffer.split("\n\n");
    buffer = parts.pop() || "";

    for (const part of parts) {
      const data = part
        .split("\n")
        .filter((line) => line.startsWith("data:"))
        .map((line) => line.slice(5).trimStart())
        .join("\n");
      if (!data) {
        continue;
      }
      onEvent(JSON.parse(data));
    }
  }

  if (buffer.trim()) {
    const data = buffer
      .split("\n")
      .filter((line) => line.startsWith("data:"))
      .map((line) => line.slice(5).trimStart())
      .join("\n");
    if (data) {
      onEvent(JSON.parse(data));
    }
  }
}

function createMessage(kind, label) {
  const wrapper = document.createElement("article");
  wrapper.className = `message ${kind}`;

  const title = document.createElement("span");
  title.className = "message-label";
  title.textContent = label;

  wrapper.append(title);
  return wrapper;
}

function createTrace(trace, pending) {
  const details = document.createElement("details");
  details.className = "trace-details";

  const summary = document.createElement("summary");
  summary.textContent = "Reasoning & tool use";
  details.append(summary);

  const list = document.createElement("div");
  list.className = "trace-list";

  trace.forEach((item) => {
    const section = document.createElement("section");
    section.className = `trace-item trace-${item.type || "entry"}`;

    const title = document.createElement("h3");
    title.textContent = item.title || "Trace";

    section.append(title, renderMarkdown(item.content || ""));
    list.append(section);
  });

  details.append(list);
  return details;
}

function scrollConversation() {
  if (!followConversation) {
    return;
  }

  scheduleConversationScroll(() => {
    if (!followConversation) {
      return;
    }
    conversation.scrollTo({
      top: conversation.scrollHeight,
      behavior: scrollBehavior("auto"),
    });
    followConversation = true;
  });
}

function scrollToMessage(message) {
  followConversation = true;
  scheduleConversationScroll(() => {
    const top = Math.max(0, message.offsetTop + message.offsetHeight - conversation.clientHeight + 20);
    conversation.scrollTo({
      top,
      behavior: scrollBehavior("smooth"),
    });
  });
}

function scheduleConversationScroll(callback) {
  if (scrollFrame) {
    cancelAnimationFrame(scrollFrame);
  }

  scrollFrame = requestAnimationFrame(() => {
    scrollFrame = null;
    callback();
  });
}

function isConversationNearBottom() {
  const distance = conversation.scrollHeight - conversation.scrollTop - conversation.clientHeight;
  return distance <= AUTO_SCROLL_THRESHOLD;
}

function scrollBehavior(behavior) {
  return reducedMotionQuery.matches ? "auto" : behavior;
}

function renderMarkdown(markdown) {
  const wrapper = document.createElement("div");
  wrapper.className = "markdown";
  wrapper.innerHTML = markdownToHTML(markdown || "");
  return wrapper;
}

function markdownToHTML(markdown) {
  const lines = markdown.replace(/\r\n/g, "\n").split("\n");
  const html = [];
  let index = 0;

  while (index < lines.length) {
    const line = lines[index];

    if (line.trim() === "") {
      index += 1;
      continue;
    }

    const fenceMatch = line.match(/^```(\S*)\s*$/);
    if (fenceMatch) {
      const language = fenceMatch[1] || "";
      const codeLines = [];
      index += 1;
      while (index < lines.length && !lines[index].match(/^```\s*$/)) {
        codeLines.push(lines[index]);
        index += 1;
      }
      if (index < lines.length) {
        index += 1;
      }
      html.push(`<pre><code${language ? ` class="language-${escapeAttribute(language)}"` : ""}>${escapeHTML(codeLines.join("\n"))}</code></pre>`);
      continue;
    }

    const headingMatch = line.match(/^(#{1,6})\s+(.+)$/);
    if (headingMatch) {
      const level = headingMatch[1].length;
      html.push(`<h${level}>${renderInline(headingMatch[2])}</h${level}>`);
      index += 1;
      continue;
    }

    if (line.match(/^\s*[-*]\s+/)) {
      const items = [];
      while (index < lines.length && lines[index].match(/^\s*[-*]\s+/)) {
        items.push(lines[index].replace(/^\s*[-*]\s+/, ""));
        index += 1;
      }
      html.push(`<ul>${items.map((item) => `<li>${renderInline(item)}</li>`).join("")}</ul>`);
      continue;
    }

    if (line.match(/^\s*\d+\.\s+/)) {
      const items = [];
      while (index < lines.length && lines[index].match(/^\s*\d+\.\s+/)) {
        items.push(lines[index].replace(/^\s*\d+\.\s+/, ""));
        index += 1;
      }
      html.push(`<ol>${items.map((item) => `<li>${renderInline(item)}</li>`).join("")}</ol>`);
      continue;
    }

    if (line.match(/^\s*>\s?/)) {
      const quotes = [];
      while (index < lines.length && lines[index].match(/^\s*>\s?/)) {
        quotes.push(lines[index].replace(/^\s*>\s?/, ""));
        index += 1;
      }
      html.push(`<blockquote>${quotes.map(renderInline).join("<br>")}</blockquote>`);
      continue;
    }

    const paragraph = [line];
    index += 1;
    while (
      index < lines.length &&
      lines[index].trim() !== "" &&
      !lines[index].match(/^```/) &&
      !lines[index].match(/^(#{1,6})\s+/) &&
      !lines[index].match(/^\s*[-*]\s+/) &&
      !lines[index].match(/^\s*\d+\.\s+/) &&
      !lines[index].match(/^\s*>\s?/)
    ) {
      paragraph.push(lines[index]);
      index += 1;
    }
    html.push(`<p>${paragraph.map(renderInline).join("<br>")}</p>`);
  }

  return html.join("");
}

function renderInline(text) {
  const codePlaceholders = [];
  let escaped = escapeHTML(text).replace(/`([^`]+)`/g, (_, code) => {
    const token = `@@CODE_${codePlaceholders.length}@@`;
    codePlaceholders.push(`<code>${code}</code>`);
    return token;
  });

  escaped = escaped.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, label, href) => {
    const safe = safeHref(href);
    return `<a href="${escapeAttribute(safe)}" target="_blank" rel="noreferrer">${label}</a>`;
  });
  escaped = escaped.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  escaped = escaped.replace(/(^|[\s(])\*([^*\n]+)\*/g, "$1<em>$2</em>");

  codePlaceholders.forEach((value, idx) => {
    escaped = escaped.replace(`@@CODE_${idx}@@`, value);
  });
  return escaped;
}

function safeHref(href) {
  const trimmed = href.trim();
  if (/^(https?:|mailto:|#|\/|\.\/|\.\.\/)/i.test(trimmed)) {
    return trimmed;
  }
  return "#";
}

function escapeHTML(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function escapeAttribute(value) {
  return escapeHTML(value).replace(/`/g, "&#96;");
}

function localFallback(message, detail) {
  return {
    trace: [
      {
        type: "think",
        title: "Thinking",
        content: [
          "The browser is showing the interactive console shell, but the Go web server is not reachable from this page.",
          "",
          `User message: ${message}`,
          detail ? `Transport detail: ${detail}` : "",
        ].filter(Boolean).join("\n"),
      },
    ],
    content: "Ternura console is ready. Run `go run ./main -serve` from the project root to connect this UI to the local agent.",
  };
}
