"use strict";

const state = {
  requirements: [], tasks: [], leases: [], filter: "", refreshTimer: 0, selected: null,
  collapsed: new Set(), collapsedGroups: new Set(),
};
const elements = {
  explorer: document.querySelector("#explorer"), details: document.querySelector("#details"), leases: document.querySelector("#leases"),
  leaseCount: document.querySelector("#lease-count"), summary: document.querySelector("#summary"),
  error: document.querySelector("#error"), updated: document.querySelector("#updated"),
  refresh: document.querySelector("#refresh"), filter: document.querySelector("#filter"),
  expandAll: document.querySelector("#expand-all"), collapseAll: document.querySelector("#collapse-all"),
  connectionDot: document.querySelector("#connection-dot"), connectionText: document.querySelector("#connection-text"),
};
let describeRequirement;
let describeTask;
let openMenu;

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

function actionsMenu(actions) {
  const menu = element("span", "actions");
  const toggle = element("button", "actions-toggle", "Actions ▾");
  toggle.type = "button";
  toggle.title = "Item actions";
  toggle.addEventListener("click", event => {
    event.stopPropagation();
    showActions(toggle, actions);
  });
  menu.append(toggle);
  return menu;
}

function closeActions() {
  if (openMenu) openMenu.remove();
  openMenu = null;
}

