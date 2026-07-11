let currentJob = null;
let currentPlan = null;
let scannedServers = [];
let selectedPlanID = "";
let selectedPlanRows = new Set();
let currentJobsPage = null;
let jobsOffset = 0;
let currentLang = localStorage.getItem("dokploy-migrator-lang") || "ru";
const LOCAL_SERVER_ID = "__dokploy_local__";
const JOBS_PAGE_LIMIT = 50;

const jobsOutput = document.querySelector("#jobs");
const planOutput = document.querySelector("#output");
const applyOutput = document.querySelector("#apply-output");
const planSummary = document.querySelector("#plan-summary");
const planStatus = document.querySelector("#plan-status");
const applyStatus = document.querySelector("#apply-status");
const applyHelp = document.querySelector("#apply-help");
const resourcesBody = document.querySelector("#resources-body");
const serversBody = document.querySelector("#servers-body");
const serversSummary = document.querySelector("#servers-summary");
const applyButton = document.querySelector("#apply-button");
const schemaHashInput = document.querySelector("#schema-hash-approval");
const confirmationInput = document.querySelector("#confirmation-text");
const sourceServerInput = document.querySelector("#source-server-id");
const targetServerInput = document.querySelector("#target-server-id");
const resourceSelectAll = document.querySelector("#resource-select-all");

