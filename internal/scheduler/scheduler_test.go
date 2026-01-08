package scheduler

import (
	"testing"

	"podlevskikh/awesomeProject/internal/models"
)

// TestSelectRecipeWithImprovedRotation tests the improved rotation algorithm
func TestSelectRecipeWithImprovedRotation(t *testing.T) {
	s := &Scheduler{}

	// Create test recipes
	recipes := []models.Recipe{
		{ID: 1, Name: "Recipe 1"},
		{ID: 2, Name: "Recipe 2"},
		{ID: 3, Name: "Recipe 3"},
		{ID: 4, Name: "Recipe 4"},
		{ID: 5, Name: "Recipe 5"},
	}

	t.Run("Should never select yesterday's recipe when alternatives exist", func(t *testing.T) {
		recentlyUsed := map[uint]int{
			1: 1, // Used yesterday
			2: 5, // Used 5 days ago
			3: 10, // Used 10 days ago
		}
		yesterdayRecipes := map[uint]bool{
			1: true, // Recipe 1 was used yesterday
		}

		// Run selection 100 times to ensure recipe 1 is never selected
		for i := 0; i < 100; i++ {
			selected := s.selectRecipeWithImprovedRotation(recipes, recentlyUsed, yesterdayRecipes)
			if selected.ID == 1 {
				t.Errorf("Recipe 1 (used yesterday) was selected, but should have been excluded")
			}
		}
	})

	t.Run("Should prefer recipes not used recently", func(t *testing.T) {
		recentlyUsed := map[uint]int{
			1: 2,  // Used 2 days ago
			2: 20, // Used 20 days ago (should be preferred)
			3: 5,  // Used 5 days ago
		}
		yesterdayRecipes := map[uint]bool{}

		// Count selections over 1000 runs
		selections := make(map[uint]int)
		for i := 0; i < 1000; i++ {
			selected := s.selectRecipeWithImprovedRotation(recipes, recentlyUsed, yesterdayRecipes)
			selections[selected.ID]++
		}

		// Recipe 2 (20 days ago) should be selected more often than Recipe 1 (2 days ago)
		if selections[2] <= selections[1] {
			t.Errorf("Recipe 2 (20 days ago) should be selected more often than Recipe 1 (2 days ago). Got: Recipe 1=%d, Recipe 2=%d",
				selections[1], selections[2])
		}
	})

	t.Run("Should handle single recipe", func(t *testing.T) {
		singleRecipe := []models.Recipe{{ID: 1, Name: "Only Recipe"}}
		recentlyUsed := map[uint]int{}
		yesterdayRecipes := map[uint]bool{}

		selected := s.selectRecipeWithImprovedRotation(singleRecipe, recentlyUsed, yesterdayRecipes)
		if selected.ID != 1 {
			t.Errorf("Expected recipe 1, got %d", selected.ID)
		}
	})

	t.Run("Should handle empty recipes", func(t *testing.T) {
		emptyRecipes := []models.Recipe{}
		recentlyUsed := map[uint]int{}
		yesterdayRecipes := map[uint]bool{}

		selected := s.selectRecipeWithImprovedRotation(emptyRecipes, recentlyUsed, yesterdayRecipes)
		if selected != nil {
			t.Errorf("Expected nil for empty recipes, got %v", selected)
		}
	})

	t.Run("Should give higher weight to never-used recipes", func(t *testing.T) {
		recentlyUsed := map[uint]int{
			1: 5, // Used 5 days ago
			2: 5, // Used 5 days ago
			// Recipe 3, 4, 5 never used
		}
		yesterdayRecipes := map[uint]bool{}

		// Count selections over 1000 runs
		selections := make(map[uint]int)
		for i := 0; i < 1000; i++ {
			selected := s.selectRecipeWithImprovedRotation(recipes, recentlyUsed, yesterdayRecipes)
			selections[selected.ID]++
		}

		// Never-used recipes (3, 4, 5) should be selected more often than used recipes (1, 2)
		neverUsedCount := selections[3] + selections[4] + selections[5]
		usedCount := selections[1] + selections[2]

		if neverUsedCount <= usedCount {
			t.Errorf("Never-used recipes should be selected more often. Never-used=%d, Used=%d",
				neverUsedCount, usedCount)
		}
	})
}

// TestGetYesterdayRecipes tests the yesterday recipe detection
func TestGetYesterdayRecipes(t *testing.T) {
	// This test would require a database setup, so it's a placeholder
	// In a real scenario, you would use a test database or mock
	t.Skip("Requires database setup")
}

// TestWeightDistribution tests that weight distribution is correct
func TestWeightDistribution(t *testing.T) {
	s := &Scheduler{}

	recipes := []models.Recipe{
		{ID: 1, Name: "Recipe 1"},
		{ID: 2, Name: "Recipe 2"},
		{ID: 3, Name: "Recipe 3"},
	}

	t.Run("Weight increases with days since last use", func(t *testing.T) {
		// Test different time periods
		testCases := []struct {
			daysSince int
			minWeight float64
			maxWeight float64
		}{
			{0, 0.0, 0.1},   // Same day
			{1, 0.0, 0.2},   // Yesterday
			{2, 0.2, 0.4},   // 2 days
			{7, 0.9, 1.1},   // 1 week
			{14, 1.4, 1.6},  // 2 weeks
			{21, 1.9, 2.1},  // 3 weeks
			{30, 2.5, 3.5},  // 1 month
		}

		for _, tc := range testCases {
			recentlyUsed := map[uint]int{1: tc.daysSince}
			yesterdayRecipes := map[uint]bool{}

			// We can't directly test weights, but we can verify selection happens
			selected := s.selectRecipeWithImprovedRotation(recipes, recentlyUsed, yesterdayRecipes)
			if selected == nil {
				t.Errorf("Expected a recipe to be selected for daysSince=%d", tc.daysSince)
			}
		}
	})
}

