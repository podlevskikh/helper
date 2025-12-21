// Section navigation
function showSection(section, event) {
    document.querySelectorAll('.content-section').forEach(s => s.style.display = 'none');
    document.querySelectorAll('.nav-btn').forEach(btn => btn.classList.remove('active'));

    document.getElementById(`${section}-section`).style.display = 'block';
    if (event && event.target) {
        event.target.classList.add('active');
    }

    // Load data for the section
    if (section === 'recipes') loadRecipes();
    else if (section === 'mealtimes') loadMealTimes();
    else if (section === 'zones') loadZones();
    else if (section === 'childcare') loadChildcare();
    else if (section === 'calendar') loadAdminCalendar();
    else if (section === 'shopping') loadAdminShoppingList();
}

// Global variables
let allMealTimes = [];

// Initialize
document.addEventListener('DOMContentLoaded', () => {
    loadMealTimesForFilters(); // Load meal times first
    loadRecipes();
    setupForms();
});

// Setup form handlers
function setupForms() {
    document.getElementById('recipeForm').addEventListener('submit', saveRecipe);
    document.getElementById('mealtimeForm').addEventListener('submit', saveMealTime);
    document.getElementById('zoneForm').addEventListener('submit', saveZone);
    document.getElementById('childcareForm').addEventListener('submit', saveChildcare);

    // Setup star rating
    setupStarRating();
}

function setupStarRating() {
    const stars = document.querySelectorAll('#star-rating-input .star');
    const ratingInput = document.getElementById('recipe-rating');

    stars.forEach((star, index) => {
        star.addEventListener('click', () => {
            const value = index + 1;
            ratingInput.value = value;
            updateStarDisplay(value);
        });

        star.addEventListener('mouseenter', () => {
            updateStarDisplay(index + 1, true);
        });
    });

    document.getElementById('star-rating-input').addEventListener('mouseleave', () => {
        updateStarDisplay(parseFloat(ratingInput.value) || 0);
    });
}

function updateStarDisplay(rating, isHover = false) {
    const stars = document.querySelectorAll('#star-rating-input .star');
    stars.forEach((star, index) => {
        star.classList.remove('filled', 'hover');
        if (index < rating) {
            star.classList.add(isHover ? 'hover' : 'filled');
        }
    });
}

// MEAL TIMES HELPERS
function loadMealTimesForFilters() {
    fetch('/admin/api/mealtimes')
        .then(r => r.json())
        .then(mealTimes => {
            allMealTimes = mealTimes.filter(mt => mt.active);
            populateMealTypeFilters();
            populateMealTypeCheckboxes();
        })
        .catch(err => console.error('Error loading meal times:', err));
}

function populateMealTypeFilters() {
    // Populate main filter
    const filterSelect = document.getElementById('filter-meal-type');
    if (filterSelect) {
        filterSelect.innerHTML = '<option value="">All Meal Types</option>' +
            allMealTimes.map(mt => `<option value="${mt.id}">${mt.name}</option>`).join('');
    }

    // Populate modal filter
    const modalSelect = document.getElementById('modal-recipe-meal-type');
    if (modalSelect) {
        modalSelect.innerHTML = '<option value="">All Meal Types</option>' +
            allMealTimes.map(mt => `<option value="${mt.id}">${mt.name}</option>`).join('');
    }
}

function populateMealTypeCheckboxes() {
    const container = document.getElementById('recipe-meal-types');
    if (!container) return;

    container.innerHTML = allMealTimes.map(mt => `
        <label class="checkbox-label">
            <input type="checkbox" name="meal-type" value="${mt.id}" data-name="${mt.name}">
            ${mt.name}
        </label>
    `).join('');
}

// RECIPES
let allRecipes = [];

function loadRecipes() {
    fetch('/admin/api/recipes')
        .then(r => r.json())
        .then(recipes => {
            allRecipes = recipes;
            displayRecipes(recipes);
        });
}

function displayRecipes(recipes) {
    const list = document.getElementById('recipes-list');
    list.innerHTML = recipes.map(recipe => {
        const stars = renderStars(recipe.rating || 0);
        const mealTypes = recipe.meal_times && recipe.meal_times.length > 0
            ? recipe.meal_times.map(mt => mt.name).join(', ')
            : (recipe.category || 'N/A'); // Fallback to old category field
        return `
            <div class="recipe-card">
                <div class="recipe-card-info">
                    <div class="recipe-card-title">${recipe.name}</div>
                    <div class="recipe-card-meta">
                        <span>${stars}</span>
                        <span>🍽️ ${mealTypes}</span>
                        <span>👤 ${recipe.family_member || 'all'}</span>
                    </div>
                </div>
                <div class="recipe-card-actions">
                    <button class="btn" onclick="editRecipe(${recipe.id})">Edit</button>
                    <button class="btn btn-danger" onclick="deleteRecipe(${recipe.id})">Delete</button>
                </div>
            </div>
        `;
    }).join('');
}