const messages = {
  ru: {
    eyebrow: "Инструмент восстановления Dokploy",
    lead: "Безопасно переносит привязки проектов между удаленными серверами и основным локальным Dokploy через dry-run.",
    refresh: "Обновить",
    scopeTitle: "Текущий scope:",
    scopeText: "готов перенос метаданных в БД Dokploy. Поиск S3-бэкапов и SSH-команды восстановления есть как низкоуровневые примитивы, но полный мастер восстановления данных еще не подключен к этой странице.",
    stepScan: "Шаг 0",
    serversTitle: "Серверы Dokploy",
    serversHint: "Скан читает таблицу серверов и показывает, сколько переносимых ресурсов сейчас привязано к каждому серверу, включая основной локальный Dokploy с serverId NULL.",
    scanServers: "Сканировать серверы",
    serversEmpty: "Серверы еще не сканировались.",
    serversNotLoaded: "Нажмите “Сканировать серверы”.",
    serverColumnName: "Сервер",
    serverColumnStatus: "Статус",
    serverColumnResources: "Ресурсы",
    serverColumnLastSeen: "Последняя активность",
    serverColumnActions: "Действия",
    scanRunning: "Сканирование серверов...",
    scanFailed: "Скан серверов не удался: {error}",
    scanLoaded: "Найдено серверов: {count}. Offline/unknown показаны первыми.",
    scanNone: "Серверы не найдены.",
    localServerLabel: "Основной локальный Dokploy (serverId NULL)",
    useSource: "Источник",
    useTarget: "Цель",
    noLastSeen: "нет данных",
    status_online: "online",
    status_offline: "offline",
    status_unknown: "unknown",
    resourcesCount: "{count} ресурс(ов)",
    stepPlan: "Шаг 1",
    planTitle: "Собрать dry-run план",
    planHint: "Dry-run только читает БД и показывает точные строки, которые будут изменены при apply.",
    sourceServer: "Source server ID",
    sourcePlaceholder: "serverId или __dokploy_local__",
    sourceHelp: "Сервер или основной локальный Dokploy, к которому сейчас привязаны приложения, compose, базы и домены.",
    targetServer: "Target server ID",
    targetPlaceholder: "serverId или __dokploy_local__",
    targetHelp: "Рабочий удаленный сервер или основной локальный Dokploy с serverId NULL.",
    modeLabel: "Режим",
    modeHelp: "Это основной production-режим для мертвого сервера. Плановый перенос зарезервирован для будущей логики и пока скрыт из UI.",
    adminToken: "Admin token",
    adminHelp: "Нужен для создания dry-run и destructive apply-запросов.",
    buildDryRun: "Построить dry-run",
    stepReview: "Шаг 2",
    reviewTitle: "Проверить план",
    reviewHint: "Сверьте список ресурсов, старый serverId, новый serverId и schemaHash перед apply.",
    statusNoPlan: "Плана нет",
    statusRunning: "Выполняется",
    statusReady: "Готов",
    statusEmpty: "Пусто",
    statusFailed: "Ошибка",
    planSummaryEmpty: "Постройте dry-run, чтобы увидеть summary переноса.",
    planNoRows: "Нет строк для apply. Вероятно, ресурсы уже перенесены или source serverId/локальный источник выбран неверно.",
    planSummary: "{count} ресурс(ов) к переносу",
    source: "Источник",
    target: "Цель",
    schemaHash: "Schema hash",
    resourceTable: "Таблица",
    resourceName: "Имя",
    resourceIDColumn: "ID-колонка",
    resourceServerChange: "Смена сервера",
    selectResource: "Выбор",
    selectAllResources: "Выбрать все ресурсы",
    selectedRows: "Выбрано",
    resourcesEmpty: "Ресурсы не загружены.",
    unnamed: "без имени",
    rawDryRun: "Raw JSON dry-run",
    stepApply: "Шаг 3",
    applyTitle: "Подтвердить и применить",
    applyHint: "Apply включается только после dry-run, совпадающего schemaHash и точного текста APPLY.",
    statusWaiting: "Ожидание",
    statusLocked: "Заблокировано",
    statusSchemaMismatch: "Schema mismatch",
    statusNoRows: "Нет строк",
    statusApplying: "Применение",
    statusApplied: "Применено",
    schemaApproval: "Подтверждение schemaHash",
    schemaPlaceholder: "вставьте plan.schemaHash или оставьте пустым для MIGRATOR_SCHEMA_ALLOWLIST",
    schemaHelp: "Если поле заполнено, hash должен совпасть с dry-run и текущей схемой БД. Если пустое, сервер использует env allowlist.",
    confirmation: "Текст подтверждения",
    confirmationPlaceholder: "Введите APPLY",
    confirmationHelp: "Введите ровно <code>APPLY</code>, чтобы разблокировать запись в БД.",
    applyButton: "Применить перепривязку",
    applyHelpNoPlan: "Сначала постройте dry-run план.",
    applyHelpNoRows: "В этом dry-run нет строк для применения.",
    applyHelpNoSelectedRows: "Выберите хотя бы один ресурс из dry-run плана.",
    applyHelpSchemaMismatch: "Подтверждение schemaHash не совпадает с plan.schemaHash.",
    applyHelpLocked: "Введите APPLY, чтобы разблокировать запись метаданных.",
    applyHelpExplicit: "Готово к apply с явным подтверждением schemaHash.",
    applyHelpAllowlist: "Готово к apply через MIGRATOR_SCHEMA_ALLOWLIST на сервере.",
    applyResult: "Результат apply",
    stepAudit: "Шаг 4",
    jobsTitle: "История jobs",
    jobsHint: "История dry-run, apply и rollback jobs из локальной SQLite БД Migrator. Последние 50 записей защищены от удаления.",
    jobsEmpty: "История jobs пока пустая.",
    jobsFailed: "Историю jobs не удалось загрузить: {error}",
    jobsPageStatus: "{start}-{end} из {total}",
    jobsPrev: "Назад",
    jobsNext: "Вперед",
    deleteJob: "Удалить",
    deleteProtected: "Последние 50 защищены",
    deleteConfirm: "Удалить job {id}? Будут удалены job, events и report.",
    deleteFailed: "Job не удалось удалить: {error}",
    jobColumnID: "Job",
    jobColumnStatus: "Статус",
    jobColumnMode: "Режим",
    jobColumnCheckpoint: "Checkpoint",
    jobColumnUpdated: "Обновлено",
    jobColumnActions: "Действия"
  },
  en: {
    eyebrow: "Dokploy recovery tool",
    lead: "Safely retargets project metadata between remote servers and the main local Dokploy server through a dry-run.",
    refresh: "Refresh",
    scopeTitle: "Current scope:",
    scopeText: "Dokploy database metadata retargeting is implemented. S3 backup discovery and SSH restore commands exist as low-level primitives, but the full data restore wizard is not wired into this page yet.",
    stepScan: "Step 0",
    serversTitle: "Dokploy servers",
    serversHint: "Scan reads the server table and shows how many movable resources are currently attached to each server, including the main local Dokploy server with serverId NULL.",
    scanServers: "Scan servers",
    serversEmpty: "Servers have not been scanned yet.",
    serversNotLoaded: "Click “Scan servers”.",
    serverColumnName: "Server",
    serverColumnStatus: "Status",
    serverColumnResources: "Resources",
    serverColumnLastSeen: "Last seen",
    serverColumnActions: "Actions",
    scanRunning: "Scanning servers...",
    scanFailed: "Server scan failed: {error}",
    scanLoaded: "Servers found: {count}. Offline/unknown servers are shown first.",
    scanNone: "No servers found.",
    localServerLabel: "Main local Dokploy",
    useSource: "Source",
    useTarget: "Target",
    noLastSeen: "no data",
    status_online: "online",
    status_offline: "offline",
    status_unknown: "unknown",
    resourcesCount: "{count} resource(s)",
    stepPlan: "Step 1",
    planTitle: "Build dry-run plan",
    planHint: "Dry-run only reads the database and shows the exact rows that apply would update.",
    sourceServer: "Source server ID",
    sourcePlaceholder: "serverId or __dokploy_local__",
    sourceHelp: "The server or main local Dokploy source currently attached to applications, compose stacks, databases, and domains.",
    targetServer: "Target server ID",
    targetPlaceholder: "serverId or __dokploy_local__",
    targetHelp: "The healthy remote server or main local Dokploy server with serverId NULL that should own the selected resources.",
    modeLabel: "Mode",
    modeHelp: "This is the production mode for a dead server. Planned relocation is reserved for future behavior and is hidden from the UI for now.",
    adminToken: "Admin token",
    adminHelp: "Required for dry-run creation and destructive apply requests.",
    buildDryRun: "Build dry-run",
    stepReview: "Step 2",
    reviewTitle: "Review plan",
    reviewHint: "Check resources, old serverId, new serverId, and schemaHash before apply.",
    statusNoPlan: "No plan",
    statusRunning: "Running",
    statusReady: "Ready",
    statusEmpty: "Empty",
    statusFailed: "Failed",
    planSummaryEmpty: "Build a dry-run plan to see the migration summary.",
    planNoRows: "No rows to apply. Resources may already be moved or the source serverId/local source is wrong.",
    planSummary: "{count} resource(s) to retarget",
    source: "Source",
    target: "Target",
    schemaHash: "Schema hash",
    resourceTable: "Table",
    resourceName: "Name",
    resourceIDColumn: "ID column",
    resourceServerChange: "Server change",
    selectResource: "Select",
    selectAllResources: "Select all resources",
    selectedRows: "Selected",
    resourcesEmpty: "No resources loaded.",
    unnamed: "unnamed",
    rawDryRun: "Raw dry-run JSON",
    stepApply: "Step 3",
    applyTitle: "Approve and apply",
    applyHint: "Apply is enabled only after a dry-run, matching schemaHash, and exact APPLY confirmation.",
    statusWaiting: "Waiting",
    statusLocked: "Locked",
    statusSchemaMismatch: "Schema mismatch",
    statusNoRows: "No rows",
    statusApplying: "Applying",
    statusApplied: "Applied",
    schemaApproval: "Schema hash approval",
    schemaPlaceholder: "paste plan.schemaHash or leave empty to use MIGRATOR_SCHEMA_ALLOWLIST",
    schemaHelp: "If filled, the hash must match the dry-run and current DB schema. If empty, the server uses the env allowlist.",
    confirmation: "Confirmation text",
    confirmationPlaceholder: "Type APPLY",
    confirmationHelp: "Type exactly <code>APPLY</code> to unlock the database write.",
    applyButton: "Apply metadata retarget",
    applyHelpNoPlan: "Build a dry-run plan first.",
    applyHelpNoRows: "This dry-run has no rows to apply.",
    applyHelpNoSelectedRows: "Select at least one resource from the dry-run plan.",
    applyHelpSchemaMismatch: "Schema hash approval does not match plan.schemaHash.",
    applyHelpLocked: "Type APPLY to unlock the metadata update.",
    applyHelpExplicit: "Ready to apply with explicit schema hash approval.",
    applyHelpAllowlist: "Ready to apply by using MIGRATOR_SCHEMA_ALLOWLIST on the server.",
    applyResult: "Apply result",
    stepAudit: "Step 4",
    jobsTitle: "Job history",
    jobsHint: "Dry-run, apply, and rollback jobs from the local Migrator SQLite database. The latest 50 records are protected from deletion.",
    jobsEmpty: "Job history is empty.",
    jobsFailed: "Could not load job history: {error}",
    jobsPageStatus: "{start}-{end} of {total}",
    jobsPrev: "Previous",
    jobsNext: "Next",
    deleteJob: "Delete",
    deleteProtected: "Latest 50 protected",
    deleteConfirm: "Delete job {id}? The job, events, and report will be deleted.",
    deleteFailed: "Could not delete job: {error}",
    jobColumnID: "Job",
    jobColumnStatus: "Status",
    jobColumnMode: "Mode",
    jobColumnCheckpoint: "Checkpoint",
    jobColumnUpdated: "Updated",
    jobColumnActions: "Actions"
  }
};