function showActions(anchor, actions) {
  closeActions();
  const list = element("div", "actions-menu");
  for (const action of actions) {
    const control = element("button", "action-item", action.label);
    control.type = "button";
    control.addEventListener("click", event => { event.stopPropagation(); closeActions(); action.run(); });
    list.append(control);
  }
  document.body.append(list);
  const bounds = anchor.getBoundingClientRect();
  const left = Math.max(3, Math.min(bounds.left, window.innerWidth - list.offsetWidth - 3));
  const below = bounds.bottom + list.offsetHeight <= window.innerHeight;
  list.style.left = `${left}px`;
  list.style.top = `${below ? bounds.bottom : Math.max(3, bounds.top - list.offsetHeight)}px`;
  openMenu = list;
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

function nodeRow({ id, title, text, meta, stateValue, stateLabel, type, hasChildren, collapsed, toggle, group, actions, selected, select }) {
  const row = element("div", `tree-row${group ? " group" : ""}${selected ? " selected" : ""}`);
  row.title = [id, title, text, meta, stateValue].filter(Boolean).join(" — ");
  row.append(button(hasChildren, collapsed, toggle), element("span", `icon ${group ? "folder" : type}`));
  if (id) row.append(element("span", "node-id", id));
  row.append(element("span", "node-title", title));
  if (text) row.append(element("span", "node-text", `— ${text}`));
  if (meta) row.append(element("span", "node-meta", meta));
  if (stateValue) row.append(element("span", `node-state ${stateValue}`, stateLabel || stateValue.replaceAll("_", " ")));
  if (actions?.length) row.append(actionsMenu(actions));
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

function renderBranch(item, children, values, describe, type) {
  if (!hasMatch(item, children, values)) return null;
  const descendants = children.get(item.id) || [];
  const key = `${type}:${item.id}`;
  const collapsed = state.collapsed.has(key) && !state.filter;
  const wrapper = element("div", "tree-node");
  const details = describe(item);
  wrapper.append(nodeRow({
    ...details, type, hasChildren: descendants.length > 0, collapsed,
    selected: state.selected?.type === type && state.selected.id === item.id,
    select: () => { state.selected = { type, id: item.id }; renderDetails(); },
    toggle: () => { collapsed ? state.collapsed.delete(key) : state.collapsed.add(key); render(); },
  }));
  if (descendants.length && !collapsed) {
    const childList = element("div", "tree-children");
    for (const child of descendants) {
      const childNode = renderBranch(child, children, values, describe, type);
      if (childNode) childList.append(childNode);
    }
    wrapper.append(childList);
  }
  return wrapper;
}

function groupNode(key, title, count, roots, children, values, describe, type) {
  const collapsed = state.collapsedGroups.has(key) && !state.filter;
  const wrapper = element("div", "tree-node");
  const complete = type === "requirement"
    ? state.requirements.filter(item => item.reconciliation_state === "implemented").length
    : state.tasks.filter(item => item.state === "complete").length;
  wrapper.append(nodeRow({
    title: `${title} (${count})`, group: true, hasChildren: roots.length > 0, collapsed,
    stateValue: complete === count && count > 0 ? (type === "requirement" ? "implemented" : "complete") : "in_progress",
    stateLabel: `${complete}/${count} ${type === "requirement" ? "implemented" : "complete"}`,
    toggle: () => { collapsed ? state.collapsedGroups.delete(key) : state.collapsedGroups.add(key); render(); },
  }));
  if (!collapsed) {
    const childList = element("div", "tree-children");
    for (const root of roots) {
      const child = renderBranch(root, children, values, describe, type);
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
  const requirementOrder = (left, right) => left.id.localeCompare(right.id);
  const requirements = graph(state.requirements, item => item.revision.parents.map(parent => parent.id), requirementOrder);
  const requirementValues = item => [item.id, item.revision.title, item.revision.statement, item.revision.level, item.reconciliation_state];
  describeRequirement = item => ({
    id: `${item.id}@${item.current_revision}`, title: item.revision.title, stateValue: item.reconciliation_state,
    actions: [
      { label: "Copy ID", run: () => copy(`${item.id}@${item.current_revision}`) },
      { label: "Confirm implementation…", run: () => {
        const commit = window.prompt(`Git commit for ${item.id}@${item.current_revision}:`);
        if (!commit) return;
        const result = window.prompt("Result: code_changed or existing_code_confirmed", "code_changed");
        if (!result) return;
        mutate(`/v1/requirements/${encodeURIComponent(item.id)}/confirm`, { commit, result });
      } },
    ],
  });

  const taskOrder = (left, right) => right.priority - left.priority || left.id.localeCompare(right.id);
  const tasks = graph(state.tasks, item => item.depends_on || [], taskOrder);
  const taskValues = item => [item.id, item.title, item.description, item.state, ...(item.requirements || []).map(link => link.requirement)];
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
      stateValue: item.state, actions,
    };
  };

  elements.explorer.append(
    groupNode("requirements", "Requirements", state.requirements.length, requirements.roots, requirements.children, requirementValues, describeRequirement, "requirement"),
    groupNode("tasks", "Tasks", state.tasks.length, tasks.roots, tasks.children, taskValues, describeTask, "task"),
  );
}

function property(list, label, value) {
  list.append(element("div", "property-label", label), element("div", "property-value", value || "—"));
}

function detailActions(actions) {
  const bar = element("div", "detail-actions");
  for (const action of actions) {
    const control = element("button", "", action.label.replace("…", ""));
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

function renderDetails() {
  elements.details.replaceChildren();
  elements.details.className = "details";
  if (!state.selected) {
    elements.details.className = "details empty";
    elements.details.textContent = "Select a requirement or task.";
    return;
  }
  if (state.selected.type === "requirement") {
    const item = state.requirements.find(value => value.id === state.selected.id);
    if (!item) { state.selected = null; renderDetails(); return; }
    const description = describeRequirement(item);
    elements.details.append(element("h2", "detail-title", `${item.id}@${item.current_revision}: ${item.revision.title}`));
    elements.details.append(element("div", `detail-status node-state ${item.reconciliation_state}`, item.reconciliation_state.replaceAll("_", " ")));
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
    return;
  }
  const item = state.tasks.find(value => value.id === state.selected.id);
  if (!item) { state.selected = null; renderDetails(); return; }
  const description = describeTask(item);
  const lease = state.leases.find(value => value.task_id === item.id);
  elements.details.append(element("h2", "detail-title", `${item.id}: ${item.title}`));
  elements.details.append(element("div", `detail-status node-state ${item.state}`, item.state.replaceAll("_", " ")));
  elements.details.append(detailActions(description.actions));
  const properties = element("div", "properties");
  property(properties, "ID", item.id);
  property(properties, "Version", String(item.version));
  property(properties, "Priority", String(item.priority));
  property(properties, "Depends on", (item.depends_on || []).join(", "));
  property(properties, "Requirements", (item.requirements || []).map(link => `${link.requirement} (${link.purpose})`).join(", "));
  property(properties, "Commit", item.completed_commit);
  property(properties, "Lease", lease?.lease_id);
  property(properties, "Agent", lease?.agent_id);
  property(properties, "Expires", lease ? new Date(lease.expires_at).toLocaleString() : "");
  elements.details.append(properties, detailSection("Description", item.description));
}

function leaseCell(className, text) { return element("div", `lease-cell ${className}`, text); }

function renderLeases() {
  elements.leases.replaceChildren();
  elements.leases.className = "lease-list";
  elements.leaseCount.textContent = state.leases.length;
  const header = element("div", "lease-header");
  for (const label of ["Lease", "Task", "Agent", "Fence", "Claimed", "Expires", "Actions"]) header.append(leaseCell("", label));
  elements.leases.append(header);
  if (!state.leases.length) {
    elements.leases.append(element("div", "empty", "No active leases."));
    return;
  }
  for (const lease of state.leases) {
    const row = element("div", "lease-row");
    row.append(
      leaseCell("lease-id", lease.lease_id),
      leaseCell("lease-task", lease.task_id),
      leaseCell("", lease.agent_id),
      leaseCell("", String(lease.fence)),
      leaseCell("", new Date(lease.claimed_at).toLocaleString()),
      leaseCell("", new Date(lease.expires_at).toLocaleString()),
    );
    const actions = element("div", "lease-actions");
    const heartbeat = element("button", "", "Heartbeat");
    heartbeat.type = "button";
    heartbeat.addEventListener("click", () => {
      const ttl = window.prompt("Extend lease by:", "30m");
      if (ttl) mutate(`/v1/leases/${encodeURIComponent(lease.lease_id)}/heartbeat`, { fence: lease.fence, ttl });
    });
    const release = element("button", "", "Release");
    release.type = "button";
    release.addEventListener("click", () => {
      if (window.confirm(`Release lease ${lease.lease_id}?`)) mutate(`/v1/leases/${encodeURIComponent(lease.lease_id)}/release`, { fence: lease.fence });
    });
    actions.append(heartbeat, release);
    row.append(actions);
    elements.leases.append(row);
  }
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

function render() { renderExplorer(); renderDetails(); renderLeases(); renderSummary(); }

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
elements.expandAll.addEventListener("click", () => { state.collapsed.clear(); state.collapsedGroups.clear(); render(); });
elements.collapseAll.addEventListener("click", () => {
  state.collapsedGroups = new Set(["requirements", "tasks"]);
  state.collapsed = new Set([...state.requirements.map(item => `requirement:${item.id}`), ...state.tasks.map(item => `task:${item.id}`)]);
  render();
});
document.addEventListener("click", closeActions);
window.addEventListener("resize", closeActions);
elements.explorer.addEventListener("scroll", closeActions);
refresh(); connect();