function renderStars(rating) {
    const fullStars = Math.floor(rating);
    let html = '<span class="star-rating-display">';
    for (let i = 1; i <= 5; i++) {
        html += `<span class="star ${i <= fullStars ? 'filled' : ''}">★</span>`;
    }
    html += '</span>';
    return html;
}

function filterRecipes() {
    const search = document.getElementById('filter-search').value.toLowerCase();
    const mealTypeId = document.getElementById('filter-meal-type').value;
    const family = document.getElementById('filter-family').value;
    const minRating = parseFloat(document.getElementById('filter-rating').value);

    const filtered = allRecipes.filter(recipe => {
        const matchSearch = !search || recipe.name.toLowerCase().includes(search);

        // Check if recipe has this meal type (either in meal_times array or old category field)
        const matchMealType = !mealTypeId ||
            (recipe.meal_times && recipe.meal_times.some(mt => mt.id == mealTypeId)) ||
            (recipe.category && allMealTimes.find(mt => mt.id == mealTypeId && mt.name === recipe.category));

        const matchFamily = !family || recipe.family_member === family;
        const matchRating = !minRating || (recipe.rating || 0) >= minRating;

        return matchSearch && matchMealType && matchFamily && matchRating;
    });

    displayRecipes(filtered);
}

function showRecipeForm() {
    document.getElementById('recipe-form').style.display = 'block';
    document.getElementById('recipe-form-title').textContent = 'Add Recipe';
    document.getElementById('recipeForm').reset();
    document.getElementById('recipe-id').value = '';
    document.getElementById('recipe-comments-section').style.display = 'none';
    updateStarDisplay(0);

    // Uncheck all meal type checkboxes
    document.querySelectorAll('#recipe-meal-types input[type="checkbox"]').forEach(cb => cb.checked = false);
}

function hideRecipeForm() {
    document.getElementById('recipe-form').style.display = 'none';
    document.getElementById('recipe-image-file').value = '';
    document.getElementById('current-image-preview').innerHTML = '';
}

function editRecipe(id) {
    fetch(`/admin/api/recipes/${id}`)
        .then(r => r.json())
        .then(recipe => {
            document.getElementById('recipe-id').value = recipe.id;
            document.getElementById('recipe-name').value = recipe.name;
            document.getElementById('recipe-description').value = recipe.description || '';
            document.getElementById('recipe-ingredients').value = recipe.ingredients || '';
            document.getElementById('recipe-instructions').value = recipe.instructions || '';
            document.getElementById('recipe-family-member').value = recipe.family_member || 'all';
            document.getElementById('recipe-tags').value = recipe.tags || '';
            document.getElementById('recipe-image-url').value = recipe.image_url || '';
            document.getElementById('recipe-video-url').value = recipe.video_url || '';
            document.getElementById('recipe-rating').value = recipe.rating || 0;
            updateStarDisplay(recipe.rating || 0);

            // Set meal type checkboxes
            const checkboxes = document.querySelectorAll('#recipe-meal-types input[type="checkbox"]');
            checkboxes.forEach(cb => cb.checked = false);

            if (recipe.meal_times && recipe.meal_times.length > 0) {
                recipe.meal_times.forEach(mt => {
                    const checkbox = document.querySelector(`#recipe-meal-types input[value="${mt.id}"]`);
                    if (checkbox) checkbox.checked = true;
                });
            } else if (recipe.category) {
                // Fallback: try to match old category to meal time name
                const matchingMealTime = allMealTimes.find(mt => mt.name === recipe.category);
                if (matchingMealTime) {
                    const checkbox = document.querySelector(`#recipe-meal-types input[value="${matchingMealTime.id}"]`);
                    if (checkbox) checkbox.checked = true;
                }
            }

            // Show current image preview
            const imagePreview = document.getElementById('current-image-preview');
            if (recipe.image_url) {
                imagePreview.innerHTML = `<img src="${recipe.image_url}" alt="Current image" style="max-width: 200px; max-height: 200px; border-radius: 4px;">`;
            } else {
                imagePreview.innerHTML = '';
            }

            document.getElementById('recipe-form-title').textContent = 'Edit Recipe';
            document.getElementById('recipe-form').style.display = 'block';

            // Load and show comments
            loadRecipeComments(id);
            document.getElementById('recipe-comments-section').style.display = 'block';
        });
}

function saveRecipe(e) {
    e.preventDefault();

    const id = document.getElementById('recipe-id').value;
    const imageFile = document.getElementById('recipe-image-file').files[0];

    // If there's a new image file, upload it first
    if (imageFile) {
        const formData = new FormData();
        formData.append('image', imageFile);

        fetch('/admin/api/recipes/upload-image', {
            method: 'POST',
            body: formData
        })
        .then(r => r.json())
        .then(result => {
            if (result.url) {
                document.getElementById('recipe-image-url').value = result.url;
            }
            saveRecipeData(id);
        })
        .catch(err => {
            console.error('Error uploading image:', err);
            alert('Failed to upload image');
        });
    } else {
        saveRecipeData(id);
    }
}

