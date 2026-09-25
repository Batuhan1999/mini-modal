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
  devClusterForm: document.querySelector("#dev-cluster-form"),
  devClusterState: document.querySelector("#dev-cluster-state"),
  devClusterResult: document.querySelector("#dev-cluster-result"),
  startDevCluster: document.querySelector("#start-dev-cluster"),
  stopDevCluster: document.querySelector("#stop-dev-cluster"),
  activityList: document.querySelector("#activity-list"),
};

const state = {
  jobs: [],
  nodes: [],
  previousJobStates: new Map(),
  hasSnapshot: false,
  refreshing: false,
  activity: [],
  devClusterHydrated: false,
  devClusterRunning: false,
  previousProcessStates: new Map(),
};

const statusOrder = new Map([
  ["running", 0],
  ["assigned", 1],
  ["pending", 2],
  ["unschedulable", 3],
  ["failed", 4],
  ["succeeded", 5],
]);

elements.jobID.value = nextJobID();
elements.jobSearch.addEventListener("input", renderJobs);
elements.statusFilter.addEventListener("change", renderJobs);
elements.jobForm.addEventListener("submit", submitJob);
elements.randomJob.addEventListener("click", submitRandomJob);
elements.devClusterForm.addEventListener("submit", startDevCluster);
elements.stopDevCluster.addEventListener("click", stopDevCluster);

document.addEventListener("visibilitychange", () => {
  if (!document.hidden) {
    refresh();
  }
});

void refresh();
window.setInterval(() => {
  if (!document.hidden) {
    refresh();
  }
}, 1000);

async function refresh() {
  if (state.refreshing) {
    return;
  }

  state.refreshing = true;
  try {
    const [jobs, nodes, devCluster] = await Promise.all([
      fetchJSON("/v1/jobs"),
      fetchJSON("/v1/nodes"),
      fetchJSON("/v1/dev/nodes"),
    ]);

    const changedJobs = trackJobTransitions(jobs);
    state.jobs = jobs;
    state.nodes = nodes;
    renderMetrics();
    renderResources();
    renderJobs(changedJobs);
    renderNodes();
    applyDevClusterStatus(devCluster, !state.devClusterHydrated);
    state.devClusterHydrated = true;
    setConnection(true);
  } catch (error) {
    setConnection(false, error.message);
  } finally {
    state.refreshing = false;
  }
}

async function fetchJSON(url, options = {}) {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), 4000);
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
      throw new Error(body.error || `Request failed with status ${response.status}`);
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

  elements.metricTotal.textContent = formatNumber(state.jobs.length);
  elements.metricActive.textContent = formatNumber(count(["assigned", "running"]));
  elements.metricWaiting.textContent = formatNumber(count(["pending", "unschedulable"]));
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
      const rank = (statusOrder.get(left.status) ?? 99) - (statusOrder.get(right.status) ?? 99);
      return rank || right.priority - left.priority || left.id.localeCompare(right.id);
    });

  elements.jobsCount.textContent = String(jobs.length);
  elements.jobsEmpty.hidden = jobs.length > 0;
  elements.jobsEmptyCopy.textContent = state.jobs.length === 0
    ? "Submit a job through this dashboard or the API to see the scheduler place it."
    : "No jobs match the current filters.";

  const rows = jobs.map((job) => {
    const row = document.createElement("tr");
    if (changedJobs.has(job.id)) {
      row.dataset.changed = "true";
    }

    const idCell = tableCell("Job");
    idCell.append(
      createElement("span", "job-id", job.id),
      createElement("span", "job-meta", `priority ${job.priority}`),
    );

    const statusCell = tableCell("State");
    statusCell.append(createElement("span", `status status-${job.status}`, job.status));

    const placementCell = tableCell("Placement", job.node_id || "Unassigned");
    if (job.node_id) {
      placementCell.classList.add("node-id");
    }

    const request = [`${job.cpus} CPU`, `${job.memory_gb} GB`];
    if (job.gpus > 0) {
      request.push(`${job.gpus} GPU`);
    }
    const requestCell = tableCell("Request", request.join(" · "));
    const attemptCell = tableCell("Attempt", job.attempt > 0 ? String(job.attempt) : "None");
    const leaseCell = tableCell("Lease");
    const lease = formatLease(job);
    leaseCell.textContent = lease.label;
    if (lease.className) {
      leaseCell.classList.add(lease.className);
    }

    row.append(idCell, statusCell, placementCell, requestCell, attemptCell, leaseCell);
    return row;
  });

  elements.jobsBody.replaceChildren(...rows);
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

