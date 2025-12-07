# Helper Management System

A web application for managing household tasks, meal planning, cleaning schedules, and childcare for your helper.

## Features

### For Admin (You)
- **Recipe Management**: Add, edit, and organize recipes with categories (breakfast, lunch, dinner, snack, baby food)
- **Meal Time Configuration**: Set default meal times for different family members
- **Cleaning Zones**: Define cleaning zones with frequency (times per week) and priority
- **Childcare Scheduling**: Add daily childcare times manually
- **Automatic Schedule Generation**: System automatically generates daily schedules based on your settings

### For Helper
- **Daily Schedule View**: See all tasks for today sorted by time
- **Upcoming Schedule**: View schedule for the next 7-14 days
- **Task Completion**: Mark tasks as complete/incomplete
- **Shopping List**: Add items needed, mark as purchased
- **Recipe Details**: View full recipe instructions when cooking

## Installation & Running

### Prerequisites
- Go 1.24 or higher
- Git

### Steps

1. **Clone or navigate to the project directory**
```bash
cd /Users/podlevskikh/go/src/podlevskikh/awesomeProject
```

2. **Install dependencies**
```bash
go mod tidy
```

3. **Run the server**
```bash
go run cmd/server/main.go
```

Note: First run will take 1-2 minutes to compile SQLite driver.

4. **Access the application**
- Admin interface: http://localhost:8080/admin
- Helper interface: http://localhost:8080/helper

## Usage Guide

### Initial Setup (Admin)

1. **Add Meal Times**
   - Go to Admin Panel → Meal Times
   - Add meal times (e.g., Breakfast at 08:00, Lunch at 13:00, Dinner at 19:00)
   - Specify which family member each meal is for

2. **Add Recipes**
   - Go to Admin Panel → Recipes
   - Add recipes with:
     - Name, description, ingredients, instructions
     - Prep time and cook time
     - Category (breakfast/lunch/dinner/snack/baby_food)
     - Family member (all/adult/baby)

3. **Configure Cleaning Zones**
   - Go to Admin Panel → Cleaning Zones
   - Add zones (e.g., Bedroom, Kitchen, Bathroom)
   - Set frequency per week (1-7 times)
   - Set estimated time and priority

4. **Add Childcare Times**
   - Go to Admin Panel → Childcare
   - Add childcare times for specific dates
   - Set start time, end time, and notes

5. **Generate Schedule**
   - Click "Regenerate Schedule" to create schedules for the next 7 days
   - System automatically generates schedules daily

### Daily Use (Helper)

1. **View Today's Schedule**
   - Open Helper interface
   - See all tasks for today sorted by time
   - Tasks include: meals, cleaning, childcare

2. **Complete Tasks**
   - Click "Complete" button when task is done
   - Click "Undo" to mark as incomplete

3. **View Recipes**
   - Click on recipe name in meal tasks
   - See full ingredients and instructions

4. **Manage Shopping List**
   - Go to Shopping List tab
   - Add items needed
   - Mark items as purchased when shopping

## Project Structure

```
.
├── cmd/server/          # Main application entry point
├── internal/
│   ├── models/          # Database models
│   ├── handlers/        # HTTP handlers (admin & helper)
│   ├── database/        # Database initialization
│   └── scheduler/       # Schedule generation logic
├── web/
│   ├── templates/       # HTML templates
│   └── static/
│       ├── css/         # Stylesheets
│       └── js/          # JavaScript files
└── helper_app.db        # SQLite database (created on first run)
```

## API Endpoints

### Admin API
- `GET/POST /admin/api/recipes` - Manage recipes
- `GET/POST /admin/api/mealtimes` - Manage meal times
- `GET/POST /admin/api/zones` - Manage cleaning zones
- `GET/POST /admin/api/childcare` - Manage childcare schedule
- `POST /admin/api/regenerate-schedule` - Regenerate schedules

### Helper API
- `GET /helper/api/schedule/today` - Get today's schedule
- `GET /helper/api/schedule/upcoming` - Get upcoming schedules
- `POST /helper/api/tasks/:id/complete` - Mark task complete
- `GET /helper/api/shopping` - Get shopping list
- `POST /helper/api/shopping` - Add shopping item

## Scheduling Algorithm

The system automatically generates daily schedules based on:

1. **Meals**: Assigned based on configured meal times, with random recipe selection from matching category
2. **Cleaning**: Zones distributed across the week based on frequency setting
   - Frequency 1x/week: One specific day
   - Frequency 2x/week: Two days spread evenly
   - Higher priority zones scheduled first
3. **Childcare**: Added from manually entered childcare schedules

## Customization

### Adding More Family Members
Edit the dropdown options in:
- `web/templates/admin.html` (recipe and meal time forms)
- `web/static/js/admin.js` (if needed)

### Changing Schedule Generation
Edit `internal/scheduler/scheduler.go` to modify:
- Recipe selection algorithm
- Cleaning zone distribution
- Time slot assignment

### Styling
Edit `web/static/css/style.css` to customize appearance

## Database

The application uses SQLite database (`helper_app.db`) which is created automatically on first run.

To reset the database, simply delete `helper_app.db` and restart the server.

## Future Enhancements

Potential features to add:
- User authentication
- Recipe rotation to avoid repetition
- Grocery list auto-generation from recipes
- Task history and statistics
- Mobile app
- Notifications/reminders
- Multi-language support

## License

This is a personal project for household management.

