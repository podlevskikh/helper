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
- Docker and Docker Compose (for local development)
- PostgreSQL database (local via Docker or cloud via Neon)

### Option 1: Run Locally with Docker Compose (Recommended for Development)

This option runs both the application and PostgreSQL database in Docker.

1. **Clone the repository**
```bash
cd /Users/podlevskikh/go/src/podlevskikh/helper
```

2. **Start the application and database**
```bash
docker-compose up -d
```

This will:
- Start a PostgreSQL database on port 5432
- Build and start the application on port 8080
- Automatically run database migrations

3. **Access the application**
- Admin interface: http://localhost:8080/admin
- Helper interface: http://localhost:8080/helper

4. **Stop the application**
```bash
docker-compose down
```

To remove the database data as well:
```bash
docker-compose down -v
```

### Option 2: Run Locally with Go (Development)

1. **Set up PostgreSQL database**

You can use the PostgreSQL from docker-compose:
```bash
docker-compose up -d postgres
```

Or install PostgreSQL locally.

2. **Configure environment variables**

Copy the example environment file:
```bash
cp .env.example .env
```

Edit `.env` and set your database connection:
```env
DATABASE_URL=postgres://helper:helper_password@localhost:5432/helper_db?sslmode=disable
PORT=8080
```

3. **Install dependencies**
```bash
go mod tidy
```

4. **Run the server**
```bash
# Load environment variables and run
export $(cat .env | xargs) && go run cmd/server/main.go
```

Or on Windows (PowerShell):
```powershell
Get-Content .env | ForEach-Object { $var = $_.Split('='); [Environment]::SetEnvironmentVariable($var[0], $var[1]) }
go run cmd/server/main.go
```

5. **Access the application**
- Admin interface: http://localhost:8080/admin
- Helper interface: http://localhost:8080/helper

### Option 3: Deploy to Production (Railway with Neon PostgreSQL)

#### Step 1: Set up Neon PostgreSQL

1. Go to [Neon](https://neon.tech) and create a free account
2. Create a new project
3. Copy the connection string (it looks like: `postgres://user:password@host/database?sslmode=require`)

#### Step 2: Deploy to Railway

1. Push your code to GitHub

2. Go to [Railway.app](https://railway.app) and create a new project

3. Connect your GitHub repository

4. Add environment variable in Railway:
   - Key: `DATABASE_URL`
   - Value: Your Neon connection string

5. Railway will automatically:
   - Detect the Dockerfile
   - Build the application
   - Deploy it
   - Provide a public URL

6. Access your application at the Railway-provided URL

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
├── cmd/
│   ├── server/          # Main application entry point
│   ├── seed/            # Database seeding utility
│   └── regenerate/      # Schedule regeneration utility
├── internal/
│   ├── models/          # Database models (GORM)
│   ├── handlers/        # HTTP handlers (admin & helper)
│   ├── database/        # Database initialization
│   ├── scheduler/       # Schedule generation logic
│   └── data/            # Data initialization (holidays)
├── web/
│   ├── templates/       # HTML templates
│   └── static/
│       ├── css/         # Stylesheets
│       └── js/          # JavaScript files
├── docker-compose.yml   # Local development setup
├── Dockerfile           # Production container
└── .env.example         # Environment variables template
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

The application uses PostgreSQL database with GORM ORM.

### Database Migrations

Database migrations run automatically on application startup. The following tables are created:
- `recipes` - Recipe information
- `meal_times` - Configured meal times
- `cleaning_zones` - Cleaning zones and schedules
- `childcare_schedules` - Childcare tasks
- `daily_schedules` - Generated daily schedules
- `schedule_tasks` - Individual tasks in schedules
- `shopping_list_items` - Shopping list
- `settings` - Application settings
- `holidays` - Holiday calendar
- `recipe_comments` - Comments on recipes

### Seeding Data

To populate the database with sample data:

```bash
# Make sure DATABASE_URL is set
export DATABASE_URL="postgres://helper:helper_password@localhost:5432/helper_db?sslmode=disable"

# Run the seed command
go run cmd/seed/seed.go
```

### Resetting the Database

For local development with docker-compose:
```bash
docker-compose down -v  # Remove volumes
docker-compose up -d    # Restart with fresh database
```

For production, you'll need to manually drop and recreate the database in your PostgreSQL provider (Neon, etc.).

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

