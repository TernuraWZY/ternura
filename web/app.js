const conversation = document.querySelector("#conversation");
const themeButton = document.querySelector("#themeButton");
const historyButton = document.querySelector("#historyButton");
const historyCount = document.querySelector("#historyCount");
const historyDialog = document.querySelector("#historyDialog");
const historyCloseButton = document.querySelector("#historyCloseButton");
const restoreButton = document.querySelector("#restoreButton");
const historyState = document.querySelector("#historyState");
const historyList = document.querySelector("#historyList");
const scheduleButton = document.querySelector("#scheduleButton");
const scheduleCount = document.querySelector("#scheduleCount");
const scheduleDialog = document.querySelector("#scheduleDialog");
const scheduleCloseButton = document.querySelector("#scheduleCloseButton");
const scheduleState = document.querySelector("#scheduleState");
const scheduleList = document.querySelector("#scheduleList");
const memoryButton = document.querySelector("#memoryButton");
const memoryDialog = document.querySelector("#memoryDialog");
const memoryCloseButton = document.querySelector("#memoryCloseButton");
const memoryDialogState = document.querySelector("#memoryDialogState");
const longTermMemoryCount = document.querySelector("#longTermMemoryCount");
const longTermMemoryList = document.querySelector("#longTermMemoryList");
const shortTermMemoryCount = document.querySelector("#shortTermMemoryCount");
const shortTermMemorySummary = document.querySelector("#shortTermMemorySummary");
const shortTermMemoryList = document.querySelector("#shortTermMemoryList");
const todoCount = document.querySelector("#todoCount");
const todoList = document.querySelector("#todoList");
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
const SCHEDULE_REFRESH_INTERVAL_MS = 5000;

let eventCount = 1;
let followConversation = true;
let scrollFrame = null;
let currentRun = null;
let persistedSessions = [];
const sessionDetails = new Map();
const scheduleSnapshots = new Map();
let currentSessionID = "";
let schedulesHaveLoaded = false;
let scheduleRefreshPromise = null;
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

historyButton.addEventListener("click", async () => {
  await refreshHistoryPanel();
  openHistoryDialog();
});

historyCloseButton.addEventListener("click", () => {
  closeHistoryDialog();
});

scheduleButton.addEventListener("click", async () => {
  await refreshSchedules();
  openScheduleDialog();
});

scheduleCloseButton.addEventListener("click", () => {
  closeScheduleDialog();
});

memoryButton.addEventListener("click", async () => {
  await refreshMemoryDialog();
  openMemoryDialog();
});

memoryCloseButton.addEventListener("click", () => {
  closeMemoryDialog();
});

historyDialog.addEventListener("click", (event) => {
  if (event.target === historyDialog) {
    closeHistoryDialog();
  }
});

memoryDialog.addEventListener("click", (event) => {
  if (event.target === memoryDialog) {
    closeMemoryDialog();
  }
});

scheduleDialog.addEventListener("click", (event) => {
  if (event.target === scheduleDialog) {
    closeScheduleDialog();
  }
});

restoreButton.addEventListener("click", async () => {
  await restoreHistory();
  closeHistoryDialog();
});

historyList.addEventListener("click", async (event) => {
  const item = event.target.closest("[data-session-id]");
  if (!item) {
    return;
  }

  const sessionID = item.dataset.sessionId || "";
  if (!sessionID) {
    return;
  }

  await selectSession(sessionID);
  closeHistoryDialog();
});

loadHistory();
window.setInterval(refreshSchedules, SCHEDULE_REFRESH_INTERVAL_MS);

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

async function loadHistory() {
  try {
    applyHistory(await fetchHistory());
    await refreshMemoryStatus();
    await refreshSchedules();
    const session = currentSession();
    if (!session || sessionRunCount(session) === 0) {
      return;
    }
    if (conversation.children.length > 0) {
      return;
    }

    await restoreSession(session.session_id);
  } catch (error) {
    addEvent("History unavailable");
  }
}