function t(key, vars = {}) {
  let value = messages[currentLang]?.[key] || messages.en[key] || key;
  for (const [name, replacement] of Object.entries(vars)) {
    value = value.replaceAll(`{${name}}`, String(replacement));
  }
  return value;
}

function applyLanguage(lang) {
  currentLang = messages[lang] ? lang : "ru";
  localStorage.setItem("dokploy-migrator-lang", currentLang);
  document.documentElement.lang = currentLang;
  document.querySelectorAll("[data-i18n]").forEach((node) => {
    node.innerHTML = t(node.dataset.i18n);
  });
  document.querySelectorAll("[data-i18n-placeholder]").forEach((node) => {
    node.placeholder = t(node.dataset.i18nPlaceholder);
  });
  document.querySelector("#lang-ru").classList.toggle("active", currentLang === "ru");
  document.querySelector("#lang-en").classList.toggle("active", currentLang === "en");
  renderServers(scannedServers);
  if (currentPlan) {
    renderPlan({ job: currentJob, plan: currentPlan });
  } else {
    renderNoPlan();
  }
  if (currentJobsPage) {
    renderJobs(currentJobsPage);
  }
}

function setStatus(element, text, kind) {
  element.textContent = text;
  element.className = `status ${kind}`;
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

function serverName(server) {
  if (server?.id === LOCAL_SERVER_ID) {
    return t("localServerLabel");
  }
  return server?.name || server?.id || "";
}

function displayServerID(value) {
  if (value === LOCAL_SERVER_ID) {
    return `${t("localServerLabel")} (${LOCAL_SERVER_ID})`;
  }
  return value || "(unknown)";
}

async function requestJSON(url, options = {}) {
  const res = await fetch(url, options);
  const text = await res.text();
  let body = null;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch (err) {
      body = { error: text };
    }
  }
  if (!res.ok) {
    const message = body?.error || body?.message || text || `${res.status} ${res.statusText}`;
    throw new Error(message);
  }
  return body;
}

