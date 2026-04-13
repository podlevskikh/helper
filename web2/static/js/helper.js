// ── State ─────────────────────────────────────────────────────────
let currentSection = 'calendar';
let currentView = 'today';

// ── API helpers ───────────────────────────────────────────────────
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
  schedule: {
    today: () => apiFetch(`${HELPER}/schedule/today`),
    upcoming: (startDate, days) => apiFetch(`${HELPER}/schedule/upcoming?start_date=${startDate}&days=${days}`),
  },
  tasks: {
    complete: id => apiFetch(`${HELPER}/tasks/${id}/complete`, { method: 'POST' }),
    uncomplete: id => apiFetch(`${HELPER}/tasks/${id}/uncomplete`, { method: 'POST' }),
  },
  shopping: {
    list: () => apiFetch(`${HELPER}/shopping`),
    add: d => apiFetch(`${HELPER}/shopping`, { method: 'POST', body: JSON.stringify(d) }),
    purchased: id => apiFetch(`${HELPER}/shopping/${id}/purchased`, { method: 'POST' }),
    delete: id => apiFetch(`${HELPER}/shopping/${id}`, { method: 'DELETE' }),
  },
  recipes: {
    get: id => apiFetch(`${HELPER}/recipes/${id}`),
  },
};

// ── Utils ─────────────────────────────────────────────────────────
function esc(str) {
  return String(str ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
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

function formatDayHeader(isoDate) {
  const d = new Date(isoDate);
  const today = new Date();
  const isToday = d.toDateString() === today.toDateString();
  const name = d.toLocaleDateString('en-US', { weekday: 'long' });
  const date = d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
  return { name, date, isToday };
}

// ── Toast ─────────────────────────────────────────────────────────
let toastTimer;
function showToast(msg, type = '') {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = 'toast show ' + type;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.className = 'toast'; }, 3000);
}

// ── Modal ─────────────────────────────────────────────────────────
function openModal(id) { document.getElementById(id).classList.add('open'); }
function closeModal(id) { document.getElementById(id).classList.remove('open'); }

document.addEventListener('click', e => {
  const mc = e.target.closest('.modal-close, [data-modal]');
  if (mc) {
    const modalId = mc.dataset.modal || mc.closest('.modal-overlay')?.id;
    if (modalId) closeModal(modalId);
  }
  if (e.target.classList.contains('modal-overlay')) closeModal(e.target.id);
});

// ── Section tabs ──────────────────────────────────────────────────
document.querySelectorAll('.section-tab').forEach(btn => {
  btn.addEventListener('click', () => {
    const name = btn.dataset.section;
    document.querySelectorAll('.section-tab').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.section-content').forEach(s => s.classList.remove('active'));
    btn.classList.add('active');
    document.getElementById(`section-${name}`).classList.add('active');
    currentSection = name;
    if (name === 'shopping') loadShoppingList();
  });
});

// ── View tabs ─────────────────────────────────────────────────────
document.querySelectorAll('.view-tab').forEach(btn => {
  btn.addEventListener('click', () => {
    const view = btn.dataset.view;
    document.querySelectorAll('.view-tab').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
    btn.classList.add('active');
    document.getElementById(`view-${view}`).classList.add('active');
    currentView = view;
    if (view === 'today') loadToday();
    else if (view === 'thisweek') loadWeek(0, 'thisWeekContent');
    else if (view === 'nextweek') loadWeek(1, 'nextWeekContent');
  });
});

// ── Collect all recipes for a task (FK + many2many) ──────────────
// The scheduler uses the old RecipeID FK field, not the many2many relation.
// So task.recipe (singular) is populated, task.recipes (plural) is empty.
// We merge both to get the full list.
function getTaskRecipes(task) {
  const many = task.recipes || [];
  const single = task.recipe;
  if (!single) return many;
  const alreadyIn = many.some(r => r.id === single.id);
  return alreadyIn ? many : [single, ...many];
}

function getTaskZones(task) {
  const many = task.zones || [];
  const single = task.zone;
  if (!single) return many;
  const alreadyIn = many.some(z => z.id === single.id);
  return alreadyIn ? many : [single, ...many];
}

