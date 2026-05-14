const conversation = document.querySelector("#conversation");
const composer = document.querySelector("#composer");
const promptInput = document.querySelector("#promptInput");
const sendButton = document.querySelector("#sendButton");
const resetButton = document.querySelector("#resetButton");
const stopButton = document.querySelector("#stopButton");
const eventsList = document.querySelector("#eventsList");
const consoleStatus = document.querySelector("#consoleStatus");
const sessionState = document.querySelector("#sessionState");
const contextState = document.querySelector("#contextState");
const memoryState = document.querySelector("#memoryState");

let controller = null;
let eventCount = 1;

composer.addEventListener("submit", async (event) => {
  event.preventDefault();

  const message = promptInput.value.trim();
  if (!message || controller) {
    return;
  }

  promptInput.value = "";
  addUserMessage(message);
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
      addEvent("Stopped");
    } else {
      streamingMessage.remove();
      addAssistantMessage(localFallback(message, error.message));
      addEvent("Local preview");
    }
  } finally {
    controller = null;
    setRunning(false);
  }
});

resetButton.addEventListener("click", async () => {
  conversation.replaceChildren();
  eventCount = 0;
  eventsList.replaceChildren();
  addEvent("Ready");
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

function setRunning(isRunning) {
  sendButton.disabled = isRunning;
  resetButton.disabled = isRunning;
  setState(isRunning ? "Running" : "Idle", isRunning ? "Active" : "Ready", "Ready", isRunning ? "Writing" : "Ready");
  consoleStatus.textContent = isRunning ? "Running" : "Ready";
  if (isRunning) {
    addEvent("Thinking");
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
  scrollConversation();
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
  scrollConversation();

  function ensureTrace() {
    if (traceDetails) {
      return;
    }
    traceDetails = document.createElement("details");
    traceDetails.className = "trace-details";
    traceDetails.open = true;

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
  details.open = pending;

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
  conversation.scrollTop = conversation.scrollHeight;
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
