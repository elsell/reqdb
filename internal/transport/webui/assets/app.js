"use strict";

const state = {
  requirements: [], tasks: [], leases: [], events: [], filter: "", statusFilter: "", refreshTimer: 0, selected: null, initialized: false,
  collapsed: new Set(), collapsedGroups: new Set(), details: new Map(),
};
const elements = {
  content: document.querySelector(".content"), activityPanels: document.querySelector("#activity-panels"), leasePanel: document.querySelector("#lease-panel"),
  verticalSplitter: document.querySelector("#vertical-splitter"), horizontalSplitter: document.querySelector("#horizontal-splitter"),
  explorer: document.querySelector("#explorer"), details: document.querySelector("#details"), leases: document.querySelector("#leases"),
  leaseCount: document.querySelector("#lease-count"), events: document.querySelector("#events"), eventCount: document.querySelector("#event-count"), summary: document.querySelector("#summary"),
  error: document.querySelector("#error"), updated: document.querySelector("#updated"),
  refresh: document.querySelector("#refresh"), filter: document.querySelector("#filter"),
  expandAll: document.querySelector("#expand-all"), collapseAll: document.querySelector("#collapse-all"),
  connectionDot: document.querySelector("#connection-dot"), connectionText: document.querySelector("#connection-text"),
};
let describeRequirement;
let describeTask;

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function icon(name, className = "ui-icon") {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", className);
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
  use.setAttribute("href", `#i-${name}`);
  svg.append(use);
  return svg;
}

function headerCell(label, definition) {
  const cell = element("div", "tree-header-cell");
  if (label) cell.append(element("span", "", label));
  if (!definition) return cell;
  const help = element("span", "column-help");
  help.setAttribute("tabindex", "0");
  help.setAttribute("aria-label", `${label}: ${definition}`);
  help.append(icon("info", "column-help-icon"));
  const tooltip = element("span", "column-tooltip", definition);
  tooltip.setAttribute("role", "tooltip");
  help.append(tooltip);
  const positionTooltip = () => {
    const triggerBounds = help.getBoundingClientRect();
    const explorerBounds = elements.explorer.getBoundingClientRect();
    const width = Math.min(230, explorerBounds.width - 12);
    const left = Math.max(explorerBounds.left + 6, Math.min(triggerBounds.right - width, explorerBounds.right - width - 6));
    tooltip.style.width = `${width}px`;
    tooltip.style.left = `${left}px`;
    tooltip.style.top = `${triggerBounds.bottom + 3}px`;
  };
  help.addEventListener("mouseenter", positionTooltip);
  help.addEventListener("focus", positionTooltip);
  cell.append(help);
  return cell;
}

function statusIcon(value) {
  if (value === "satisfied" || value === "complete") return "check";
  if (value === "needs_reconciliation") return "warning";
  if (value === "ready_for_review") return "review";
  if (value === "ready_for_work" || value === "ready_to_lease") return "ready";
  if (value === "in_progress" || value === "work_in_progress") return "progress";
  if (value === "awaiting_review") return "review";
  if (value === "no_work_required") return "check";
  if (value === "retired" || value === "closed" || value === "unavailable") return "retired";
  return "pending";
}

function statusChip(value, label) {
  const chip = element("span", `status-chip ${value}`);
  chip.append(icon(statusIcon(value), "status-icon"), element("span", "", label || value.replaceAll("_", " ")));
  return chip;
}

function actionIcon(label) {
  if (label.startsWith("Copy")) return "copy";
  if (label.startsWith("Review") || label.startsWith("Complete")) return "check";
  if (label.startsWith("Retire") || label.startsWith("Release")) return "retired";
  if (label.startsWith("Lease")) return "lease";
  if (label.startsWith("Link")) return "link";
  if (label.startsWith("Heartbeat")) return "refresh";
  return "more";
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
    const envelope = await fetchPage(`${path}?limit=200&cursor=${encodeURIComponent(cursor)}`);
    items.push(...(envelope.data || []));
    cursor = envelope.meta?.next_cursor || "";
  } while (cursor);
  return items;
}

async function refresh() {
  elements.refresh.disabled = true;
  try {
    [state.requirements, state.tasks, state.leases] = await Promise.all([fetchAll("/v1/requirements"), fetchAll("/v1/tasks"), fetchAll("/v1/leases")]);
    if (!state.initialized) {
      state.collapsed = new Set(state.requirements.map(item => `requirement:${item.id}`));
      state.collapsedGroups.delete("requirements");
      state.initialized = true;
    }
    state.details.clear();
    elements.error.hidden = true;
    render();
    if (state.selected) await loadDetail(state.selected.type, state.selected.id);
    elements.updated.textContent = `Updated ${new Date().toLocaleTimeString()}`;
  } catch (error) {
    elements.error.textContent = error.message;
    elements.error.hidden = false;
  } finally {
    elements.refresh.disabled = false;
  }
}