async function submitJob(event) {
  event.preventDefault();
  const form = new FormData(elements.jobForm);
  const job = {
    id: String(form.get("id") || "").trim(),
    priority: Number(form.get("priority")),
    gpus: 0,
    cpus: Number(form.get("cpus")),
    memory_gb: Number(form.get("memory_gb")),
    duration_seconds: Number(form.get("duration_seconds")),
    command: String(form.get("command") || "").trim(),
    args: String(form.get("args") || "[]").trim(),
  };

  await sendJob(job, elements.submitJob, "Submitting", "Submit");
}

async function submitRandomJob() {
  const duration = randomInteger(2, 10);
  const job = {
    id: nextJobID(),
    priority: randomInteger(1, 100),
    gpus: 0,
    cpus: randomCapacity("total_cpus", 8),
    memory_gb: randomCapacity("total_memory_gb", 16),
    duration_seconds: duration,
    command: "sleep",
    args: JSON.stringify([String(duration)]),
  };

  populateJobForm(job);
  await sendJob(job, elements.randomJob, "Sending", "Random");
}

async function sendJob(job, button, busyLabel, idleLabel) {
  setBusy(button, true, busyLabel);
  setFormResult(elements.jobFormResult, "", "");

  try {
    const args = JSON.parse(job.args);
    if (!Array.isArray(args) || args.some((argument) => typeof argument !== "string")) {
      throw new Error("Arguments must be a JSON array of strings");
    }

    const created = await fetchJSON("/v1/jobs", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(job),
    });
    setFormResult(elements.jobFormResult, `${created.id} accepted with state ${created.status}.`, "success");
    addActivity(`${created.id} submitted at priority ${created.priority}`);
    elements.jobID.value = nextJobID();
    await refresh();
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

async function startDevCluster(event) {
  event.preventDefault();
  setBusy(elements.startDevCluster, true, "Launching");
  const form = new FormData(elements.devClusterForm);
  const config = {
    node_count: Number(form.get("node_count")),
    cpus_per_node: Number(form.get("cpus_per_node")),
    memory_gb_per_node: Number(form.get("memory_gb_per_node")),
  };

  try {
    const status = await fetchJSON("/v1/dev/nodes", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(config),
    });
    applyDevClusterStatus(status, false);
    addActivity(`Launched ${config.node_count} local agent processes`);
    await refresh();
  } catch (error) {
    setFormResult(elements.devClusterResult, error.message, "error");
    addActivity(`Agent launch failed: ${error.message}`);
  } finally {
    setBusy(elements.startDevCluster, false, state.devClusterRunning ? "Apply and restart" : "Launch agents");
  }
}

async function stopDevCluster() {
  setBusy(elements.stopDevCluster, true, "Stopping");
  try {
    const status = await fetchJSON("/v1/dev/nodes", { method: "DELETE" });
    applyDevClusterStatus(status, false);
    addActivity("Stopped local agents; their heartbeats will become stale");
  } catch (error) {
    setFormResult(elements.devClusterResult, error.message, "error");
  } finally {
    elements.stopDevCluster.textContent = "Stop";
    elements.stopDevCluster.disabled = !state.devClusterRunning;
  }
}

function applyDevClusterStatus(status, hydrateForm) {
  const processes = status.processes || [];
  const running = processes.filter((process) => process.running).length;
  const degraded = processes.length > 0 && running < processes.length;
  trackProcessTransitions(processes);
  state.devClusterRunning = status.running;

  elements.devClusterState.dataset.state = degraded
    ? "degraded"
    : status.running ? "running" : "stopped";
  elements.devClusterState.textContent = degraded
    ? "Degraded"
    : status.running ? "Running" : "Stopped";
  elements.startDevCluster.textContent = status.running ? "Apply and restart" : "Launch agents";
  elements.stopDevCluster.disabled = !status.running;

  if (hydrateForm && status.config) {
    for (const [name, value] of Object.entries(status.config)) {
      const input = elements.devClusterForm.elements.namedItem(name);
      if (input) {
        input.value = String(value);
      }
    }
  }

  const firstError = processes.find((process) => process.error)?.error;
  const message = firstError
    ? `${running}/${processes.length} processes running. ${firstError}`
    : status.running
      ? `${running}/${processes.length} agent processes running. Waiting for gRPC registration if they are not listed below yet.`
      : "Launches separate agent processes that register over gRPC.";
  setFormResult(elements.devClusterResult, message, firstError ? "error" : status.running ? "success" : "");
}

function trackProcessTransitions(processes) {
  const next = new Map();
  for (const process of processes) {
    next.set(process.node_id, process.running);
    const previous = state.previousProcessStates.get(process.node_id);
    if (state.devClusterHydrated && previous === true && !process.running) {
      addActivity(`${process.node_id} process exited${process.error ? `: ${process.error}` : ""}`);
    }
  }
  state.previousProcessStates = next;
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
  if (!["assigned", "running"].includes(job.status) || !job.lease_expires_at) {
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
