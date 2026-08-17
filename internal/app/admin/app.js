"use strict";

const state = {
  runtime: null,
  tasks: [],
  sessions: [],
  cronJobs: [],
  taskFilter: "all",
  token: sessionStorage.getItem("ternura-api-token") || "",
  refreshTimer: 0,
  streamController: null,
  streamGeneration: 0,
};

const statusLabels = {
  running: "运行中",
  queued: "排队中",
  waiting_approval: "待审批",
  approved: "已批准",
  rejected: "已拒绝",
  succeeded: "成功",
  failed: "失败",
  cancelled: "已取消",
  scheduled: "已启用",
  completed: "已完成",
  ok: "成功",
  error: "失败",
  skipped: "已跳过",
};

const stageLabels = {
  queued: "等待执行",
  started: "准备运行",
  thinking: "模型思考",
  tool: "工具调用",
  finishing: "整理结果",
};

const sourceLabels = {
  feishu: "飞书",
  api: "API",
  cron: "定时任务",
};

class AuthenticationError extends Error {}

function element(id) {
  return document.getElementById(id);
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function safeClass(value) {
  return String(value || "unknown").replace(/[^a-z0-9_-]/gi, "_");
}

function compact(value, limit = 96) {
  const text = String(value || "").trim().replace(/\s+/g, " ");
  return text.length > limit ? `${text.slice(0, limit)}...` : text;
}

function statusBadge(status) {
  const normalized = String(status || "unknown");
  return `<span class="status-badge status-${safeClass(normalized)}">${escapeHTML(statusLabels[normalized] || normalized)}</span>`;
}

function sourceBadge(source) {
  const normalized = String(source || "api");
  return `<span class="source-badge source-${safeClass(normalized)}">${escapeHTML(sourceLabels[normalized] || normalized)}</span>`;
}

function formatDuration(milliseconds) {
  const value = Math.max(0, Number(milliseconds) || 0);
  if (value < 1000) return `${Math.round(value)} ms`;
  if (value < 60_000) return `${(value / 1000).toFixed(value < 10_000 ? 1 : 0)} 秒`;
  if (value < 3_600_000) {
    const minutes = Math.floor(value / 60_000);
    const seconds = Math.floor((value % 60_000) / 1000);
    return `${minutes} 分 ${seconds} 秒`;
  }
  const hours = Math.floor(value / 3_600_000);
  const minutes = Math.floor((value % 3_600_000) / 60_000);
  return `${hours} 小时 ${minutes} 分`;
}

function formatUptime(seconds) {
  return formatDuration((Number(seconds) || 0) * 1000);
}

function formatTime(value) {
  if (!value) return "--";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(date);
}

function formatEpoch(milliseconds) {
  return milliseconds ? formatTime(new Date(Number(milliseconds)).toISOString()) : "--";
}

function prettyJSON(value) {
  if (!value) return "";
  try {
    return JSON.stringify(typeof value === "string" ? JSON.parse(value) : value, null, 2);
  } catch (_) {
    return String(value);
  }
}

function authHeaders(extra = {}) {
  const headers = { Accept: "application/json", ...extra };
  if (state.token) headers.Authorization = `Bearer ${state.token}`;
  return headers;
}

async function apiRequest(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: authHeaders(options.headers || {}),
  });
  if (response.status === 401) {
    throw new AuthenticationError("API token is required");
  }
  if (!response.ok) {
    const message = (await response.text()).trim();
    throw new Error(message || `请求失败 (${response.status})`);
  }
  if (response.status === 204) return null;
  return response.json();
}

function showAuthentication() {
  setLiveState("error", "需要认证");
  const dialog = element("auth-dialog");
  element("auth-error").classList.add("hidden");
  if (!dialog.open) dialog.showModal();
  requestAnimationFrame(() => element("api-token").focus());
}

function handleError(error, message = "加载运行状态失败") {
  if (error instanceof AuthenticationError) {
    showAuthentication();
    return;
  }
  console.error(error);
  showToast(`${message}: ${error.message}`, true);
}

function showToast(message, isError = false) {
  const toast = document.createElement("div");
  toast.className = `toast${isError ? " error" : ""}`;
  toast.textContent = message;
  element("toast-region").append(toast);
  window.setTimeout(() => toast.remove(), 3600);
}

function setLiveState(nextState, label) {
  const liveState = element("live-state");
  liveState.dataset.state = nextState;
  element("live-state-text").textContent = label;
}