function adminToken() {
  return document.querySelector("#admin-token").value;
}

function renderNoPlan() {
  currentJob = null;
  currentPlan = null;
  selectedPlanID = "";
  selectedPlanRows = new Set();
  planSummary.textContent = t("planSummaryEmpty");
  planSummary.className = "summary empty";
  setStatus(planStatus, t("statusNoPlan"), "muted");
  resourcesBody.innerHTML = `<tr><td colspan="6" class="empty-cell">${escapeHTML(t("resourcesEmpty"))}</td></tr>`;
  updateApplyState();
}

function renderPlan(body) {
  currentJob = body?.job || null;
  currentPlan = body?.plan || null;
  planOutput.textContent = JSON.stringify(body, null, 2);

  if (!currentPlan) {
    renderNoPlan();
    return;
  }

  const rows = Array.isArray(currentPlan.rows) ? currentPlan.rows : [];
  syncSelectionForPlan(rows);
  const selectedCount = selectedRows().length;
  const hash = currentPlan.schemaHash || "(empty)";
  const source = displayServerID(currentPlan.sourceServerId || currentJob?.sourceServerId);
  const target = displayServerID(currentPlan.targetServerId || currentJob?.targetServerId);

  planSummary.className = rows.length ? "summary plan-summary-grid" : "summary empty";
  planSummary.innerHTML = rows.length
    ? `
      <div>
        <span class="metric">${rows.length}</span>
        <span>${escapeHTML(t("planSummary", { count: rows.length }))}</span>
      </div>
      <div><strong>${escapeHTML(t("selectedRows"))}:</strong> ${selectedCount} / ${rows.length}</div>
      <div><strong>${escapeHTML(t("source"))}:</strong> <code class="mono-break">${escapeHTML(source)}</code></div>
      <div><strong>${escapeHTML(t("target"))}:</strong> <code class="mono-break">${escapeHTML(target)}</code></div>
      <div><strong>${escapeHTML(t("schemaHash"))}:</strong> <code class="mono-break">${escapeHTML(hash)}</code></div>
    `
    : escapeHTML(t("planNoRows"));

  setStatus(planStatus, rows.length ? t("statusReady") : t("statusEmpty"), rows.length ? "ok" : "muted");

  if (!rows.length) {
    resourcesBody.innerHTML = `<tr><td colspan="6" class="empty-cell">${escapeHTML(t("planNoRows"))}</td></tr>`;
    updateApplyState();
    return;
  }

  resourcesBody.innerHTML = rows.map((row) => {
    const key = planRowKey(row);
    return `
    <tr>
      <td class="select-column">
        <input class="resource-select" type="checkbox" data-row-key="${escapeHTML(key)}" aria-label="${escapeHTML(t("selectResource"))}" ${selectedPlanRows.has(key) ? "checked" : ""}>
      </td>
      <td><code>${escapeHTML(row.table)}</code></td>
      <td>${escapeHTML(row.name || t("unnamed"))}</td>
      <td><code class="mono-break">${escapeHTML(row.id)}</code></td>
      <td><code>${escapeHTML(row.idColumn)}</code></td>
      <td><code class="mono-break">${escapeHTML(displayServerID(row.oldServerId))}</code> -> <code class="mono-break">${escapeHTML(displayServerID(row.newServerId))}</code></td>
    </tr>
  `;
  }).join("");

  updateApplyState();
}