async function refreshHistoryPanel() {
  try {
    applyHistory(await fetchHistory());
    await refreshMemoryStatus();
    await refreshSchedules();
  } catch {
    historyState.textContent = "History unavailable";
    restoreButton.disabled = true;
  }
}

async function fetchHistory() {
  const response = await fetch("/api/history");
  if (!response.ok) {
    throw new Error(`History request failed: ${response.status}`);
  }

  const history = await response.json();
  return history && typeof history === "object" ? history : { sessions: [], current_session_id: "" };
}

async function fetchSessionDetail(sessionID) {
  const query = sessionID ? `?session_id=${encodeURIComponent(sessionID)}` : "";
  const response = await fetch(`/api/session${query}`);
  if (!response.ok) {
    throw new Error(`Session detail request failed: ${response.status}`);
  }

  const detail = await response.json();
  return detail && typeof detail === "object" ? detail : { session: null, current_session_id: "" };
}

async function fetchMemoryStatus(sessionID) {
  const query = sessionID ? `?session_id=${encodeURIComponent(sessionID)}` : "";
  const response = await fetch(`/api/memory/status${query}`);
  if (!response.ok) {
    throw new Error(`Memory status request failed: ${response.status}`);
  }

  const status = await response.json();
  return status && typeof status === "object" ? status : null;
}

async function fetchMemoryDetail(sessionID = currentSessionID) {
  const query = sessionID ? `?session_id=${encodeURIComponent(sessionID)}` : "";
  const response = await fetch(`/api/memory${query}`);
  if (!response.ok) {
    throw new Error(`Memory detail request failed: ${response.status}`);
  }

  const detail = await response.json();
  return detail && typeof detail === "object" ? detail : null;
}

async function fetchSchedules() {
  const response = await fetch("/api/schedules");
  if (!response.ok) {
    throw new Error(`Schedules request failed: ${response.status}`);
  }

  const schedules = await response.json();
  return schedules && typeof schedules === "object" ? schedules : { tasks: [], current_session_id: "" };
}

async function refreshMemoryStatus(sessionID = currentSessionID) {
  try {
    applyMemoryStatus(await fetchMemoryStatus(sessionID));
  } catch {
    memoryState.textContent = "Unavailable";
  }
}

function applyMemoryStatus(status) {
  const longTermCount = Number(status?.long_term_count || 0);
  const shortTermTurns = Number(status?.short_term_turns || 0);
  if (longTermCount === 0 && shortTermTurns === 0) {
    memoryState.textContent = "Ready";
    return;
  }
  memoryState.textContent = `${longTermCount} LT · ${shortTermTurns} ST`;
}

async function refreshSchedules() {
  if (scheduleRefreshPromise) {
    return scheduleRefreshPromise;
  }

  scheduleRefreshPromise = refreshSchedulesNow();
  try {
    return await scheduleRefreshPromise;
  } finally {
    scheduleRefreshPromise = null;
  }
}

async function refreshSchedulesNow() {
  try {
    const payload = await fetchSchedules();
    const terminalTasks = detectScheduleTerminalChanges(payload);
    renderSchedules(payload);
    await syncCompletedSchedules(terminalTasks);
  } catch {
    scheduleCount.textContent = "0";
    scheduleState.textContent = "Schedules unavailable";
    scheduleList.replaceChildren(scheduleEmptyItem("Schedules unavailable."));
  }
}

