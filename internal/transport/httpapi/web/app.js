const elements = {
  connection: document.querySelector("#connection"),
  connectionLabel: document.querySelector("#connection-label"),
  lastUpdated: document.querySelector("#last-updated"),
  errorBanner: document.querySelector("#error-banner"),
  errorMessage: document.querySelector("#error-message"),
  metricTotal: document.querySelector("#metric-total"),
  metricActive: document.querySelector("#metric-active"),
  metricWaiting: document.querySelector("#metric-waiting"),
  metricNodes: document.querySelector("#metric-nodes"),
  resourceCPU: document.querySelector("#resource-cpu"),
  resourceMemory: document.querySelector("#resource-memory"),
  resourceGPU: document.querySelector("#resource-gpu"),
  jobsBody: document.querySelector("#jobs-body"),
  jobsCount: document.querySelector("#jobs-count"),
  jobsEmpty: document.querySelector("#jobs-empty"),
  jobsEmptyCopy: document.querySelector("#jobs-empty-copy"),
  jobSearch: document.querySelector("#job-search"),
  statusFilter: document.querySelector("#status-filter"),
  nodeList: document.querySelector("#node-list"),
  nodesCount: document.querySelector("#nodes-count"),
  nodesEmpty: document.querySelector("#nodes-empty"),
  jobForm: document.querySelector("#job-form"),
  jobID: document.querySelector("#job-id"),
  submitJob: document.querySelector("#submit-job"),
  randomJob: document.querySelector("#random-job"),
  jobFormResult: document.querySelector("#job-form-result"),
  functionsCount: document.querySelector("#functions-count"),
  functionsList: document.querySelector("#functions-list"),
  functionVersionList: document.querySelector("#function-version-list"),
  functionVersionDetail: document.querySelector("#function-version-detail"),
  functionVersionsFor: document.querySelector("#function-versions-for"),
  functionUploadForm: document.querySelector("#function-upload-form"),
  functionUploadButton: document.querySelector("#function-upload-button"),
  functionUploadResult: document.querySelector("#function-upload-result"),
  functionID: document.querySelector("#function-id"),
  functionFile: document.querySelector("#function-file"),
  functionDeployForm: document.querySelector("#function-deploy-form"),
  functionDeployButton: document.querySelector("#function-deploy-button"),
  functionDeployResult: document.querySelector("#function-deploy-result"),
  functionArtifactKey: document.querySelector("#function-artifact-key"),
  functionInvokeForm: document.querySelector("#function-invoke-form"),
  functionInvokeButton: document.querySelector("#function-invoke-button"),
  functionInvokeResult: document.querySelector("#function-invoke-result"),
  functionVersionSelect: document.querySelector("#function-version-select"),
  functionJobID: document.querySelector("#function-job-id"),
  activityList: document.querySelector("#activity-list"),
  dashboardView: document.querySelector("#dashboard-view"),
  detailView: document.querySelector("#job-detail-view"),
  detailLoading: document.querySelector("#detail-loading"),
  detailError: document.querySelector("#detail-error"),
  detailErrorMessage: document.querySelector("#detail-error-message"),
  detailRetry: document.querySelector("#detail-retry"),
  detailContent: document.querySelector("#detail-content"),
  detailJobID: document.querySelector("#detail-job-id"),
  detailCommand: document.querySelector("#detail-command"),
  detailStatus: document.querySelector("#detail-status"),
  detailCancel: document.querySelector("#detail-cancel"),
  detailActionResult: document.querySelector("#detail-action-result"),
  detailLogAttempt: document.querySelector("#detail-log-attempt"),
  detailLogTerminal: document.querySelector("#detail-log-terminal"),
  detailFields: document.querySelector("#detail-fields"),
  detailResult: document.querySelector("#detail-result"),
  detailEvents: document.querySelector("#detail-events"),
  detailEventsCount: document.querySelector("#detail-events-count"),
};

const state = {
  jobs: [],
  totalJobs: 0,
  nodes: [],
  previousJobStates: new Map(),
  hasSnapshot: false,
  refreshing: false,
  activity: [],
  expandedJobIDs: new Set(),
  inlineLogs: new Map(),
  detailJobID: jobIDFromPath(),
  detailJob: null,
  detailVersion: null,
  detailVersionError: "",
  detailResult: null,
  detailResultError: "",
  detailLogs: new Map(),
  detailEvents: [],
  cancellingJobIDs: new Set(),
  functions: [],
  functionVersions: [],
  selectedFunctionID: "",
  selectedVersionID: "",
};

if (state.detailJobID) {
  elements.dashboardView.hidden = true;
  elements.detailView.hidden = false;
  elements.detailRetry.addEventListener("click", () => void refreshDetail());
  elements.detailCancel.addEventListener("click", () => {
    if (state.detailJob) {
      void cancelJob(state.detailJob, elements.detailCancel);
    }
  });
} else {
  elements.jobID.value = nextJobID();
  elements.jobSearch.addEventListener("input", renderJobs);
  elements.statusFilter.addEventListener("change", renderJobs);
  elements.jobForm.addEventListener("submit", submitJob);
  elements.randomJob.addEventListener("click", submitRandomJob);
  elements.functionJobID.value = nextJobID();
  elements.functionUploadForm.addEventListener("submit", uploadFunction);
  elements.functionDeployForm.addEventListener("submit", deployFunction);
  elements.functionInvokeForm.addEventListener("submit", invokeFunction);
  elements.functionID.addEventListener("change", () => void selectFunction(elements.functionID.value.trim()));
  elements.functionFile.addEventListener("change", () => {
    const file = elements.functionFile.files?.[0];
    if (file?.name.endsWith(".py")) {
      elements.functionDeployForm.elements.namedItem("entrypoint").value = `${file.name.slice(0, -3)}:greet`;
    }
  });
  void refreshFunctions();
}

document.addEventListener("visibilitychange", () => {
  if (!document.hidden) {
    refreshCurrentView();
  }
});

void refreshCurrentView();
window.setInterval(() => {
  if (!document.hidden) {
    refreshCurrentView();
  }
}, 1000);

function refreshCurrentView() {
  return state.detailJobID ? refreshDetail() : refreshDashboard();
}