function syncSelectionForPlan(rows) {
  const planID = currentPlan?.id || "";
  const rowKeys = rows.map(planRowKey);
  if (selectedPlanID !== planID) {
    selectedPlanID = planID;
    selectedPlanRows = new Set(rowKeys);
    return;
  }
  selectedPlanRows = new Set(rowKeys.filter((key) => selectedPlanRows.has(key)));
}

function planRowKey(row) {
  return [
    row?.table || "",
    row?.idColumn || "",
    row?.id || "",
    row?.oldServerId || "",
    row?.newServerId || ""
  ].join("\u001f");
}

function selectedRows() {
  const rows = Array.isArray(currentPlan?.rows) ? currentPlan.rows : [];
  return rows.filter((row) => selectedPlanRows.has(planRowKey(row)));
}

function selectedPlan() {
  return {
    ...currentPlan,
    rows: selectedRows()
  };
}

function syncSelectAllControl(rows) {
  if (!resourceSelectAll) {
    return;
  }
  const allRows = Array.isArray(rows) ? rows : [];
  const selectedCount = selectedRows().length;
  resourceSelectAll.disabled = allRows.length === 0;
  resourceSelectAll.checked = allRows.length > 0 && selectedCount === allRows.length;
  resourceSelectAll.indeterminate = selectedCount > 0 && selectedCount < allRows.length;
  resourceSelectAll.setAttribute("aria-label", t("selectAllResources"));
}

function renderServers(servers) {
  scannedServers = Array.isArray(servers) ? servers : [];
  const sourceOptions = document.querySelector("#source-server-options");
  const targetOptions = document.querySelector("#target-server-options");

  sourceOptions.innerHTML = scannedServers.map(serverOption).join("");
  targetOptions.innerHTML = scannedServers.map(serverOption).join("");

  if (!scannedServers.length) {
    serversSummary.textContent = t("serversEmpty");
    serversSummary.className = "summary empty";
    serversBody.innerHTML = `<tr><td colspan="5" class="empty-cell">${escapeHTML(t("serversNotLoaded"))}</td></tr>`;
    return;
  }

  const offlineCount = scannedServers.filter((server) => server.status === "offline").length;
  const unknownCount = scannedServers.filter((server) => server.status === "unknown").length;
  serversSummary.className = "summary";
  serversSummary.innerHTML = `
    <div>
      <span class="metric">${scannedServers.length}</span>
      <span>${escapeHTML(t("scanLoaded", { count: scannedServers.length }))}</span>
    </div>
    <div><strong>offline:</strong> ${offlineCount}</div>
    <div><strong>unknown:</strong> ${unknownCount}</div>
    <div><strong>online:</strong> ${scannedServers.length - offlineCount - unknownCount}</div>
  `;

  serversBody.innerHTML = scannedServers.map((server) => {
    const name = serverName(server);
    const targetButton = `<button class="secondary compact" type="button" data-server-target="${escapeHTML(server.id)}">${escapeHTML(t("useTarget"))}</button>`;
    return `
    <tr>
      <td>
        <strong>${escapeHTML(name)}</strong>
        <div class="muted-code">${escapeHTML(server.id)}</div>
      </td>
      <td><span class="status ${serverStatusClass(server.status)}">${escapeHTML(t(`status_${server.status || "unknown"}`))}</span></td>
      <td>${escapeHTML(t("resourcesCount", { count: server.resourceCount ?? 0 }))}</td>
      <td>${escapeHTML(formatLastSeen(server.lastSeenAt))}</td>
      <td class="row-actions">
        <button class="secondary compact" type="button" data-server-source="${escapeHTML(server.id)}">${escapeHTML(t("useSource"))}</button>
        ${targetButton}
      </td>
    </tr>
  `;
  }).join("");
}

