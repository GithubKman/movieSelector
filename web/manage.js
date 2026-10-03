// Management mode: work the queue, mark progress, export, trigger digests.

const $ = (id) => document.getElementById(id);
const state = { token: "", status: "", type: "" };

try { state.token = localStorage.getItem("adminToken") || ""; } catch {}

const adminApi = (path, opts = {}) => api(path, { ...opts, token: state.token }).catch((e) => {
  if (e.status === 401) logout();
  throw e;
});

async function unlock(token) {
  await api("/api/auth", { token });
  state.token = token;
  try { localStorage.setItem("adminToken", token); } catch {}
  $("login").classList.add("hidden");
  $("app").classList.remove("hidden");
  $("logout").classList.remove("hidden");
  load();
}

function logout() {
  state.token = "";
  try { localStorage.removeItem("adminToken"); } catch {}
  $("app").classList.add("hidden");
  $("logout").classList.add("hidden");
  $("login").classList.remove("hidden");
}

$("login").addEventListener("submit", async (e) => {
  e.preventDefault();
  $("login-err").textContent = "";
  try { await unlock($("token").value.trim()); }
  catch (err) { $("login-err").textContent = err.status === 401 ? "Wrong token." : err.message; }
});
$("logout").addEventListener("click", logout);

document.querySelectorAll("#type button").forEach((btn) =>
  btn.addEventListener("click", () => {
    document.querySelectorAll("#type button").forEach((b) => b.classList.toggle("on", b === btn));
    state.type = btn.dataset.type;
    load();
  }));

function queueQuery(format) {
  const p = new URLSearchParams({ status: state.status || "queued,in_progress" });
  if (state.type) p.set("type", state.type);
  if (format) p.set("format", format);
  return "/api/queue?" + p;
}

async function load() {
  let res;
  try { res = await adminApi(queueQuery()); }
  catch (e) { if (e.status !== 401) toast("Could not load queue: " + e.message, true); return; }

  statusChips($("filters"), res.counts, state.status, (s) => { state.status = s; load(); });
  $("exp-txt").href = queueQuery("txt");
  $("exp-csv").href = queueQuery("csv");
  $("exp-json").href = queueQuery("json");
  $("list").replaceChildren(...res.items.map(row));
  $("empty").classList.toggle("hidden", res.items.length > 0);
}

async function update(r, changes) {
  try {
    await adminApi(`/api/requests/${r.id}`, { method: "PATCH", body: changes });
    load();
  } catch (e) { toast("Update failed: " + e.message, true); }
}

function row(r) {
  const action = (label, status, cls = "") =>
    r.status === status ? null : h("button", { class: "btn small " + cls, onclick: () => update(r, { status }) }, label);

  const source = h("select", { title: "Source", onchange: (e) => update(r, { source: e.target.value }) },
    ...[["", "Source…"], ["torrent", "Torrent"], ["bluray", "Blu-ray rip"], ["other", "Other"]].map(([v, l]) =>
      h("option", { value: v, selected: r.source === v }, l)));
  const note = h("input", { value: r.note, placeholder: "Note", onchange: (e) => update(r, { note: e.target.value }) });

  return h("div", { class: "row" },
    poster(r.poster_url),
    h("div", { class: "info" },
      h("div", { class: "name" }, r.display_name, typeBadge(r.media_type), statusBadge(r.status)),
      h("div", { class: "folder" },
        r.folder_name, " ",
        h("button", { class: "btn small", title: "Copy folder name", onclick: () => copy(r.folder_name) }, "Copy")),
      h("div", { class: "details" },
        h("span", {}, `#${r.id}`),
        h("span", {}, "by " + (r.requested_by || "someone") + (r.request_count > 1 ? ` ×${r.request_count}` : "")),
        h("span", { title: r.created_at }, ago(r.created_at)),
        r.assignee && h("span", {}, "assignee: " + r.assignee),
        r.imdb_id && h("a", { href: `https://www.imdb.com/title/${r.imdb_id}/`, target: "_blank", rel: "noopener" }, "IMDb"),
        h("a", { href: `https://www.themoviedb.org/${r.media_type}/${r.tmdb_id}`, target: "_blank", rel: "noopener" }, "TMDB")),
      h("div", { class: "edit" }, source, note)),
    h("div", { class: "actions" },
      h("div", { class: "btns" },
        action("Start", "in_progress"),
        action("Complete", "completed", "primary"),
        r.status !== "queued" && action("Requeue", "queued"),
        r.status !== "completed" && action("Failed", "failed", "danger")),
      h("button", { class: "btn small danger", onclick: () => remove(r) }, "Delete")));
}

async function remove(r) {
  if (!confirm(`Delete "${r.display_name}" from the queue?`)) return;
  try { await adminApi(`/api/requests/${r.id}`, { method: "DELETE" }); load(); }
  catch (e) { toast("Delete failed: " + e.message, true); }
}

async function copy(text) {
  try { await navigator.clipboard.writeText(text); toast("Copied: " + text); }
  catch { prompt("Copy:", text); }
}

function showDialog(title, body) {
  $("dialog-title").textContent = title;
  $("dialog-body").textContent = body;
  $("dialog").showModal();
}

$("digest-preview").addEventListener("click", async () => {
  try {
    const d = await adminApi("/api/digest/preview");
    if (!d.pending) showDialog("Digest preview", "No new requests since the last digest.");
    else showDialog(d.subject, d.text);
  } catch (e) { toast(e.message, true); }
});

$("digest-send").addEventListener("click", async () => {
  try {
    const d = await adminApi("/api/digest/send", { method: "POST" });
    if (!d.sent) toast("No new requests to send.");
    else toast(d.emailed ? `Digest emailed (${d.sent} titles).` : `Email not configured — digest of ${d.sent} titles logged to server output.`);
  } catch (e) { toast("Digest failed: " + e.message, true); }
});

if (state.token) unlock(state.token).catch(logout);
else logout();