async function refreshDashboard() {
  if (state.refreshing) {
    return;
  }

  state.refreshing = true;
  try {
    const [jobs, jobCount, nodes] = await Promise.all([
      fetchJSON("/v1/jobs?limit=100"),
      fetchJSON("/v1/jobs/count"),
      fetchJSON("/v1/nodes"),
    ]);

    const changedJobs = trackJobTransitions(jobs);
    state.jobs = jobs;
    state.totalJobs = Number(jobCount.count || 0);
    state.nodes = nodes;
    renderMetrics();
    renderResources();
    renderJobs(changedJobs);
    renderNodes();
    await syncExpandedJobLogs();
    setConnection(true);
  } catch (error) {
    setConnection(false, error.message);
  } finally {
    state.refreshing = false;
  }
}

async function refreshDetail() {
  if (state.refreshing) {
    return;
  }

  state.refreshing = true;
  try {
    const [job, events] = await Promise.all([
      fetchJSON(`/v1/jobs/${encodeURIComponent(state.detailJobID)}`),
      fetchJSON(`/v1/jobs/${encodeURIComponent(state.detailJobID)}/events`),
    ]);
    const statusChanged = state.detailJob?.status !== job.status;
    state.detailJob = job;
    if (state.detailVersion?.id !== job.function_version_id) {
      try {
        state.detailVersion = await fetchJSON(`/v1/function-versions/${encodeURIComponent(job.function_version_id)}`);
        state.detailVersionError = "";
      } catch (error) {
        state.detailVersion = null;
        state.detailVersionError = error.message;
      }
    }
    if (job.status === "succeeded" && state.detailResult?.job_id !== job.id) {
      try {
        state.detailResult = await fetchJSON(`/v1/jobs/${encodeURIComponent(job.id)}/result`);
        state.detailResultError = "";
      } catch (error) {
        state.detailResult = null;
        state.detailResultError = error.message;
      }
    } else if (job.status !== "succeeded") {
      state.detailResult = null;
      state.detailResultError = "";
    }
    state.detailEvents = events;
    renderJobDetail(job);
    const active = ["assigned", "running", "cancelling"].includes(job.status);
    if (job.attempt > 0) {
      await loadDetailLogs(job, !statusChanged && !active);
    }
    setConnection(true);
  } catch (error) {
    renderDetailError(error.message);
    setConnection(false, error.message);
  } finally {
    state.refreshing = false;
  }
}

async function fetchJSON(url, options = {}, timeoutMs = 4000) {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    const response = await fetch(url, {
      cache: "no-store",
      ...options,
      headers: {
        Accept: "application/json",
        ...(options.headers || {}),
      },
      signal: controller.signal,
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(body.error || `Request failed with status ${response.status}`);
      error.status = response.status;
      throw error;
    }
    return body;
  } finally {
    window.clearTimeout(timeout);
  }
}

function setConnection(connected, message = "") {
  if (connected) {
    elements.connection.dataset.state = "live";
    elements.connectionLabel.textContent = "Live";
    elements.lastUpdated.textContent = `Updated ${formatClock(new Date())}`;
    elements.errorBanner.hidden = true;
    return;
  }

  elements.connection.dataset.state = "offline";
  elements.connectionLabel.textContent = "Disconnected";
  elements.lastUpdated.textContent = "Retrying every second";
  elements.errorMessage.textContent = message || "The last successful snapshot remains visible.";
  elements.errorBanner.hidden = false;
}

function renderMetrics() {
  const count = (statuses) => state.jobs.filter((job) => statuses.includes(job.status)).length;
  const healthyNodes = state.nodes.filter((node) => node.healthy).length;

  elements.metricTotal.textContent = formatNumber(state.totalJobs);
  elements.metricActive.textContent = formatNumber(count(["assigned", "running", "cancelling"]));
  elements.metricWaiting.textContent = formatNumber(count(["pending", "retrying", "unschedulable"]));
  elements.metricNodes.textContent = `${healthyNodes} / ${state.nodes.length}`;
}

function renderResources() {
  const healthy = state.nodes.filter((node) => node.healthy);
  const total = (field) => healthy.reduce((sum, node) => sum + Number(node[field] || 0), 0);

  renderResource(
    elements.resourceCPU,
    "CPU",
    total("total_cpus"),
    total("available_cpus"),
    "cores",
  );
  renderResource(
    elements.resourceMemory,
    "Memory",
    total("total_memory_gb"),
    total("available_memory_gb"),
    "GB",
  );
  renderResource(
    elements.resourceGPU,
    "GPU",
    total("total_gpus"),
    total("available_gpus"),
    "units",
  );
}

function renderResource(container, label, total, available, unit) {
  const used = Math.max(0, total - available);
  const percent = total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0;
  const line = createElement("div", "resource-line");
  line.append(
    createElement("span", "resource-label", label),
    createElement("span", "resource-value", `${used} / ${total} ${unit}`),
  );

  const track = createElement("div", "resource-track");
  track.setAttribute("role", "progressbar");
  track.setAttribute("aria-label", `${label} utilization`);
  track.setAttribute("aria-valuemin", "0");
  track.setAttribute("aria-valuemax", "100");
  track.setAttribute("aria-valuenow", String(percent));
  const fill = createElement("div", "resource-fill");
  fill.style.width = `${percent}%`;
  track.append(fill);

  container.replaceChildren(line, track);
}