function saveRecipeData(id) {
    // Get selected meal type IDs from checkboxes
    const selectedMealTypes = Array.from(
        document.querySelectorAll('#recipe-meal-types input[type="checkbox"]:checked')
    ).map(cb => parseInt(cb.value));

    const data = {
        name: document.getElementById('recipe-name').value,
        description: document.getElementById('recipe-description').value,
        ingredients: document.getElementById('recipe-ingredients').value,
        instructions: document.getElementById('recipe-instructions').value,
        meal_time_ids: selectedMealTypes, // Send array of meal time IDs
        family_member: document.getElementById('recipe-family-member').value,
        tags: document.getElementById('recipe-tags').value,
        image_url: document.getElementById('recipe-image-url').value,
        video_url: document.getElementById('recipe-video-url').value,
        rating: parseFloat(document.getElementById('recipe-rating').value) || 0
    };

    const url = id ? `/admin/api/recipes/${id}` : '/admin/api/recipes';
    const method = id ? 'PUT' : 'POST';

    fetch(url, {
        method: method,
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(data)
    })
    .then(r => r.json())
    .then(() => {
        hideRecipeForm();
        loadRecipes();
    });
}

function deleteRecipe(id) {
    if (confirm('Delete this recipe?')) {
        fetch(`/admin/api/recipes/${id}`, {method: 'DELETE'})
            .then(() => loadRecipes());
    }
}

// MEAL TIMES
function loadMealTimes() {
    fetch('/admin/api/mealtimes')
        .then(r => r.json())
        .then(mealtimes => {
            const list = document.getElementById('mealtimes-list');
            list.innerHTML = mealtimes.map(mt => `
                <div class="list-item">
                    <h3>${mt.name} - ${mt.default_time}</h3>
                    <p><strong>For:</strong> ${mt.family_member} | <strong>Active:</strong> ${mt.active ? 'Yes' : 'No'}</p>
                    <div class="actions">
                        <button class="btn" onclick="editMealTime(${mt.id})">Edit</button>
                        <button class="btn btn-danger" onclick="deleteMealTime(${mt.id})">Delete</button>
                    </div>
                </div>
            `).join('');
        });
}

function showMealTimeForm() {
    document.getElementById('mealtime-form').style.display = 'block';
    document.getElementById('mealtime-form-title').textContent = 'Add Meal Time';
    document.getElementById('mealtimeForm').reset();
    document.getElementById('mealtime-id').value = '';
}

function hideMealTimeForm() {
    document.getElementById('mealtime-form').style.display = 'none';
}

function editMealTime(id) {
    fetch(`/admin/api/mealtimes/${id}`)
        .then(r => r.json())
        .then(mt => {
            document.getElementById('mealtime-id').value = mt.id;
            document.getElementById('mealtime-name').value = mt.name;
            document.getElementById('mealtime-time').value = mt.default_time;
            document.getElementById('mealtime-family-member').value = mt.family_member;
            document.getElementById('mealtime-active').checked = mt.active;

            document.getElementById('mealtime-form-title').textContent = 'Edit Meal Time';
            const form = document.getElementById('mealtime-form');
            form.style.display = 'block';
            form.scrollIntoView({ behavior: 'smooth', block: 'start' });
        });
}

function saveMealTime(e) {
    e.preventDefault();

    const id = document.getElementById('mealtime-id').value;
    const data = {
        name: document.getElementById('mealtime-name').value,
        default_time: document.getElementById('mealtime-time').value,
        family_member: document.getElementById('mealtime-family-member').value,
        active: document.getElementById('mealtime-active').checked
    };

    const url = id ? `/admin/api/mealtimes/${id}` : '/admin/api/mealtimes';
    const method = id ? 'PUT' : 'POST';

    fetch(url, {
        method: method,
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(data)
    })
    .then(() => {
        hideMealTimeForm();
        loadMealTimes();
        // Regenerate schedule to include updated meal times
        fetch('/admin/api/regenerate-schedule', {method: 'POST'})
            .then(r => r.json())
            .then(data => {
                console.log('Schedule regenerated:', data.message);
                if (id) {
                    alert('Meal time updated and schedule refreshed!');
                } else {
                    alert('Meal time created and schedule updated!');
                }
            });
    });
}

function deleteMealTime(id) {
    if (confirm('Delete this meal time?')) {
        fetch(`/admin/api/mealtimes/${id}`, {method: 'DELETE'})
            .then(() => loadMealTimes());
    }
}