function serverOption(server) {
  return `<option value="${escapeHTML(server.id)}" label="${escapeHTML(serverName(server))}"></option>`;
}

function serverStatusClass(status) {
  if (status === "online") {
    return "ok";
  }
  if (status === "offline") {
    return "error";
  }
  return "warn";
}

function formatLastSeen(value) {
  if (!value) {
    return t("noLastSeen");
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return t("noLastSeen");
  }
  return new Intl.DateTimeFormat(currentLang === "ru" ? "ru-RU" : "en-US", {
    dateStyle: "short",
    timeStyle: "short"
  }).format(date);
}

function updateApplyState() {
  const allRows = Array.isArray(currentPlan?.rows) ? currentPlan.rows : [];
  const rows = selectedRows();
  const confirmationOK = confirmationInput.value === "APPLY";
  const approval = schemaHashInput.value.trim();
  const schemaOK = !approval || approval === currentPlan?.schemaHash;
  syncSelectAllControl(allRows);

  if (!currentPlan) {
    applyButton.disabled = true;
    applyHelp.textContent = t("applyHelpNoPlan");
    setStatus(applyStatus, t("statusWaiting"), "muted");
    return;
  }

  if (!allRows.length) {
    applyButton.disabled = true;
    applyHelp.textContent = t("applyHelpNoRows");
    setStatus(applyStatus, t("statusNoRows"), "muted");
    return;
  }

  if (!rows.length) {
    applyButton.disabled = true;
    applyHelp.textContent = t("applyHelpNoSelectedRows");
    setStatus(applyStatus, t("statusNoRows"), "muted");
    return;
  }

  if (!schemaOK) {
    applyButton.disabled = true;
    applyHelp.textContent = t("applyHelpSchemaMismatch");
    setStatus(applyStatus, t("statusSchemaMismatch"), "warn");
    return;
  }

  if (!confirmationOK) {
    applyButton.disabled = true;
    applyHelp.textContent = t("applyHelpLocked");
    setStatus(applyStatus, t("statusLocked"), "warn");
    return;
  }

  applyButton.disabled = false;
  applyHelp.textContent = approval ? t("applyHelpExplicit") : t("applyHelpAllowlist");
  setStatus(applyStatus, t("statusReady"), "ok");
}

async function fetchJobs(offset = jobsOffset) {
  jobsOffset = Math.max(0, offset);
  try {
    const params = new URLSearchParams({
      limit: String(JOBS_PAGE_LIMIT),
      offset: String(jobsOffset)
    });
    const body = await requestJSON(`/api/jobs?${params.toString()}`);
    renderJobs(body);
  } catch (err) {
    jobsOutput.innerHTML = `<div class="summary empty error-box">${escapeHTML(t("jobsFailed", { error: err.message || String(err) }))}</div>`;
  }
}

function normalizeJobsPage(body) {
  const rows = Array.isArray(body) ? body : (Array.isArray(body?.jobs) ? body.jobs : []);
  const total = Number.isFinite(Number(body?.total)) ? Number(body.total) : rows.length;
  const limit = Number.isFinite(Number(body?.limit)) && Number(body.limit) > 0 ? Number(body.limit) : JOBS_PAGE_LIMIT;
  const offset = Number.isFinite(Number(body?.offset)) && Number(body.offset) >= 0 ? Number(body.offset) : jobsOffset;
  const protectedCount = Number.isFinite(Number(body?.protectedCount)) && Number(body.protectedCount) >= 0
    ? Number(body.protectedCount)
    : JOBS_PAGE_LIMIT;
  return { jobs: rows, total, limit, offset, protectedCount };
}