function renderJobs(changedJobs = new Set()) {
  const query = elements.jobSearch.value.trim().toLowerCase();
  const selectedStatus = elements.statusFilter.value;
  const jobs = [...state.jobs]
    .filter((job) => selectedStatus === "all" || job.status === selectedStatus)
    .filter((job) => {
      if (!query) {
        return true;
      }
      return job.id.toLowerCase().includes(query) || job.node_id.toLowerCase().includes(query);
    })
    .sort((left, right) => {
      return Number(right.updated_at || 0) - Number(left.updated_at || 0)
        || left.id.localeCompare(right.id);
    });

  elements.jobsCount.textContent = query || selectedStatus !== "all"
    ? String(jobs.length)
    : formatNumber(state.totalJobs);
  elements.jobsEmpty.hidden = jobs.length > 0;
  elements.jobsEmptyCopy.textContent = state.jobs.length === 0
    ? "Submit a job through this dashboard or the API to see the scheduler place it."
    : "No jobs match the current filters.";

  const rows = [];
  jobs.forEach((job, index) => {
    const expanded = state.expandedJobIDs.has(job.id);
    const panelID = `job-logs-${index}`;
    const row = document.createElement("tr");
    row.dataset.expanded = String(expanded);
    if (changedJobs.has(job.id)) {
      row.dataset.changed = "true";
    }

    const idCell = tableCell("Job");
    const titleLine = createElement("div", "job-title-line");
    const expandButton = createElement("button", "job-expand", expanded ? "Hide logs" : "Logs");
    expandButton.type = "button";
    expandButton.setAttribute("aria-expanded", String(expanded));
    expandButton.setAttribute("aria-controls", panelID);
    expandButton.setAttribute("aria-label", `${expanded ? "Hide" : "Show"} logs for ${job.id}`);
    expandButton.addEventListener("click", () => {
      void toggleJobLogs(job);
    });
    const jobLink = createElement("a", "job-link", job.id);
    jobLink.href = `/jobs/${encodeURIComponent(job.id)}`;
    titleLine.append(expandButton, jobLink);
    idCell.append(
      titleLine,
      createElement("span", "job-meta", `priority ${job.priority}`),
    );

    const statusCell = tableCell("State");
    statusCell.append(createElement("span", `status status-${job.status}`, job.status));

    const updatedCell = tableCell("Updated at", formatUpdatedAt(job.updated_at));
    updatedCell.classList.add("job-updated");
    if (job.updated_at) {
      updatedCell.title = formatTimestamp(job.updated_at);
    }

    const placementCell = tableCell("Placement", job.node_id || "Unassigned");
    if (job.node_id) {
      placementCell.classList.add("node-id");
    }

    const requestCell = tableCell("Function version", job.function_version_id.slice(0, 12));
    const attemptCell = tableCell("Executions", String(job.execution_attempts));
    const leaseCell = tableCell("Lease");
    const lease = formatLease(job);
    leaseCell.textContent = lease.label;
    if (lease.className) {
      leaseCell.classList.add(lease.className);
    }

    const actionCell = tableCell("Actions");
    actionCell.classList.add("job-action");
    const cancelButton = buildCancelButton(job);
    if (cancelButton) {
      actionCell.append(cancelButton);
    } else {
      actionCell.textContent = "—";
    }

    row.append(idCell, statusCell, updatedCell, placementCell, requestCell, attemptCell, leaseCell, actionCell);
    rows.push(row);

    if (expanded) {
      const logRow = createElement("tr", "job-log-row");
      const logCell = tableCell("Logs");
      logCell.colSpan = 8;
      logCell.append(buildInlineLogPanel(job, panelID));
      logRow.append(logCell);
      rows.push(logRow);
    }
  });

  elements.jobsBody.replaceChildren(...rows);
}

async function toggleJobLogs(job) {
  if (state.expandedJobIDs.has(job.id)) {
    state.expandedJobIDs.delete(job.id);
    renderJobs();
    return;
  }

  state.expandedJobIDs.add(job.id);
  renderJobs();
  await loadInlineJobLogs(job, false);
}

function buildInlineLogPanel(job, panelID) {
  const panel = createElement("section", "inline-log-panel");
  panel.id = panelID;
  panel.setAttribute("aria-label", `Logs for ${job.id}`);

  const header = createElement("div", "inline-log-header");
  const identity = document.createElement("div");
  identity.append(
    createElement("span", "inline-log-title", job.attempt > 0 ? `Assignment ${job.attempt}` : "Not assigned"),
    createElement("span", "inline-log-command", formatCommand(job)),
  );
  const detailLink = createElement("a", "inline-detail-link", "View full job");
  detailLink.href = `/jobs/${encodeURIComponent(job.id)}`;
  header.append(identity, detailLink);

  panel.append(header, buildLogTerminal(job, ensureInlineLogState(job), () => {
    void loadInlineJobLogs(job, false);
  }, true));
  return panel;
}

function ensureInlineLogState(job) {
  let logState = state.inlineLogs.get(job.id);
  if (!logState || logState.attempt !== job.attempt) {
    logState = createLogState(job.attempt);
    state.inlineLogs.set(job.id, logState);
  }
  return logState;
}

async function syncExpandedJobLogs() {
  const requests = [];
  for (const jobID of state.expandedJobIDs) {
    const job = state.jobs.find((candidate) => candidate.id === jobID);
    if (!job) {
      state.expandedJobIDs.delete(jobID);
      state.inlineLogs.delete(jobID);
      continue;
    }

    const logState = ensureInlineLogState(job);
    const statusChanged = logState.status !== job.status;
    const active = ["assigned", "running", "cancelling"].includes(job.status);
    if (!logState.loaded || statusChanged || active) {
      requests.push(loadInlineJobLogs(job, true));
    }
    logState.status = job.status;
  }
  await Promise.all(requests);
}

async function loadInlineJobLogs(job, quiet) {
  const logState = ensureInlineLogState(job);
  if (logState.loading) {
    return;
  }
  if (job.attempt <= 0) {
    logState.logs = [];
    logState.loaded = true;
    logState.error = "";
    renderJobs();
    return;
  }

  logState.loading = true;
  if (!quiet || !logState.loaded) {
    renderJobs();
  }
  try {
    logState.logs = await fetchJobLogs(job);
    logState.loaded = true;
    logState.error = "";
  } catch (error) {
    logState.error = error.message;
  } finally {
    logState.loading = false;
    if (state.expandedJobIDs.has(job.id)) {
      renderJobs();
    }
  }
}

function renderJobDetail(job) {
  elements.detailLoading.hidden = true;
  elements.detailError.hidden = true;
  elements.detailContent.hidden = false;
  elements.detailJobID.textContent = job.id;
  elements.detailCommand.textContent = state.detailVersion
    ? `${state.detailVersion.function_id} · ${state.detailVersion.entrypoint} · ${state.detailVersion.image}`
    : formatCommand(job);
  elements.detailStatus.className = `status status-${job.status}`;
  elements.detailStatus.textContent = job.status;
  renderDetailCancelAction(job);
  elements.detailLogAttempt.textContent = job.attempt > 0
    ? `${job.attempt} ${job.attempt === 1 ? "assignment" : "assignments"}`
    : "No assignments";
  document.title = `${job.id} · Mini Modal`;
  renderDetailResult(job);
  renderDetailFields(job);
  renderDetailEvents(state.detailEvents);
  renderDetailLogs(job);
}

