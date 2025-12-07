// Section navigation
let currentScheduleView = 'today';

function showSection(section) {
    document.querySelectorAll('.content-section').forEach(s => s.style.display = 'none');
    document.querySelectorAll('.nav-btn').forEach(btn => btn.classList.remove('active'));

    document.getElementById(`${section}-section`).style.display = 'block';
    event.target.classList.add('active');

    // Load data for the section
    if (section === 'schedule') {
        showScheduleView(currentScheduleView);
    } else if (section === 'shopping') {
        loadShoppingList();
    }
}

function showScheduleView(view) {
    currentScheduleView = view;

    // Hide all schedule views
    document.querySelectorAll('.schedule-view').forEach(v => v.style.display = 'none');
    document.querySelectorAll('.schedule-nav-btn').forEach(btn => btn.classList.remove('active'));

    // Show selected view
    document.getElementById(`${view}-view`).style.display = 'block';
    document.getElementById(`${view}-btn`).classList.add('active');

    // Load data for the view
    if (view === 'today') {
        loadTodaySchedule();
    } else if (view === 'thisweek') {
        loadWeekCalendar(0);
    } else if (view === 'nextweek') {
        loadWeekCalendar(1);
    }
}

// Initialize
document.addEventListener('DOMContentLoaded', () => {
    loadTodaySchedule();
    setupForms();
    updateTodayDate();
});