// CLEANING ZONES
function loadZones() {
    fetch('/admin/api/zones')
        .then(r => r.json())
        .then(zones => {
            const list = document.getElementById('zones-list');
            list.innerHTML = zones.map(zone => {
                // Handle both old numeric priority and new string priority
                let priority = zone.priority;
                if (!isNaN(priority)) {
                    const num = parseInt(priority);
                    if (num <= 3) priority = 'high';
                    else if (num <= 7) priority = 'medium';
                    else priority = 'low';
                }
                const priorityLabel = priority ? priority.charAt(0).toUpperCase() + priority.slice(1) : 'Medium';
                return `
                    <div class="list-item">
                        <h3>${zone.name}</h3>
                        <p>${zone.description || ''}</p>
                        <p><strong>Frequency:</strong> ${zone.frequency_per_week}x/week | <strong>Priority:</strong> ${priorityLabel}</p>
                        <div class="actions">
                            <button class="btn" onclick="editZone(${zone.id})">Edit</button>
                            <button class="btn btn-danger" onclick="deleteZone(${zone.id})">Delete</button>
                        </div>
                    </div>
                `;
            }).join('');
        });
}

function showZoneForm() {
    document.getElementById('zone-form').style.display = 'block';
    document.getElementById('zone-form-title').textContent = 'Add Cleaning Zone';
    document.getElementById('zoneForm').reset();
    document.getElementById('zone-id').value = '';
}

function hideZoneForm() {
    document.getElementById('zone-form').style.display = 'none';
}

function editZone(id) {
    fetch(`/admin/api/zones/${id}`)
        .then(r => r.json())
        .then(zone => {
            document.getElementById('zone-id').value = zone.id;
            document.getElementById('zone-name').value = zone.name;
            document.getElementById('zone-description').value = zone.description || '';
            document.getElementById('zone-frequency').value = zone.frequency_per_week;

            // Handle both old numeric priority and new string priority
            let priority = zone.priority || 'medium';
            if (!isNaN(priority)) {
                const num = parseInt(priority);
                if (num <= 3) priority = 'high';
                else if (num <= 7) priority = 'medium';
                else priority = 'low';
            }
            document.getElementById('zone-priority').value = priority;

            document.getElementById('zone-form-title').textContent = 'Edit Cleaning Zone';
            const form = document.getElementById('zone-form');
            form.style.display = 'block';
            form.scrollIntoView({ behavior: 'smooth', block: 'start' });
        });
}

function saveZone(e) {
    e.preventDefault();

    const id = document.getElementById('zone-id').value;
    const data = {
        name: document.getElementById('zone-name').value,
        description: document.getElementById('zone-description').value,
        frequency_per_week: parseInt(document.getElementById('zone-frequency').value),
        priority: document.getElementById('zone-priority').value
    };

    const url = id ? `/admin/api/zones/${id}` : '/admin/api/zones';
    const method = id ? 'PUT' : 'POST';

    fetch(url, {
        method: method,
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(data)
    })
    .then(() => {
        hideZoneForm();
        loadZones();
        // Regenerate schedule to include new/updated zone
        fetch('/admin/api/regenerate-schedule', {method: 'POST'})
            .then(r => r.json())
            .then(data => {
                console.log('Schedule regenerated:', data.message);
                if (id) {
                    alert('Zone updated and schedule refreshed!');
                } else {
                    alert('Zone created and schedule updated!');
                }
            });
    });
}

function deleteZone(id) {
    if (confirm('Delete this zone?')) {
        fetch(`/admin/api/zones/${id}`, {method: 'DELETE'})
            .then(() => loadZones());
    }
}

// CHILDCARE
function loadChildcare() {
    fetch('/admin/api/childcare')
        .then(r => r.json())
        .then(schedules => {
            const list = document.getElementById('childcare-list');
            list.innerHTML = schedules.map(cc => `
                <div class="list-item">
                    <h3>${new Date(cc.date).toLocaleDateString()}</h3>
                    <p><strong>Time:</strong> ${cc.start_time} - ${cc.end_time}</p>
                    <p>${cc.notes || ''}</p>
                    <div class="actions">
                        <button class="btn" onclick="editChildcare(${cc.id})">Edit</button>
                        <button class="btn btn-danger" onclick="deleteChildcare(${cc.id})">Delete</button>
                    </div>
                </div>
            `).join('');
        });
}

function showChildcareForm() {
    document.getElementById('childcare-form').style.display = 'block';
    document.getElementById('childcare-form-title').textContent = 'Add Childcare Time';
    document.getElementById('childcareForm').reset();
    document.getElementById('childcare-id').value = '';
    document.getElementById('childcare-date').value = new Date().toISOString().split('T')[0];
}

function hideChildcareForm() {
    document.getElementById('childcare-form').style.display = 'none';
}

function editChildcare(id) {
    fetch(`/admin/api/childcare/${id}`)
        .then(r => r.json())
        .then(cc => {
            document.getElementById('childcare-id').value = cc.id;
            document.getElementById('childcare-date').value = new Date(cc.date).toISOString().split('T')[0];
            document.getElementById('childcare-start').value = cc.start_time;
            document.getElementById('childcare-end').value = cc.end_time;
            document.getElementById('childcare-notes').value = cc.notes || '';

            document.getElementById('childcare-form-title').textContent = 'Edit Childcare Time';
            document.getElementById('childcare-form').style.display = 'block';
        });
}