function renderDetailResult(job) {
  if (state.detailResult && Object.hasOwn(state.detailResult, "output_json")) {
    elements.detailResult.textContent = JSON.stringify(state.detailResult.output_json, null, 2);
  } else if (state.detailResultError) {
    elements.detailResult.textContent = `Could not load result: ${state.detailResultError}`;
  } else if (job.status === "succeeded") {
    elements.detailResult.textContent = "Result is being saved";
  } else if (["failed", "cancelled", "unschedulable"].includes(job.status)) {
    elements.detailResult.textContent = "No result; see logs and last error";
  } else {
    elements.detailResult.textContent = "Waiting for execution";
  }
}

function renderDetailFields(job) {
  const version = state.detailVersion;
  const groups = [
    ["Identity", [
      ["Job ID", job.id],
      ["Function version", job.function_version_id],
      ["Status", job.status],
      ["Priority", String(job.priority)],
      ["Updated at", formatTimestamp(job.updated_at)],
    ]],
    ["Function version", version ? [
      ["Function", version.function_id],
      ["Entrypoint", version.entrypoint],
      ["Image", version.image],
      ["Artifact key", version.artifact_key],
      ["CPUs", String(version.cpus)],
      ["Memory", `${version.memory_gb} GB`],
      ["GPUs", version.gpus ? `${version.gpus} ${version.gpu_type || "GPU"}` : "None"],
      ["Timeout", version.timeout_seconds ? `${version.timeout_seconds} seconds` : "None"],
      ["Max attempts", String(version.max_attempts)],
      ["Retry backoff", `${version.retry_backoff_seconds} seconds`],
    ] : [["Status", state.detailVersionError || "Loading version"]]],
    ["Placement", [
      ["Node ID", job.node_id || "Unassigned"],
      ["Assignment generation", job.attempt > 0 ? String(job.attempt) : "None"],
      ["Lease expires", formatLeaseTimestamp(job.lease_expires_at)],
      ["Lease remaining", formatLease(job).label],
    ]],
    ["Execution", [
      ["Executions started", String(job.execution_attempts)],
      ["Input", JSON.stringify(job.input_json)],
      ["Next attempt", job.next_attempt_at ? formatTimestamp(job.next_attempt_at) : "Not scheduled"],
      ["Last error", job.last_error || "None"],
    ]],
  ];

  const sections = groups.map(([title, fields]) => {
    const section = createElement("section", "detail-field-group");
    section.append(createElement("h4", "", title));
    const list = createElement("dl", "detail-field-list");
    for (const [label, value] of fields) {
      const item = document.createElement("div");
      item.append(createElement("dt", "", label), createElement("dd", "", value));
      list.append(item);
    }
    section.append(list);
    return section;
  });
  elements.detailFields.replaceChildren(...sections);
}

function renderDetailEvents(events) {
  elements.detailEventsCount.textContent = `${events.length} ${events.length === 1 ? "event" : "events"}`;
  if (events.length === 0) {
    elements.detailEvents.replaceChildren(
      createElement("li", "detail-event-empty", "No lifecycle events recorded."),
    );
    return;
  }

  const items = events.map((event, index) => {
    const item = createElement("li", `detail-event detail-event-${event.event}`);
    if (index === events.length - 1) {
      item.dataset.latest = "true";
    }

    const marker = createElement("span", "detail-event-marker");
    marker.setAttribute("aria-hidden", "true");

    const content = createElement("div", "detail-event-content");
    content.append(
      createElement("strong", "detail-event-name", formatEventName(event.event)),
      createElement("p", "detail-event-message", event.message),
    );

    const context = [];
    context.push(event.attempt > 0 ? `Assignment ${event.attempt}` : "Before first assignment");
    if (event.node_id) {
      context.push(event.node_id);
    }
    context.push(formatTimestamp(event.created_at));
    content.append(createElement("span", "detail-event-meta", context.join(" · ")));
    item.append(marker, content);
    return item;
  });

  elements.detailEvents.replaceChildren(...items);
}