function detectScheduleTerminalChanges(payload) {
  const tasks = Array.isArray(payload?.tasks) ? payload.tasks : [];
  const next = new Map();
  const changed = [];

  tasks.forEach((task) => {
    const id = task.id || "";
    if (!id) {
      return;
    }

    const previous = scheduleSnapshots.get(id);
    const snapshot = {
      status: task.status || "",
      title: task.title || "",
      sessionID: task.session_id || "",
      lastRunID: task.last_run_id || "",
      lastError: task.last_error || "",
    };
    next.set(id, snapshot);

    if (!schedulesHaveLoaded || !isScheduleTerminal(task.status)) {
      return;
    }
    if (!previous) {
      return;
    }
    if (previous.status !== snapshot.status || previous.lastRunID !== snapshot.lastRunID || previous.lastError !== snapshot.lastError) {
      changed.push(task);
    }
  });

  if (schedulesHaveLoaded) {
    scheduleSnapshots.forEach((previous, id) => {
      if (next.has(id) || !isScheduleActive(previous.status)) {
        return;
      }
      changed.push({
        id,
        title: previous.title || id,
        session_id: previous.sessionID || "",
        status: "completed",
        disappeared: true,
      });
    });
  }

  scheduleSnapshots.clear();
  next.forEach((value, key) => {
    scheduleSnapshots.set(key, value);
  });
  schedulesHaveLoaded = true;
  return changed;
}

function isScheduleTerminal(status) {
  return status === "completed" || status === "failed";
}

function isScheduleActive(status) {
  return status === "scheduled" || status === "running";
}

async function syncCompletedSchedules(tasks) {
  if (!tasks.length) {
    return;
  }

  tasks.forEach((task) => {
    const label = task.disappeared ? "finished" : task.status === "failed" ? "failed" : "completed";
    addEvent(`Schedule ${label} · ${task.title || task.id || "Untitled"}`);
  });

  const currentSessionTask = tasks.find((task) => task.session_id === currentSessionID && (task.last_run_id || task.disappeared));
  try {
    applyHistory(await fetchHistory());
    await refreshMemoryStatus();
  } catch {
    addEvent("Schedule result saved");
    return;
  }

  if (!currentSessionTask) {
    if (currentSessionTask) {
      addEvent("Scheduled result ready");
    }
    return;
  }

  sessionDetails.delete(currentSessionID);
  await restoreSession(currentSessionID);
  addEvent("Scheduled result loaded");
}

function renderSchedules(payload) {
  const tasks = Array.isArray(payload?.tasks) ? payload.tasks : [];
  const activeCount = tasks.filter((task) => task.status === "scheduled" || task.status === "running").length;
  scheduleCount.textContent = String(activeCount);
  scheduleButton.title = activeCount === 0 ? "No active schedules" : `Open ${activeCount} active ${activeCount === 1 ? "schedule" : "schedules"}`;
  scheduleState.textContent = tasks.length === 0 ? "No scheduled tasks" : `${activeCount} active · ${tasks.length} total`;

  if (tasks.length === 0) {
    scheduleList.replaceChildren(scheduleEmptyItem("No scheduled tasks yet."));
    return;
  }
  scheduleList.replaceChildren(...tasks.map(renderScheduleTask));
}

function renderScheduleTask(task) {
  const item = document.createElement("li");
  item.className = `schedule-card ${scheduleStatusClass(task.status)}`;

  const header = document.createElement("div");
  header.className = "schedule-card-header";

  const title = document.createElement("span");
  title.className = "schedule-title";
  title.textContent = task.title || "Untitled schedule";

  const status = document.createElement("span");
  status.className = `schedule-status ${scheduleStatusClass(task.status)}`;
  status.textContent = scheduleStatusLabel(task.status);

  header.append(title, status);

  const prompt = document.createElement("p");
  prompt.className = "schedule-prompt";
  prompt.textContent = task.prompt || "";

  const footer = document.createElement("div");
  footer.className = "schedule-card-footer";

  const meta = document.createElement("span");
  meta.className = "schedule-meta";
  meta.textContent = scheduleMeta(task);

  footer.append(meta);

  if (task.last_error) {
    const error = document.createElement("p");
    error.className = "schedule-prompt memory-content-muted";
    error.textContent = task.last_error;
    item.append(header, prompt, error, footer);
    return item;
  }

  item.append(header, prompt, footer);
  return item;
}

function scheduleEmptyItem(text) {
  const item = document.createElement("li");
  item.className = "schedule-empty";
  item.textContent = text;
  return item;
}