async function refreshAll({ quiet = false } = {}) {
  const button = element("refresh-button");
  if (!quiet) {
    button.disabled = true;
    button.textContent = "刷新中";
  }
  try {
    const [runtime, taskData, sessionData, cronData] = await Promise.all([
      apiRequest("/api/runtime"),
      apiRequest("/api/tasks"),
      apiRequest("/api/sessions"),
      apiRequest("/api/cron/jobs"),
    ]);
    state.runtime = runtime;
    state.tasks = taskData.tasks || [];
    state.sessions = sessionData.sessions || [];
    state.cronJobs = cronData.jobs || [];
    renderAll();
    element("updated-at").textContent = new Date().toLocaleTimeString("zh-CN", { hour12: false });
  } catch (error) {
    handleError(error);
  } finally {
    button.disabled = false;
    button.textContent = "刷新";
  }
}

async function refreshRuntimeAndTasks() {
  try {
    const [runtime, taskData] = await Promise.all([
      apiRequest("/api/runtime"),
      apiRequest("/api/tasks"),
    ]);
    state.runtime = runtime;
    state.tasks = taskData.tasks || [];
    renderOverview();
    renderSystem();
    element("updated-at").textContent = new Date().toLocaleTimeString("zh-CN", { hour12: false });
  } catch (error) {
    handleError(error);
  }
}

function scheduleRefresh() {
  window.clearTimeout(state.refreshTimer);
  state.refreshTimer = window.setTimeout(refreshRuntimeAndTasks, 220);
}

function renderAll() {
  renderOverview();
  renderSessions();
  renderCronJobs();
  renderSystem();
}

function renderOverview() {
  if (!state.runtime) return;
  renderMetrics();
  renderActiveRuns();
  renderApprovals();
  renderRecentRuns();
}

function renderMetrics() {
  const runtime = state.runtime;
  const counts = runtime.counts || {};
  const feishu = runtime.feishu || {};
  const memory = runtime.memory || {};
  const metrics = [
    ["运行中", counts.active_runs || 0, counts.active_runs ? "Agent 正在处理任务" : "当前空闲"],
    ["等待审批", counts.pending_approvals || 0, counts.pending_approvals ? "需要人工确认" : "没有阻塞项"],
    ["飞书", feishu.enabled ? (feishu.connected ? "已连接" : "未连接") : "未启用", feishu.mode || "--"],
    ["会话", counts.sessions || 0, "已持久化"],
    ["定时任务", counts.cron_jobs || 0, "当前已注册"],
    ["记忆", (memory.long_term_count || 0) + (memory.tool_memory_count || 0), `${memory.short_term_turns || 0} 轮短期上下文`],
  ];
  element("metric-grid").innerHTML = metrics.map(([label, value, note]) => `
    <div class="metric-item">
      <div class="metric-label">${escapeHTML(label)}</div>
      <div class="metric-value">${escapeHTML(value)}</div>
      <div class="metric-note" title="${escapeHTML(note)}">${escapeHTML(note)}</div>
    </div>
  `).join("");
}

function renderActiveRuns() {
  const runs = state.runtime?.active_runs || [];
  element("active-summary").textContent = runs.length ? `${runs.length} 个任务正在处理` : "当前没有运行中的任务";
  element("active-empty").classList.toggle("hidden", runs.length > 0);
  element("active-runs").innerHTML = runs.map((run) => `
    <tr class="clickable-row" data-run-id="${escapeHTML(run.run_id)}">
      <td>
        <div class="cell-title" title="${escapeHTML(run.input)}">${escapeHTML(compact(run.input) || "无输入")}</div>
        <div class="cell-subtitle mono">${escapeHTML(run.run_id)}</div>
      </td>
      <td>${sourceBadge(run.source)}</td>
      <td>
        <div>${escapeHTML(stageLabels[run.stage] || run.stage)}</div>
        <div class="cell-subtitle">${escapeHTML(run.detail || "")}</div>
      </td>
      <td data-started-at="${escapeHTML(run.started_at)}">${escapeHTML(formatDuration(activeElapsed(run)))}</td>
      <td>${escapeHTML(`${run.model_calls || 0} 模型 / ${run.tool_calls || 0} 工具`)}</td>
      <td class="action-column">
        <button class="button button-danger" type="button" data-action="cancel" data-run-id="${escapeHTML(run.run_id)}">停止</button>
      </td>
    </tr>
  `).join("");
}

function activeElapsed(run) {
  const started = new Date(run.started_at).getTime();
  return Number.isNaN(started) ? run.elapsed_ms || 0 : Date.now() - started;
}

