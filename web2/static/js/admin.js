// ── State ─────────────────────────────────────────────────────────
let currentTab = 'recipes';
let currentWeek = 'current';
let allMealTimes = [];
let pendingDelete = null; // { fn: async function }

// ── API helpers ───────────────────────────────────────────────────
const ADMIN = '/admin/api';
const HELPER = '/helper/api';

async function apiFetch(url, opts = {}) {
  const res = await fetch(url, {
    headers: { 'Content-Type': 'application/json', ...opts.headers },
    ...opts,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `HTTP ${res.status}`);
  }
  return res.json();
}

const api = {
  recipes: {
    list: () => apiFetch(`${ADMIN}/recipes`),
    create: d => apiFetch(`${ADMIN}/recipes`, { method: 'POST', body: JSON.stringify(d) }),
    update: (id, d) => apiFetch(`${ADMIN}/recipes/${id}`, { method: 'PUT', body: JSON.stringify(d) }),
    delete: id => apiFetch(`${ADMIN}/recipes/${id}`, { method: 'DELETE' }),
  },
  mealTimes: {
    list: () => apiFetch(`${ADMIN}/mealtimes`),
    create: d => apiFetch(`${ADMIN}/mealtimes`, { method: 'POST', body: JSON.stringify(d) }),
    update: (id, d) => apiFetch(`${ADMIN}/mealtimes/${id}`, { method: 'PUT', body: JSON.stringify(d) }),
    delete: id => apiFetch(`${ADMIN}/mealtimes/${id}`, { method: 'DELETE' }),
  },
  zones: {
    list: () => apiFetch(`${ADMIN}/zones`),
    create: d => apiFetch(`${ADMIN}/zones`, { method: 'POST', body: JSON.stringify(d) }),
    update: (id, d) => apiFetch(`${ADMIN}/zones/${id}`, { method: 'PUT', body: JSON.stringify(d) }),
    delete: id => apiFetch(`${ADMIN}/zones/${id}`, { method: 'DELETE' }),
  },
  childcare: {
    list: () => apiFetch(`${ADMIN}/childcare`),
    create: d => apiFetch(`${ADMIN}/childcare`, { method: 'POST', body: JSON.stringify(d) }),
    update: (id, d) => apiFetch(`${ADMIN}/childcare/${id}`, { method: 'PUT', body: JSON.stringify(d) }),
    delete: id => apiFetch(`${ADMIN}/childcare/${id}`, { method: 'DELETE' }),
  },
  schedule: {
    upcoming: (startDate, days) => apiFetch(`${HELPER}/schedule/upcoming?start_date=${startDate}&days=${days}`),
    regenerate: () => apiFetch(`${ADMIN}/regenerate-schedule`, { method: 'POST' }),
  },
  shopping: {
    list: () => apiFetch(`${HELPER}/shopping`),
    purchased: id => apiFetch(`${HELPER}/shopping/${id}/purchased`, { method: 'POST' }),
  },
};

// ── Toast ─────────────────────────────────────────────────────────
let toastTimer;
function showToast(msg, type = '') {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = 'toast show ' + type;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.className = 'toast'; }, 3000);
}

// ── Modals ────────────────────────────────────────────────────────
function openModal(id) {
  document.getElementById(id).classList.add('open');
}
function closeModal(id) {
  document.getElementById(id).classList.remove('open');
}

document.addEventListener('click', e => {
  const mc = e.target.closest('.modal-close, [data-modal]');
  if (mc) {
    const modalId = mc.dataset.modal || mc.closest('.modal-overlay')?.id;
    if (modalId) closeModal(modalId);
  }
  // Close on overlay click
  if (e.target.classList.contains('modal-overlay')) {
    closeModal(e.target.id);
  }
});

// ── Tab routing ───────────────────────────────────────────────────
document.getElementById('mainTabs').addEventListener('click', e => {
  const tab = e.target.closest('.tab');
  if (!tab) return;
  const name = tab.dataset.tab;
  document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
  document.querySelectorAll('.tab-content').forEach(t => t.classList.remove('active'));
  tab.classList.add('active');
  document.getElementById(`tab-${name}`).classList.add('active');
  currentTab = name;
  loadTab(name);
});