function saveChildcare(e) {
    e.preventDefault();

    const id = document.getElementById('childcare-id').value;
    const data = {
        date: document.getElementById('childcare-date').value + 'T00:00:00Z',
        start_time: document.getElementById('childcare-start').value,
        end_time: document.getElementById('childcare-end').value,
        notes: document.getElementById('childcare-notes').value
    };

    const url = id ? `/admin/api/childcare/${id}` : '/admin/api/childcare';
    const method = id ? 'PUT' : 'POST';

    fetch(url, {
        method: method,
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(data)
    })
    .then(() => {
        hideChildcareForm();
        loadChildcare();
    });
}

function deleteChildcare(id) {
    if (confirm('Delete this childcare schedule?')) {
        fetch(`/admin/api/childcare/${id}`, {method: 'DELETE'})
            .then(() => loadChildcare());
    }
}

// Schedule regeneration
function regenerateSchedule() {
    if (confirm('Regenerate schedule for the next 7 days? This will delete existing schedules and create new ones.')) {
        // Show loading indicator
        const originalText = event && event.target ? event.target.textContent : '';
        if (event && event.target) {
            event.target.textContent = 'Regenerating...';
            event.target.disabled = true;
        }

        fetch('/admin/api/regenerate-schedule', {method: 'POST'})
            .then(r => {
                if (!r.ok) {
                    throw new Error(`HTTP error! status: ${r.status}`);
                }
                return r.json();
            })
            .then(data => {
                alert(data.message || 'Schedule regenerated successfully!');
                console.log('Schedule regeneration response:', data);
            })
            .catch(error => {
                console.error('Error regenerating schedule:', error);
                alert('Error regenerating schedule: ' + error.message);
            })
            .finally(() => {
                // Restore button state
                if (event && event.target) {
                    event.target.textContent = originalText;
                    event.target.disabled = false;
                }
            });
    }
}

// SHOPPING LIST (ADMIN)
function loadAdminShoppingList() {
    fetch('/helper/api/shopping')
        .then(r => r.json())
        .then(items => {
            const list = document.getElementById('admin-shopping-list');

            if (!items || items.length === 0) {
                list.innerHTML = '<p>Список покупок пуст.</p>';
                return;
            }

            // Filter only unpurchased items
            const unpurchasedItems = items.filter(item => !item.purchased);

            if (unpurchasedItems.length === 0) {
                list.innerHTML = '<p>Все покупки отмечены! ✓</p>';
                return;
            }

            list.innerHTML = unpurchasedItems.map(item => `
                <div class="shopping-checklist-item">
                    <label class="checkbox-label">
                        <input type="checkbox" onchange="adminMarkPurchased(${item.id})">
                        <span class="item-text">
                            <strong>${item.item}</strong>
                            ${item.quantity ? `<span class="item-quantity">${item.quantity}</span>` : ''}
                        </span>
                    </label>
                </div>
            `).join('');
        });
}

function adminMarkPurchased(itemId) {
    fetch(`/helper/api/shopping/${itemId}/purchased`, {method: 'POST'})
        .then(() => {
            // Remove the item from the list with animation
            loadAdminShoppingList();
        });
}

// RECIPE COMMENTS
function loadRecipeComments(recipeId) {
    fetch(`/admin/api/recipes/${recipeId}/comments`)
        .then(r => r.json())
        .then(comments => {
            const list = document.getElementById('recipe-comments-list');

            if (!comments || comments.length === 0) {
                list.innerHTML = '<p style="color: #7f8c8d;">No comments yet.</p>';
                return;
            }

            list.innerHTML = comments.map(comment => `
                <div class="comment-item" style="padding: 10px; border-bottom: 1px solid #ecf0f1; margin-bottom: 10px;">
                    <div style="font-size: 12px; color: #7f8c8d; margin-bottom: 5px;">
                        ${new Date(comment.created_at).toLocaleString()}
                    </div>
                    <div>${comment.comment}</div>
                    <button onclick="deleteRecipeComment(${comment.id})" class="btn" style="margin-top: 5px; font-size: 12px;">Delete</button>
                </div>
            `).join('');
        });
}

function addRecipeComment() {
    const recipeId = document.getElementById('recipe-id').value;
    const comment = document.getElementById('new-comment').value.trim();

    if (!comment || !recipeId) {
        alert('Please enter a comment');
        return;
    }

    fetch(`/admin/api/recipes/${recipeId}/comments`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({comment: comment})
    })
    .then(() => {
        document.getElementById('new-comment').value = '';
        loadRecipeComments(recipeId);
    });
}