function renderApprovals() {
  const approvals = state.runtime?.pending_approvals || [];
  element("approval-section").classList.toggle("hidden", approvals.length === 0);
  element("approval-count").textContent = approvals.length;
  element("approval-list").innerHTML = approvals.map((approval) => {
    const task = approval.task || {};
    return `
      <article class="approval-item">
        <div>
          <div class="cell-title">${escapeHTML(approval.tool || "工具调用")}</div>
          <div class="cell-subtitle">${escapeHTML(approval.reason || "该操作需要人工确认")}</div>
          <div class="cell-subtitle mono" title="${escapeHTML(approval.arguments)}">${escapeHTML(compact(prettyJSON(approval.arguments), 180))}</div>
        </div>
        <div class="approval-actions">
          <button class="button button-danger" type="button" data-action="reject" data-run-id="${escapeHTML(task.run_id)}">拒绝</button>
          <button class="button button-approve" type="button" data-action="approve" data-run-id="${escapeHTML(task.run_id)}">批准</button>
        </div>
      </article>
    `;
  }).join("");
}

function filteredTasks() {
  const tasks = state.tasks.slice(0, 100);
  if (state.taskFilter === "all") return tasks.slice(0, 40);
  return tasks.filter((task) => task.status === state.taskFilter).slice(0, 40);
}

function renderRecentRuns() {
  const tasks = filteredTasks();
  element("recent-empty").classList.toggle("hidden", tasks.length > 0);
  element("recent-runs").innerHTML = tasks.map((task) => `
    <tr class="clickable-row" data-run-id="${escapeHTML(task.run_id)}">
      <td>
        <div class="cell-title" title="${escapeHTML(task.input)}">${escapeHTML(compact(task.input) || "无输入")}</div>
        <div class="cell-subtitle mono">${escapeHTML(task.run_id)}</div>
      </td>
      <td>${statusBadge(task.status)}</td>
      <td>${escapeHTML(formatTime(task.started_at))}</td>
      <td>${escapeHTML(task.finished_at ? formatDuration(task.duration_ms) : "--")}</td>
      <td>${escapeHTML((task.artifacts || []).length)}</td>
    </tr>
  `).join("");
}

function renderSessions() {
  const sessions = state.sessions || [];
  element("session-count").textContent = sessions.length;
  element("session-empty").classList.toggle("hidden", sessions.length > 0);
  element("session-list").innerHTML = sessions.map((session) => `
    <tr>
      <td>
        <div class="cell-title">${escapeHTML(session.title || "未命名会话")}${session.current ? " · 当前" : ""}</div>
        <div class="cell-subtitle mono">${escapeHTML(session.session_id)}</div>
      </td>
      <td>${session.last_status ? statusBadge(session.last_status) : "--"}</td>
      <td>${escapeHTML(session.message_count || 0)}</td>
      <td>${escapeHTML(session.run_count || 0)}</td>
      <td>${escapeHTML(session.todo_count || 0)}</td>
      <td>${escapeHTML(formatTime(session.updated_at))}</td>
    </tr>
  `).join("");
}

function formatSchedule(schedule = {}) {
  if (schedule.kind === "every") return `每 ${formatDuration(schedule.every_ms || 0)}`;
  if (schedule.kind === "cron") return `${schedule.expr || "--"}${schedule.tz ? ` (${schedule.tz})` : ""}`;
  if (schedule.kind === "at") return formatEpoch(schedule.at_ms);
  return schedule.kind || "--";
}

function renderCronJobs() {
  const jobs = state.cronJobs || [];
  element("cron-count").textContent = jobs.length;
  element("cron-empty").classList.toggle("hidden", jobs.length > 0);
  element("cron-list").innerHTML = jobs.map((job) => {
    const delivery = job.payload?.delivery;
    const destination = delivery?.channel || (job.payload?.deliver ? "已配置" : "本地");
    const status = job.state?.running ? "running" : (job.enabled ? "scheduled" : "disabled");
    return `
      <tr>
        <td>
          <div class="cell-title" title="${escapeHTML(job.payload?.message)}">${escapeHTML(job.name || compact(job.payload?.message) || "定时任务")}</div>
          <div class="cell-subtitle mono">${escapeHTML(job.id)}</div>
        </td>
        <td>${statusBadge(status)}</td>
        <td>${escapeHTML(formatSchedule(job.schedule))}</td>
        <td>${escapeHTML(formatEpoch(job.state?.next_run_at_ms))}</td>
        <td>${job.state?.last_status ? statusBadge(job.state.last_status) : "--"}</td>
        <td>${escapeHTML(destination)}</td>
      </tr>
    `;
  }).join("");
}