function updateTodayDate() {
    const today = new Date();
    const options = { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' };
    document.getElementById('today-date').textContent = today.toLocaleDateString('en-US', options);
}

function setupForms() {
    document.getElementById('shoppingForm').addEventListener('submit', addShoppingItem);
}

// TODAY'S SCHEDULE
function loadTodaySchedule() {
    fetch('/helper/api/schedule/today')
        .then(r => r.json())
        .then(data => {
            const container = document.getElementById('today-schedule');

            if (!data.tasks || data.tasks.length === 0) {
                container.innerHTML = '<p class="no-tasks">No tasks for today.</p>';
                return;
            }

            // Sort tasks: tasks with time first, then tasks without time
            const tasks = data.tasks.sort((a, b) => {
                if (a.time && !b.time) return -1;
                if (!a.time && b.time) return 1;
                if (a.time && b.time) return a.time.localeCompare(b.time);
                return 0;
            });

            // Separate tasks with time and without time
            const timedTasks = tasks.filter(t => t.time);
            const untimedTasks = tasks.filter(t => !t.time);

            let html = '<div class="today-tasks-container">';

            if (timedTasks.length > 0) {
                html += `
                    <div class="task-section">
                        <h3>Schedule:</h3>
                        ${timedTasks.map(task => renderCalendarTask(task)).join('')}
                    </div>
                `;
            }

            if (untimedTasks.length > 0) {
                html += `
                    <div class="task-section">
                        <h3>Cleaning (anytime):</h3>
                        ${untimedTasks.map(task => renderCalendarTask(task)).join('')}
                    </div>
                `;
            }

            html += '</div>';
            container.innerHTML = html;
        })
        .catch(err => {
            console.error('Error loading schedule:', err);
            document.getElementById('today-schedule').innerHTML = '<p>Error loading schedule.</p>';
        });
}

// CALENDAR FUNCTIONS
function loadWeekCalendar(weekOffset) {
    const today = new Date();
    const startDate = new Date(today);
    startDate.setDate(today.getDate() + (weekOffset * 7));

    // Get Monday of the week
    const dayOfWeek = startDate.getDay();
    const diff = startDate.getDate() - dayOfWeek + (dayOfWeek === 0 ? -6 : 1);
    startDate.setDate(diff);

    const containerId = weekOffset === 0 ? 'thisweek-calendar' : 'nextweek-calendar';

    // Load 7 days starting from Monday
    fetch(`/helper/api/schedule/upcoming?days=7&start_date=${startDate.toISOString().split('T')[0]}`)
        .then(r => r.json())
        .then(schedules => {
            const container = document.getElementById(containerId);

            if (!schedules || schedules.length === 0) {
                container.innerHTML = '<p>No schedules available.</p>';
                return;
            }

            container.innerHTML = schedules.map(schedule => {
                const date = new Date(schedule.date);
                const isToday = date.toDateString() === new Date().toDateString();
                const dateStr = date.toLocaleDateString('en-US', {
                    weekday: 'long',
                    day: 'numeric',
                    month: 'long'
                });

                const tasks = schedule.tasks ? schedule.tasks.sort((a, b) => {
                    // Sort: tasks with time first, then tasks without time
                    if (a.time && !b.time) return -1;
                    if (!a.time && b.time) return 1;
                    if (a.time && b.time) return a.time.localeCompare(b.time);
                    return 0;
                }) : [];

                // Separate tasks with time and without time
                const timedTasks = tasks.filter(t => t.time);
                const untimedTasks = tasks.filter(t => !t.time);

                return `
                    <div class="calendar-day ${isToday ? 'today' : ''}">
                        <div class="calendar-day-header">
                            ${dateStr}
                            ${isToday ? '<span class="today-badge">Today</span>' : ''}
                        </div>
                        <div class="calendar-day-content">
                            ${timedTasks.length > 0 ? `
                                <div class="task-section">
                                    <h4>Schedule:</h4>
                                    ${timedTasks.map(task => renderCalendarTask(task)).join('')}
                                </div>
                            ` : ''}
                            ${untimedTasks.length > 0 ? `
                                <div class="task-section">
                                    <h4>Cleaning (anytime):</h4>
                                    ${untimedTasks.map(task => renderCalendarTask(task)).join('')}
                                </div>
                            ` : ''}
                            ${tasks.length === 0 ? '<p class="no-tasks">No tasks</p>' : ''}
                        </div>
                    </div>
                `;
            }).join('');
        });
}

// Render a single task (for today's view)
function renderTask(task) {
    const typeClass = task.task_type || 'other';
    const completedClass = task.completed ? 'completed' : '';

    let description = task.description || '';
    if (task.recipe && task.recipe.name) {
        description = `<a href="#" onclick="showRecipe(${task.recipe.id}); return false;">${task.recipe.name}</a>`;
    } else if (task.zone && task.zone.name) {
        const zoneName = task.zone.description ? `${task.zone.name}: ${task.zone.description}` : task.zone.name;
        description = `<a href="#" onclick="showZone(${task.zone.id}); return false;" style="cursor: pointer;">${zoneName}</a>`;
    }

    // Format time display (with range for childcare)
    let timeDisplay = 'Anytime';
    if (task.time) {
        if (task.end_time && task.task_type === 'childcare') {
            timeDisplay = `${task.time} - ${task.end_time}`;
        } else {
            timeDisplay = task.time;
        }
    }

    return `
        <div class="task-item ${completedClass}">
            <div class="task-info">
                <div class="task-time">${timeDisplay}</div>
                <div class="task-title">${task.title}</div>
                <div class="task-description">${description}</div>
                <span class="task-type ${typeClass}">${task.task_type}</span>
                ${task.duration && task.task_type !== 'cleaning' ? `<span style="margin-left: 10px; font-size: 12px; color: #7f8c8d;">${task.duration} min</span>` : ''}
            </div>
            <div class="task-actions">
                ${task.completed ?
                    `<button class="btn" onclick="uncompleteTask(${task.id})">Cancel</button>` :
                    `<button class="btn btn-success" onclick="completeTask(${task.id})">Done</button>`
                }
            </div>
        </div>
    `;
}

// Render task for calendar view
function renderCalendarTask(task) {
    const typeClass = task.task_type || 'other';
    const typeIcon = {
        'meal': '🍽️',
        'cleaning': '🧹',
        'childcare': '👶'
    }[task.task_type] || '📋';

    let description = task.description || '';
    let clickable = false;
    let clickHandler = '';

    if (task.recipe && task.recipe.name) {
        description = task.recipe.name;
        clickable = true;
        clickHandler = `showRecipe(${task.recipe.id})`;
    } else if (task.zone && task.zone.name) {
        // Show zone description if available, otherwise just the name
        description = task.zone.description ? `${task.zone.name}: ${task.zone.description}` : task.zone.name;
        clickable = true;
        clickHandler = `showZone(${task.zone.id})`;
    }

    // Format time display (with range for childcare)
    let timeDisplay = '';
    if (task.time) {
        if (task.end_time && task.task_type === 'childcare') {
            timeDisplay = `${task.time} - ${task.end_time}`;
        } else {
            timeDisplay = task.time;
        }
    }

    const taskContent = `
        <span class="task-icon">${typeIcon}</span>
        ${timeDisplay ? `<span class="task-time">${timeDisplay}</span>` : ''}
        <span class="task-title">${description || task.title}</span>
        ${task.duration && task.task_type !== 'cleaning' ? `<span class="task-duration">${task.duration} min</span>` : ''}
    `;

    if (clickable) {
        return `
            <div class="calendar-task ${typeClass}" onclick="${clickHandler}" style="cursor: pointer;">
                ${taskContent}
            </div>
        `;
    } else {
        return `
            <div class="calendar-task ${typeClass}">
                ${taskContent}
            </div>
        `;
    }
}

// Complete/Uncomplete tasks
function completeTask(taskId) {
    fetch(`/helper/api/tasks/${taskId}/complete`, {method: 'POST'})
        .then(() => {
            loadTodaySchedule();
        });
}

function uncompleteTask(taskId) {
    fetch(`/helper/api/tasks/${taskId}/uncomplete`, {method: 'POST'})
        .then(() => {
            loadTodaySchedule();
        });
}

// SHOPPING LIST
function loadShoppingList() {
    fetch('/helper/api/shopping')
        .then(r => r.json())
        .then(items => {
            const container = document.getElementById('shopping-list');
            
            if (!items || items.length === 0) {
                container.innerHTML = '<p>Shopping list is empty.</p>';
                return;
            }
            
            // Group by category
            const grouped = items.reduce((acc, item) => {
                const cat = item.category || 'other';
                if (!acc[cat]) acc[cat] = [];
                acc[cat].push(item);
                return acc;
            }, {});
            
            container.innerHTML = Object.keys(grouped).map(category => `
                <div style="margin-bottom: 20px;">
                    <h3 style="text-transform: capitalize; color: #3498db; margin-bottom: 10px;">${category}</h3>
                    ${grouped[category].map(item => `
                        <div class="shopping-item ${item.purchased ? 'purchased' : ''}">
                            <div class="shopping-item-info">
                                <div class="shopping-item-name">${item.item}</div>
                                <div class="shopping-item-quantity">${item.quantity || ''}</div>
                            </div>
                            <div style="display: flex; gap: 10px;">
                                ${!item.purchased ? 
                                    `<button class="btn btn-success" onclick="markPurchased(${item.id})">✓</button>` : 
                                    ''
                                }
                                <button class="btn btn-danger" onclick="deleteShoppingItem(${item.id})">Delete</button>
                            </div>
                        </div>
                    `).join('')}
                </div>
            `).join('');
        });
}

function showShoppingForm() {
    document.getElementById('shopping-form').style.display = 'block';
    document.getElementById('shoppingForm').reset();
}

function hideShoppingForm() {
    document.getElementById('shopping-form').style.display = 'none';
}

function addShoppingItem(e) {
    e.preventDefault();

    const data = {
        item: document.getElementById('shopping-item').value,
        quantity: document.getElementById('shopping-quantity').value,
        category: 'other'
    };

    fetch('/helper/api/shopping', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(data)
    })
    .then(() => {
        hideShoppingForm();
        loadShoppingList();
    });
}

