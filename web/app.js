// Browse page: search, multi-select, request, and a read-only queue view.

const state = {
  q: "",
  type: "",
  page: 1,
  totalPages: 1,
  selected: new Map(), // "movie:603" -> title
  queueStatus: "",
  shown: new Map(), // titles currently rendered, by key
};

const $ = (id) => document.getElementById(id);
const key = (t) => `${t.media_type}:${t.tmdb_id}`;

// ---- Views ----
document.querySelectorAll("nav [data-view]").forEach((btn) =>
  btn.addEventListener("click", () => showView(btn.dataset.view)));

function showView(view) {
  document.querySelectorAll("nav [data-view]").forEach((b) => b.classList.toggle("active", b.dataset.view === view));
  $("browse").classList.toggle("hidden", view !== "browse");
  $("queue").classList.toggle("hidden", view !== "queue");
  if (view === "queue") loadQueue();
  if (view === "browse") search(false);
  location.hash = view === "queue" ? "queue" : "";
}

// ---- Search ----
let debounce;
$("q").addEventListener("input", () => {
  clearTimeout(debounce);
  debounce = setTimeout(() => { state.q = $("q").value.trim(); search(false); }, 300);
});
$("search-form").addEventListener("submit", (e) => {
  e.preventDefault();
  state.q = $("q").value.trim();
  search(false);
});
document.querySelectorAll("#type button").forEach((btn) =>
  btn.addEventListener("click", () => {
    document.querySelectorAll("#type button").forEach((b) => b.classList.toggle("on", b === btn));
    state.type = btn.dataset.type;
    search(false);
  }));
$("more").addEventListener("click", () => search(true));

let searchSeq = 0;
async function search(append) {
  const seq = ++searchSeq;
  state.page = append ? state.page + 1 : 1;
  const params = new URLSearchParams({ q: state.q, page: state.page });
  if (state.type) params.set("type", state.type);
  let res;
  try {
    res = await api("/api/search?" + params);
  } catch (e) {
    toast("Search failed: " + e.message, true);
    return;
  }
  if (seq !== searchSeq) return; // a newer search started
  state.totalPages = res.total_pages;
  $("results-title").textContent = state.q ? `Results for “${state.q}”` : "Trending";
  if (!append) { $("results").replaceChildren(); state.shown.clear(); }
  res.results.forEach((t) => $("results").append(card(t)));
  $("no-results").classList.toggle("hidden", $("results").children.length > 0);
  $("more").classList.toggle("hidden", state.page >= state.totalPages);
}

function card(t) {
  state.shown.set(key(t), t);
  const requested = !!t.status;
  const el = h("div", { class: "card" + (requested ? " requested" : "") + (state.selected.has(key(t)) ? " selected" : ""), "data-key": key(t) },
    h("div", { class: "poster" },
      t.poster_url ? h("img", { src: t.poster_url, alt: t.title, loading: "lazy" }) : h("div", { class: "noimg" }, t.title),
      t.overview && h("div", { class: "overview" }, h("p", {}, t.overview)),
      requested ? statusBadge(t.status) : null,
      h("div", { class: "check" }, "✓")),
    h("div", { class: "meta" },
      h("div", { class: "title" }, t.title),
      h("div", { class: "sub" }, [t.year || "—", t.media_type === "tv" ? "Show" : "Movie", t.rating ? "★ " + t.rating.toFixed(1) : null].filter(Boolean).join(" · "))));
  if (!requested) el.addEventListener("click", () => toggle(t, el));
  return el;
}

// ---- Selection ----
function toggle(t, el) {
  const k = key(t);
  if (state.selected.has(k)) state.selected.delete(k);
  else state.selected.set(k, t);
  el.classList.toggle("selected", state.selected.has(k));
  renderTray();
}

function renderTray() {
  const n = state.selected.size;
  $("tray").classList.toggle("hidden", n === 0);
  $("tray-count").textContent = `${n} selected`;
  $("tray-submit").textContent = `Request ${n}`;
  $("tray-thumbs").replaceChildren(...[...state.selected.values()].slice(0, 14).map((t) =>
    t.poster_url ? h("img", { src: t.poster_url, alt: "", title: t.title }) : h("span", {})));
}

$("tray-clear").addEventListener("click", () => {
  state.selected.clear();
  document.querySelectorAll(".card.selected").forEach((c) => c.classList.remove("selected"));
  renderTray();
});

try { $("requester").value = localStorage.getItem("requester") || ""; } catch {}

$("tray-submit").addEventListener("click", async () => {
  const name = $("requester").value.trim();
  try { localStorage.setItem("requester", name); } catch {}
  const items = [...state.selected.values()].map((t) => ({ media_type: t.media_type, tmdb_id: t.tmdb_id }));
  $("tray-submit").disabled = true;
  try {
    const res = await api("/api/requests", { method: "POST", body: { requested_by: name, items } });
    const parts = [];
    if (res.added.length) parts.push(`${res.added.length} added to the queue`);
    if (res.existing.length) parts.push(`${res.existing.length} already requested`);
    if (res.errors.length) parts.push(`${res.errors.length} failed`);
    toast(parts.join(", ") + ".", res.errors.length > 0 && !res.added.length);
    for (const r of [...res.added, ...res.existing]) {
      const k = key(r);
      state.selected.delete(k);
      const t = state.shown.get(k);
      if (t) document.querySelector(`.card[data-key="${k}"]`)?.replaceWith(card({ ...t, status: r.status }));
    }
    renderTray();
  } catch (e) {
    toast("Request failed: " + e.message, true);
  } finally {
    $("tray-submit").disabled = false;
  }
});

// ---- Queue (read-only) ----
async function loadQueue() {
  const status = state.queueStatus || "queued,in_progress";
  let res;
  try {
    res = await api("/api/queue?status=" + status);
  } catch (e) {
    toast("Could not load queue: " + e.message, true);
    return;
  }
  statusChips($("queue-filters"), res.counts, state.queueStatus, (s) => { state.queueStatus = s; loadQueue(); });
  $("queue-list").replaceChildren(...res.items.map((r) =>
    h("div", { class: "row" },
      poster(r.poster_url),
      h("div", { class: "info" },
        h("div", { class: "name" }, r.display_name, typeBadge(r.media_type)),
        h("div", { class: "details" },
          h("span", {}, "Requested by " + (r.requested_by || "someone") + (r.request_count > 1 ? ` (+${r.request_count - 1})` : "")),
          h("span", {}, ago(r.created_at)))),
      h("div", { class: "actions" }, statusBadge(r.status)))));
  $("queue-empty").classList.toggle("hidden", res.items.length > 0);
}

// ---- Init ----
api("/api/health").then((h) => $("demo-banner").classList.toggle("hidden", h.provider !== "demo")).catch(() => {});
if (location.hash === "#queue") showView("queue");
else search(false);