function scheduleStatusClass(status) {
  switch (status) {
    case "running":
      return "running";
    case "completed":
      return "completed";
    case "cancelled":
      return "cancelled";
    case "failed":
      return "failed";
    case "scheduled":
    default:
      return "scheduled";
  }
}

function scheduleStatusLabel(status) {
  switch (status) {
    case "running":
      return "Running";
    case "completed":
      return "Done";
    case "cancelled":
      return "Cancelled";
    case "failed":
      return "Failed";
    case "scheduled":
    default:
      return "Scheduled";
  }
}

function scheduleMeta(task) {
  const parts = [];
  if (task.run_at) {
    parts.push(`Run at ${formatMemoryDate(task.run_at)}`);
  }
  if (task.last_run_id) {
    parts.push(task.last_run_id);
  } else if (task.id) {
    parts.push(task.id);
  }
  return parts.join(" · ");
}

async function refreshMemoryDialog() {
  try {
    renderMemoryDialog(await fetchMemoryDetail());
  } catch {
    memoryDialogState.textContent = "Memory unavailable";
    longTermMemoryCount.textContent = "0";
    shortTermMemoryCount.textContent = "0";
    longTermMemoryList.replaceChildren(memoryEmptyItem("Long-term memory unavailable."));
    shortTermMemorySummary.textContent = "";
    shortTermMemorySummary.hidden = true;
    shortTermMemoryList.replaceChildren(memoryEmptyItem("Short-term memory unavailable."));
  }
}

function renderMemoryDialog(detail) {
  const longTerm = Array.isArray(detail?.long_term) ? detail.long_term : [];
  const shortTerm = detail?.short_term && typeof detail.short_term === "object" ? detail.short_term : {};
  const turns = Array.isArray(shortTerm.turns) ? shortTerm.turns : [];

  memoryDialogState.textContent = `${longTerm.length} long-term · ${turns.length} short-term`;
  longTermMemoryCount.textContent = String(longTerm.length);
  shortTermMemoryCount.textContent = String(turns.length);

  if (longTerm.length === 0) {
    longTermMemoryList.replaceChildren(memoryEmptyItem("No long-term memory yet."));
  } else {
    longTermMemoryList.replaceChildren(...longTerm.map(renderLongTermMemory));
  }

  shortTermMemorySummary.textContent = shortTerm.summary || "";
  shortTermMemorySummary.hidden = !shortTerm.summary;
  if (turns.length === 0) {
    shortTermMemoryList.replaceChildren(memoryEmptyItem("No short-term turns in this session yet."));
  } else {
    shortTermMemoryList.replaceChildren(...turns.slice().reverse().map(renderShortTermTurn));
  }
}

function renderLongTermMemory(memory) {
  const item = document.createElement("li");
  item.className = "memory-card";

  const header = document.createElement("div");
  header.className = "memory-card-header";

  const category = document.createElement("span");
  category.className = "memory-category";
  category.textContent = memory.category || "memory";

  const id = document.createElement("span");
  id.className = "memory-id";
  id.textContent = memory.id || "";

  header.append(category, id);

  const content = document.createElement("p");
  content.className = "memory-content";
  content.textContent = memory.content || "";

  const footer = document.createElement("div");
  footer.className = "memory-card-footer";

  const meta = document.createElement("span");
  meta.className = "memory-meta";
  meta.textContent = memory.updated_at ? `Updated ${formatMemoryDate(memory.updated_at)}` : "";

  footer.append(meta);
  item.append(header, content, footer);
  return item;
}

function renderShortTermTurn(turn) {
  const item = document.createElement("li");
  item.className = "memory-card";

  const header = document.createElement("div");
  header.className = "memory-card-header";

  const label = document.createElement("span");
  label.className = "memory-category";
  label.textContent = "turn";

  const at = document.createElement("span");
  at.className = "memory-id";
  at.textContent = turn.at ? formatMemoryDate(turn.at) : "";

  header.append(label, at);

  const user = document.createElement("p");
  user.className = "memory-content";
  user.textContent = `User: ${turn.user || ""}`;
  item.append(header, user);

  if (turn.assistant) {
    const assistant = document.createElement("p");
    assistant.className = "memory-content memory-content-muted";
    assistant.textContent = `Ternura: ${turn.assistant}`;
    item.append(assistant);
  }
  return item;
}