async function loadDetail(type, id) {
  try {
    const resource = type === "requirement" ? "requirements" : "tasks";
    const envelope = await fetchPage(`/v1/${resource}/${encodeURIComponent(id)}`);
    state.details.set(`${type}:${id}`, envelope.data);
    renderDetails();
  } catch (error) {
    elements.error.textContent = error.message;
    elements.error.hidden = false;
  }
}

function scheduleRefresh() {
  window.clearTimeout(state.refreshTimer);
  state.refreshTimer = window.setTimeout(refresh, 80);
}

function matches(values) { return !state.filter || values.join(" ").toLowerCase().includes(state.filter); }

function statusMatches(type, item) {
  switch (state.statusFilter) {
  case "requirements": return type === "requirement";
  case "satisfied": return type === "requirement" && item.reconciliation_state === "satisfied";
  case "retired": return type === "requirement" && item.lifecycle_state === "retired";
  case "needs_reconciliation": return type === "requirement" && item.reconciliation_state === "needs_reconciliation";
  case "open_tasks": return type === "task" && item.state === "open";
  case "complete_tasks": return type === "task" && item.state === "complete";
  case "active_leases": return type === "task" && state.leases.some(lease => lease.task_id === item.id);
  default: return true;
  }
}

function itemMatches(type, item, values) { return matches(values) && statusMatches(type, item); }
function filterActive() { return Boolean(state.filter || state.statusFilter); }