function deleteRecipeComment(commentId) {
    if (confirm('Delete this comment?')) {
        const recipeId = document.getElementById('recipe-id').value;
        fetch(`/admin/api/comments/${commentId}`, {method: 'DELETE'})
            .then(() => loadRecipeComments(recipeId));
    }
}

// CALENDAR FUNCTIONS

let currentAdminCalendarView = 'thisweek';
let currentTaskForRecipeSelection = null;
let allRecipesForModal = [];

function loadAdminCalendar() {
    showAdminCalendarView('thisweek');
}

function showAdminCalendarView(view) {
    currentAdminCalendarView = view;

    // Hide all calendar views
    document.querySelectorAll('#calendar-section .schedule-view').forEach(v => v.style.display = 'none');
    document.querySelectorAll('#calendar-section .schedule-nav-btn').forEach(btn => btn.classList.remove('active'));

    // Show selected view
    document.getElementById(`admin-${view}-view`).style.display = 'block';
    document.getElementById(`admin-${view}-btn`).classList.add('active');

    // Load data for the view
    if (view === 'thisweek') {
        loadAdminWeekCalendar(0);
    } else if (view === 'nextweek') {
        loadAdminWeekCalendar(1);
    }
}

function loadAdminWeekCalendar(weekOffset) {
    const today = new Date();
    const startDate = new Date(today);
    startDate.setDate(today.getDate() + (weekOffset * 7));

    // Get Monday of the week
    const dayOfWeek = startDate.getDay();
    const diff = startDate.getDate() - dayOfWeek + (dayOfWeek === 0 ? -6 : 1);
    startDate.setDate(diff);

    const containerId = weekOffset === 0 ? 'admin-thisweek-calendar' : 'admin-nextweek-calendar';

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
                    if (a.time && !b.time) return -1;
                    if (!a.time && b.time) return 1;
                    if (a.time && b.time) return a.time.localeCompare(b.time);
                    return 0;
                }) : [];

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
                                    ${timedTasks.map(task => renderAdminCalendarTask(task, date)).join('')}
                                </div>
                            ` : ''}
                            ${untimedTasks.length > 0 ? `
                                <div class="task-section">
                                    <h4>Cleaning (anytime):</h4>
                                    ${untimedTasks.map(task => renderAdminCalendarTask(task, date)).join('')}
                                </div>
                            ` : ''}
                            ${tasks.length === 0 ? '<p class="no-tasks">No tasks</p>' : ''}
                        </div>
                    </div>
                `;
            }).join('');
        });
}

function renderAdminCalendarTask(task, date) {
    const typeClass = task.task_type || 'other';

    let timeDisplay = 'Anytime';
    if (task.time) {
        if (task.end_time && task.task_type === 'childcare') {
            timeDisplay = `${task.time} - ${task.end_time}`;
        } else {
            timeDisplay = task.time;
        }
    }

    let description = task.description || '';

    // For meal tasks, show recipes
    if (task.task_type === 'meal') {
        const recipes = task.recipes || [];
        if (recipes.length > 0) {
            description = recipes.map(r => r.name).join(', ');
        } else if (task.recipe && task.recipe.name) {
            description = task.recipe.name;
        }

        // Make meal tasks clickable to manage recipes
        return `
            <div class="calendar-task ${typeClass}" onclick="openRecipeSelectionModal(${task.id}, '${task.title}', '${date.toISOString()}')" style="cursor: pointer;">
                <div class="task-time">${timeDisplay}</div>
                <div class="task-title">${task.title}</div>
                ${description ? `<div class="task-description">${description}</div>` : ''}
            </div>
        `;
    } else if (task.task_type === 'cleaning') {
        // For cleaning tasks, show only zone names
        const zones = task.zones || [];
        if (zones.length > 0) {
            description = zones.map(z => z.name).join(', ');
        } else if (task.zone && task.zone.name) {
            description = task.zone.name;
        }

        // Make cleaning tasks clickable to manage zones
        return `
            <div class="calendar-task ${typeClass}" onclick="openZoneSelectionModal(${task.id}, '${task.title}', '${date.toISOString()}')" style="cursor: pointer;">
                <div class="task-time">${timeDisplay}</div>
                <div class="task-title">${task.title}</div>
                ${description ? `<div class="task-description">${description}</div>` : ''}
            </div>
        `;
    }

    return `
        <div class="calendar-task ${typeClass}">
            <div class="task-time">${timeDisplay}</div>
            <div class="task-title">${task.title}</div>
            ${description ? `<div class="task-description">${description}</div>` : ''}
        </div>
    `;
}



// RECIPE SELECTION MODAL