// ── Task content builders ─────────────────────────────────────────
// Returns { label, main, mainClickId, sub, extras }
// label   — small muted top line (time + context)
// main    — bold primary line
// mainClickId — recipe id if main is clickable, else null
// sub     — secondary muted line
// extras  — extra recipe chips array [{id, name}]
function taskContent(task) {
  const recipes = getTaskRecipes(task);
  const zones   = getTaskZones(task);
  const timeStr = task.time || '';
  const timeRange = task.end_time && task.time ? `${task.time} – ${task.end_time}` : timeStr;

  if (task.task_type === 'meal') {
    // label = "08:00  Breakfast - adult"
    // main  = recipe name (bold, clickable blue)
    // sub   = nothing (all info already above)
    const recipeName = recipes.length > 0 ? recipes[0].name
                     : task.description   ? task.description
                     : '';
    const recipeId   = recipes.length > 0 ? recipes[0].id : null;
    return {
      label:       (timeStr ? timeStr + '  ' : '') + task.title,
      main:        recipeName,
      mainClickId: recipeId,
      sub:         '',
      extras:      recipes.slice(1),
    };
  }

  if (task.task_type === 'cleaning') {
    // label = nothing (no specific time for cleaning)
    // main  = zone name (title)
    // sub   = zone description / task description
    const zoneDesc = zones.length > 0 && zones[0].description
      ? zones[0].description
      : task.description || '';
    return {
      label:       '',
      main:        task.title,
      mainClickId: null,
      sub:         zoneDesc,
      extras:      [],
    };
  }

  if (task.task_type === 'childcare') {
    // label = time range (prominent)
    // main  = "Childcare"
    // sub   = notes
    return {
      label:       timeRange,
      main:        task.title,
      mainClickId: null,
      sub:         task.description || '',
      extras:      [],
    };
  }

  // fallback
  return {
    label:       timeRange,
    main:        task.title,
    mainClickId: null,
    sub:         task.description || '',
    extras:      [],
  };
}

// ── Today view ────────────────────────────────────────────────────
function renderTaskCard(task) {
  const c = taskContent(task);

  const labelHtml = c.label
    ? `<div class="tc-label">${esc(c.label)}</div>`
    : '';

  let mainHtml = '';
  if (task.task_type === 'meal') {
    const allR = getTaskRecipes(task);
    if (allR.length > 0) {
      mainHtml = allR.map(r =>
        `<div class="tc-main tc-recipe" onclick="showRecipe(${r.id}, event)">${esc(r.name)}</div>`
      ).join('');
    } else if (c.main) {
      mainHtml = `<div class="tc-main">${esc(c.main)}</div>`;
    }
  } else {
    mainHtml = c.main
      ? `<div class="tc-main">${esc(c.main)}</div>`
      : '';
  }

  const subHtml = c.sub
    ? `<div class="tc-sub">${esc(c.sub)}</div>`
    : '';

  return `
    <div class="task-card type-${esc(task.task_type)} ${task.completed ? 'completed' : ''}" id="task-${task.id}">
      <input type="checkbox" class="task-checkbox" ${task.completed ? 'checked' : ''}
        onchange="toggleTask(${task.id}, this)">
      <div class="task-info">
        ${labelHtml}${mainHtml}${subHtml}
      </div>
    </div>
  `;
}

// ── Week view ─────────────────────────────────────────────────────
function renderWeekTask(task) {
  const c = taskContent(task);

  // In the compact week view we show time in its own column,
  // so strip the time prefix from label if it starts with it.
  const displayTime = task.task_type === 'childcare' && task.end_time
    ? `${task.time}–${task.end_time}`
    : (task.time || '');

  // For meal: show meal label (without time, time is in column)
  const contextLabel = task.task_type === 'meal'
    ? task.title   // "Breakfast - adult"
    : '';

  // For meal: show all recipes as clickable lines
  let recipesHtml = '';
  if (task.task_type === 'meal') {
    const allR = getTaskRecipes(task);
    recipesHtml = allR.map(r =>
      `<div class="wt-sub wt-recipe" onclick="showRecipe(${r.id}, event)">${esc(r.name)}</div>`
    ).join('');
    if (!recipesHtml && c.sub) {
      recipesHtml = `<div class="wt-sub">${esc(c.sub)}</div>`;
    }
  }

  const subLine = task.task_type === 'meal'
    ? recipesHtml
    : (c.sub ? `<div class="wt-sub">${esc(c.sub)}</div>` : '');

  const titleText = task.task_type === 'meal' ? contextLabel : c.main;

  return `
    <div class="week-task type-${esc(task.task_type)} ${task.completed ? 'wt-completed' : ''}" id="wtask-${task.id}">
      <input type="checkbox" class="wt-check" ${task.completed ? 'checked' : ''}
        onchange="toggleTask(${task.id}, this, true)">
      <span class="wt-time">${esc(displayTime)}</span>
      <div class="wt-body">
        <div class="wt-title">${esc(titleText)}</div>
        ${subLine}
      </div>
    </div>
  `;
}