function renderJobs(body) {
  const page = normalizeJobsPage(body);
  currentJobsPage = page;
  jobsOffset = page.offset;
  const rows = page.jobs;
  if (!rows.length) {
    jobsOutput.innerHTML = `
      <div class="summary empty">${escapeHTML(t("jobsEmpty"))}</div>
      ${jobsPagerHTML(page)}
    `;
    return;
  }
  jobsOutput.innerHTML = `
    ${jobsPagerHTML(page)}
    <div class="table-wrap jobs-table">
      <table>
        <thead>
          <tr>
            <th>${escapeHTML(t("jobColumnID"))}</th>
            <th>${escapeHTML(t("jobColumnStatus"))}</th>
            <th>${escapeHTML(t("jobColumnMode"))}</th>
            <th>${escapeHTML(t("source"))}</th>
            <th>${escapeHTML(t("target"))}</th>
            <th>${escapeHTML(t("jobColumnCheckpoint"))}</th>
            <th>${escapeHTML(t("jobColumnUpdated"))}</th>
            <th>${escapeHTML(t("jobColumnActions"))}</th>
          </tr>
        </thead>
        <tbody>
          ${rows.map((job, index) => {
            const canDelete = page.offset + index >= page.protectedCount;
            return `
            <tr>
              <td><code class="mono-break">${escapeHTML(job.id)}</code></td>
              <td><span class="status ${jobStatusClass(job.status)}">${escapeHTML(job.status || "")}</span></td>
              <td>${escapeHTML(job.mode || "")}</td>
              <td><code class="mono-break">${escapeHTML(displayServerID(job.sourceServerId))}</code></td>
              <td><code class="mono-break">${escapeHTML(displayServerID(job.targetServerId))}</code></td>
              <td>${escapeHTML(job.checkpoint || "")}</td>
              <td>${escapeHTML(formatLastSeen(job.updatedAt))}</td>
              <td class="row-actions">
                ${canDelete
                  ? `<button class="danger compact" type="button" data-job-delete="${escapeHTML(job.id)}">${escapeHTML(t("deleteJob"))}</button>`
                  : `<span class="muted-code">${escapeHTML(t("deleteProtected"))}</span>`}
              </td>
            </tr>
          `;
          }).join("")}
        </tbody>
      </table>
    </div>
  `;
}

function jobsPagerHTML(page) {
  const total = Math.max(0, page.total);
  const shown = page.jobs.length;
  const start = shown ? page.offset + 1 : 0;
  const end = shown ? page.offset + shown : 0;
  const canPrev = page.offset > 0;
  const canNext = page.offset + shown < total;
  return `
    <div class="summary jobs-pager">
      <div><strong>${escapeHTML(t("jobsPageStatus", { start, end, total }))}</strong></div>
      <div class="row-actions">
        <button class="secondary compact" type="button" data-jobs-prev="${Math.max(0, page.offset - page.limit)}" ${canPrev ? "" : "disabled"}>${escapeHTML(t("jobsPrev"))}</button>
        <button class="secondary compact" type="button" data-jobs-next="${page.offset + page.limit}" ${canNext ? "" : "disabled"}>${escapeHTML(t("jobsNext"))}</button>
      </div>
    </div>
  `;
}

async function deleteJob(jobID) {
  if (!window.confirm(t("deleteConfirm", { id: jobID }))) {
    return;
  }
  try {
    await requestJSON(`/api/jobs/${encodeURIComponent(jobID)}`, {
      method: "DELETE",
      headers: {
        "X-Migrator-Admin-Token": adminToken()
      }
    });
    const onlyRowOnPage = currentJobsPage?.jobs?.length === 1;
    const nextOffset = onlyRowOnPage ? Math.max(0, jobsOffset - JOBS_PAGE_LIMIT) : jobsOffset;
    await fetchJobs(nextOffset);
  } catch (err) {
    window.alert(t("deleteFailed", { error: err.message || String(err) }));
  }
}

function jobStatusClass(status) {
  if (status === "succeeded" || status === "rolled_back") {
    return "ok";
  }
  if (status === "failed") {
    return "error";
  }
  if (status === "running") {
    return "warn";
  }
  return "muted";
}