function memoryEmptyItem(text) {
  const item = document.createElement("li");
  item.className = "memory-empty";
  item.textContent = text;
  return item;
}

function formatMemoryDate(raw) {
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) {
    return raw;
  }
  return date.toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function applyHistory(history) {
  const sessions = Array.isArray(history.sessions) ? history.sessions : [];
  persistedSessions = sessions.map((session) => {
    const cached = sessionDetails.get(session.session_id || "");
    if (cached && hasCompleteSessionDetail(cached, session)) {
      return {
        ...cached,
        ...session,
        runs: cached.runs,
        last_run: session.last_run || cached.last_run,
        todos: session.todos || cached.todos,
      };
    }
    return session;
  });
  currentSessionID = currentSessionID || history.current_session_id || persistedSessions[persistedSessions.length - 1]?.session_id || "";
  renderHistoryPanel();
  renderTodosPanel();
}

async function restoreHistory() {
  await restoreSession(currentSessionID);
}

async function restoreSession(sessionID) {
  const session = await ensureSessionDetail(sessionID);
  if (!session) {
    return;
  }

  if (scrollFrame) {
    cancelAnimationFrame(scrollFrame);
    scrollFrame = null;
  }

  conversation.replaceChildren();
  followConversation = true;
  sessionRuns(session).forEach(renderPersistedRun);

  currentSessionID = session.session_id || currentSessionID;
  const runs = sessionRuns(session);
  currentRun = runs.length > 0 ? runFromHistory(runs[runs.length - 1]) : null;
  setRunState(currentRun);
  setState("Idle", "Ready", "Ready", "Ready");
  renderTodosPanel();
  refreshMemoryStatus();
  refreshSchedules();
  renderHistoryPanel();
  addEvent(`Session restored · ${session.title || "Untitled session"}`);
  scrollConversation();
}

async function selectSession(sessionID) {
  if (!sessionID) {
    return;
  }

  try {
    await restoreSession(sessionID);
  } catch {
    addEvent("Session unavailable");
  }
}

async function ensureSessionDetail(sessionID) {
  const session = sessionByID(sessionID);
  if (!session || hasCompleteSessionDetail(session, session)) {
    return session;
  }

  const detail = await fetchSessionDetail(sessionID);
  if (detail.current_session_id) {
    currentSessionID = detail.current_session_id;
  }
  if (!detail.session) {
    return sessionByID(sessionID);
  }
  mergeSessionDetail(detail.session);
  renderHistoryPanel();
  renderTodosPanel();
  return sessionByID(sessionID);
}

function mergeSessionDetail(session) {
  if (!session?.session_id) {
    return;
  }
  sessionDetails.set(session.session_id, session);
  persistedSessions = persistedSessions.map((item) => item.session_id === session.session_id ? { ...item, ...session } : item);
}

function renderPersistedRun(run) {
  if (run.user_message) {
    const trigger = run.trigger_kind === "schedule"
      ? addScheduleTrigger(run.user_message)
      : addUserMessage(run.user_message);
    trigger.dataset.runId = run.run_id || "";
  }
  const assistantMessage = addAssistantMessage({
    content: restoredRunContent(run),
    trace: Array.isArray(run.trace) ? run.trace : [],
  });
  assistantMessage.dataset.runId = run.run_id || "";
}

function restoredRunContent(run) {
  if (run.content) {
    return run.content;
  }
  if (run.error) {
    return `Run ${runStatusLabel(run.status).toLowerCase()}: ${run.error}`;
  }
  if (run.status === "cancelled") {
    return "Run cancelled before a final response.";
  }
  if (run.status === "running") {
    return "Run was interrupted before a final response.";
  }
  return "";
}