function formatEventName(event) {
  return event
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function renderDetailLogs(job) {
  if (job.attempt <= 0) {
    elements.detailLogTerminal.replaceChildren(buildLogTerminal(job, createLogState(), () => {
      void loadDetailLogs(job, false);
    }, false));
    return;
  }

  const groups = createElement("div", "attempt-log-groups");
  for (let attempt = job.attempt; attempt >= 1; attempt -= 1) {
    const logState = ensureDetailLogState(attempt);
    const group = document.createElement("details");
    group.className = "attempt-log-group";
    group.open = attempt === job.attempt;

    const summary = document.createElement("summary");
    const label = createElement("span", "attempt-log-label", `Assignment ${attempt}`);
    const meta = logState.loading && !logState.loaded
      ? "Loading"
      : `${logState.logs.length} ${logState.logs.length === 1 ? "chunk" : "chunks"}`;
    summary.append(label, createElement("span", "attempt-log-meta", meta));

    const content = createElement("div", "attempt-log-content");
    content.append(buildLogTerminal(job, logState, () => {
      logState.loaded = false;
      void loadDetailLogs(job, false);
    }, false));
    group.append(summary, content);
    groups.append(group);
  }
  elements.detailLogTerminal.replaceChildren(groups);
}

async function loadDetailLogs(job, quiet) {
  if (job.attempt <= 0) {
    renderDetailLogs(job);
    return;
  }

  const active = ["assigned", "running", "cancelling"].includes(job.status);
  const attempts = [];
  for (let attempt = 1; attempt <= job.attempt; attempt += 1) {
    const logState = ensureDetailLogState(attempt);
    const refreshCurrent = attempt === job.attempt && active;
    if (!logState.loading && (!logState.loaded || refreshCurrent)) {
      logState.loading = true;
      attempts.push([attempt, logState]);
    }
  }
  if (attempts.length === 0) {
    return;
  }
  if (!quiet || attempts.some(([, logState]) => !logState.loaded)) {
    renderDetailLogs(job);
  }

  await Promise.all(attempts.map(async ([attempt, logState]) => {
    try {
      logState.logs = await fetchJobLogs(job, attempt);
      logState.loaded = true;
      logState.error = "";
    } catch (error) {
      logState.error = error.message;
    } finally {
      logState.loading = false;
    }
  }));
  if (state.detailJob?.id === job.id) {
    renderDetailLogs(state.detailJob);
  }
}

function ensureDetailLogState(attempt) {
  let logState = state.detailLogs.get(attempt);
  if (!logState) {
    logState = createLogState(attempt);
    state.detailLogs.set(attempt, logState);
  }
  return logState;
}

async function fetchJobLogs(job, attempt = job.attempt) {
  const logs = await fetchJSON(
    `/v1/jobs/${encodeURIComponent(job.id)}/logs?attempt=${attempt}`,
  );
  return Array.isArray(logs)
    ? [...logs].sort((left, right) => left.sequence - right.sequence)
    : [];
}

function buildLogTerminal(job, logState, retry, compact) {
  const terminal = createElement("div", compact ? "log-terminal compact-log-terminal" : "log-terminal-inner");
  const toolbar = createElement("div", "log-toolbar");
  toolbar.append(
    createElement("span", "", logStreamLabel(job)),
    createElement("span", "", `${logState.logs.length} ${logState.logs.length === 1 ? "chunk" : "chunks"}`),
  );
  terminal.append(toolbar);

  if (logState.loading && !logState.loaded) {
    const loading = createElement("div", "log-loading");
    loading.setAttribute("aria-hidden", "true");
    loading.append(
      createElement("span", "skeleton"),
      createElement("span", "skeleton skeleton-short"),
      createElement("span", "skeleton"),
    );
    terminal.append(loading);
    return terminal;
  }

  if (logState.error) {
    const message = createElement("div", "log-message log-error");
    message.setAttribute("role", "alert");
    message.append(
      createElement("strong", "", "Could not load logs"),
      createElement("p", "", logState.error),
    );
    const retryButton = createElement("button", "secondary-button", "Retry");
    retryButton.type = "button";
    retryButton.addEventListener("click", retry);
    message.append(retryButton);
    terminal.append(message);
    return terminal;
  }

  if (logState.logs.length === 0) {
    const message = createElement("div", "log-message");
    message.append(
      createElement("strong", "", "No output yet"),
      createElement("p", "", "Image preparation and process output will appear here."),
    );
    terminal.append(message);
    return terminal;
  }

  const output = createElement("div", "log-output");
  output.setAttribute("role", "log");
  output.setAttribute("aria-live", "polite");
  output.setAttribute("aria-label", `Output for ${job.id}`);
  output.append(...buildLogEntries(logState.logs));
  terminal.append(output);
  window.requestAnimationFrame(() => {
    output.scrollTop = output.scrollHeight;
  });
  return terminal;
}

function buildLogEntries(logs) {
  return logs.map((chunk) => {
    const entry = createElement("div", "log-entry");
    entry.dataset.source = chunk.source;

    const sequence = createElement("span", "log-sequence", String(chunk.sequence).padStart(3, "0"));
    const source = createElement("span", "log-source", chunk.source);
    const output = document.createElement("pre");
    output.className = "log-data";
    output.textContent = chunk.data;
    if (chunk.created_at) {
      entry.title = formatLogTime(chunk.created_at);
    }

    entry.append(sequence, source, output);
    return entry;
  });
}

function renderDetailError(message) {
  if (state.detailJob) {
    return;
  }
  elements.detailLoading.hidden = true;
  elements.detailContent.hidden = true;
  elements.detailError.hidden = false;
  elements.detailErrorMessage.textContent = message;
}

function createLogState(attempt = 0) {
  return { attempt, status: "", logs: [], loaded: false, loading: false, error: "" };
}

function canCancelJob(job) {
  return ["pending", "assigned", "running", "retrying", "unschedulable"].includes(job.status);
}

function buildCancelButton(job) {
  if (!canCancelJob(job) && job.status !== "cancelling") {
    return null;
  }

  const cancelling = job.status === "cancelling" || state.cancellingJobIDs.has(job.id);
  const button = createElement("button", "danger-button compact", cancelling ? "Cancelling" : "Cancel");
  button.type = "button";
  button.disabled = cancelling;
  button.setAttribute("aria-label", `${cancelling ? "Cancelling" : "Cancel"} job ${job.id}`);
  if (!cancelling) {
    button.addEventListener("click", () => void cancelJob(job, button));
  }
  return button;
}

function renderDetailCancelAction(job) {
  const cancelling = job.status === "cancelling" || state.cancellingJobIDs.has(job.id);
  const visible = canCancelJob(job) || cancelling;
  elements.detailCancel.hidden = !visible;
  elements.detailCancel.disabled = cancelling;
  elements.detailCancel.textContent = cancelling ? "Cancelling" : "Cancel job";
}

async function cancelJob(job, button) {
  if (!canCancelJob(job)) {
    return;
  }
  if (!window.confirm(`Cancel job "${job.id}"?`)) {
    return;
  }

  state.cancellingJobIDs.add(job.id);
  setBusy(button, true, "Cancelling");
  if (state.detailJobID) {
    setFormResult(elements.detailActionResult, "Sending cancellation request…", "pending");
  } else {
    renderJobs();
  }

  try {
    const updated = await fetchJSON(`/v1/jobs/${encodeURIComponent(job.id)}/cancel`, {
      method: "POST",
    });
    if (state.detailJobID === job.id) {
      state.detailJob = updated;
      setFormResult(
        elements.detailActionResult,
        updated.status === "cancelled" ? "Job cancelled." : "Cancellation requested.",
        "success",
      );
      renderJobDetail(updated);
    } else {
      state.jobs = state.jobs.map((candidate) => candidate.id === updated.id ? updated : candidate);
      state.previousJobStates.set(updated.id, updated.status);
      addActivity(
        updated.status === "cancelled"
          ? `${updated.id}: cancelled`
          : `${updated.id}: cancellation requested`,
      );
      renderJobs(new Set([updated.id]));
    }
    await refreshCurrentView();
  } catch (error) {
    if (state.detailJobID) {
      setFormResult(elements.detailActionResult, error.message, "error");
    } else {
      addActivity(`Could not cancel ${job.id}: ${error.message}`);
    }
  } finally {
    state.cancellingJobIDs.delete(job.id);
    if (state.detailJobID && state.detailJob) {
      renderJobDetail(state.detailJob);
    } else {
      renderJobs();
    }
  }
}

function logStreamLabel(job) {
  switch (job.status) {
  case "assigned":
    return "Waiting for agent";
  case "running":
    return "Live, refreshing every second";
  case "cancelling":
    return "Cancellation requested, awaiting agent";
  case "retrying":
    return "Attempt failed, waiting for retry";
  case "cancelled":
  case "succeeded":
  case "failed":
    return "Final output";
  default:
    return "Execution has not started";
  }
}

function formatCommand(job) {
  return `Function version ${job.function_version_id}`;
}

function formatLeaseTimestamp(timestamp) {
  if (!timestamp) {
    return "None";
  }
  return formatTimestamp(timestamp);
}

function formatTimestamp(timestamp) {
  if (!timestamp) {
    return "None";
  }
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "medium",
  }).format(new Date(timestamp));
}