function renderSystem() {
  if (!state.runtime) return;
  const runtime = state.runtime;
  const counts = runtime.counts || {};
  const feishu = runtime.feishu || {};
  const memory = runtime.memory || {};
  const rows = [
    ["守护进程", runtime.status === "ok" ? "运行正常" : runtime.status, ""],
    ["启动时间", formatTime(runtime.started_at), ""],
    ["已运行", formatUptime(runtime.uptime_seconds), "", "uptime"],
    ["模型", runtime.model || "--", ""],
    ["Provider", runtime.provider || "--", ""],
    ["上下文窗口", runtime.context_window ? runtime.context_window.toLocaleString("zh-CN") : "--", ""],
    ["飞书连接", feishu.enabled ? (feishu.connected ? `已连接 · ${feishu.mode || "--"}` : "未连接") : "未启用", ""],
    ["飞书连接时间", formatTime(feishu.connected_at), ""],
    ["Skills", counts.skills || 0, "skills"],
    ["Tools", counts.tools || 0, "tools"],
    ["MCP Tools", counts.mcp_tools || 0, "mcp-tools"],
    ["当前会话", memory.current_session_id || "--", "session"],
    ["长期记忆", memory.long_term_count || 0, "long-term-memory"],
    ["工具记忆", memory.tool_memory_count || 0, "tool-memory"],
    ["短期上下文", `${memory.short_term_turns || 0} 轮`, "short-term-memory"],
  ];
  element("system-list").innerHTML = rows.map(([label, value, section, key]) => `
    <div class="system-row${section ? " system-row-action" : ""}"
      ${key ? `data-system-key="${escapeHTML(key)}"` : ""}
      ${section ? `role="button" tabindex="0" data-system-section="${escapeHTML(section)}" data-system-label="${escapeHTML(label)}" aria-label="查看 ${escapeHTML(label)} 详情"` : ""}>
      <dt>${escapeHTML(label)}</dt>
      <dd><span>${escapeHTML(value)}</span>${section ? '<span class="system-chevron" aria-hidden="true">›</span>' : ""}</dd>
    </div>
  `).join("");
}

function openDrawerLoading(badgeText, title, subtitle, loadingText) {
  const drawer = element("run-drawer");
  drawer.classList.add("open");
  drawer.setAttribute("aria-hidden", "false");
  document.body.style.overflow = "hidden";
  const badge = element("drawer-status");
  badge.className = "status-badge status-system";
  badge.textContent = badgeText;
  element("drawer-title").textContent = title;
  element("drawer-run-id").textContent = subtitle || "";
  element("drawer-content").innerHTML = `<div class="empty-state">${escapeHTML(loadingText)}</div>`;
}

async function openSystemDetail(section, label) {
  openDrawerLoading("系统", label, "正在读取", "正在读取系统详情");
  try {
    const response = await apiRequest(`/api/system/${encodeURIComponent(section)}`);
    renderSystemDetail(section, label, response || {});
  } catch (error) {
    handleError(error, `读取${label}失败`);
    element("drawer-content").innerHTML = `<div class="empty-state">${escapeHTML(error.message)}</div>`;
  }
}

function renderSystemDetail(section, label, response) {
  element("drawer-title").textContent = label;
  if (section === "skills") {
    const skills = response.skills || [];
    element("drawer-run-id").textContent = `${skills.length} 个已加载 Skill`;
    element("drawer-content").innerHTML = renderSkillInventory(skills);
    return;
  }
  if (section === "tools" || section === "mcp-tools") {
    const tools = response.tools || [];
    element("drawer-run-id").textContent = `${tools.length} 个已注册 Tool`;
    element("drawer-content").innerHTML = renderToolInventory(tools);
    return;
  }
  if (section === "session") {
    renderSessionDetail(response);
    return;
  }
  if (section === "long-term-memory") {
    const memories = response.memories || [];
    element("drawer-run-id").textContent = `${memories.length} 条 · ${response.session_id || ""}`;
    element("drawer-content").innerHTML = renderLongTermMemories(memories);
    return;
  }
  if (section === "tool-memory") {
    const memories = response.memories || [];
    element("drawer-run-id").textContent = `${memories.length} 条 · ${response.session_id || ""}`;
    element("drawer-content").innerHTML = renderToolMemories(memories);
    return;
  }
  if (section === "short-term-memory") {
    const turns = response.turns || [];
    element("drawer-run-id").textContent = `${turns.length} 轮 · ${response.session_id || ""}`;
    element("drawer-content").innerHTML = renderShortTermMemory(response.summary, response.updated_at, turns);
  }
}

