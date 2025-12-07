package main

import (
	"log"
	"podlevskikh/awesomeProject/internal/database"
	"podlevskikh/awesomeProject/internal/models"

	"gorm.io/gorm"
)

func main() {
	// Initialize database
	if err := database.Initialize("./helper_app.db"); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	db := database.GetDB()

	log.Println("Starting database seeding...")

	// Seed Recipes
	seedRecipes(db)

	// Seed Meal Times
	seedMealTimes(db)

	// Seed Cleaning Zones
	seedCleaningZones(db)

	log.Println("Database seeding completed successfully!")
}

func seedRecipes(db *gorm.DB) {
	log.Println("Seeding recipes...")

	recipes := []models.Recipe{
		// Breakfast recipes
		{
			Name:         "Овсяная каша с фруктами",
			Description:  "Полезная овсяная каша с бананом и ягодами",
			Ingredients:  "Овсяные хлопья - 1 стакан, Молоко - 2 стакана, Банан - 1 шт, Ягоды - 100г, Мед - 1 ст.л.",
			Instructions: "1. Вскипятить молоко\n2. Добавить овсяные хлопья и варить 5 минут\n3. Нарезать банан\n4. Выложить кашу в тарелку, добавить фрукты и мед",
			PrepTime:     5,
			CookTime:     10,
			Servings:     2,
			Category:     "breakfast",
			FamilyMember: "all",
			Tags:         "здоровое питание, быстро",
		},
		{
			Name:         "Яичница с тостами",
			Description:  "Классический завтрак с яйцами и хлебом",
			Ingredients:  "Яйца - 3 шт, Хлеб - 2 ломтика, Масло сливочное - 20г, Соль, перец",
			Instructions: "1. Разогреть сковороду с маслом\n2. Разбить яйца на сковороду\n3. Поджарить хлеб в тостере\n4. Посолить, поперчить яйца\n5. Подавать с тостами",
			PrepTime:     3,
			CookTime:     7,
			Servings:     1,
			Category:     "breakfast",
			FamilyMember: "all",
			Tags:         "быстро, простое",
		},
		{
			Name:         "Блинчики с творогом",
			Description:  "Нежные блинчики с творожной начинкой",
			Ingredients:  "Мука - 200г, Яйца - 2 шт, Молоко - 400мл, Творог - 300г, Сахар - 3 ст.л., Соль",
			Instructions: "1. Смешать муку, яйца, молоко, соль\n2. Испечь тонкие блинчики\n3. Смешать творог с сахаром\n4. Завернуть начинку в блинчики",
			PrepTime:     15,
			CookTime:     25,
			Servings:     4,
			Category:     "breakfast",
			FamilyMember: "all",
			Tags:         "выходной день, вкусно",
		},

		// Lunch recipes
		{
			Name:         "Борщ",
			Description:  "Традиционный украинский борщ",
			Ingredients:  "Свекла - 2 шт, Капуста - 300г, Картофель - 3 шт, Морковь - 1 шт, Лук - 1 шт, Мясо - 500г, Томатная паста - 2 ст.л.",
			Instructions: "1. Сварить мясной бульон\n2. Нарезать овощи\n3. Добавить картофель в бульон\n4. Обжарить свеклу, морковь, лук\n5. Добавить капусту и зажарку\n6. Варить 30 минут",
			PrepTime:     20,
			CookTime:     90,
			Servings:     6,
			Category:     "lunch",
			FamilyMember: "all",
			Tags:         "традиционное, сытное",
		},
		{
			Name:         "Куриный суп с лапшой",
			Description:  "Легкий куриный суп",
			Ingredients:  "Курица - 500г, Лапша - 100г, Морковь - 1 шт, Лук - 1 шт, Картофель - 2 шт, Зелень",
			Instructions: "1. Сварить куриный бульон\n2. Достать курицу, нарезать\n3. Добавить нарезанные овощи\n4. Варить 20 минут\n5. Добавить лапшу за 5 минут до готовности\n6. Добавить зелень",
			PrepTime:     15,
			CookTime:     45,
			Servings:     4,
			Category:     "lunch",
			FamilyMember: "all",
			Tags:         "легкое, полезное",
		},
		{
			Name:         "Рыба с овощами на пару",
			Description:  "Диетическое блюдо из рыбы",
			Ingredients:  "Рыба (филе) - 400г, Брокколи - 200г, Морковь - 1 шт, Лимон - 1 шт, Специи",
			Instructions: "1. Подготовить пароварку\n2. Приправить рыбу специями и лимонным соком\n3. Нарезать овощи\n4. Готовить на пару 20-25 минут\n5. Подавать с лимоном",
			PrepTime:     10,
			CookTime:     25,
			Servings:     2,
			Category:     "lunch",
			FamilyMember: "all",
			Tags:         "здоровое питание, диетическое",
		},

		// Dinner recipes
		{
			Name:         "Запеченная курица с картофелем",
			Description:  "Сытный ужин из духовки",
			Ingredients:  "Курица - 1 кг, Картофель - 6 шт, Лук - 2 шт, Чеснок - 4 зубчика, Специи, Масло",
			Instructions: "1. Разогреть духовку до 200°C\n2. Нарезать картофель и лук\n3. Натереть курицу специями и чесноком\n4. Выложить все в форму\n5. Запекать 60 минут",
			PrepTime:     15,
			CookTime:     60,
			Servings:     4,
			Category:     "dinner",
			FamilyMember: "all",
			Tags:         "сытное, семейное",
		},
		{
			Name:         "Паста Карбонара",
			Description:  "Итальянская паста с беконом",
			Ingredients:  "Спагетти - 400г, Бекон - 200г, Яйца - 3 шт, Пармезан - 100г, Чеснок - 2 зубчика, Черный перец",
			Instructions: "1. Отварить пасту\n2. Обжарить бекон с чесноком\n3. Взбить яйца с тертым пармезаном\n4. Смешать горячую пасту с беконом\n5. Добавить яичную смесь, быстро перемешать\n6. Посыпать перцем",
			PrepTime:     10,
			CookTime:     20,
			Servings:     3,
			Category:     "dinner",
			FamilyMember: "all",
			Tags:         "быстро, вкусно",
		},
		{
			Name:         "Овощное рагу",
			Description:  "Легкое овощное блюдо",
			Ingredients:  "Кабачок - 2 шт, Баклажан - 1 шт, Перец - 2 шт, Помидоры - 3 шт, Лук - 1 шт, Чеснок - 3 зубчика",
			Instructions: "1. Нарезать все овощи кубиками\n2. Обжарить лук и чеснок\n3. Добавить баклажаны, через 5 мин кабачки\n4. Добавить перец и помидоры\n5. Тушить 25 минут\n6. Приправить специями",
			PrepTime:     15,
			CookTime:     35,
			Servings:     4,
			Category:     "dinner",
			FamilyMember: "all",
			Tags:         "вегетарианское, легкое",
		},
	}

	for _, recipe := range recipes {
		if err := db.Create(&recipe).Error; err != nil {
			log.Printf("Warning: Failed to create recipe %s: %v", recipe.Name, err)
		} else {
			log.Printf("Created recipe: %s", recipe.Name)
		}
	}
}