function openRecipeSelectionModal(taskId, taskTitle, taskDate) {
    currentTaskForRecipeSelection = taskId;

    document.getElementById('modal-meal-title').textContent = taskTitle;
    const date = new Date(taskDate);
    document.getElementById('modal-meal-date').textContent = date.toLocaleDateString('en-US', {
        weekday: 'long',
        day: 'numeric',
        month: 'long',
        year: 'numeric'
    });

    // Load current task with recipes
    fetch(`/admin/api/tasks/${taskId}`)
        .then(r => r.json())
        .then(task => {
            displayCurrentRecipes(task.recipes || []);
        });

    // Load all recipes for selection
    fetch('/admin/api/recipes')
        .then(r => r.json())
        .then(recipes => {
            allRecipesForModal = recipes;
            filterModalRecipes();
        });

    document.getElementById('recipe-selection-modal').style.display = 'block';
}

function closeRecipeSelectionModal() {
    document.getElementById('recipe-selection-modal').style.display = 'none';
    currentTaskForRecipeSelection = null;

    // Reload calendar to show updated recipes
    if (currentAdminCalendarView === 'thisweek') {
        loadAdminWeekCalendar(0);
    } else if (currentAdminCalendarView === 'nextweek') {
        loadAdminWeekCalendar(1);
    }
}

function displayCurrentRecipes(recipes) {
    const container = document.getElementById('current-recipes-list');

    if (!recipes || recipes.length === 0) {
        container.innerHTML = '<p style="color: #7f8c8d;">No recipes selected yet</p>';
        return;
    }

    container.innerHTML = recipes.map(recipe => `
        <div class="recipe-item" style="display: flex; justify-content: space-between; align-items: center; padding: 10px; border: 1px solid #ecf0f1; border-radius: 5px; margin-bottom: 10px;">
            <div>
                <strong>${recipe.name}</strong>
                ${recipe.description ? `<div style="font-size: 12px; color: #7f8c8d;">${recipe.description}</div>` : ''}
            </div>
            <button onclick="removeRecipeFromMeal(${recipe.id})" class="btn btn-danger" style="font-size: 12px;">Remove</button>
        </div>
    `).join('');
}

function filterModalRecipes() {
    const searchTerm = document.getElementById('modal-recipe-search').value.toLowerCase();
    const mealTypeId = document.getElementById('modal-recipe-meal-type').value;

    const filtered = allRecipesForModal.filter(recipe => {
        const matchesSearch = !searchTerm || recipe.name.toLowerCase().includes(searchTerm);

        // Check if recipe has this meal type (either in meal_times array or old category field)
        const matchesMealType = !mealTypeId ||
            (recipe.meal_times && recipe.meal_times.some(mt => mt.id == mealTypeId)) ||
            (recipe.category && allMealTimes.find(mt => mt.id == mealTypeId && mt.name === recipe.category));

        return matchesSearch && matchesMealType;
    });

    displayAvailableRecipes(filtered);
}

function displayAvailableRecipes(recipes) {
    const container = document.getElementById('available-recipes-list');

    if (!recipes || recipes.length === 0) {
        container.innerHTML = '<p style="color: #7f8c8d;">No recipes found</p>';
        return;
    }

    container.innerHTML = recipes.map(recipe => {
        const mealTypes = recipe.meal_times && recipe.meal_times.length > 0
            ? recipe.meal_times.map(mt => mt.name).join(', ')
            : (recipe.category || 'N/A');

        return `
        <div class="recipe-item" style="display: flex; justify-content: space-between; align-items: center; padding: 10px; border: 1px solid #ecf0f1; border-radius: 5px; margin-bottom: 10px;">
            <div style="flex: 1;">
                <strong>${recipe.name}</strong>
                ${recipe.description ? `<div style="font-size: 12px; color: #7f8c8d;">${recipe.description}</div>` : ''}
                <div style="font-size: 11px; color: #95a5a6; margin-top: 3px;">
                    ${mealTypes ? `Meal Types: ${mealTypes}` : ''}
                    ${recipe.family_member ? `| For: ${recipe.family_member}` : ''}
                </div>
            </div>
            <button onclick="addRecipeToMeal(${recipe.id})" class="btn btn-primary" style="font-size: 12px;">Add</button>
        </div>
        `;
    }).join('');
}

function addRecipeToMeal(recipeId) {
    if (!currentTaskForRecipeSelection) return;

    fetch(`/admin/api/tasks/${currentTaskForRecipeSelection}/recipes`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({recipe_id: recipeId})
    })
    .then(r => r.json())
    .then(task => {
        displayCurrentRecipes(task.recipes || []);
    })
    .catch(err => {
        console.error('Error adding recipe:', err);
        alert('Failed to add recipe');
    });
}

function removeRecipeFromMeal(recipeId) {
    if (!currentTaskForRecipeSelection) return;

    fetch(`/admin/api/tasks/${currentTaskForRecipeSelection}/recipes/${recipeId}`, {
        method: 'DELETE'
    })
    .then(r => r.json())
    .then(task => {
        displayCurrentRecipes(task.recipes || []);
    })
    .catch(err => {
        console.error('Error removing recipe:', err);
        alert('Failed to remove recipe');
    });
}


// ZONE SELECTION MODAL

let currentTaskForZoneSelection = null;
let allZonesForModal = [];