function renderSkillInventory(skills) {
  if (!skills.length) return '<div class="empty-state">没有加载 Skill</div>';
  return `<div class="detail-accordion">${skills.map((skill) => `
    <details class="trace-item inventory-item">
      <summary>
        <span class="inventory-name">${escapeHTML(skill.name || "未命名 Skill")}</span>
        <span class="source-badge">${escapeHTML(skill.source || "builtin")}</span>
        <span class="muted inventory-count">${escapeHTML(`${(skill.tools || []).length} tools`)}</span>
      </summary>
      <div class="inventory-content">
        <p class="detail-text">${escapeHTML(skill.description || "没有描述")}</p>
        ${detailMetadataRow("加载方式", skill.lazy_load ? "按需读取" : "启动时装配")}
        ${skill.path ? detailMetadataRow("文件", skill.path, true) : ""}
        ${detailTagRow("Tools", skill.tools || [])}
        ${detailTagRow("Hooks", skill.hooks || [])}
      </div>
    </details>
  `).join("")}</div>`;
}

function renderToolInventory(tools) {
  if (!tools.length) return '<div class="empty-state">当前没有已注册的 Tool</div>';
  return `<div class="detail-accordion">${tools.map((tool) => `
    <details class="trace-item inventory-item">
      <summary>
        <span class="inventory-name mono">${escapeHTML(tool.name || "未命名 Tool")}</span>
        ${tool.mcp ? '<span class="source-badge source-api">MCP</span>' : ""}
        <span class="muted inventory-count">${escapeHTML((tool.skills || []).join(", "))}</span>
      </summary>
      <div class="inventory-content">
        <p class="detail-text">${escapeHTML(tool.description || "没有描述")}</p>
        ${detailTagRow("所属 Skills", tool.skills || [])}
      </div>
    </details>
  `).join("")}</div>`;
}

function renderSessionDetail(response) {
  const session = response.session || {};
  const runs = response.recent_runs || [];
  const todos = response.todos || [];
  element("drawer-title").textContent = session.title || "当前会话";
  element("drawer-run-id").textContent = session.session_id || "";
  const metrics = [
    ["消息", session.message_count || 0],
    ["运行", session.run_count || 0],
    ["待办", session.todo_count || 0],
  ].map(([name, value]) => `<div class="detail-metric"><span>${escapeHTML(name)}</span><strong>${escapeHTML(value)}</strong></div>`).join("");
  const todoHTML = todos.length ? todos.map((todo) => `
    <div class="detail-list-row">
      <span>${escapeHTML(todo.content || "未命名待办")}</span>
      <span class="muted">${escapeHTML(todo.status || "pending")}</span>
    </div>
  `).join("") : '<div class="muted">没有待办</div>';
  const runHTML = runs.length ? runs.map((run) => `
    <button class="detail-run-row" type="button" data-detail-run-id="${escapeHTML(run.run_id)}">
      <span>
        <strong>${escapeHTML(compact(run.input, 72) || "无输入")}</strong>
        <small class="mono">${escapeHTML(run.run_id)}</small>
      </span>
      <span>${statusBadge(run.status)}</span>
    </button>
  `).join("") : '<div class="muted">没有运行记录</div>';
  element("drawer-content").innerHTML = `
    <section class="detail-block">
      <h3>会话状态</h3>
      <div class="detail-grid">${metrics}</div>
      <div class="detail-timestamps">创建于 ${escapeHTML(formatTime(session.created_at))} · 更新于 ${escapeHTML(formatTime(session.updated_at))}</div>
    </section>
    <section class="detail-block"><h3>待办</h3><div class="detail-list">${todoHTML}</div></section>
    <section class="detail-block"><h3>最近运行</h3><div class="detail-list">${runHTML}</div></section>
  `;
}

function renderLongTermMemories(memories) {
  if (!memories.length) return '<div class="empty-state">没有长期记忆</div>';
  return `<div class="detail-accordion">${memories.map((memory) => `
    <details class="trace-item inventory-item">
      <summary>
        <span class="source-badge">${escapeHTML(memory.category || "other")}</span>
        <span class="inventory-name">${escapeHTML(compact(memory.content, 72))}</span>
      </summary>
      <div class="inventory-content">
        <p class="detail-text">${escapeHTML(memory.content || "")}</p>
        ${memory.source ? detailMetadataRow("来源", memory.source) : ""}
        ${detailMetadataRow("Memory ID", memory.id || "--", true)}
        ${detailMetadataRow("使用", `${memory.use_count || 0} 次 · 最近 ${formatTime(memory.last_used_at)}`)}
        ${detailMetadataRow("更新", formatTime(memory.updated_at))}
      </div>
    </details>
  `).join("")}</div>`;
}

