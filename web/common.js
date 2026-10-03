// Shared helpers for the browse and manage pages.

const STATUS_LABELS = {
  queued: "Queued",
  in_progress: "In progress",
  completed: "Available",
  failed: "Failed",
};

// h("div", {class: "x", onclick: fn}, child, "text", ...) — builds DOM without innerHTML.
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "class") el.className = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

async function api(path, { method = "GET", body, token } = {}) {
  const headers = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (token) headers["Authorization"] = "Bearer " + token;
  const res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  if (res.status === 204) return null;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(data.error || res.statusText);
    err.status = res.status;
    throw err;
  }
  return data;
}

let toastTimer;
function toast(msg, isError = false) {
  document.querySelector(".toast")?.remove();
  const el = h("div", { class: "toast" + (isError ? " error" : "") }, msg);
  document.body.append(el);
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.remove(), 4000);
}

function statusBadge(status) {
  return h("span", { class: "badge status " + status }, STATUS_LABELS[status] || status);
}

function typeBadge(type) {
  return h("span", { class: "badge" }, type === "tv" ? "Show" : "Movie");
}

function poster(url, cls) {
  return url ? h("img", { src: url, alt: "", loading: "lazy", class: cls }) : h("div", { class: "ph" });
}

function ago(iso) {
  const s = (Date.now() - new Date(iso)) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return Math.floor(s / 60) + "m ago";
  if (s < 86400) return Math.floor(s / 3600) + "h ago";
  return Math.floor(s / 86400) + "d ago";
}

// Status filter chips with counts; calls onPick(status) where "" means active queue.
function statusChips(container, counts, current, onPick, { includeAll = true } = {}) {
  container.replaceChildren();
  const opts = [["", "Open", (counts.queued || 0) + (counts.in_progress || 0)]];
  for (const s of ["queued", "in_progress", "completed", "failed"]) opts.push([s, STATUS_LABELS[s], counts[s] || 0]);
  if (includeAll) opts.push(["all", "All", Object.values(counts).reduce((a, b) => a + b, 0)]);
  for (const [value, label, n] of opts) {
    container.append(h("button", { class: "chip" + (value === current ? " on" : ""), onclick: () => onPick(value) }, label, h("b", {}, n)));
  }
}