async function mutate(path, body) {
  try {
    const response = await fetch(path, {
      method: "POST", headers: { Accept: "application/json", "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    const envelope = await response.json();
    if (!response.ok || envelope.error) throw new Error(envelope.error?.message || `Request failed with status ${response.status}`);
    await refresh();
  } catch (error) {
    elements.error.textContent = error.message;
    elements.error.hidden = false;
  }
}

function copy(value) {
  navigator.clipboard.writeText(value).catch(() => window.prompt("Copy this value:", value));
}

function graph(items, parentIDs, order) {
  const byID = new Map(items.map(item => [item.id, item]));
  const children = new Map(items.map(item => [item.id, []]));
  const roots = [];
  for (const item of items) {
    const parent = parentIDs(item).find(id => byID.has(id));
    if (parent) children.get(parent).push(item); else roots.push(item);
  }
  roots.sort(order);
  for (const values of children.values()) values.sort(order);
  return { roots, children };
}

function button(hasChildren, collapsed, toggle) {
  const control = element("button", `twisty${hasChildren ? "" : " placeholder"}`, hasChildren ? (collapsed ? "+" : "−") : "");
  control.type = "button";
  control.tabIndex = hasChildren ? 0 : -1;
  if (hasChildren) {
    control.title = collapsed ? "Expand" : "Collapse";
    control.addEventListener("click", event => { event.stopPropagation(); toggle(); });
  }
  return control;
}

function nodeRow({ id, title, lifecycleValue, leaseLabel, workability, stateValue, stateLabel, type, iconName, depth = 0, hasChildren, collapsed, toggle, group, actions, selected, select }) {
  const row = element("div", `tree-row${group ? " group" : ""}${selected ? " selected" : ""}`);
  row.setAttribute("role", "treeitem");
  if (hasChildren) row.setAttribute("aria-expanded", String(!collapsed));
  if (select) row.setAttribute("aria-selected", String(Boolean(selected)));
  row.title = [id, title, lifecycleValue, stateValue, workability?.disposition].filter(Boolean).join(" — ");

  const primary = element("div", "tree-primary");
  primary.style.setProperty("--depth", String(depth));
  primary.append(button(hasChildren, collapsed, toggle), icon(group ? "folder" : (iconName || type), `item-icon ${group ? "folder" : (iconName || type)}`));
  if (id) primary.append(element("span", "node-id", id));
  primary.append(element("span", "node-title", title));

  const lifecycle = element("div", `tree-attribute${lifecycleValue ? "" : " muted"}`);
  if (lifecycleValue) lifecycle.append(statusChip(lifecycleValue)); else lifecycle.textContent = "—";
  const workState = element("div", `tree-attribute${stateValue ? "" : " muted"}`);
  if (stateValue) workState.append(statusChip(stateValue, stateLabel)); else workState.textContent = "—";
  if (leaseLabel) {
    workState.append(icon("lease", "status-icon lease-mark"));
    workState.title = leaseLabel;
  }
  const workabilityCell = element("div", `tree-attribute${group ? " muted" : ""}`);
  if (group) workabilityCell.textContent = "—";
  else workabilityCell.append(statusChip(workability?.disposition || "waiting"));
  row.append(primary, lifecycle, workState, workabilityCell);
  if (select) {
    row.classList.add("clickable");
    row.tabIndex = 0;
    const choose = () => {
      for (const selectedRow of elements.explorer.querySelectorAll(".tree-row.selected")) selectedRow.classList.remove("selected");
      row.classList.add("selected");
      select();
    };
    row.addEventListener("click", choose);
    row.addEventListener("keydown", event => {
      if (event.key === "Enter" || event.key === " ") { event.preventDefault(); choose(); }
    });
  }
  if (hasChildren) row.addEventListener("dblclick", event => { event.preventDefault(); toggle(); });
  return row;
}

function hasMatch(item, children, values) {
  if (matches(values(item))) return true;
  return (children.get(item.id) || []).some(child => hasMatch(child, children, values));
}

function renderBranch(item, children, values, describe, type, depth) {
  if (!hasMatch(item, children, values)) return null;
  const descendants = children.get(item.id) || [];
  const key = `${type}:${item.id}`;
  const collapsed = state.collapsed.has(key) && !filterActive();
  const wrapper = element("div", "tree-node");
  const details = describe(item);
  wrapper.append(nodeRow({
    ...details, type, depth, hasChildren: descendants.length > 0, collapsed,
    selected: state.selected?.type === type && state.selected.id === item.id,
    select: () => { state.selected = { type, id: item.id }; renderDetails(); loadDetail(type, item.id); },
    toggle: () => { collapsed ? state.collapsed.delete(key) : state.collapsed.add(key); render(); },
  }));
  if (descendants.length && !collapsed) {
    const childList = element("div", "tree-children");
    childList.setAttribute("role", "group");
    for (const child of descendants) {
      const childNode = renderBranch(child, children, values, describe, type, depth + 1);
      if (childNode) childList.append(childNode);
    }
    wrapper.append(childList);
  }
  return wrapper;
}

function groupNode(key, title, count, roots, children, values, describe, type, renderNode) {
  const collapsed = state.collapsedGroups.has(key) && !filterActive();
  const wrapper = element("div", "tree-node");
  const complete = type === "requirement"
    ? state.requirements.filter(item => item.reconciliation_state === "satisfied").length
    : state.tasks.filter(item => item.state === "complete").length;
  wrapper.append(nodeRow({
    title: `${title} (${count})`, group: true, depth: 0, hasChildren: roots.length > 0, collapsed,
    stateValue: complete === count && count > 0 ? (type === "requirement" ? "satisfied" : "complete") : "in_progress",
    stateLabel: `${complete}/${count} ${type === "requirement" ? "satisfied" : "complete"}`,
    toggle: () => { collapsed ? state.collapsedGroups.delete(key) : state.collapsedGroups.add(key); render(); },
  }));
  if (!collapsed) {
    const childList = element("div", "tree-children");
    childList.setAttribute("role", "group");
    for (const root of roots) {
      const child = renderNode ? renderNode(root, 1) : renderBranch(root, children, values, describe, type, 1);
      if (child) childList.append(child);
    }
    if (!childList.children.length) childList.append(element("div", "empty", count ? "No items match the filter." : "No items found."));
    wrapper.append(childList);
  }
  return wrapper;
}

function renderExplorer() {
  elements.explorer.replaceChildren();
  elements.explorer.className = "tree";
  elements.explorer.setAttribute("role", "tree");
  const header = element("div", "tree-header");
  header.append(
    headerCell("Item"),
    headerCell("Lifecycle", "Shows whether the item is active or retired."),
    headerCell("Work state", "Shows the requirement reconciliation state or the task progress state."),
    headerCell("Workability", "Shows the computed work disposition. Open the item to see whether it is workable and why."),
  );
  elements.explorer.append(header);
  const requirementOrder = (left, right) => left.id.localeCompare(right.id);
  const requirements = graph(state.requirements, item => item.revision.parents.map(parent => parent.id), requirementOrder);
  const requirementValues = item => [item.id, item.revision.title, item.revision.statement, item.revision.level, item.lifecycle_state, item.reconciliation_state, item.workability?.disposition, ...(item.workability?.reasons || [])];
  describeRequirement = item => {
    const actions = [{ label: "Copy ID", run: () => copy(`${item.id}@${item.current_revision}`) }];
    if (item.lifecycle_state !== "retired") {
      actions.push({ label: "Review…", run: () => {
        const commit = window.prompt(`Git commit for ${item.id}@${item.current_revision}:`);
        if (!commit) return;
        const verdict = window.prompt("Verdict: accept or reject", "accept");
        if (!verdict) return;
        const taskId = window.prompt("Completed task ID (optional):", "") || "";
        const findings = [];
        if (verdict === "reject") {
          const message = window.prompt("Finding:");
          if (!message) return;
          const path = window.prompt("Path (optional):", "") || "";
          const line = path ? Number(window.prompt("Line (optional):", "0") || "0") : 0;
          findings.push({ message, path, line });
        }
        mutate(`/v1/requirements/${encodeURIComponent(item.id)}/reviews`, { commit, verdict, task_id: taskId, findings });
      } });
      actions.push({ label: "Retire…", run: () => {
        if (window.confirm(`Retire requirement ${item.id}?`)) mutate(`/v1/requirements/${encodeURIComponent(item.id)}/retire`, {});
      } });
    }
    return {
      id: `${item.id}@${item.current_revision}`, title: item.revision.title,
      iconName: item.revision.level, lifecycleValue: item.lifecycle_state, stateValue: item.reconciliation_state,
      workability: item.workability, actions,
    };
  };

  const taskOrder = (left, right) => right.priority - left.priority || left.id.localeCompare(right.id);
  const taskValues = item => [item.id, item.title, item.description, item.state, item.workability?.disposition, ...(item.requirements || []).map(link => link.requirement), ...(item.workability?.reasons || [])];
  describeTask = item => {
    const lease = state.leases.find(value => value.task_id === item.id);
    const actions = [{ label: "Copy ID", run: () => copy(item.id) }];
    if (item.state === "open" && !lease) actions.push({ label: "Lease…", run: () => {
      const agent = window.prompt(`Agent for ${item.id}:`);
      if (!agent) return;
      const ttl = window.prompt("Lease duration:", "30m");
      if (!ttl) return;
      mutate(`/v1/tasks/${encodeURIComponent(item.id)}/lease`, { agent, ttl });
    } });
    if (item.state === "open" && !lease) actions.push({ label: "Close…", run: () => {
      if (window.confirm(`Close task ${item.id} without completing it?`)) mutate(`/v1/tasks/${encodeURIComponent(item.id)}/close`, {});
    } });
    if (lease) actions.push({ label: "Complete…", run: () => {
      const commit = window.prompt(`Git commit for ${item.id}:`);
      if (!commit) return;
      mutate(`/v1/tasks/${encodeURIComponent(item.id)}/complete`, { lease: lease.lease_id, fence: lease.fence, commit });
    } });
    actions.push({ label: "Link pull request…", run: () => {
      const raw = window.prompt(`GitHub pull request URL for ${item.id}:`);
      if (!raw) return;
      try {
        const url = new URL(raw);
        const parts = url.pathname.split("/").filter(Boolean);
        if (parts.length < 4 || parts.at(-2) !== "pull" || !Number(parts.at(-1))) throw new Error("Pull request URL is invalid.");
        mutate(`/v1/tasks/${encodeURIComponent(item.id)}/pull-requests`, {
          repository: `${url.host}/${parts[0]}/${parts[1]}`, number: Number(parts.at(-1)), url: raw,
        });
      } catch (error) {
        elements.error.textContent = error.message;
        elements.error.hidden = false;
      }
    } });
    return {
      id: `${item.id}@${item.version}`, title: item.title,
      meta: item.pull_requests?.length ? `${item.pull_requests.length} PR` : "",
      stateValue: item.state, workability: item.workability, leaseLabel: lease ? `leased · ${lease.agent_id}` : "", actions,
    };
  };

  const tasksByRequirement = new Map(state.requirements.map(item => [item.id, []]));
  for (const task of state.tasks) {
    for (const link of task.requirements || []) {
      const requirementID = link.requirement.split("@", 1)[0];
      const linkedTasks = tasksByRequirement.get(requirementID);
      if (linkedTasks && !linkedTasks.some(value => value.id === task.id)) linkedTasks.push(task);
    }
  }
  for (const linkedTasks of tasksByRequirement.values()) linkedTasks.sort(taskOrder);

  const requirementHasMatch = item => {
    if (itemMatches("requirement", item, requirementValues(item))) return true;
    if ((tasksByRequirement.get(item.id) || []).some(task => itemMatches("task", task, taskValues(task)))) return true;
    return (requirements.children.get(item.id) || []).some(requirementHasMatch);
  };
  const renderTaskLeaf = (item, depth) => {
    if (!itemMatches("task", item, taskValues(item))) return null;
    const wrapper = element("div", "tree-node");
    wrapper.append(nodeRow({
      ...describeTask(item), type: "task", depth, hasChildren: false,
      selected: state.selected?.type === "task" && state.selected.id === item.id,
      select: () => { state.selected = { type: "task", id: item.id }; renderDetails(); loadDetail("task", item.id); },
    }));
    return wrapper;
  };
  const renderRequirementBranch = (item, depth) => {
    if (!requirementHasMatch(item)) return null;
    const requirementChildren = (requirements.children.get(item.id) || []).filter(requirementHasMatch);
    const taskChildren = (tasksByRequirement.get(item.id) || []).filter(task => itemMatches("task", task, taskValues(task)));
    const hasChildren = requirementChildren.length > 0 || taskChildren.length > 0;
    const key = `requirement:${item.id}`;
    const collapsed = state.collapsed.has(key) && !filterActive();
    const wrapper = element("div", "tree-node");
    wrapper.append(nodeRow({
      ...describeRequirement(item), type: "requirement", depth, hasChildren, collapsed,
      selected: state.selected?.type === "requirement" && state.selected.id === item.id,
      select: () => { state.selected = { type: "requirement", id: item.id }; renderDetails(); loadDetail("requirement", item.id); },
      toggle: () => { collapsed ? state.collapsed.delete(key) : state.collapsed.add(key); render(); },
    }));
    if (hasChildren && !collapsed) {
      const childList = element("div", "tree-children");
      childList.setAttribute("role", "group");
      for (const child of requirementChildren) childList.append(renderRequirementBranch(child, depth + 1));
      for (const task of taskChildren) childList.append(renderTaskLeaf(task, depth + 1));
      wrapper.append(childList);
    }
    return wrapper;
  };

  elements.explorer.append(
    groupNode("requirements", "Requirements", state.requirements.length, requirements.roots, requirements.children, requirementValues, describeRequirement, "requirement", renderRequirementBranch),
  );
}

function property(list, label, value) {
  list.append(element("div", "property-label", label), element("div", "property-value", value || "—"));
}

function detailActions(actions) {
  const bar = element("div", "detail-actions");
  for (const action of actions) {
    const control = element("button");
    control.append(icon(actionIcon(action.label)), element("span", "", action.label.replace("…", "")));
    control.type = "button";
    control.addEventListener("click", action.run);
    bar.append(control);
  }
  return bar;
}

function detailSection(title, text) {
  const section = element("section", "detail-section");
  section.append(element("h3", "", title), element("p", "detail-text", text || "—"));
  return section;
}

function detailItems(title, items) {
  const section = element("section", "detail-section");
  section.append(element("h3", "", title));
  const list = element("ul", "detail-list");
  for (const item of items) list.append(element("li", "", item));
  if (!items.length) list.append(element("li", "", "None"));
  section.append(list);
  return section;
}

function detailTitle(iconName, text) {
  const title = element("h2", "detail-title");
  title.append(icon(iconName, `item-icon ${iconName}`), element("span", "", text));
  return title;
}

function renderDetails() {
  elements.details.replaceChildren();
  elements.details.className = "details";
  if (!state.selected) {
    elements.details.className = "details empty";
    elements.details.textContent = "Select a requirement or task.";
    return;
  }
  if (state.selected.type === "requirement") {
    const summary = state.requirements.find(value => value.id === state.selected.id);
    const item = state.details.get(`requirement:${state.selected.id}`) || summary;
    if (!item) { state.selected = null; renderDetails(); return; }
    const description = describeRequirement(item);
    elements.details.append(detailTitle(item.revision.level, `${item.id}@${item.current_revision}: ${item.revision.title}`));
    const statuses = element("div", "detail-statuses");
    statuses.append(statusChip(item.lifecycle_state));
    statuses.append(statusChip(item.reconciliation_state));
    if (item.workability) statuses.append(statusChip(item.workability.disposition));
    elements.details.append(statuses);
    elements.details.append(detailActions(description.actions));
    const properties = element("div", "properties");
    property(properties, "ID", item.id);
    property(properties, "Revision", String(item.current_revision));
    property(properties, "Level", item.revision.level);
    property(properties, "Parents", item.revision.parents.map(parent => `${parent.id}@${parent.revision}`).join(", "));
    property(properties, "Depends on", (item.revision.dependencies || []).map(value => `${value.id}@${value.revision}`).join(", "));
    property(properties, "Actor", item.revision.actor_id);
    property(properties, "Created", new Date(item.revision.created_at).toLocaleString());
    elements.details.append(properties, detailSection("Statement", item.revision.statement));
    if (!item.workability) elements.details.append(detailSection("History and workability", "Loading…"));
    else {
      elements.details.append(detailItems("Workability", [`Workable: ${item.workability.workable ? "yes" : "no"}`, `Disposition: ${item.workability.disposition}`, ...item.workability.reasons]));
      elements.details.append(detailItems("Revision history", (item.revision_history || []).map(value => `Revision ${value.revision}: ${value.title} — ${new Date(value.created_at).toLocaleString()} — ${value.actor_id}`)));
      elements.details.append(detailItems("State history", (item.state_history || []).map(value => `${value.field}: ${value.from || "initial"} → ${value.to} — ${new Date(value.occurred_at).toLocaleString()} — ${value.actor_id}`)));
      elements.details.append(detailItems("Reviews", (item.reviews || []).flatMap(value => [
        `${value.id}: ${value.verdict} — ${value.commit}${value.task_id ? ` — ${value.task_id}` : ""}`,
        ...(value.findings || []).map(finding => `Finding: ${finding.message}${finding.path ? ` — ${finding.path}${finding.line ? `:${finding.line}` : ""}` : ""}`),
      ])));
      elements.details.append(detailItems("Open causes", (item.open_causes || []).map(value => `${value.requirement.id}@${value.requirement.revision}`)));
    }
    return;
  }
  const summary = state.tasks.find(value => value.id === state.selected.id);
  const item = state.details.get(`task:${state.selected.id}`) || summary;
  if (!item) { state.selected = null; renderDetails(); return; }
  const description = describeTask(item);
  const lease = state.leases.find(value => value.task_id === item.id);
  elements.details.append(detailTitle("task", `${item.id}: ${item.title}`));
  const statuses = element("div", "detail-statuses");
  statuses.append(statusChip(item.state));
  if (item.workability) statuses.append(statusChip(item.workability.disposition));
  if (lease) statuses.append(statusChip("in_progress", `leased · ${lease.agent_id}`));
  elements.details.append(statuses);
  elements.details.append(detailActions(description.actions));
  const properties = element("div", "properties");
  property(properties, "ID", item.id);
  property(properties, "Version", String(item.version));
  property(properties, "Priority", String(item.priority));
  property(properties, "Depends on", (item.depends_on || []).join(", "));
  property(properties, "Requirements", (item.requirements || []).map(link => `${link.requirement} (${link.purpose})`).join(", "));
  property(properties, "Commit", item.completed_commit);
  property(properties, "Pull requests", (item.pull_requests || []).map(value => value.url).join(", "));
  property(properties, "Lease", lease?.lease_id);
  property(properties, "Agent", lease?.agent_id);
  property(properties, "Expires", lease ? new Date(lease.expires_at).toLocaleString() : "");
  elements.details.append(properties, detailSection("Description", item.description));
  if (!item.workability) elements.details.append(detailSection("History and workability", "Loading…"));
  else {
    elements.details.append(detailItems("Workability", [`Workable: ${item.workability.workable ? "yes" : "no"}`, `Disposition: ${item.workability.disposition}`, ...item.workability.reasons]));
    elements.details.append(detailItems("State history", (item.state_history || []).map(value => `${value.from || "initial"} → ${value.to} — ${new Date(value.occurred_at).toLocaleString()} — ${value.actor_id}`)));
  }
}

function leaseCell(className, text) { return element("div", `lease-cell ${className}`, text); }

function remainingTime(value) {
  const milliseconds = new Date(value).getTime() - Date.now();
  const minutes = Math.max(0, Math.ceil(milliseconds / 60000));
  if (minutes < 60) return `${minutes}m left`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m left`;
}

function selectTaskInTree(taskID) {
  const task = state.tasks.find(item => item.id === taskID);
  if (!task) return;
  const requirementsByID = new Map(state.requirements.map(item => [item.id, item]));
  const expanded = new Set();
  const expand = requirementID => {
    if (expanded.has(requirementID)) return;
    expanded.add(requirementID);
    state.collapsed.delete(`requirement:${requirementID}`);
    const requirement = requirementsByID.get(requirementID);
    for (const parent of requirement?.revision.parents || []) expand(parent.id);
  };
  for (const link of task.requirements || []) expand(link.requirement.split("@", 1)[0]);
  state.collapsedGroups.delete("requirements");
  state.selected = { type: "task", id: taskID };
  render();
  loadDetail("task", taskID);
  window.requestAnimationFrame(() => elements.explorer.querySelector(".tree-row.selected")?.scrollIntoView({ block: "nearest" }));
}

function renderLeases() {
  elements.leases.replaceChildren();
  elements.leases.className = "lease-list";
  elements.leaseCount.textContent = state.leases.length;
  elements.leasePanel.classList.toggle("empty", state.leases.length === 0);
  const header = element("div", "lease-header");
  for (const label of ["Lease", "Task", "Agent", "Fence", "Claimed", "Expires", "Actions"]) header.append(leaseCell("", label));
  elements.leases.append(header);
  if (!state.leases.length) {
    elements.leases.append(element("div", "empty", "No active leases."));
    return;
  }
  for (const lease of state.leases) {
    const expiring = new Date(lease.expires_at).getTime() - Date.now() < 5 * 60000;
    const row = element("div", `lease-row${expiring ? " expiring" : ""}`);
    row.classList.add("clickable");
    row.tabIndex = 0;
    row.setAttribute("role", "button");
    row.setAttribute("aria-label", `Show task ${lease.task_id}`);
    row.addEventListener("click", event => { if (!event.target.closest("button")) selectTaskInTree(lease.task_id); });
    row.addEventListener("keydown", event => {
      if (event.key === "Enter" || event.key === " ") { event.preventDefault(); selectTaskInTree(lease.task_id); }
    });
    const leaseID = leaseCell("lease-id");
    leaseID.append(icon("lease", "status-icon"), element("span", "", lease.lease_id));
    const expiry = leaseCell(`lease-expiry${expiring ? " expiring" : ""}`);
    expiry.title = new Date(lease.expires_at).toLocaleString();
    expiry.append(icon(expiring ? "warning" : "progress", "status-icon"), element("span", "", remainingTime(lease.expires_at)));
    row.append(
      leaseID,
      leaseCell("lease-task", lease.task_id),
      leaseCell("", lease.agent_id),
      leaseCell("", String(lease.fence)),
      leaseCell("", new Date(lease.claimed_at).toLocaleString()),
      expiry,
    );
    const actions = element("div", "lease-actions");
    const heartbeat = element("button", "", "Heartbeat");
    heartbeat.prepend(icon("refresh"));
    heartbeat.type = "button";
    heartbeat.addEventListener("click", () => {
      const ttl = window.prompt("Extend lease by:", "30m");
      if (ttl) mutate(`/v1/leases/${encodeURIComponent(lease.lease_id)}/heartbeat`, { fence: lease.fence, ttl });
    });
    const release = element("button", "", "Release");
    release.prepend(icon("retired"));
    release.type = "button";
    release.addEventListener("click", () => {
      if (window.confirm(`Release lease ${lease.lease_id}?`)) mutate(`/v1/leases/${encodeURIComponent(lease.lease_id)}/release`, { fence: lease.fence });
    });
    actions.append(heartbeat, release);
    row.append(actions);
    elements.leases.append(row);
  }
}

function renderEvents() {
  elements.events.replaceChildren();
  elements.eventCount.textContent = state.events.length;
  const header = element("div", "event-header");
  for (const label of ["Time", "Event", "Item", "Correlation"]) header.append(element("div", "event-cell", label));
  elements.events.append(header);
  if (!state.events.length) {
    elements.events.append(element("div", "empty", "Waiting for change events."));
    return;
  }
  for (const event of state.events) {
    const fields = event.fields || {};
    const entity = fields.requirement_id || fields.task_id || fields.lease_id || "—";
    const correlation = event.correlation_id || "—";
    const row = element("div", "event-row");
    row.title = `${event.name} — ${entity} — ${correlation}`;
    row.append(
      element("div", "event-cell", new Date(event.received_at).toLocaleTimeString()),
      element("div", "event-cell event-kind", event.name),
      element("div", "event-cell event-entity", entity),
      element("div", "event-cell event-correlation", correlation.slice(0, 8)),
    );
    elements.events.append(row);
  }
}

function renderSummary() {
  const values = [
    [state.requirements.length, "requirements", "requirement", "requirements"],
    [state.requirements.filter(item => item.reconciliation_state === "satisfied").length, "satisfied", "check", "satisfied"],
    [state.requirements.filter(item => item.lifecycle_state === "retired").length, "retired", "retired", "retired"],
    [state.requirements.filter(item => item.reconciliation_state === "needs_reconciliation").length, "need reconciliation", "warning", "needs_reconciliation"],
    [state.tasks.filter(item => item.state === "open").length, "open tasks", "pending", "open_tasks"],
    [state.tasks.filter(item => item.state === "complete").length, "complete tasks", "check", "complete_tasks"],
    [state.leases.length, "active leases", "lease", "active_leases"],
  ];
  elements.summary.replaceChildren(...values.map(([value, label, iconName, filter]) => {
    const item = element("button", `summary-item summary-filter${state.statusFilter === filter ? " selected" : ""}`);
    item.type = "button";
    item.setAttribute("aria-pressed", String(state.statusFilter === filter));
    item.title = `Filter by ${label}`;
    item.append(icon(iconName, "status-icon"), element("strong", "", String(value)), ` ${label}`);
    item.addEventListener("click", () => {
      state.statusFilter = state.statusFilter === filter ? "" : filter;
      renderExplorer();
      renderSummary();
    });
    return item;
  }));
}

function render() { renderExplorer(); renderDetails(); renderLeases(); renderEvents(); renderSummary(); }

function connect() {
  const events = new EventSource("/v1/events");
  events.addEventListener("ready", () => {
    elements.connectionDot.className = "connection-dot online";
    elements.connectionText.textContent = "Live";
  });
  events.addEventListener("change", event => {
    try {
      const change = JSON.parse(event.data);
      change.received_at = new Date().toISOString();
      state.events.unshift(change);
      state.events = state.events.slice(0, 100);
      renderEvents();
    } catch (_) {
      // A malformed notification can still trigger a state refresh.
    }
    scheduleRefresh();
  });
  events.onerror = () => {
    elements.connectionDot.className = "connection-dot offline";
    elements.connectionText.textContent = "Reconnecting";
  };
}

function dragSplitter(splitter, move) {
  splitter.addEventListener("pointerdown", event => {
    event.preventDefault();
    splitter.setPointerCapture(event.pointerId);
    const onMove = pointerEvent => move(pointerEvent);
    const onUp = pointerEvent => {
      splitter.releasePointerCapture(pointerEvent.pointerId);
      splitter.removeEventListener("pointermove", onMove);
    };
    splitter.addEventListener("pointermove", onMove);
    splitter.addEventListener("pointerup", onUp, { once: true });
  });
}

dragSplitter(elements.verticalSplitter, event => {
  const bounds = elements.content.getBoundingClientRect();
  const width = Math.max(420, Math.min(event.clientX - bounds.left, bounds.width - 305));
  elements.content.style.setProperty("--explorer-width", `${width}px`);
});

dragSplitter(elements.horizontalSplitter, event => {
  const summaryHeight = elements.summary.getBoundingClientRect().height;
  const height = Math.max(70, Math.min(window.innerHeight - event.clientY - summaryHeight, window.innerHeight * .55));
  document.documentElement.style.setProperty("--lease-height", `${height}px`);
});

elements.verticalSplitter.addEventListener("keydown", event => {
  if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
  event.preventDefault();
  const bounds = elements.content.getBoundingClientRect();
  const current = elements.verticalSplitter.getBoundingClientRect().left - bounds.left;
  const width = Math.max(420, Math.min(current + (event.key === "ArrowRight" ? 24 : -24), bounds.width - 305));
  elements.content.style.setProperty("--explorer-width", `${width}px`);
});

elements.horizontalSplitter.addEventListener("keydown", event => {
  if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
  event.preventDefault();
  const current = elements.activityPanels.getBoundingClientRect().height;
  const height = Math.max(70, Math.min(current + (event.key === "ArrowUp" ? 24 : -24), window.innerHeight * .55));
  document.documentElement.style.setProperty("--lease-height", `${height}px`);
});

elements.refresh.addEventListener("click", refresh);
elements.filter.addEventListener("input", event => { state.filter = event.target.value.trim().toLowerCase(); render(); });
elements.expandAll.addEventListener("click", () => { state.collapsed.clear(); state.collapsedGroups.clear(); render(); });
elements.collapseAll.addEventListener("click", () => {
  state.collapsedGroups = new Set(["requirements"]);
  state.collapsed = new Set(state.requirements.map(item => `requirement:${item.id}`));
  render();
});
refresh(); connect();