function renderToolMemories(memories) {
  if (!memories.length) return '<div class="empty-state">当前会话没有工具记忆</div>';
  return `<div class="detail-accordion">${memories.map((memory) => `
    <details class="trace-item inventory-item">
      <summary>
        <span class="source-badge source-api">${escapeHTML(memory.tool || "tool")}</span>
        <span class="inventory-name">${escapeHTML(compact(memory.summary, 68))}</span>
      </summary>
      <div class="inventory-content">
        <p class="detail-text">${escapeHTML(memory.summary || "")}</p>
        ${memory.arguments ? `<details class="nested-detail"><summary>调用参数</summary><pre>${escapeHTML(prettyJSON(memory.arguments))}</pre></details>` : ""}
        ${memory.error ? detailMetadataRow("错误", memory.error) : ""}
        ${memory.raw_ref ? detailMetadataRow("原始结果", memory.raw_ref, true) : ""}
        ${detailMetadataRow("输出规模", `${memory.output_runes || 0} 字符`)}
        ${detailMetadataRow("记录时间", formatTime(memory.created_at))}
      </div>
    </details>
  `).join("")}</div>`;
}

function renderShortTermMemory(summary, updatedAt, turns) {
  const summaryHTML = summary ? `<section class="detail-block"><h3>摘要</h3><p class="detail-text">${escapeHTML(summary)}</p><div class="detail-timestamps">更新于 ${escapeHTML(formatTime(updatedAt))}</div></section>` : "";
  const turnsHTML = turns.length ? turns.map((turn) => `
    <details class="trace-item inventory-item">
      <summary>
        <span class="inventory-name">${escapeHTML(compact(turn.user, 76) || "空消息")}</span>
        <span class="muted inventory-count">${escapeHTML(formatTime(turn.at))}</span>
      </summary>
      <div class="inventory-content">
        ${detailConversationMessage("用户", turn.user)}
        ${detailConversationMessage("Agent", turn.assistant || "没有回答")}
      </div>
    </details>
  `).join("") : '<div class="empty-state">没有短期上下文</div>';
  return `${summaryHTML}<section class="detail-block"><h3>最近轮次</h3><div class="detail-accordion">${turnsHTML}</div></section>`;
}

function detailMetadataRow(label, value, mono = false) {
  return `<div class="inventory-meta"><span>${escapeHTML(label)}</span><strong class="${mono ? "mono" : ""}">${escapeHTML(value || "--")}</strong></div>`;
}

function detailTagRow(label, values) {
  if (!values.length) return "";
  return `<div class="inventory-meta inventory-meta-tags"><span>${escapeHTML(label)}</span><div class="detail-tags">${values.map((value) => `<span class="detail-tag">${escapeHTML(value)}</span>`).join("")}</div></div>`;
}

function detailConversationMessage(role, content) {
  return `<div class="conversation-detail"><strong>${escapeHTML(role)}</strong><p class="detail-text">${escapeHTML(content || "")}</p></div>`;
}

async function openRunDrawer(runID) {
  openDrawerLoading("任务", "任务详情", runID, "正在读取任务记录");
  try {
    const response = await apiRequest(`/api/tasks/${encodeURIComponent(runID)}`);
    renderRunDetail(response.task || {});
  } catch (error) {
    handleError(error, "读取任务详情失败");
    element("drawer-content").innerHTML = `<div class="empty-state">${escapeHTML(error.message)}</div>`;
  }
}