function runFromHistory(run) {
  return {
    id: run.run_id || "",
    status: run.status || "succeeded",
    startedAt: run.started_at || "",
    finishedAt: run.finished_at || "",
    durationMS: run.duration_ms || 0,
  };
}

function renderHistoryPanel() {
  const visibleSessions = persistedSessions.filter((session) => sessionRunCount(session) > 0);
  const sessionCount = visibleSessions.length;
  historyCount.textContent = String(sessionCount);
  historyButton.title = sessionCount === 0 ? "No saved sessions" : `Open ${sessionCount} saved ${sessionCount === 1 ? "session" : "sessions"}`;
  historyList.replaceChildren();
  restoreButton.disabled = !currentSession() || sessionRunCount(currentSession()) === 0;

  if (sessionCount === 0) {
    historyState.textContent = "No saved sessions";
    const empty = document.createElement("li");
    empty.className = "history-empty";
    empty.textContent = "No saved sessions yet";
    historyList.append(empty);
    return;
  }

  historyState.textContent = `${sessionCount} ${sessionCount === 1 ? "session" : "sessions"} saved`;
  visibleSessions.slice().reverse().forEach((session) => {
    const runCount = sessionRunCount(session);
    const lastRun = sessionLastRun(session);
    const listItem = document.createElement("li");
    listItem.className = "history-card";
    const button = document.createElement("button");
    button.className = "history-item";
    button.type = "button";
    button.dataset.sessionId = session.session_id || "";
    button.title = session.session_id || "";
    if (session.session_id === currentSessionID) {
      listItem.classList.add("active");
    }

    const header = document.createElement("span");
    header.className = "history-item-header";

    const title = document.createElement("span");
    title.className = "history-title";
    title.textContent = session.title || lastRun?.user_message || "Untitled session";

    const status = document.createElement("span");
    status.className = `history-status status-${lastRun?.status || "unknown"}`;
    const todos = sessionTodos(session);
    const runLabel = `${runCount} ${runCount === 1 ? "run" : "runs"}`;
    const todoLabel = todos.length > 0 ? ` · ${todos.length} ${todos.length === 1 ? "todo" : "todos"}` : "";
    status.textContent = `${runLabel}${todoLabel}`;

    header.append(title, status);

    const preview = document.createElement("span");
    preview.className = "history-preview";
    preview.textContent = sessionPreview(session);

    const footer = document.createElement("span");
    footer.className = "history-item-footer";

    const id = document.createElement("span");
    id.className = "history-id";
    id.textContent = session.session_id || "";

    const duration = document.createElement("span");
    duration.className = "history-duration";
    duration.textContent = sessionUpdatedLabel(session);

    footer.append(id, duration);

    button.append(header, preview, footer);
    listItem.append(button);
    historyList.append(listItem);
  });
}

function currentSession() {
  return sessionByID(currentSessionID);
}

function sessionByID(sessionID) {
  return persistedSessions.find((session) => session.session_id === sessionID) || null;
}

function sessionRuns(session) {
  return Array.isArray(session?.runs) ? session.runs : [];
}

function sessionRunCount(session) {
  const count = Number(session?.run_count);
  return Number.isFinite(count) ? count : sessionRuns(session).length;
}

function sessionLastRun(session) {
  const runs = sessionRuns(session);
  if (runs.length > 0) {
    return runs[runs.length - 1];
  }
  return session?.last_run || null;
}

function sessionTodos(session) {
  return Array.isArray(session?.todos) ? session.todos : [];
}

function hasCompleteSessionDetail(detailSession, summarySession) {
  if (!detailSession) {
    return false;
  }
  const detailRuns = sessionRuns(detailSession);
  const expectedRuns = sessionRunCount(summarySession);
  return Array.isArray(detailSession.runs) && detailRuns.length >= expectedRuns;
}