function formatUpdatedAt(timestamp) {
  if (!timestamp) {
    return "Unknown";
  }
  const elapsedSeconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000));
  if (elapsedSeconds < 5) {
    return "Just now";
  }
  if (elapsedSeconds < 60) {
    return `${elapsedSeconds}s ago`;
  }
  const elapsedMinutes = Math.floor(elapsedSeconds / 60);
  if (elapsedMinutes < 60) {
    return `${elapsedMinutes}m ago`;
  }
  const elapsedHours = Math.floor(elapsedMinutes / 60);
  if (elapsedHours < 24) {
    return `${elapsedHours}h ago`;
  }
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
  }).format(new Date(timestamp));
}

function formatLogTime(timestamp) {
  return new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    fractionalSecondDigits: 3,
  }).format(new Date(timestamp));
}

function renderNodes() {
  const nodes = [...state.nodes].sort((left, right) => {
    return Number(right.healthy) - Number(left.healthy) || left.id.localeCompare(right.id);
  });

  elements.nodesCount.textContent = String(nodes.length);
  elements.nodesEmpty.hidden = nodes.length > 0;
  elements.nodeList.hidden = nodes.length === 0;

  const cards = nodes.map((node) => {
    const card = createElement("article", "node-card");
    card.dataset.health = node.healthy ? "healthy" : "stale";

    const identity = document.createElement("div");
    identity.append(
      createElement("div", "node-id", node.id),
      createElement("p", "node-gpu", node.gpu_type || "CPU worker"),
    );

    const health = createElement("span", "node-health");
    health.append(
      createElement("span", "health-dot"),
      document.createTextNode(node.healthy ? "Healthy" : "Stale"),
    );

    const top = createElement("div", "node-topline");
    top.append(identity, health);

    const capacity = createElement("div", "capacity-grid");
    capacity.append(
      capacityLine("CPU available", node.available_cpus, node.total_cpus, "cores"),
      capacityLine("Memory available", node.available_memory_gb, node.total_memory_gb, "GB"),
    );
    if (node.total_gpus > 0) {
      capacity.append(capacityLine("GPU available", node.available_gpus, node.total_gpus, "units"));
    }

    card.append(top, capacity);
    return card;
  });

  elements.nodeList.replaceChildren(...cards);
}

function trackJobTransitions(jobs) {
  const changed = new Set();
  const nextStates = new Map();

  for (const job of jobs) {
    nextStates.set(job.id, job.status);
    const previous = state.previousJobStates.get(job.id);
    if (!state.hasSnapshot) {
      continue;
    }
    if (previous === undefined) {
      addActivity(`${job.id} entered ${job.status}`);
      changed.add(job.id);
    } else if (previous !== job.status) {
      addActivity(`${job.id}: ${previous} to ${job.status}`);
      changed.add(job.id);
    }
  }

  state.previousJobStates = nextStates;
  state.hasSnapshot = true;
  return changed;
}

async function refreshFunctions() {
  try {
    state.functions = await fetchJSON("/v1/functions");
    elements.functionsCount.textContent = `${state.functions.length}${state.functions.length === 100 ? "+" : ""}`;
    if (!state.selectedFunctionID && state.functions.length > 0) {
      await selectFunction(state.functions[0].id);
    } else {
      renderFunctionList();
      if (state.selectedFunctionID) {
        await loadFunctionVersions(state.selectedFunctionID);
      }
    }
  } catch (error) {
    setFormResult(elements.functionUploadResult, `Could not load functions: ${error.message}`, "error");
  }
}

function renderFunctionList() {
  if (state.functions.length === 0) {
    elements.functionsList.replaceChildren(createElement("p", "function-empty", "No functions deployed yet."));
    return;
  }
  const buttons = state.functions.map((fn) => {
    const button = createElement("button", "function-list-item");
    button.type = "button";
    button.dataset.selected = String(fn.id === state.selectedFunctionID);
    button.setAttribute("aria-pressed", String(fn.id === state.selectedFunctionID));
    button.append(
      createElement("span", "function-item-title", fn.id),
      createElement("span", "function-item-meta", `${fn.version_count} version${fn.version_count === 1 ? "" : "s"} · ${formatFunctionDate(fn.updated_at)}`),
    );
    button.addEventListener("click", () => void selectFunction(fn.id));
    return button;
  });
  elements.functionsList.replaceChildren(...buttons);
}

async function selectFunction(functionID) {
  const changed = functionID !== state.selectedFunctionID;
  state.selectedFunctionID = functionID;
  elements.functionID.value = functionID;
  if (changed) {
    state.selectedVersionID = "";
    state.functionVersions = [];
    elements.functionArtifactKey.value = "";
    setFormResult(elements.functionDeployResult, "", "");
    setFormResult(elements.functionInvokeResult, "", "");
  }
  renderFunctionList();
  await loadFunctionVersions(functionID);
}

async function loadFunctionVersions(functionID) {
  elements.functionVersionsFor.textContent = functionID || "Select a function";
  if (!functionID) {
    state.functionVersions = [];
    state.selectedVersionID = "";
    renderFunctionVersions();
    return;
  }
  try {
    const versions = await fetchJSON(`/v1/functions/${encodeURIComponent(functionID)}/versions`);
    if (state.selectedFunctionID !== functionID) {
      return;
    }
    state.functionVersions = versions;
    if (!versions.some((version) => version.id === state.selectedVersionID)) {
      state.selectedVersionID = versions[0]?.id || "";
    }
    const jobVersionField = elements.jobForm.elements.namedItem("function_version_id");
    if (jobVersionField instanceof HTMLInputElement && !jobVersionField.value) {
      jobVersionField.value = state.selectedVersionID;
    }
    renderFunctionVersions();
  } catch (error) {
    elements.functionVersionList.replaceChildren(createElement("p", "function-empty", `Could not load versions: ${error.message}`));
  }
}

