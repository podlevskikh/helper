package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/menu"
	"podlevskikh/awesomeProject/internal/models"
)

// ── Покупки под меню ───────────────────────────────────────────────────────
//
// Помощник выбирает, на сколько дней покупаем (всегда с сегодня), и получает
// черновик списка: продукты из рецептов (на 1 порцию) × порции приёма пищи,
// одинаковые сложены, по отделам магазина. «Обычно есть дома» (соль, мука…)
// в пункты не попадает — показывается «проверьте».

// MenuMaxDays — на сколько дней вперёд планировщик собирает меню.
const MenuMaxDays = 7

func menuDays(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > MenuMaxDays {
		return 0, false
	}
	return n, true
}

func (h *HelperHandler) menuRange(c *gin.Context, n int) (time.Time, time.Time, time.Time) {
	org := h.orgID(c)
	now := clock.OrgNow(h.db, org)
	from := clock.Day(now)
	return from, from.AddDate(0, 0, n-1), now
}

// GET /shopping-lists/menu?days=N — меню с сегодня на N дней (по умолчанию 4).
func (h *HelperHandler) GetShoppingMenu(c *gin.Context) {
	n := 4
	if q := c.Query("days"); q != "" {
		var ok bool
		if n, ok = menuDays(q); !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "days must be 1..7"})
			return
		}
	}
	from, to, now := h.menuRange(c, n)
	days, err := menu.Menu(h.db, h.orgID(c), from, to, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"from":     from.Format("2006-01-02"),
		"to":       to.Format("2006-01-02"),
		"days":     days,
		"max_days": MenuMaxDays,
	})
}

// POST /shopping-lists/from-menu {days} — список из меню. Помощник получает черновик,
// наниматель — сразу открытый список (как с обычным списком).
func (h *HelperHandler) CreateShoppingListFromMenu(c *gin.Context) {
	var body struct {
		Days int `json:"days"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	n, ok := menuDays(strconv.Itoa(body.Days))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "days must be 1..7"})
		return
	}
	from, to, now := h.menuRange(c, n)
	days, err := menu.Menu(h.db, h.orgID(c), from, to, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	needs, pantry := menu.Needs(days)

	ts := time.Now()
	l := models.ShoppingList{
		OrganizationID:  h.orgID(c),
		ListDate:        from.Format("2006-01-02"),
		Status:          models.ShoppingListDraft,
		CreatedByUserID: currentUserID(c),
		MenuFrom:        from.Format("2006-01-02"),
		MenuTo:          to.Format("2006-01-02"),
		MenuPantry:      strings.Join(pantry, "\n"),
	}
	if !isHelperRole(c) {
		l.Status = models.ShoppingListOpen
		l.SentAt = &ts
		l.ViewedAt = &ts
	}
	addedBy := "admin"
	if isHelperRole(c) {
		addedBy = "helper"
	}
	uid := currentUserID(c)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&l).Error; err != nil {
			return err
		}
		for i, need := range needs {
			it := menu.Item(l.OrganizationID, l.ID, need, i+1, addedBy, &uid)
			if err := tx.Create(&it).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.writeList(c, http.StatusCreated, l.ID)
}