async function fetchServers() {
  serversSummary.textContent = t("scanRunning");
  serversSummary.className = "summary empty";
  try {
    const body = await requestJSON("/api/servers");
    if (!Array.isArray(body) || body.length === 0) {
      scannedServers = [];
      serversSummary.textContent = t("scanNone");
      serversSummary.className = "summary empty";
      serversBody.innerHTML = `<tr><td colspan="5" class="empty-cell">${escapeHTML(t("scanNone"))}</td></tr>`;
      return;
    }
    renderServers(body);
  } catch (err) {
    serversSummary.textContent = t("scanFailed", { error: err.message || String(err) });
    serversSummary.className = "summary empty error-box";
  }
}

document.querySelector("#refresh").addEventListener("click", () => fetchJobs());
document.querySelector("#scan-servers").addEventListener("click", fetchServers);
document.querySelector("#lang-ru").addEventListener("click", () => applyLanguage("ru"));
document.querySelector("#lang-en").addEventListener("click", () => applyLanguage("en"));

jobsOutput.addEventListener("click", async (event) => {
  const prev = event.target.closest("[data-jobs-prev]");
  if (prev) {
    await fetchJobs(Number(prev.dataset.jobsPrev));
    return;
  }
  const next = event.target.closest("[data-jobs-next]");
  if (next) {
    await fetchJobs(Number(next.dataset.jobsNext));
    return;
  }
  const deleteButton = event.target.closest("[data-job-delete]");
  if (deleteButton) {
    await deleteJob(deleteButton.dataset.jobDelete);
  }
});

serversBody.addEventListener("click", (event) => {
  const source = event.target.closest("[data-server-source]");
  if (source) {
    sourceServerInput.value = source.dataset.serverSource;
    return;
  }
  const target = event.target.closest("[data-server-target]");
  if (target) {
    targetServerInput.value = target.dataset.serverTarget;
  }
});

resourcesBody.addEventListener("change", (event) => {
  const checkbox = event.target.closest("[data-row-key]");
  if (!checkbox) {
    return;
  }
  if (checkbox.checked) {
    selectedPlanRows.add(checkbox.dataset.rowKey);
  } else {
    selectedPlanRows.delete(checkbox.dataset.rowKey);
  }
  renderPlan({ job: currentJob, plan: currentPlan });
});

resourceSelectAll.addEventListener("change", () => {
  const rows = Array.isArray(currentPlan?.rows) ? currentPlan.rows : [];
  selectedPlanRows = resourceSelectAll.checked ? new Set(rows.map(planRowKey)) : new Set();
  renderPlan({ job: currentJob, plan: currentPlan });
});

document.querySelector("#plan-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  const payload = {
    sourceServerId: String(form.get("sourceServerId") || "").trim(),
    targetServerId: String(form.get("targetServerId") || "").trim(),
    mode: String(form.get("mode") || "dead_recovery")
  };

  planOutput.textContent = "Building dry-run plan...";
  applyOutput.textContent = "No apply yet.";
  setStatus(planStatus, t("statusRunning"), "warn");

  try {
    const body = await requestJSON("/api/plan", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Migrator-Admin-Token": adminToken()
      },
      body: JSON.stringify(payload)
    });
    renderPlan(body);
    await fetchJobs(0);
  } catch (err) {
    planOutput.textContent = String(err);
    renderNoPlan();
    setStatus(planStatus, t("statusFailed"), "error");
  }
});

document.querySelector("#apply-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  updateApplyState();
  if (applyButton.disabled) {
    return;
  }

  applyOutput.textContent = "Applying metadata retarget...";
  setStatus(applyStatus, t("statusApplying"), "warn");

  try {
    const body = await requestJSON("/api/apply", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Migrator-Admin-Token": adminToken()
      },
      body: JSON.stringify({
        jobId: currentJob.id,
        plan: selectedPlan(),
        schemaHashApproval: schemaHashInput.value.trim(),
        confirmationText: confirmationInput.value
      })
    });
    applyOutput.textContent = JSON.stringify(body, null, 2);
    setStatus(applyStatus, t("statusApplied"), "ok");
    await fetchJobs(0);
  } catch (err) {
    applyOutput.textContent = String(err);
    setStatus(applyStatus, t("statusFailed"), "error");
  }
});

schemaHashInput.addEventListener("input", updateApplyState);
confirmationInput.addEventListener("input", updateApplyState);

applyLanguage(currentLang);
fetchJobs();