function renderRunDetail(run) {
  const metrics = run.metrics || {};
  const trace = run.trace || [];
  const artifacts = run.artifacts || [];
  const badge = element("drawer-status");
  badge.className = `status-badge status-${safeClass(run.status)}`;
  badge.textContent = statusLabels[run.status] || run.status || "--";
  element("drawer-title").textContent = compact(run.user_message, 120) || "任务详情";
  element("drawer-run-id").textContent = run.run_id || "";

  const metricsHTML = [
    ["耗时", formatDuration(run.duration_ms)],
    ["模型调用", metrics.model_calls || 0],
    ["工具调用", metrics.tool_calls || 0],
    ["输入 Token", metrics.prompt_tokens || 0],
    ["输出 Token", metrics.completion_tokens || 0],
    ["总 Token", metrics.total_tokens || 0],
  ].map(([label, value]) => `
    <div class="detail-metric"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value)}</strong></div>
  `).join("");

  const traceHTML = trace.length ? trace.map((item) => `
    <details class="trace-item">
      <summary>
        ${statusBadge(item.status || "succeeded")}
        <span>${escapeHTML(item.title || item.type || "过程")}</span>
        <span class="muted">${escapeHTML(item.duration_ms ? formatDuration(item.duration_ms) : "")}</span>
      </summary>
      <div class="trace-content">${escapeHTML(item.content || "无内容")}</div>
    </details>
  `).join("") : '<div class="muted">没有过程记录</div>';

  const artifactHTML = artifacts.length ? artifacts.map((artifact) => `
    <div class="detail-metric">
      <span>${escapeHTML(artifact.mime_type || "artifact")}</span>
      <strong>${escapeHTML(artifact.name || artifact.id || "未命名产物")}</strong>
      <div class="cell-subtitle mono">${escapeHTML(artifact.uri || artifact.id || "")}</div>
    </div>
  `).join("") : '<div class="muted">没有产物</div>';

  element("drawer-content").innerHTML = `
    <section class="detail-block">
      <h3>输入</h3>
      <p class="detail-text">${escapeHTML(run.user_message || "无输入")}</p>
    </section>
    ${run.error ? `<section class="detail-block"><h3>错误</h3><p class="detail-text">${escapeHTML(run.error)}</p></section>` : ""}
    <section class="detail-block">
      <h3>回答</h3>
      <p class="detail-text">${escapeHTML(run.content || "尚未生成回答")}</p>
    </section>
    <section class="detail-block">
      <h3>运行指标</h3>
      <div class="detail-grid">${metricsHTML}</div>
    </section>
    <section class="detail-block">
      <h3>过程记录</h3>
      <div class="trace-list">${traceHTML}</div>
    </section>
    <section class="detail-block">
      <h3>产物</h3>
      <div class="detail-grid">${artifactHTML}</div>
    </section>
  `;
}

function closeRunDrawer() {
  const drawer = element("run-drawer");
  drawer.classList.remove("open");
  drawer.setAttribute("aria-hidden", "true");
  document.body.style.overflow = "";
}

async function cancelRun(runID, button) {
  button.disabled = true;
  try {
    await apiRequest(`/api/tasks/${encodeURIComponent(runID)}/cancel`, { method: "POST" });
    showToast("已发送停止请求");
    await refreshRuntimeAndTasks();
  } catch (error) {
    handleError(error, "停止任务失败");
  } finally {
    button.disabled = false;
  }
}

async function decideRun(runID, approved, button) {
  button.disabled = true;
  try {
    await apiRequest(`/api/tasks/${encodeURIComponent(runID)}/decision`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        approved,
        reason: approved ? "Approved from runtime console" : "Rejected from runtime console",
      }),
    });
    showToast(approved ? "已批准，Agent 将继续运行" : "已拒绝该工具调用");
    await refreshRuntimeAndTasks();
  } catch (error) {
    handleError(error, approved ? "批准失败" : "拒绝失败");
  } finally {
    button.disabled = false;
  }
}

function applyRuntimeEvent(event) {
  if (!state.runtime || !event?.run) return;
  const active = state.runtime.active_runs || [];
  const index = active.findIndex((run) => run.run_id === event.run.run_id);
  if (event.type === "run_finished") {
    if (index >= 0) active.splice(index, 1);
  } else if (index >= 0) {
    active[index] = event.run;
  } else {
    active.unshift(event.run);
  }
  state.runtime.counts.active_runs = active.length;
  renderOverview();
  scheduleRefresh();
}

async function connectRuntimeEvents() {
  state.streamGeneration += 1;
  const generation = state.streamGeneration;
  if (state.streamController) state.streamController.abort();
  const controller = new AbortController();
  state.streamController = controller;
  let retryDelay = 1200;

  while (generation === state.streamGeneration) {
    setLiveState("connecting", "正在连接");
    try {
      const response = await fetch("/api/events", {
        headers: authHeaders({ Accept: "text/event-stream" }),
        signal: controller.signal,
      });
      if (response.status === 401) throw new AuthenticationError("API token is required");
      if (!response.ok || !response.body) throw new Error(`实时连接失败 (${response.status})`);
      setLiveState("live", "实时更新");
      retryDelay = 1200;
      await consumeEventStream(response.body, applyRuntimeEvent);
      if (controller.signal.aborted) return;
      throw new Error("实时连接已断开");
    } catch (error) {
      if (controller.signal.aborted || generation !== state.streamGeneration) return;
      if (error instanceof AuthenticationError) {
        showAuthentication();
        return;
      }
      console.warn(error);
      setLiveState("error", "正在重连");
      await new Promise((resolve) => window.setTimeout(resolve, retryDelay));
      retryDelay = Math.min(retryDelay * 1.7, 10_000);
    }
  }
}