function markPurchased(itemId) {
    fetch(`/helper/api/shopping/${itemId}/purchased`, {method: 'POST'})
        .then(() => loadShoppingList());
}

function deleteShoppingItem(itemId) {
    if (confirm('Delete this item?')) {
        fetch(`/helper/api/shopping/${itemId}`, {method: 'DELETE'})
            .then(() => loadShoppingList());
    }
}

// RECIPE MODAL
function showRecipe(recipeId) {
    fetch(`/helper/api/recipes/${recipeId}`)
        .then(r => r.json())
        .then(recipe => {
            const modal = document.getElementById('recipe-modal');
            const details = document.getElementById('recipe-details');

            // Render star rating
            const rating = recipe.rating || 0;
            const fullStars = Math.floor(rating);
            let starsHtml = '<div style="font-size: 24px; color: #f39c12; margin: 10px 0;">';
            for (let i = 1; i <= 5; i++) {
                starsHtml += i <= fullStars ? '★' : '☆';
            }
            starsHtml += ` <span style="font-size: 16px; color: #7f8c8d;">(${rating.toFixed(1)})</span></div>`;

            // Load comments
            fetch(`/admin/api/recipes/${recipeId}/comments`)
                .then(r => r.json())
                .then(comments => {
                    let commentsHtml = '';
                    if (comments && comments.length > 0) {
                        commentsHtml = '<h3 style="margin-top: 20px;">Comments</h3>';
                        commentsHtml += '<div style="background: #f8f9fa; padding: 15px; border-radius: 5px; margin-top: 10px;">';
                        comments.forEach(comment => {
                            const date = new Date(comment.created_at).toLocaleDateString();
                            commentsHtml += `
                                <div style="margin-bottom: 15px; padding-bottom: 15px; border-bottom: 1px solid #ddd;">
                                    <p style="margin: 0; color: #2c3e50;">${comment.comment}</p>
                                    <small style="color: #7f8c8d;">${date}</small>
                                </div>
                            `;
                        });
                        commentsHtml += '</div>';
                    }

                    details.innerHTML = `
                        <h2>${recipe.name}</h2>
                        ${starsHtml}

                        ${recipe.image_url ? `<img src="${recipe.image_url}" alt="${recipe.name}" style="max-width: 100%; height: auto; border-radius: 8px; margin: 15px 0;">` : ''}

                        <p><strong>Category:</strong> ${recipe.category || 'N/A'} | <strong>For:</strong> ${recipe.family_member || 'all'}</p>

                        ${recipe.description ? `<p style="margin: 15px 0;">${recipe.description}</p>` : ''}

                        ${recipe.video_url ? `
                            <h3 style="margin-top: 20px;">Video</h3>
                            <div style="position: relative; padding-bottom: 56.25%; height: 0; overflow: hidden; max-width: 100%; margin: 15px 0;">
                                <iframe src="${recipe.video_url}"
                                        style="position: absolute; top: 0; left: 0; width: 100%; height: 100%; border: 0;"
                                        allowfullscreen></iframe>
                            </div>
                        ` : ''}

                        <h3 style="margin-top: 20px;">Ingredients</h3>
                        <pre style="white-space: pre-wrap; background: #f8f9fa; padding: 15px; border-radius: 5px;">${recipe.ingredients || 'No ingredients listed'}</pre>

                        <h3 style="margin-top: 20px;">Instructions</h3>
                        <pre style="white-space: pre-wrap; background: #f8f9fa; padding: 15px; border-radius: 5px;">${recipe.instructions || 'No instructions'}</pre>

                        ${commentsHtml}

                        ${recipe.tags ? `<p style="margin-top: 20px;"><strong>Tags:</strong> ${recipe.tags}</p>` : ''}
                    `;

                    modal.style.display = 'flex';
                });
        });
}

