// Section navigation
function showSection(section) {
    document.querySelectorAll('.content-section').forEach(s => s.style.display = 'none');
    document.querySelectorAll('.nav-btn').forEach(btn => btn.classList.remove('active'));

    document.getElementById(`${section}-section`).style.display = 'block';
    event.target.classList.add('active');

    // Load data for the section
    if (section === 'recipes') loadRecipes();
    else if (section === 'mealtimes') loadMealTimes();
    else if (section === 'zones') loadZones();
    else if (section === 'childcare') loadChildcare();
    else if (section === 'shopping') loadAdminShoppingList();
}

// Initialize
document.addEventListener('DOMContentLoaded', () => {
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
        return `
            <div class="recipe-card">
                <div class="recipe-card-info">
                    <div class="recipe-card-title">${recipe.name}</div>
                    <div class="recipe-card-meta">
                        <span>${stars}</span>
                        <span>📁 ${recipe.category || 'N/A'}</span>
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
    const category = document.getElementById('filter-category').value;
    const family = document.getElementById('filter-family').value;
    const minRating = parseFloat(document.getElementById('filter-rating').value);

    const filtered = allRecipes.filter(recipe => {
        const matchSearch = !search || recipe.name.toLowerCase().includes(search);
        const matchCategory = !category || recipe.category === category;
        const matchFamily = !family || recipe.family_member === family;
        const matchRating = !minRating || (recipe.rating || 0) >= minRating;

        return matchSearch && matchCategory && matchFamily && matchRating;
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
}

function hideRecipeForm() {
    document.getElementById('recipe-form').style.display = 'none';
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
            document.getElementById('recipe-category').value = recipe.category || 'breakfast';
            document.getElementById('recipe-family-member').value = recipe.family_member || 'all';
            document.getElementById('recipe-tags').value = recipe.tags || '';
            document.getElementById('recipe-image-url').value = recipe.image_url || '';
            document.getElementById('recipe-video-url').value = recipe.video_url || '';
            document.getElementById('recipe-rating').value = recipe.rating || 0;
            updateStarDisplay(recipe.rating || 0);

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
    const data = {
        name: document.getElementById('recipe-name').value,
        description: document.getElementById('recipe-description').value,
        ingredients: document.getElementById('recipe-ingredients').value,
        instructions: document.getElementById('recipe-instructions').value,
        category: document.getElementById('recipe-category').value,
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
    if (confirm('Regenerate schedule for the next 7 days?')) {
        fetch('/admin/api/regenerate-schedule', {method: 'POST'})
            .then(r => r.json())
            .then(data => alert(data.message));
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