async function consumeEventStream(stream, onEvent) {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true }).replaceAll("\r\n", "\n");
    let boundary = buffer.indexOf("\n\n");
    while (boundary >= 0) {
      const frame = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      const data = frame.split("\n")
        .filter((line) => line.startsWith("data:"))
        .map((line) => line.slice(5).trimStart())
        .join("\n");
      if (data) {
        try {
          onEvent(JSON.parse(data));
        } catch (error) {
          console.warn("Ignored malformed runtime event", error);
        }
      }
      boundary = buffer.indexOf("\n\n");
    }
  }
}

function bindInteractions() {
  document.querySelectorAll(".tab-button").forEach((button) => {
    button.addEventListener("click", () => {
      document.querySelectorAll(".tab-button").forEach((item) => item.classList.toggle("active", item === button));
      document.querySelectorAll(".tab-panel").forEach((panel) => panel.classList.toggle("active", panel.id === `tab-${button.dataset.tab}`));
    });
  });

  document.querySelectorAll(".filter-button").forEach((button) => {
    button.addEventListener("click", () => {
      state.taskFilter = button.dataset.status || "all";
      document.querySelectorAll(".filter-button").forEach((item) => item.classList.toggle("active", item === button));
      renderRecentRuns();
    });
  });

  element("refresh-button").addEventListener("click", () => refreshAll());
  document.querySelectorAll("[data-close-drawer]").forEach((button) => button.addEventListener("click", closeRunDrawer));
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && element("run-drawer").classList.contains("open")) closeRunDrawer();
  });

  element("tab-overview").addEventListener("click", (event) => {
    const actionButton = event.target.closest("[data-action]");
    if (actionButton) {
      event.stopPropagation();
      const runID = actionButton.dataset.runId;
      if (actionButton.dataset.action === "cancel") cancelRun(runID, actionButton);
      if (actionButton.dataset.action === "approve") decideRun(runID, true, actionButton);
      if (actionButton.dataset.action === "reject") decideRun(runID, false, actionButton);
      return;
    }
    const row = event.target.closest("[data-run-id]");
    if (row?.dataset.runId) openRunDrawer(row.dataset.runId);
  });

  element("system-list").addEventListener("click", (event) => {
    const row = event.target.closest("[data-system-section]");
    if (row) openSystemDetail(row.dataset.systemSection, row.dataset.systemLabel || "系统详情");
  });
  element("system-list").addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    const row = event.target.closest("[data-system-section]");
    if (!row) return;
    event.preventDefault();
    openSystemDetail(row.dataset.systemSection, row.dataset.systemLabel || "系统详情");
  });
  element("drawer-content").addEventListener("click", (event) => {
    const run = event.target.closest("[data-detail-run-id]");
    if (run?.dataset.detailRunId) openRunDrawer(run.dataset.detailRunId);
  });

  element("auth-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const token = element("api-token").value.trim();
    state.token = token;
    if (token) sessionStorage.setItem("ternura-api-token", token);
    else sessionStorage.removeItem("ternura-api-token");
    try {
      await apiRequest("/api/runtime");
      element("auth-dialog").close();
      await refreshAll({ quiet: true });
      connectRuntimeEvents();
    } catch (error) {
      element("auth-error").classList.remove("hidden");
      if (!(error instanceof AuthenticationError)) handleError(error, "认证失败");
    }
  });
}

function tickElapsedTimes() {
  if (state.runtime) {
    state.runtime.uptime_seconds = Math.max(0, Number(state.runtime.uptime_seconds || 0) + 1);
    renderActiveRuns();
    const uptime = element("system-list").querySelector('[data-system-key="uptime"] dd span');
    if (uptime) uptime.textContent = formatUptime(state.runtime.uptime_seconds);
  }
}

bindInteractions();
refreshAll({ quiet: true });
connectRuntimeEvents();
window.setInterval(tickElapsedTimes, 1000);