function loadTab(name) {
  if (name === 'recipes') loadRecipes();
  else if (name === 'mealtimes') loadMealTimes();
  else if (name === 'zones') loadZones();
  else if (name === 'childcare') loadChildcare();
  else if (name === 'calendar') loadCalendar();
  else if (name === 'shopping') loadShopping();
}

// ── Helpers ───────────────────────────────────────────────────────
function formatDate(isoStr) {
  if (!isoStr) return '';
  return new Date(isoStr).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

function esc(str) {
  return String(str ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function stars(rating) {
  const r = Math.round(rating * 2) / 2;
  if (!r) return '<span style="color:#94a3b8">—</span>';
  return `<span class="star">${'★'.repeat(Math.floor(r))}${r % 1 ? '½' : ''}</span> ${r}`;
}

function getMondayOfWeek(offset = 0) {
  const now = new Date();
  const day = now.getDay();
  const diff = now.getDate() - day + (day === 0 ? -6 : 1) + offset * 7;
  const d = new Date(now);
  d.setDate(diff);
  d.setHours(0, 0, 0, 0);
  return d;
}

function fmtDateParam(d) {
  return d.toISOString().slice(0, 10);
}

function dayLabel(isoDate) {
  const d = new Date(isoDate);
  return {
    name: d.toLocaleDateString('en-US', { weekday: 'long' }),
    date: d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' }),
  };
}

// ── Confirm delete ────────────────────────────────────────────────
function confirmDelete(message, fn) {
  document.getElementById('confirmMessage').textContent = message;
  pendingDelete = fn;
  openModal('confirmModal');
}

document.getElementById('confirmDeleteBtn').addEventListener('click', async () => {
  if (!pendingDelete) return;
  try {
    await pendingDelete();
    closeModal('confirmModal');
    pendingDelete = null;
    showToast('Deleted', 'success');
  } catch (err) {
    showToast(err.message, 'error');
  }
});

// ── RECIPES ───────────────────────────────────────────────────────
let editingRecipe = null;

async function loadRecipes() {
  const tbody = document.getElementById('recipesList');
  tbody.innerHTML = '<tr><td colspan="8" style="padding:24px;text-align:center;color:#94a3b8">Loading...</td></tr>';
  try {
    const [recipes, mealTimes] = await Promise.all([api.recipes.list(), api.mealTimes.list()]);
    allMealTimes = mealTimes;
    if (!recipes.length) {
      tbody.innerHTML = '<tr><td colspan="8"><div class="empty">No recipes yet. Add your first recipe!</div></td></tr>';
      return;
    }
    tbody.innerHTML = recipes.map(r => `
      <tr>
        <td><strong>${esc(r.name)}</strong>${r.tags ? `<br><span style="font-size:12px;color:#64748b">${esc(r.tags)}</span>` : ''}</td>
        <td><span class="badge badge-blue">${esc(r.family_member || 'all')}</span></td>
        <td style="color:#64748b;font-size:13px">${r.prep_time || 0}m / ${r.cook_time || 0}m</td>
        <td style="text-align:center">${r.servings || '—'}</td>
        <td>${stars(r.rating)}</td>
        <td><div class="meal-times">${(r.meal_times || []).map(mt => `<span class="badge badge-purple">${esc(mt.name)}</span>`).join('')}</div></td>
        <td>${r.is_active ? '<span class="badge badge-green">Active</span>' : '<span class="badge badge-gray">Inactive</span>'}</td>
        <td class="actions">
          <button class="btn-icon" onclick="openEditRecipe(${r.id})" title="Edit">✏️</button>
          <button class="btn-icon danger" onclick="deleteRecipe(${r.id}, '${esc(r.name)}')" title="Delete">🗑️</button>
        </td>
      </tr>
    `).join('');
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="8"><div class="empty">Error: ${esc(err.message)}</div></td></tr>`;
  }
}

function buildMealTimeCheckboxes(selected = []) {
  const container = document.getElementById('mealTimesCheckboxes');
  const selectedIds = new Set(selected.map(mt => mt.id));
  container.innerHTML = allMealTimes.map(mt => `
    <label class="checkbox-item ${selectedIds.has(mt.id) ? 'checked' : ''}" data-id="${mt.id}">
      <input type="checkbox" value="${mt.id}" ${selectedIds.has(mt.id) ? 'checked' : ''} style="display:none">
      ${esc(mt.name)} <small style="color:#94a3b8">${mt.default_time}</small>
    </label>
  `).join('');
  container.querySelectorAll('.checkbox-item').forEach(label => {
    label.addEventListener('click', () => {
      const cb = label.querySelector('input');
      cb.checked = !cb.checked;
      label.classList.toggle('checked', cb.checked);
    });
  });
}

function getSelectedMealTimeIds() {
  return Array.from(document.querySelectorAll('#mealTimesCheckboxes input:checked')).map(cb => parseInt(cb.value));
}

document.getElementById('addRecipeBtn').addEventListener('click', async () => {
  editingRecipe = null;
  if (!allMealTimes.length) allMealTimes = await api.mealTimes.list().catch(() => []);
  document.getElementById('recipeModalTitle').textContent = 'Add Recipe';
  document.getElementById('recipeId').value = '';
  document.getElementById('recipeName').value = '';
  document.getElementById('recipeFamilyMember').value = 'all';
  document.getElementById('recipeDescription').value = '';
  document.getElementById('recipeIngredients').value = '';
  document.getElementById('recipeInstructions').value = '';
  document.getElementById('recipePrepTime').value = 0;
  document.getElementById('recipeCookTime').value = 0;
  document.getElementById('recipeServings').value = 2;
  document.getElementById('recipeRating').value = 0;
  document.getElementById('recipeImageURL').value = '';
  document.getElementById('recipeVideoURL').value = '';
  document.getElementById('recipeTags').value = '';
  document.getElementById('recipeIsActive').checked = true;
  buildMealTimeCheckboxes([]);
  openModal('recipeModal');
});

async function openEditRecipe(id) {
  try {
    const [recipe, mealTimes] = await Promise.all([
      apiFetch(`${ADMIN}/recipes/${id}`),
      allMealTimes.length ? Promise.resolve(allMealTimes) : api.mealTimes.list(),
    ]);
    allMealTimes = mealTimes;
    editingRecipe = recipe;
    document.getElementById('recipeModalTitle').textContent = 'Edit Recipe';
    document.getElementById('recipeId').value = recipe.id;
    document.getElementById('recipeName').value = recipe.name;
    document.getElementById('recipeFamilyMember').value = recipe.family_member || 'all';
    document.getElementById('recipeDescription').value = recipe.description || '';
    document.getElementById('recipeIngredients').value = recipe.ingredients || '';
    document.getElementById('recipeInstructions').value = recipe.instructions || '';
    document.getElementById('recipePrepTime').value = recipe.prep_time || 0;
    document.getElementById('recipeCookTime').value = recipe.cook_time || 0;
    document.getElementById('recipeServings').value = recipe.servings || 2;
    document.getElementById('recipeRating').value = recipe.rating || 0;
    document.getElementById('recipeImageURL').value = recipe.image_url || '';
    document.getElementById('recipeVideoURL').value = recipe.video_url || '';
    document.getElementById('recipeTags').value = recipe.tags || '';
    document.getElementById('recipeIsActive').checked = recipe.is_active !== false;
    buildMealTimeCheckboxes(recipe.meal_times || []);
    openModal('recipeModal');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

document.getElementById('saveRecipeBtn').addEventListener('click', async () => {
  const id = document.getElementById('recipeId').value;
  const data = {
    name: document.getElementById('recipeName').value.trim(),
    family_member: document.getElementById('recipeFamilyMember').value,
    description: document.getElementById('recipeDescription').value.trim(),
    ingredients: document.getElementById('recipeIngredients').value.trim(),
    instructions: document.getElementById('recipeInstructions').value.trim(),
    prep_time: parseInt(document.getElementById('recipePrepTime').value) || 0,
    cook_time: parseInt(document.getElementById('recipeCookTime').value) || 0,
    servings: parseInt(document.getElementById('recipeServings').value) || 2,
    rating: parseFloat(document.getElementById('recipeRating').value) || 0,
    image_url: document.getElementById('recipeImageURL').value.trim(),
    video_url: document.getElementById('recipeVideoURL').value.trim(),
    tags: document.getElementById('recipeTags').value.trim(),
    is_active: document.getElementById('recipeIsActive').checked,
    meal_time_ids: getSelectedMealTimeIds(),
  };
  if (!data.name) { showToast('Name is required', 'error'); return; }
  try {
    if (id) {
      await api.recipes.update(id, data);
      showToast('Recipe updated', 'success');
    } else {
      await api.recipes.create(data);
      showToast('Recipe created', 'success');
    }
    closeModal('recipeModal');
    loadRecipes();
  } catch (err) {
    showToast(err.message, 'error');
  }
});

function deleteRecipe(id, name) {
  confirmDelete(`Delete recipe "${name}"?`, async () => {
    await api.recipes.delete(id);
    loadRecipes();
  });
}

// ── MEAL TIMES ────────────────────────────────────────────────────
async function loadMealTimes() {
  const tbody = document.getElementById('mealTimesList');
  tbody.innerHTML = '<tr><td colspan="6" style="padding:24px;text-align:center;color:#94a3b8">Loading...</td></tr>';
  try {
    const items = await api.mealTimes.list();
    allMealTimes = items;
    if (!items.length) {
      tbody.innerHTML = '<tr><td colspan="6"><div class="empty">No meal times yet.</div></td></tr>';
      return;
    }
    tbody.innerHTML = items.map(mt => `
      <tr>
        <td><strong>${esc(mt.name)}</strong></td>
        <td>${esc(mt.default_time)}</td>
        <td style="font-size:12px;color:#64748b">${esc(mt.default_times || '—')}</td>
        <td><span class="badge badge-blue">${esc(mt.family_member || 'all')}</span></td>
        <td>${mt.active ? '<span class="badge badge-green">Active</span>' : '<span class="badge badge-gray">Inactive</span>'}</td>
        <td class="actions">
          <button class="btn-icon" onclick="openEditMealTime(${mt.id})" title="Edit">✏️</button>
          <button class="btn-icon danger" onclick="deleteMealTime(${mt.id}, '${esc(mt.name)}')" title="Delete">🗑️</button>
        </td>
      </tr>
    `).join('');
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="6"><div class="empty">Error: ${esc(err.message)}</div></td></tr>`;
  }
}

document.getElementById('addMealTimeBtn').addEventListener('click', () => {
  document.getElementById('mealTimeModalTitle').textContent = 'Add Meal Time';
  document.getElementById('mealTimeId').value = '';
  document.getElementById('mealTimeName').value = '';
  document.getElementById('mealTimeFamilyMember').value = 'all';
  document.getElementById('mealTimeDefaultTime').value = '';
  document.getElementById('mealTimeDefaultTimes').value = '';
  document.getElementById('mealTimeActive').checked = true;
  openModal('mealTimeModal');
});

async function openEditMealTime(id) {
  try {
    const mt = await apiFetch(`${ADMIN}/mealtimes/${id}`);
    document.getElementById('mealTimeModalTitle').textContent = 'Edit Meal Time';
    document.getElementById('mealTimeId').value = mt.id;
    document.getElementById('mealTimeName').value = mt.name;
    document.getElementById('mealTimeFamilyMember').value = mt.family_member || '';
    document.getElementById('mealTimeDefaultTime').value = mt.default_time;
    document.getElementById('mealTimeDefaultTimes').value = mt.default_times || '';
    document.getElementById('mealTimeActive').checked = mt.active !== false;
    openModal('mealTimeModal');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

document.getElementById('saveMealTimeBtn').addEventListener('click', async () => {
  const id = document.getElementById('mealTimeId').value;
  const data = {
    name: document.getElementById('mealTimeName').value.trim(),
    family_member: document.getElementById('mealTimeFamilyMember').value.trim(),
    default_time: document.getElementById('mealTimeDefaultTime').value,
    default_times: document.getElementById('mealTimeDefaultTimes').value.trim(),
    active: document.getElementById('mealTimeActive').checked,
  };
  if (!data.name || !data.default_time) { showToast('Name and time are required', 'error'); return; }
  try {
    if (id) {
      await api.mealTimes.update(id, data);
      showToast('Meal time updated', 'success');
    } else {
      await api.mealTimes.create(data);
      showToast('Meal time created', 'success');
    }
    closeModal('mealTimeModal');
    loadMealTimes();
  } catch (err) {
    showToast(err.message, 'error');
  }
});

function deleteMealTime(id, name) {
  confirmDelete(`Delete meal time "${name}"?`, async () => {
    await api.mealTimes.delete(id);
    loadMealTimes();
  });
}

// ── CLEANING ZONES ────────────────────────────────────────────────
async function loadZones() {
  const tbody = document.getElementById('zonesList');
  tbody.innerHTML = '<tr><td colspan="5" style="padding:24px;text-align:center;color:#94a3b8">Loading...</td></tr>';
  try {
    const items = await api.zones.list();
    if (!items.length) {
      tbody.innerHTML = '<tr><td colspan="5"><div class="empty">No cleaning zones yet.</div></td></tr>';
      return;
    }
    tbody.innerHTML = items.map(z => `
      <tr>
        <td><strong>${esc(z.name)}</strong></td>
        <td style="font-size:13px;color:#64748b">${esc(z.description || '—')}</td>
        <td style="text-align:center">${z.frequency_per_week}×</td>
        <td><span class="badge priority-${z.priority}">${esc(z.priority)}</span></td>
        <td class="actions">
          <button class="btn-icon" onclick="openEditZone(${z.id})" title="Edit">✏️</button>
          <button class="btn-icon danger" onclick="deleteZone(${z.id}, '${esc(z.name)}')" title="Delete">🗑️</button>
        </td>
      </tr>
    `).join('');
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="5"><div class="empty">Error: ${esc(err.message)}</div></td></tr>`;
  }
}

document.getElementById('addZoneBtn').addEventListener('click', () => {
  document.getElementById('zoneModalTitle').textContent = 'Add Cleaning Zone';
  document.getElementById('zoneId').value = '';
  document.getElementById('zoneName').value = '';
  document.getElementById('zonePriority').value = 'medium';
  document.getElementById('zoneDescription').value = '';
  document.getElementById('zoneFrequency').value = 1;
  openModal('zoneModal');
});

async function openEditZone(id) {
  try {
    const z = await apiFetch(`${ADMIN}/zones/${id}`);
    document.getElementById('zoneModalTitle').textContent = 'Edit Cleaning Zone';
    document.getElementById('zoneId').value = z.id;
    document.getElementById('zoneName').value = z.name;
    document.getElementById('zonePriority').value = z.priority;
    document.getElementById('zoneDescription').value = z.description || '';
    document.getElementById('zoneFrequency').value = z.frequency_per_week;
    openModal('zoneModal');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

document.getElementById('saveZoneBtn').addEventListener('click', async () => {
  const id = document.getElementById('zoneId').value;
  const data = {
    name: document.getElementById('zoneName').value.trim(),
    priority: document.getElementById('zonePriority').value,
    description: document.getElementById('zoneDescription').value.trim(),
    frequency_per_week: parseInt(document.getElementById('zoneFrequency').value) || 1,
  };
  if (!data.name) { showToast('Name is required', 'error'); return; }
  try {
    if (id) {
      await api.zones.update(id, data);
      showToast('Zone updated', 'success');
    } else {
      await api.zones.create(data);
      showToast('Zone created', 'success');
    }
    closeModal('zoneModal');
    loadZones();
  } catch (err) {
    showToast(err.message, 'error');
  }
});

function deleteZone(id, name) {
  confirmDelete(`Delete zone "${name}"?`, async () => {
    await api.zones.delete(id);
    loadZones();
  });
}

// ── CHILDCARE ─────────────────────────────────────────────────────
async function loadChildcare() {
  const tbody = document.getElementById('childcareList');
  tbody.innerHTML = '<tr><td colspan="5" style="padding:24px;text-align:center;color:#94a3b8">Loading...</td></tr>';
  try {
    const items = await api.childcare.list();
    if (!items.length) {
      tbody.innerHTML = '<tr><td colspan="5"><div class="empty">No childcare schedules for the next 30 days.</div></td></tr>';
      return;
    }
    tbody.innerHTML = items.map(s => `
      <tr>
        <td>${formatDate(s.date)}</td>
        <td>${esc(s.start_time)}</td>
        <td>${esc(s.end_time)}</td>
        <td style="font-size:13px;color:#64748b">${esc(s.notes || '—')}</td>
        <td class="actions">
          <button class="btn-icon" onclick="openEditChildcare(${s.id})" title="Edit">✏️</button>
          <button class="btn-icon danger" onclick="deleteChildcare(${s.id})" title="Delete">🗑️</button>
        </td>
      </tr>
    `).join('');
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="5"><div class="empty">Error: ${esc(err.message)}</div></td></tr>`;
  }
}

document.getElementById('addChildcareBtn').addEventListener('click', () => {
  document.getElementById('childcareModalTitle').textContent = 'Add Childcare Schedule';
  document.getElementById('childcareId').value = '';
  document.getElementById('childcareDate').value = '';
  document.getElementById('childcareStartTime').value = '';
  document.getElementById('childcareEndTime').value = '';
  document.getElementById('childcareNotes').value = '';
  openModal('childcareModal');
});

async function openEditChildcare(id) {
  try {
    const s = await apiFetch(`${ADMIN}/childcare/${id}`);
    document.getElementById('childcareModalTitle').textContent = 'Edit Childcare Schedule';
    document.getElementById('childcareId').value = s.id;
    document.getElementById('childcareDate').value = s.date ? s.date.slice(0, 10) : '';
    document.getElementById('childcareStartTime').value = s.start_time;
    document.getElementById('childcareEndTime').value = s.end_time;
    document.getElementById('childcareNotes').value = s.notes || '';
    openModal('childcareModal');
  } catch (err) {
    showToast(err.message, 'error');
  }
}

document.getElementById('saveChildcareBtn').addEventListener('click', async () => {
  const id = document.getElementById('childcareId').value;
  const dateVal = document.getElementById('childcareDate').value;
  const data = {
    date: dateVal ? new Date(dateVal + 'T00:00:00Z').toISOString() : '',
    start_time: document.getElementById('childcareStartTime').value,
    end_time: document.getElementById('childcareEndTime').value,
    notes: document.getElementById('childcareNotes').value.trim(),
  };
  if (!data.date || !data.start_time || !data.end_time) {
    showToast('Date and times are required', 'error'); return;
  }
  try {
    if (id) {
      await api.childcare.update(id, data);
      showToast('Schedule updated', 'success');
    } else {
      await api.childcare.create(data);
      showToast('Schedule created', 'success');
    }
    closeModal('childcareModal');
    loadChildcare();
  } catch (err) {
    showToast(err.message, 'error');
  }
});

function deleteChildcare(id) {
  confirmDelete('Delete this childcare schedule?', async () => {
    await api.childcare.delete(id);
    loadChildcare();
  });
}

// ── CALENDAR ──────────────────────────────────────────────────────
document.getElementById('regenerateBtn').addEventListener('click', async () => {
  const btn = document.getElementById('regenerateBtn');
  btn.disabled = true;
  btn.textContent = 'Regenerating...';
  try {
    await api.schedule.regenerate();
    showToast('Schedule regenerated successfully', 'success');
    loadCalendar();
  } catch (err) {
    showToast(err.message, 'error');
  } finally {
    btn.disabled = false;
    btn.textContent = '↺ Regenerate Schedule';
  }
});

document.querySelectorAll('.week-tab').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.week-tab').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    currentWeek = btn.dataset.week;
    renderCalendar();
  });
});

let calendarData = { current: [], next: [] };

async function loadCalendar() {
  const monday = getMondayOfWeek(0);
  const nextMonday = getMondayOfWeek(1);
  const container = document.getElementById('calendarContent');
  container.innerHTML = '<div style="padding:32px;text-align:center;color:#94a3b8">Loading calendar...</div>';
  try {
    const [current, next] = await Promise.all([
      api.schedule.upcoming(fmtDateParam(monday), 7),
      api.schedule.upcoming(fmtDateParam(nextMonday), 7),
    ]);
    calendarData.current = current;
    calendarData.next = next;
    renderCalendar();
  } catch (err) {
    container.innerHTML = `<div style="padding:32px;text-align:center;color:#ef4444">Error: ${esc(err.message)}</div>`;
  }
}

function renderCalendar() {
  const container = document.getElementById('calendarContent');
  const weekOffset = currentWeek === 'next' ? 1 : 0;
  const monday = getMondayOfWeek(weekOffset);
  const schedules = currentWeek === 'next' ? calendarData.next : calendarData.current;

  // Build a map: date-string → schedule
  const schedMap = {};
  (schedules || []).forEach(s => {
    const key = s.date ? s.date.slice(0, 10) : '';
    if (key) schedMap[key] = s;
  });

  const days = [];
  for (let i = 0; i < 7; i++) {
    const d = new Date(monday);
    d.setDate(monday.getDate() + i);
    days.push(d);
  }

  container.innerHTML = days.map(d => {
    const key = fmtDateParam(d);
    const { name, date } = dayLabel(key);
    const sched = schedMap[key];
    const tasks = sched ? (sched.tasks || []) : [];

    const tasksHtml = tasks.length
      ? tasks.sort((a, b) => (a.time || '').localeCompare(b.time || '')).map(t => `
          <div class="day-task">
            <span class="task-time">${t.time || ''}</span>
            <div class="task-body">
              <div><span class="task-type type-${t.task_type}">${t.task_type}</span></div>
              <div class="task-title">${esc(t.title)}</div>
              ${t.description ? `<div class="task-sub">${esc(t.description)}</div>` : ''}
              ${(t.recipes || []).map(r => `<span class="badge badge-blue" style="font-size:11px">${esc(r.name)}</span>`).join(' ')}
              ${(t.zones || []).map(z => `<span class="badge badge-green" style="font-size:11px">${esc(z.name)}</span>`).join(' ')}
            </div>
          </div>
        `).join('')
      : '<div class="day-card-empty">No tasks</div>';

    return `
      <div class="day-card">
        <div class="day-card-header">
          <div class="day-name">${name}</div>
          <div class="day-date">${date}</div>
        </div>
        <div class="day-card-body">${tasksHtml}</div>
      </div>
    `;
  }).join('');
}

// ── SHOPPING ──────────────────────────────────────────────────────
async function loadShopping() {
  const container = document.getElementById('shoppingContent');
  container.innerHTML = '<div style="padding:32px;text-align:center;color:#94a3b8">Loading...</div>';
  try {
    const items = await api.shopping.list();
    if (!items || !items.length) {
      container.innerHTML = '<div class="shopping-list-wrap"><div class="shopping-empty">Shopping list is empty</div></div>';
      return;
    }

    // Group by category
    const groups = {};
    items.forEach(item => {
      const cat = item.category || 'other';
      if (!groups[cat]) groups[cat] = [];
      groups[cat].push(item);
    });

    const html = `
      <div class="shopping-list-wrap">
        ${Object.entries(groups).map(([cat, catItems]) => `
          <div class="shopping-category">
            <div class="shopping-cat-header">${esc(cat)}</div>
            ${catItems.map(item => `
              <div class="shopping-item" id="shop-${item.id}">
                <input type="checkbox" ${item.purchased ? 'checked' : ''} onchange="markPurchased(${item.id}, this)">
                <span class="shopping-item-name">${esc(item.item)}</span>
                ${item.quantity ? `<span class="shopping-item-qty">${esc(item.quantity)}</span>` : ''}
              </div>
            `).join('')}
          </div>
        `).join('')}
      </div>
    `;
    container.innerHTML = html;
  } catch (err) {
    container.innerHTML = `<div style="padding:32px;text-align:center;color:#ef4444">Error: ${esc(err.message)}</div>`;
  }
}

async function markPurchased(id, cb) {
  try {
    await api.shopping.purchased(id);
    const row = document.getElementById(`shop-${id}`);
    if (row) {
      row.style.opacity = '0';
      setTimeout(() => row.remove(), 300);
    }
  } catch (err) {
    cb.checked = false;
    showToast(err.message, 'error');
  }
}

document.getElementById('refreshShoppingBtn')?.addEventListener('click', loadShopping);

// ── Init ──────────────────────────────────────────────────────────
loadRecipes();