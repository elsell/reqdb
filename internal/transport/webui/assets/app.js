"use strict";

const state = { requirements: [], tasks: [], filter: "", refreshTimer: 0 };
const elements = {
  requirements: document.querySelector("#requirements"),
  tasks: document.querySelector("#tasks"),
  requirementCount: document.querySelector("#requirement-count"),
  taskCount: document.querySelector("#task-count"),
  summary: document.querySelector("#summary"),
  error: document.querySelector("#error"),
  updated: document.querySelector("#updated"),
  refresh: document.querySelector("#refresh"),
  filter: document.querySelector("#filter"),
  connectionDot: document.querySelector("#connection-dot"),
  connectionText: document.querySelector("#connection-text"),
};

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

async function fetchPage(path) {
  const response = await fetch(path, { headers: { Accept: "application/json" } });
  const envelope = await response.json();
  if (!response.ok || envelope.error) {
    const message = envelope.error?.message || `Request failed with status ${response.status}`;
    throw new Error(message);
  }
  return envelope;
}

async function fetchAll(path) {
  const items = [];
  let cursor = "";
  do {
    const separator = path.includes("?") ? "&" : "?";
    const envelope = await fetchPage(`${path}${separator}limit=200&cursor=${encodeURIComponent(cursor)}`);
    items.push(...(envelope.data || []));
    cursor = envelope.meta?.next_cursor || "";
  } while (cursor);
  return items;
}

async function refresh() {
  elements.refresh.disabled = true;
  try {
    const [requirements, tasks] = await Promise.all([
      fetchAll("/v1/requirements"),
      fetchAll("/v1/tasks"),
    ]);
    state.requirements = requirements;
    state.tasks = tasks;
    elements.error.hidden = true;
    render();
    elements.updated.textContent = `Updated ${new Date().toLocaleTimeString()}`;
  } catch (error) {
    elements.error.textContent = error.message;
    elements.error.hidden = false;
  } finally {
    elements.refresh.disabled = false;
  }
}

function scheduleRefresh() {
  window.clearTimeout(state.refreshTimer);
  state.refreshTimer = window.setTimeout(refresh, 80);
}

function searchableRequirement(item) {
  return [item.id, item.revision.title, item.revision.statement, item.revision.level, item.reconciliation_state].join(" ").toLowerCase();
}

function searchableTask(item) {
  return [item.id, item.title, item.description, item.state, ...item.requirements.map(link => link.requirement)].join(" ").toLowerCase();
}

function matches(value) {
  return !state.filter || value.includes(state.filter);
}

function badge(value) {
  return element("span", `badge ${value}`, value.replaceAll("_", " "));
}

function requirementNode(item, children, linkedTasks) {
  const wrapper = element("div", "node");
  const details = element("details");
  details.open = item.revision.level === "business" || item.revision.level === "stakeholder" || Boolean(state.filter);
  const summary = element("summary");
  summary.append(element("span", "node-id", `${item.id}@${item.current_revision}`));
  summary.append(element("span", "node-title", item.revision.title));
  summary.append(badge(item.reconciliation_state));
  details.append(summary);

  const body = element("div", "node-body");
  body.append(element("p", "", item.revision.statement));
  const meta = element("div", "meta");
  meta.append(element("span", "", `Level: ${item.revision.level}`));
  meta.append(element("span", "", `Actor: ${item.revision.actor_id}`));
  if (item.revision.parents.length) meta.append(element("span", "", `Parents: ${item.revision.parents.map(parent => `${parent.id}@${parent.revision}`).join(", ")}`));
  body.append(meta);
  if (linkedTasks.length) {
    const links = element("div", "links");
    links.append(element("strong", "", "Tasks: "));
    for (const task of linkedTasks) links.append(element("span", "task-link", `${task.id} · ${task.state}`));
    body.append(links);
  }
  details.append(body);
  wrapper.append(details);
  for (const child of children) wrapper.append(child);
  return wrapper;
}