// ── Toggle task ───────────────────────────────────────────────────
async function toggleTask(id, cb, isWeek = false) {
  const card = document.getElementById(isWeek ? `wtask-${id}` : `task-${id}`);
  try {
    if (cb.checked) {
      await api.tasks.complete(id);
    } else {
      await api.tasks.uncomplete(id);
    }
    if (card) {
      if (isWeek) {
        card.classList.toggle('wt-completed', cb.checked);
      } else {
        card.classList.toggle('completed', cb.checked);
      }
    }
  } catch (err) {
    cb.checked = !cb.checked;
    showToast(err.message, 'error');
  }
}

// ── Today ─────────────────────────────────────────────────────────
async function loadToday() {
  const container = document.getElementById('todayTasks');
  const titleEl = document.getElementById('todayTitle');

  const today = new Date();
  titleEl.textContent = today.toLocaleDateString('en-US', {
    weekday: 'long', month: 'long', day: 'numeric'
  });

  container.innerHTML = '<div class="no-tasks">Loading...</div>';
  try {
    const data = await api.schedule.today();
    const tasks = data.tasks || [];

    if (!tasks.length) {
      container.innerHTML = '<div class="no-tasks">No tasks for today</div>';
      return;
    }

    const sorted = tasks.sort((a, b) => (a.time || '').localeCompare(b.time || ''));
    container.innerHTML = sorted.map(renderTaskCard).join('');
  } catch (err) {
    container.innerHTML = `<div class="no-tasks" style="color:#ef4444">Error: ${esc(err.message)}</div>`;
  }
}

// ── Week view ─────────────────────────────────────────────────────
async function loadWeek(weekOffset, containerId) {
  const container = document.getElementById(containerId);
  container.innerHTML = '<div style="padding:32px;text-align:center;color:#475569">Loading...</div>';

  const monday = getMondayOfWeek(weekOffset);

  try {
    const schedules = await api.schedule.upcoming(fmtDateParam(monday), 7);

    // Map date → schedule
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
      const { name, date, isToday } = formatDayHeader(key);
      const sched = schedMap[key];
      const tasks = sched ? (sched.tasks || []) : [];
      const sorted = tasks.sort((a, b) => (a.time || '').localeCompare(b.time || ''));

      return `
        <div class="week-day-card">
          <div class="week-day-header ${isToday ? 'today-header' : ''}">
            <span class="week-day-name">${name}${isToday ? ' (Today)' : ''}</span>
            <span class="week-day-date">${date}</span>
          </div>
          <div class="week-day-tasks">
            ${sorted.length
              ? sorted.map(renderWeekTask).join('')
              : '<div class="week-day-empty">No tasks</div>'
            }
          </div>
        </div>
      `;
    }).join('');
  } catch (err) {
    container.innerHTML = `<div style="padding:32px;text-align:center;color:#ef4444">Error: ${esc(err.message)}</div>`;
  }
}

// ── Recipe modal ──────────────────────────────────────────────────
async function showRecipe(id, event) {
  if (event) event.stopPropagation();
  try {
    const recipe = await api.recipes.get(id);
    document.getElementById('recipeModalTitle').textContent = recipe.name;

    const imgHtml = recipe.image_url
      ? `<img class="recipe-detail-img" src="${esc(recipe.image_url)}" alt="${esc(recipe.name)}">`
      : '';

    const metaItems = [];
    if (recipe.prep_time) metaItems.push(`Prep: ${recipe.prep_time} min`);
    if (recipe.cook_time) metaItems.push(`Cook: ${recipe.cook_time} min`);
    if (recipe.servings) metaItems.push(`Serves: ${recipe.servings}`);
    if (recipe.family_member) metaItems.push(`For: ${recipe.family_member}`);
    if (recipe.rating) metaItems.push(`Rating: ${recipe.rating}/5`);

    const metaHtml = metaItems.length
      ? `<div class="recipe-meta">${metaItems.map(m => `<span class="recipe-meta-item">${esc(m)}</span>`).join('')}</div>`
      : '';

    const descHtml = recipe.description
      ? `<div class="recipe-section"><p>${esc(recipe.description)}</p></div>`
      : '';

    const ingredientsHtml = recipe.ingredients
      ? `<div class="recipe-section"><h4>Ingredients</h4><pre>${esc(recipe.ingredients)}</pre></div>`
      : '';

    const instructionsHtml = recipe.instructions
      ? `<div class="recipe-section"><h4>Instructions</h4><pre>${esc(recipe.instructions)}</pre></div>`
      : '';

    const videoHtml = recipe.video_url
      ? `<div class="recipe-section"><a href="${esc(recipe.video_url)}" target="_blank" rel="noopener" style="color:#0891b2">Watch video</a></div>`
      : '';

    document.getElementById('recipeModalBody').innerHTML =
      imgHtml + metaHtml + descHtml + ingredientsHtml + instructionsHtml + videoHtml;

    openModal('recipeModal');
  } catch (err) {
    showToast('Failed to load recipe details', 'error');
  }
}