function renderFunctionVersions() {
  const versions = state.functionVersions;
  if (versions.length === 0) {
    elements.functionVersionList.replaceChildren(createElement("p", "function-empty", "No versions for this function yet."));
    elements.functionVersionSelect.replaceChildren(new Option("Deploy a version first", ""));
    elements.functionVersionDetail.hidden = true;
    return;
  }
  const cards = versions.map((version) => {
    const button = createElement("button", "function-version-item");
    button.type = "button";
    button.dataset.selected = String(version.id === state.selectedVersionID);
    button.setAttribute("aria-pressed", String(version.id === state.selectedVersionID));
    button.append(
      createElement("span", "function-item-title", version.id),
      createElement("span", "function-item-meta", `${formatFunctionDate(version.created_at)} · ${version.image} · ${version.cpus} CPU / ${version.memory_gb} GB`),
      createElement("span", "function-item-key", version.artifact_key || "No artifact"),
    );
    button.addEventListener("click", () => {
      state.selectedVersionID = version.id;
      elements.functionArtifactKey.value = version.artifact_key;
      elements.jobForm.elements.namedItem("function_version_id").value = version.id;
      renderFunctionVersions();
    });
    return button;
  });
  elements.functionVersionList.replaceChildren(...cards);
  const options = versions.map((version) => new Option(`${version.id.slice(0, 8)} · ${formatFunctionDate(version.created_at)}`, version.id));
  elements.functionVersionSelect.replaceChildren(...options);
  elements.functionVersionSelect.value = state.selectedVersionID;
  const selected = versions.find((version) => version.id === state.selectedVersionID);
  const details = [
    ["Version ID", selected.id],
    ["Created", formatFunctionDate(selected.created_at)],
    ["Artifact key", selected.artifact_key || "None"],
    ["Image", selected.image],
    ["Entrypoint", selected.entrypoint],
    ["Resources", `${selected.cpus} CPU · ${selected.memory_gb} GB memory · ${selected.gpus} GPU${selected.gpu_type ? ` (${selected.gpu_type})` : ""}`],
    ["Timeout", `${selected.timeout_seconds}s`],
    ["Retries", `${selected.max_attempts} max attempts · ${selected.retry_backoff_seconds}s backoff`],
  ];
  const list = createElement("dl", "function-version-fields");
  for (const [label, value] of details) {
    const row = document.createElement("div");
    row.append(createElement("dt", "", label), createElement("dd", "", String(value)));
    list.append(row);
  }
  elements.functionVersionDetail.replaceChildren(createElement("h4", "", "Selected version"), list);
  elements.functionVersionDetail.hidden = false;
}

async function uploadFunction(event) {
  event.preventDefault();
  const functionID = elements.functionID.value.trim();
  const file = elements.functionFile.files?.[0];
  setBusy(elements.functionUploadButton, true, "Uploading");
  setFormResult(elements.functionUploadResult, "", "");
  try {
    if (!file) {
      throw new Error("Choose a .py or .tar.gz file");
    }
    if (file.size > 32 * 1024 * 1024) {
      throw new Error("File exceeds the 32 MiB upload limit");
    }
    const pythonFile = file.name.endsWith(".py");
    if (!pythonFile && !file.name.endsWith(".tar.gz")) {
      throw new Error("Choose a .py or .tar.gz file");
    }
    await selectFunction(functionID);
    const path = `/v1/functions/${encodeURIComponent(functionID)}/artifacts${pythonFile ? `?filename=${encodeURIComponent(file.name)}` : ""}`;
    const uploaded = await fetchJSON(path, {
      method: "POST",
      headers: { "Content-Type": pythonFile ? "text/x-python" : "application/gzip" },
      body: file,
    }, 60000);
    elements.functionArtifactKey.value = uploaded.artifact_key;
    setFormResult(elements.functionUploadResult, `${file.name} stored (${formatNumber(uploaded.size_bytes)} bytes). Deploy a version next.`, "success");
    addActivity(`${functionID}: uploaded ${file.name}`);
  } catch (error) {
    setFormResult(elements.functionUploadResult, error.message, "error");
  } finally {
    setBusy(elements.functionUploadButton, false, "Upload artifact");
  }
}

