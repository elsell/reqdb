"use strict";

const state = {
  requirements: [], tasks: [], filter: "", refreshTimer: 0,
  collapsedRequirements: new Set(), collapsedTasks: new Set(),
};
const elements = {
  requirements: document.querySelector("#requirements"), tasks: document.querySelector("#tasks"),
  requirementCount: document.querySelector("#requirement-count"), taskCount: document.querySelector("#task-count"),
  summary: document.querySelector("#summary"), error: document.querySelector("#error"),
  updated: document.querySelector("#updated"), refresh: document.querySelector("#refresh"),
  filter: document.querySelector("#filter"), connectionDot: document.querySelector("#connection-dot"),
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
  if (!response.ok || envelope.error) throw new Error(envelope.error?.message || `Request failed with status ${response.status}`);
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
    [state.requirements, state.tasks] = await Promise.all([fetchAll("/v1/requirements"), fetchAll("/v1/tasks")]);
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

function matches(values) { return !state.filter || values.join(" ").toLowerCase().includes(state.filter); }
function cell(className, text) { return element("div", `grid-cell ${className}`, text); }
function textCell(title, description) {
  const node = cell("text-cell");
  node.append(element("span", "primary", title));
  if (description) node.append(element("span", "secondary", description));
  return node;
}
function header(labels) {
  const row = element("div", "grid-header");
  for (const label of labels) row.append(cell("", label));
  return row;
}
function treeCell(id, depth, hasChildren, collapsed, toggle) {
  const node = cell("tree-cell");
  node.style.paddingLeft = `${5 + depth * 17}px`;
  const button = element("button", `twisty${hasChildren ? "" : " placeholder"}`, collapsed ? "+" : "−");
  button.type = "button";
  button.title = collapsed ? "Expand" : "Collapse";
  button.addEventListener("click", toggle);
  node.append(button, element("span", "object-id", id));
  return node;
}

function requirementGraph() {
  const byID = new Map(state.requirements.map(item => [item.id, item]));
  const children = new Map(state.requirements.map(item => [item.id, []]));
  const roots = [];
  for (const item of state.requirements) {
    const parent = item.revision.parents.find(ref => byID.has(ref.id));
    if (parent) children.get(parent.id).push(item); else roots.push(item);
  }
  const taskLinks = new Map();
  for (const task of state.tasks) for (const link of task.requirements) {
    const id = link.requirement.split("@")[0];
    if (!taskLinks.has(id)) taskLinks.set(id, []);
    taskLinks.get(id).push(task);
  }
  return { roots, children, taskLinks };
}

function renderRequirements() {
  elements.requirements.replaceChildren();
  elements.requirements.className = "tree requirements-grid";
  elements.requirements.append(header(["Object Identifier", "Level", "Object Heading and Text", "Reconciliation", "Linked tasks"]));
  const { roots, children, taskLinks } = requirementGraph();
  let visible = 0;
  const visit = (item, depth, parentVisible) => {
    const descendants = children.get(item.id) || [];
    const linked = taskLinks.get(item.id) || [];
    const ownMatch = matches([item.id, item.revision.title, item.revision.statement, item.revision.level, item.reconciliation_state, ...linked.map(task => task.id)]);
    const descendantMatch = descendants.some(child => requirementMatches(child, children, taskLinks));
    if (!parentVisible || (!ownMatch && !descendantMatch)) return;
    visible++;
    const row = element("div", "grid-row");
    const collapsed = state.collapsedRequirements.has(item.id) && !state.filter;
    row.append(
      treeCell(`${item.id}@${item.current_revision}`, depth, descendants.length > 0, collapsed, () => {
        if (collapsed) state.collapsedRequirements.delete(item.id); else state.collapsedRequirements.add(item.id);
        renderRequirements();
      }),
      cell("", item.revision.level),
      textCell(item.revision.title, item.revision.statement),
      cell(`state ${item.reconciliation_state}`, item.reconciliation_state.replaceAll("_", " ")),
      cell("references", linked.map(task => task.id).join(", ") || "—"),
    );
    elements.requirements.append(row);
    for (const child of descendants) visit(child, depth + 1, !collapsed);
  };
  for (const root of roots) visit(root, 0, true);
  if (!visible) elements.requirements.append(element("div", "empty", state.requirements.length ? "No requirements match the filter." : "No requirements found."));
}

function requirementMatches(item, children, taskLinks) {
  const linked = taskLinks.get(item.id) || [];
  if (matches([item.id, item.revision.title, item.revision.statement, item.revision.level, item.reconciliation_state, ...linked.map(task => task.id)])) return true;
  return (children.get(item.id) || []).some(child => requirementMatches(child, children, taskLinks));
}

function taskGraph() {
  const byID = new Map(state.tasks.map(item => [item.id, item]));
  const children = new Map(state.tasks.map(item => [item.id, []]));
  const roots = [];
  for (const item of state.tasks) {
    const parent = item.depends_on.find(id => byID.has(id));
    if (parent) children.get(parent).push(item); else roots.push(item);
  }
  const order = (left, right) => right.priority - left.priority || left.id.localeCompare(right.id);
  roots.sort(order);
  for (const items of children.values()) items.sort(order);
  return { roots, children };
}

function taskMatches(item, children) {
  if (matches([item.id, item.title, item.description, item.state, ...item.requirements.map(link => link.requirement)])) return true;
  return (children.get(item.id) || []).some(child => taskMatches(child, children));
}

function renderTasks() {
  elements.tasks.replaceChildren();
  elements.tasks.className = "tree tasks-grid";
  elements.tasks.append(header(["Task Identifier", "Heading and Description", "State", "Priority", "Requirement links"]));
  const { roots, children } = taskGraph();
  let visible = 0;
  const visit = (item, depth, parentVisible) => {
    const descendants = children.get(item.id) || [];
    if (!parentVisible || !taskMatches(item, children)) return;
    visible++;
    const row = element("div", "grid-row");
    const collapsed = state.collapsedTasks.has(item.id) && !state.filter;
    row.append(
      treeCell(item.id, depth, descendants.length > 0, collapsed, () => {
        if (collapsed) state.collapsedTasks.delete(item.id); else state.collapsedTasks.add(item.id);
        renderTasks();
      }),
      textCell(item.title, item.description),
      cell(`state ${item.state}`, item.state.replaceAll("_", " ")),
      cell("", String(item.priority)),
      cell("references", item.requirements.map(link => link.requirement).join(", ") || "—"),
    );
    elements.tasks.append(row);
    for (const child of descendants) visit(child, depth + 1, !collapsed);
  };
  for (const root of roots) visit(root, 0, true);
  if (!visible) elements.tasks.append(element("div", "empty", state.tasks.length ? "No tasks match the filter." : "No tasks found."));
}

function renderSummary() {
  const values = [
    [state.requirements.length, "requirements"],
    [state.requirements.filter(item => item.reconciliation_state === "implemented").length, "implemented"],
    [state.requirements.filter(item => item.reconciliation_state === "needs_reconciliation").length, "need reconciliation"],
    [state.tasks.filter(item => item.state === "open").length, "open tasks"],
    [state.tasks.filter(item => item.state === "complete").length, "complete tasks"],
  ];
  elements.summary.replaceChildren(...values.map(([value, label]) => {
    const item = element("span", "summary-item");
    item.append(element("strong", "", String(value)), ` ${label}`);
    return item;
  }));
}

function render() {
  elements.requirementCount.textContent = state.requirements.length;
  elements.taskCount.textContent = state.tasks.length;
  renderSummary(); renderRequirements(); renderTasks();
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
elements.filter.addEventListener("input", event => { state.filter = event.target.value.trim().toLowerCase(); render(); });
refresh(); connect();