function closeRecipeModal() {
    document.getElementById('recipe-modal').style.display = 'none';
}

// CLEANING ZONE MODAL
function showZone(zoneId) {
    fetch(`/admin/api/zones/${zoneId}`)
        .then(r => r.json())
        .then(zone => {
            const modal = document.getElementById('zone-modal');
            const details = document.getElementById('zone-details');

            // Handle both old numeric priority and new string priority
            let priority = zone.priority;
            if (!isNaN(priority)) {
                // Convert old numeric priority to new string format
                const num = parseInt(priority);
                if (num <= 3) priority = 'high';
                else if (num <= 7) priority = 'medium';
                else priority = 'low';
            }

            const priorityLabel = priority ? priority.charAt(0).toUpperCase() + priority.slice(1) : 'Medium';
            const priorityColor = {
                'high': '#e74c3c',
                'medium': '#f39c12',
                'low': '#3498db'
            }[priority.toLowerCase()] || '#f39c12';

            details.innerHTML = `
                <h2>🧹 ${zone.name}</h2>

                <div style="background: #f8f9fa; padding: 15px; border-radius: 8px; margin: 15px 0;">
                    <p style="margin: 5px 0;"><strong>Priority:</strong> <span style="color: ${priorityColor}; font-weight: bold;">${priorityLabel}</span></p>
                    <p style="margin: 5px 0;"><strong>Frequency:</strong> ${zone.frequency_per_week}x per week</p>
                </div>

                ${zone.description ? `
                    <h3 style="margin-top: 20px;">Description</h3>
                    <p style="background: #f8f9fa; padding: 15px; border-radius: 5px; white-space: pre-wrap;">${zone.description}</p>
                ` : ''}
            `;

            modal.style.display = 'flex';
        })
        .catch(err => {
            console.error('Error loading zone:', err);
            alert('Failed to load zone details');
        });
}

function closeZoneModal() {
    document.getElementById('zone-modal').style.display = 'none';
}

// Close modal when clicking outside
window.onclick = function(event) {
    const recipeModal = document.getElementById('recipe-modal');
    const zoneModal = document.getElementById('zone-modal');
    if (event.target === recipeModal) {
        closeRecipeModal();
    } else if (event.target === zoneModal) {
        closeZoneModal();
    }
}