async function deployFunction(event) {
  event.preventDefault();
  const functionID = elements.functionID.value.trim();
  const form = new FormData(elements.functionDeployForm);
  setBusy(elements.functionDeployButton, true, "Deploying");
  setFormResult(elements.functionDeployResult, "", "");
  try {
    await selectFunction(functionID);
    const version = await fetchJSON(`/v1/functions/${encodeURIComponent(functionID)}/versions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        artifact_key: String(form.get("artifact_key") || "").trim(),
        image: String(form.get("image") || "").trim(),
        entrypoint: String(form.get("entrypoint") || "").trim(),
        cpus: Number(form.get("cpus")),
        memory_gb: Number(form.get("memory_gb")),
        gpu_type: String(form.get("gpu_type") || "").trim(),
        gpus: Number(form.get("gpus")),
        timeout_seconds: Number(form.get("timeout_seconds")),
        max_attempts: Number(form.get("max_attempts")),
        retry_backoff_seconds: Number(form.get("retry_backoff_seconds")),
      }),
    }, 10000);
    state.selectedVersionID = version.id;
    elements.jobForm.elements.namedItem("function_version_id").value = version.id;
    setFormResult(elements.functionDeployResult, `Version ${version.id} deployed. You can create a job now.`, "success");
    addActivity(`${functionID}: deployed version ${version.id.slice(0, 8)}`);
    await refreshFunctions();
  } catch (error) {
    setFormResult(elements.functionDeployResult, error.message, "error");
  } finally {
    setBusy(elements.functionDeployButton, false, "Deploy version");
  }
}

async function invokeFunction(event) {
  event.preventDefault();
  const functionID = elements.functionID.value.trim();
  const form = new FormData(elements.functionInvokeForm);
  const versionID = String(form.get("version_id") || "");
  setBusy(elements.functionInvokeButton, true, "Creating");
  setFormResult(elements.functionInvokeResult, "", "");
  try {
    if (!versionID || !state.functionVersions.some((version) => version.id === versionID && version.function_id === functionID)) {
      throw new Error("Select a version belonging to this function");
    }
    const job = await fetchJSON(`/v1/functions/${encodeURIComponent(functionID)}/jobs`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        id: String(form.get("id") || "").trim(),
        version_id: versionID,
        priority: Number(form.get("priority")),
        input: parseInvocationInput(form.get("input")),
      }),
    }, 10000);
    const link = createElement("a", "inline-detail-link", `View job ${job.id}`);
    link.href = `/jobs/${encodeURIComponent(job.id)}`;
    elements.functionInvokeResult.replaceChildren(link, document.createTextNode(` · ${job.status}`));
    elements.functionInvokeResult.dataset.state = "success";
    elements.functionJobID.value = nextJobID();
    addActivity(`${job.id} created from ${functionID}`);
    await refreshDashboard();
  } catch (error) {
    setFormResult(elements.functionInvokeResult, error.message, "error");
  } finally {
    setBusy(elements.functionInvokeButton, false, "Create job");
  }
}

function formatFunctionDate(timestamp) {
  return new Date(Number(timestamp)).toLocaleString();
}

function parseInvocationInput(raw) {
  const input = JSON.parse(String(raw || "{}"));
  const args = input?.args ?? [];
  const kwargs = input?.kwargs ?? {};
  if (!input || Array.isArray(input) || typeof input !== "object" || !Array.isArray(args) || !kwargs || Array.isArray(kwargs) || typeof kwargs !== "object" || Object.keys(input).some((key) => key !== "args" && key !== "kwargs")) {
    throw new Error("Input must be a JSON object with args array and kwargs object");
  }
  return { args, kwargs };
}

async function submitJob(event) {
  event.preventDefault();
  const form = new FormData(elements.jobForm);
  const job = {
    id: String(form.get("id") || "").trim(),
    function_version_id: String(form.get("function_version_id") || "").trim(),
    priority: Number(form.get("priority")),
    input: String(form.get("input") || "{}").trim(),
  };

  await sendJob(job, elements.submitJob, "Submitting", "Submit");
}

async function submitRandomJob() {
  const job = {
    id: nextJobID(),
    function_version_id: String(elements.jobForm.elements.namedItem("function_version_id").value || "").trim(),
    priority: randomInteger(1, 100),
    input: String(elements.jobForm.elements.namedItem("input").value || "{}").trim(),
  };

  populateJobForm(job);
  await sendJob(job, elements.randomJob, "Sending", "Random");
}

async function sendJob(job, button, busyLabel, idleLabel) {
  setBusy(button, true, busyLabel);
  setFormResult(elements.jobFormResult, "", "");

  try {
    if (!job.function_version_id) {
      throw new Error("Select or deploy a function version before submitting jobs");
    }
    const payload = { ...job, input: parseInvocationInput(job.input) };

    const created = await fetchJSON("/v1/jobs", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    setFormResult(elements.jobFormResult, `${created.id} accepted with state ${created.status}.`, "success");
    addActivity(`${created.id} submitted at priority ${created.priority}`);
    elements.jobID.value = nextJobID();
    await refreshDashboard();
  } catch (error) {
    setFormResult(elements.jobFormResult, error.message, "error");
    addActivity(`Job submission failed: ${error.message}`);
  } finally {
    setBusy(button, false, idleLabel);
  }
}

function populateJobForm(job) {
  for (const [name, value] of Object.entries(job)) {
    const field = elements.jobForm.elements.namedItem(name);
    if (field instanceof HTMLInputElement) {
      field.value = String(value);
    }
  }
}

function randomCapacity(field, fallback) {
  const healthyNodes = state.nodes.filter((node) => node.healthy);
  const maximum = healthyNodes.length > 0
    ? healthyNodes.reduce((current, node) => Math.max(current, Number(node[field]) || 0), 0)
    : fallback;
  const options = [1, 2, 4, 8, 16].filter((value) => value <= maximum);
  return options[randomInteger(0, options.length - 1)];
}

function randomInteger(minimum, maximum) {
  return Math.floor(Math.random() * (maximum - minimum + 1)) + minimum;
}

function addActivity(message) {
  state.activity.unshift({ message, time: new Date() });
  state.activity = state.activity.slice(0, 8);

  const items = state.activity.map((event) => {
    const item = document.createElement("li");
    item.append(
      createElement("time", "activity-time", formatClock(event.time)),
      createElement("span", "", event.message),
    );
    return item;
  });
  elements.activityList.replaceChildren(...items);
}

function capacityLine(label, available, total, unit) {
  const line = createElement("div", "capacity-line");
  line.append(
    createElement("span", "", label),
    createElement("span", "", `${available} / ${total} ${unit}`),
  );
  return line;
}

function tableCell(label, value = "") {
  const cell = document.createElement("td");
  cell.dataset.label = label;
  cell.textContent = value;
  return cell;
}

function formatLease(job) {
  if (!["assigned", "running", "cancelling"].includes(job.status) || !job.lease_expires_at) {
    return { label: "None", className: "" };
  }
  const remaining = job.lease_expires_at - Date.now();
  if (remaining <= 0) {
    return { label: "Expired", className: "lease-expired" };
  }
  const seconds = Math.ceil(remaining / 1000);
  return {
    label: `${seconds}s`,
    className: seconds <= 5 ? "lease-expiring" : "",
  };
}

function createElement(tag, className = "", text = "") {
  const element = document.createElement(tag);
  if (className) {
    element.className = className;
  }
  if (text !== "") {
    element.textContent = text;
  }
  return element;
}

function setBusy(button, busy, label) {
  button.disabled = busy;
  button.textContent = label;
}

function setFormResult(element, message, status) {
  element.textContent = message;
  if (status) {
    element.dataset.state = status;
  } else {
    delete element.dataset.state;
  }
}

function nextJobID() {
  const stamp = new Date().toISOString().replace(/[-:TZ.]/g, "");
  const suffix = Math.random().toString(36).slice(2, 6);
  return `job-${stamp}-${suffix}`;
}

function jobIDFromPath() {
  const match = window.location.pathname.match(/^\/jobs\/([^/]+)\/?$/);
  if (!match) {
    return null;
  }
  try {
    return decodeURIComponent(match[1]);
  } catch {
    return match[1];
  }
}

function formatClock(date) {
  return new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(date);
}

function formatNumber(value) {
  return new Intl.NumberFormat().format(value);
}