function openZoneSelectionModal(taskId, taskTitle, taskDate) {
    currentTaskForZoneSelection = taskId;

    document.getElementById('modal-cleaning-title').textContent = taskTitle;
    const date = new Date(taskDate);
    document.getElementById('modal-cleaning-date').textContent = date.toLocaleDateString('en-US', {
        weekday: 'long',
        day: 'numeric',
        month: 'long',
        year: 'numeric'
    });

    // Load current task with zones
    fetch(`/admin/api/tasks/${taskId}`)
        .then(r => r.json())
        .then(task => {
            displayCurrentZones(task.zones || []);
        })
        .catch(err => {
            console.error('Error loading task:', err);
        });

    // Load all zones
    fetch('/admin/api/zones')
        .then(r => r.json())
        .then(zones => {
            allZonesForModal = zones;
            filterModalZones();
        })
        .catch(err => {
            console.error('Error loading zones:', err);
        });

    document.getElementById('zone-selection-modal').style.display = 'block';
}

function closeZoneSelectionModal() {
    document.getElementById('zone-selection-modal').style.display = 'none';
    currentTaskForZoneSelection = null;

    // Reload calendar to show updated zones
    if (currentAdminCalendarView === 'thisweek') {
        loadAdminWeekCalendar(0);
    } else if (currentAdminCalendarView === 'nextweek') {
        loadAdminWeekCalendar(1);
    }
}

function displayCurrentZones(zones) {
    const container = document.getElementById('current-zones-list');
    if (zones.length === 0) {
        container.innerHTML = '<p style="color: #999;">No zones selected yet</p>';
        return;
    }

    container.innerHTML = zones.map(zone => `
        <div class="zone-item" style="display: flex; justify-content: space-between; align-items: center; padding: 10px; border: 1px solid #ddd; margin-bottom: 8px; border-radius: 4px;">
            <div>
                <strong>${zone.name}</strong>
                ${zone.description ? `<div style="font-size: 12px; color: #666;">${zone.description}</div>` : ''}
                ${zone.priority ? `<span style="font-size: 11px; padding: 2px 6px; background: ${zone.priority === 'high' ? '#e74c3c' : zone.priority === 'medium' ? '#f39c12' : '#95a5a6'}; color: white; border-radius: 3px; margin-top: 4px; display: inline-block;">${zone.priority}</span>` : ''}
            </div>
            <button onclick="removeZoneFromCleaning(${zone.id})" class="btn-danger" style="padding: 5px 10px;">Remove</button>
        </div>
    `).join('');
}

function filterModalZones() {
    const searchTerm = document.getElementById('modal-zone-search').value.toLowerCase();
    const priorityFilter = document.getElementById('modal-zone-priority').value;

    const filtered = allZonesForModal.filter(zone => {
        const matchesSearch = zone.name.toLowerCase().includes(searchTerm) ||
                            (zone.description && zone.description.toLowerCase().includes(searchTerm));
        const matchesPriority = !priorityFilter || zone.priority === priorityFilter;
        return matchesSearch && matchesPriority;
    });

    const container = document.getElementById('available-zones-list');
    container.innerHTML = filtered.map(zone => `
        <div class="zone-item" style="display: flex; justify-content: space-between; align-items: center; padding: 10px; border: 1px solid #ddd; margin-bottom: 8px; border-radius: 4px;">
            <div>
                <strong>${zone.name}</strong>
                ${zone.description ? `<div style="font-size: 12px; color: #666;">${zone.description}</div>` : ''}
                ${zone.priority ? `<span style="font-size: 11px; padding: 2px 6px; background: ${zone.priority === 'high' ? '#e74c3c' : zone.priority === 'medium' ? '#f39c12' : '#95a5a6'}; color: white; border-radius: 3px; margin-top: 4px; display: inline-block;">${zone.priority}</span>` : ''}
            </div>
            <button onclick="addZoneToCleaning(${zone.id})" class="btn-primary" style="padding: 5px 10px;">Add</button>
        </div>
    `).join('');
}

function addZoneToCleaning(zoneId) {
    if (!currentTaskForZoneSelection) return;

    fetch(`/admin/api/tasks/${currentTaskForZoneSelection}/zones`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({zone_id: zoneId})
    })
    .then(r => r.json())
    .then(task => {
        displayCurrentZones(task.zones || []);
    })
    .catch(err => {
        console.error('Error adding zone:', err);
        alert('Failed to add zone');
    });
}

function removeZoneFromCleaning(zoneId) {
    if (!currentTaskForZoneSelection) return;

    fetch(`/admin/api/tasks/${currentTaskForZoneSelection}/zones/${zoneId}`, {
        method: 'DELETE'
    })
    .then(r => r.json())
    .then(task => {
        displayCurrentZones(task.zones || []);
    })
    .catch(err => {
        console.error('Error removing zone:', err);
        alert('Failed to remove zone');
    });
}