// ── Shopping list ─────────────────────────────────────────────────
async function loadShoppingList() {
  const container = document.getElementById('shoppingList');
  container.innerHTML = '<div class="no-tasks">Loading...</div>';
  try {
    const items = await api.shopping.list();
    renderShoppingList(items || []);
  } catch (err) {
    container.innerHTML = `<div class="no-tasks" style="color:#ef4444">Error: ${esc(err.message)}</div>`;
  }
}

function renderShoppingList(items) {
  const container = document.getElementById('shoppingList');
  if (!items.length) {
    container.innerHTML = '<div class="shopping-empty">Shopping list is empty. Add something!</div>';
    return;
  }

  // Group by category
  const groups = {};
  items.forEach(item => {
    const cat = item.category || 'other';
    if (!groups[cat]) groups[cat] = [];
    groups[cat].push(item);
  });

  // Sort categories
  const catOrder = ['produce', 'dairy', 'meat', 'bakery', 'frozen', 'beverages', 'household', 'other'];
  const sortedCats = Object.keys(groups).sort((a, b) => {
    const ia = catOrder.indexOf(a), ib = catOrder.indexOf(b);
    return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib);
  });

  container.innerHTML = sortedCats.map(cat => `
    <div class="shopping-cat-group">
      <div class="shopping-cat-label">${esc(cat)}</div>
      <div class="shopping-cat-items">
        ${groups[cat].map(item => `
          <div class="shopping-item" id="sitem-${item.id}">
            <input type="checkbox" class="shop-check" onchange="markPurchased(${item.id}, this)">
            <span class="shop-item-name">${esc(item.item)}</span>
            ${item.quantity ? `<span class="shop-item-qty">${esc(item.quantity)}</span>` : ''}
            <button class="shop-delete" onclick="deleteShoppingItem(${item.id})" title="Remove">×</button>
          </div>
        `).join('')}
      </div>
    </div>
  `).join('');
}

document.getElementById('addShoppingForm').addEventListener('submit', async e => {
  e.preventDefault();
  const name = document.getElementById('newItemName').value.trim();
  const qty = document.getElementById('newItemQty').value.trim();
  const cat = document.getElementById('newItemCategory').value;
  if (!name) return;

  try {
    const item = await api.shopping.add({
      item: name,
      quantity: qty,
      category: cat,
      added_by: 'helper',
    });
    document.getElementById('newItemName').value = '';
    document.getElementById('newItemQty').value = '';
    document.getElementById('newItemCategory').value = '';
    showToast(`"${name}" added to list`, 'success');
    loadShoppingList();
  } catch (err) {
    showToast(err.message, 'error');
  }
});

async function markPurchased(id, cb) {
  try {
    await api.shopping.purchased(id);
    const el = document.getElementById(`sitem-${id}`);
    if (el) {
      el.style.transition = 'opacity 0.3s';
      el.style.opacity = '0';
      setTimeout(() => { loadShoppingList(); }, 300);
    }
  } catch (err) {
    cb.checked = false;
    showToast(err.message, 'error');
  }
}

async function deleteShoppingItem(id) {
  try {
    await api.shopping.delete(id);
    const el = document.getElementById(`sitem-${id}`);
    if (el) {
      el.style.transition = 'opacity 0.3s';
      el.style.opacity = '0';
      setTimeout(() => { loadShoppingList(); }, 300);
    }
  } catch (err) {
    showToast(err.message, 'error');
  }
}

// ── Init ──────────────────────────────────────────────────────────
loadToday();