function renderRequirements() {
  elements.requirements.replaceChildren();
  const byID = new Map(state.requirements.map(item => [item.id, item]));
  const children = new Map();
  for (const item of state.requirements) children.set(item.id, []);
  const roots = [];
  for (const item of state.requirements) {
    const parent = item.revision.parents.find(ref => byID.has(ref.id));
    if (parent) children.get(parent.id).push(item);
    else roots.push(item);
  }
  const taskLinks = new Map();
  for (const task of state.tasks) {
    for (const link of task.requirements) {
      const id = link.requirement.split("@")[0];
      if (!taskLinks.has(id)) taskLinks.set(id, []);
      taskLinks.get(id).push(task);
    }
  }

  const build = item => {
    const childResults = (children.get(item.id) || []).map(build).filter(Boolean);
    const linked = taskLinks.get(item.id) || [];
    const ownMatch = matches(searchableRequirement(item)) || linked.some(task => matches(searchableTask(task)));
    if (!ownMatch && !childResults.length) return null;
    return requirementNode(item, childResults, linked);
  };
  for (const root of roots) {
    const node = build(root);
    if (node) elements.requirements.append(node);
  }
  if (!elements.requirements.children.length) elements.requirements.append(element("div", "empty", state.requirements.length ? "No requirements match the filter." : "No requirements found."));
  elements.requirements.classList.remove("loading");
}

function taskNode(item, children) {
  const wrapper = element("div", "node");
  const details = element("details");
  details.open = item.state !== "complete" || Boolean(state.filter);
  const summary = element("summary");
  summary.append(element("span", "node-id", item.id));
  summary.append(element("span", "node-title", item.title));
  summary.append(badge(item.state));
  details.append(summary);
  const body = element("div", "node-body");
  body.append(element("p", "", item.description));
  const meta = element("div", "meta");
  meta.append(element("span", "", `Priority: ${item.priority}`));
  meta.append(element("span", "", `Version: ${item.version}`));
  if (item.depends_on.length) meta.append(element("span", "", `Depends on: ${item.depends_on.join(", ")}`));
  if (item.completed_commit) meta.append(element("span", "", `Commit: ${item.completed_commit}`));
  body.append(meta);
  if (item.requirements.length) {
    const links = element("div", "links");
    links.append(element("strong", "", "Requirements: "));
    for (const link of item.requirements) links.append(element("span", "task-link", `${link.requirement} · ${link.purpose}`));
    body.append(links);
  }
  details.append(body);
  wrapper.append(details);
  for (const child of children) wrapper.append(child);
  return wrapper;
}

function renderTasks() {
  elements.tasks.replaceChildren();
  const byID = new Map(state.tasks.map(item => [item.id, item]));
  const children = new Map();
  for (const item of state.tasks) children.set(item.id, []);
  const roots = [];
  for (const item of state.tasks) {
    const parents = item.depends_on.filter(id => byID.has(id));
    if (!parents.length) roots.push(item);
    else children.get(parents[0]).push(item);
  }
  const taskOrder = (left, right) => right.priority - left.priority || left.id.localeCompare(right.id);
  roots.sort(taskOrder);
  for (const items of children.values()) items.sort(taskOrder);
  const build = item => {
    const childResults = (children.get(item.id) || []).map(build).filter(Boolean);
    if (!matches(searchableTask(item)) && !childResults.length) return null;
    return taskNode(item, childResults);
  };
  for (const root of roots) {
    const node = build(root);
    if (node) elements.tasks.append(node);
  }
  if (!elements.tasks.children.length) elements.tasks.append(element("div", "empty", state.tasks.length ? "No tasks match the filter." : "No tasks found."));
  elements.tasks.classList.remove("loading");
}

function renderSummary() {
  const values = [
    [state.requirements.length, "Requirements"],
    [state.requirements.filter(item => item.reconciliation_state === "implemented").length, "Implemented"],
    [state.requirements.filter(item => item.reconciliation_state === "needs_reconciliation").length, "Need reconciliation"],
    [state.tasks.filter(item => item.state === "open").length, "Open tasks"],
    [state.tasks.filter(item => item.state === "complete").length, "Complete tasks"],
  ];
  elements.summary.replaceChildren(...values.map(([value, label]) => {
    const card = element("div", "summary-card");
    card.append(element("strong", "", String(value)), element("span", "", label));
    return card;
  }));
}

function render() {
  elements.requirementCount.textContent = state.requirements.length;
  elements.taskCount.textContent = state.tasks.length;
  renderSummary();
  renderRequirements();
  renderTasks();
}

function connect() {
  const events = new EventSource("/v1/events");
  events.addEventListener("ready", () => {
    elements.connectionDot.className = "connection-dot online";
    elements.connectionText.textContent = "Live";
  });
  events.addEventListener("change", scheduleRefresh);
  events.onerror = () => {
    elements.connectionDot.className = "connection-dot offline";
    elements.connectionText.textContent = "Reconnecting";
  };
}

elements.refresh.addEventListener("click", refresh);
elements.filter.addEventListener("input", event => {
  state.filter = event.target.value.trim().toLowerCase();
  render();
});

refresh();
connect();