function renderTodosPanel() {
  const todos = sessionTodos(currentSession());
  todoCount.textContent = String(todos.length);
  todoList.replaceChildren();

  if (todos.length === 0) {
    const empty = document.createElement("li");
    empty.className = "todo-empty";
    empty.textContent = "No active plan";
    todoList.append(empty);
    return;
  }

  todos.forEach((todo, index) => {
    const item = document.createElement("li");
    item.className = `todo-item todo-${todoStatusClass(todo.status)}`;

    const marker = document.createElement("span");
    marker.className = "todo-marker";
    marker.textContent = String(index + 1);

    const body = document.createElement("span");
    body.className = "todo-body";

    const content = document.createElement("span");
    content.className = "todo-content";
    content.textContent = todo.content || "Untitled todo";

    const meta = document.createElement("span");
    meta.className = "todo-meta";
    const status = todoStatusLabel(todo.status);
    const id = todo.id ? ` · ${todo.id}` : "";
    meta.textContent = `${status}${id}`;

    body.append(content, meta);
    item.append(marker, body);
    todoList.append(item);
  });
}

function todoStatusClass(status) {
  switch (status) {
    case "in_progress":
      return "in-progress";
    case "done":
      return "done";
    case "blocked":
      return "blocked";
    case "cancelled":
      return "cancelled";
    case "pending":
    default:
      return "pending";
  }
}

function todoStatusLabel(status) {
  switch (status) {
    case "in_progress":
      return "In progress";
    case "done":
      return "Done";
    case "blocked":
      return "Blocked";
    case "cancelled":
      return "Cancelled";
    case "pending":
      return "Pending";
    default:
      return status || "Pending";
  }
}

function sessionPreview(session) {
  if (sessionRunCount(session) === 0) {
    return "No messages in this session yet.";
  }
  const run = sessionLastRun(session);
  if (!run) {
    return "Session details are available on restore.";
  }
  if (run.user_message) {
    return run.user_message;
  }
  if (run.content) {
    return run.content;
  }
  if (run.error) {
    return run.error;
  }
  if (run.status === "running") {
    return "Interrupted before a final response.";
  }
  return "No final response saved.";
}

function sessionUpdatedLabel(session) {
  const raw = session?.updated_at || "";
  if (!raw) {
    return "";
  }
  const updatedAt = new Date(raw);
  if (Number.isNaN(updatedAt.getTime())) {
    return raw;
  }
  return updatedAt.toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function openHistoryDialog() {
  if (typeof historyDialog.showModal === "function") {
    if (!historyDialog.open) {
      historyDialog.showModal();
    }
    return;
  }
  historyDialog.setAttribute("open", "");
}

function closeHistoryDialog() {
  if (typeof historyDialog.close === "function" && historyDialog.open) {
    historyDialog.close();
    return;
  }
  historyDialog.removeAttribute("open");
}

function openScheduleDialog() {
  if (typeof scheduleDialog.showModal === "function") {
    if (!scheduleDialog.open) {
      scheduleDialog.showModal();
    }
    return;
  }
  scheduleDialog.setAttribute("open", "");
}

function closeScheduleDialog() {
  if (typeof scheduleDialog.close === "function" && scheduleDialog.open) {
    scheduleDialog.close();
    return;
  }
  scheduleDialog.removeAttribute("open");
}

function openMemoryDialog() {
  if (typeof memoryDialog.showModal === "function") {
    if (!memoryDialog.open) {
      memoryDialog.showModal();
    }
    return;
  }
  memoryDialog.setAttribute("open", "");
}

function closeMemoryDialog() {
  if (typeof memoryDialog.close === "function" && memoryDialog.open) {
    memoryDialog.close();
    return;
  }
  memoryDialog.removeAttribute("open");
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

function addScheduleTrigger(text) {
  const wrapper = document.createElement("article");
  wrapper.className = "schedule-trigger";

  const badge = document.createElement("span");
  badge.className = "schedule-trigger-badge";
  badge.textContent = "⏰ Scheduled trigger";

  const body = document.createElement("div");
  body.className = "schedule-trigger-body";
  body.append(renderMarkdown(text));

  wrapper.append(badge, body);
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