func seedMealTimes(db *gorm.DB) {
	log.Println("Seeding meal times...")

	mealTimes := []models.MealTime{
		{
			Name:         "breakfast",
			DefaultTime:  "08:00",
			FamilyMember: "all",
			Active:       true,
		},
		{
			Name:         "lunch",
			DefaultTime:  "13:00",
			FamilyMember: "all",
			Active:       true,
		},
		{
			Name:         "dinner",
			DefaultTime:  "18:00",
			FamilyMember: "all",
			Active:       true,
		},
	}

	for _, mealTime := range mealTimes {
		if err := db.Create(&mealTime).Error; err != nil {
			log.Printf("Warning: Failed to create meal time %s: %v", mealTime.Name, err)
		} else {
			log.Printf("Created meal time: %s at %s", mealTime.Name, mealTime.DefaultTime)
		}
	}
}

func seedCleaningZones(db *gorm.DB) {
	log.Println("Seeding cleaning zones...")

	zones := []models.CleaningZone{
		{
			Name:             "Мастер спальня",
			Description:      "Уборка главной спальни",
			FrequencyPerWeek: 2,
			EstimatedTime:    45,
			Priority:         5,
			Active:           true,
		},
		{
			Name:             "Гостиная",
			Description:      "Уборка гостиной комнаты",
			FrequencyPerWeek: 3,
			EstimatedTime:    60,
			Priority:         5,
			Active:           true,
		},
		{
			Name:             "Кухня",
			Description:      "Уборка кухни",
			FrequencyPerWeek: 7,
			EstimatedTime:    30,
			Priority:         10,
			Active:           true,
		},
		{
			Name:             "Кабинет",
			Description:      "Уборка рабочего кабинета",
			FrequencyPerWeek: 1,
			EstimatedTime:    40,
			Priority:         3,
			Active:           true,
		},
		{
			Name:             "Коридор на втором этаже",
			Description:      "Уборка коридора второго этажа",
			FrequencyPerWeek: 2,
			EstimatedTime:    20,
			Priority:         4,
			Active:           true,
		},
		{
			Name:             "Чердак",
			Description:      "Уборка чердака",
			FrequencyPerWeek: 1,
			EstimatedTime:    50,
			Priority:         1,
			Active:           true,
		},
	}

	for _, zone := range zones {
		if err := db.Create(&zone).Error; err != nil {
			log.Printf("Warning: Failed to create cleaning zone %s: %v", zone.Name, err)
		} else {
			log.Printf("Created cleaning zone: %s (frequency: %d times/week)", zone.Name, zone.FrequencyPerWeek)
		}
	}
}

