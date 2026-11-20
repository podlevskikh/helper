package handlers

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/menu"
	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/notify"
)

// ── Списки покупок (раздел «Входящие») ──────────────────────────────────────
//
// Помощник собирает список (draft) и отправляет нанимателю (open). Наниматель
// в магазине отмечает купленное, может дописать пункты и закрывает список
// (closed). При закрытии некупленное переносится в новый открытый список
// с той же датой — ничего не теряется.

type shoppingListView struct {
	models.ShoppingList
	AuthorName   string `json:"author_name"`
	ClosedByName string `json:"closed_by_name,omitempty"`
	Total        int    `json:"total"`
	Purchased    int    `json:"purchased"`
}

func isHelperRole(c *gin.Context) bool {
	return middleware.MustMembership(c).Role == models.RoleHelper
}

func currentUserID(c *gin.Context) uint {
	v, _ := c.Get(middleware.ContextKeyUserID)
	uid, _ := v.(uint)
	return uid
}

func validListDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// userNames возвращает имена пользователей по id (пустое имя → часть email до @).
func (h *HelperHandler) userNames(ids []uint) map[uint]string {
	out := map[uint]string{}
	if len(ids) == 0 {
		return out
	}
	var users []models.User
	h.db.Where("id IN ?", ids).Find(&users)
	for _, u := range users {
		name := strings.TrimSpace(u.Name)
		if name == "" {
			name = strings.Split(u.Email, "@")[0]
		}
		out[u.ID] = name
	}
	return out
}

func (h *HelperHandler) listViews(lists []models.ShoppingList) []shoppingListView {
	ids := []uint{}
	listIDs := []uint{}
	for _, l := range lists {
		ids = append(ids, l.CreatedByUserID)
		if l.ClosedByUserID != nil {
			ids = append(ids, *l.ClosedByUserID)
		}
		listIDs = append(listIDs, l.ID)
	}
	names := h.userNames(ids)

	type cnt struct {
		ListID    uint
		Total     int
		Purchased int
	}
	counts := map[uint]cnt{}
	if len(listIDs) > 0 {
		var rows []cnt
		h.db.Model(&models.ShoppingListItem{}).
			Select("list_id, COUNT(*) AS total, SUM(CASE WHEN purchased THEN 1 ELSE 0 END) AS purchased").
			Where("list_id IN ?", listIDs).Group("list_id").Scan(&rows)
		for _, r := range rows {
			counts[r.ListID] = r
		}
	}

	out := make([]shoppingListView, 0, len(lists))
	for _, l := range lists {
		v := shoppingListView{ShoppingList: l, AuthorName: names[l.CreatedByUserID]}
		if l.ClosedByUserID != nil {
			v.ClosedByName = names[*l.ClosedByUserID]
		}
		v.Total = counts[l.ID].Total
		v.Purchased = counts[l.ID].Purchased
		out = append(out, v)
	}
	return out
}

// loadList находит список организации; хелпер не видит чужие черновики,
// наниматель — черновики вообще.
func (h *HelperHandler) loadList(c *gin.Context) (*models.ShoppingList, bool) {
	var l models.ShoppingList
	if err := h.orgDB(c).First(&l, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "list not found"})
		return nil, false
	}
	if l.Status == models.ShoppingListDraft {
		if !isHelperRole(c) || l.CreatedByUserID != currentUserID(c) {
			c.JSON(http.StatusNotFound, gin.H{"error": "list not found"})
			return nil, false
		}
	}
	return &l, true
}

func (h *HelperHandler) writeList(c *gin.Context, status int, id uint) {
	var l models.ShoppingList
	if err := h.db.Preload("Items", func(db *gorm.DB) *gorm.DB {
		return db.Order("position ASC, id ASC")
	}).First(&l, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(status, h.listViews([]models.ShoppingList{l})[0])
}

// GET /shopping-lists?status=open|closed
// open: черновики автора (только помощнику) + открытые; closed: последние 50 закрытых.
func (h *HelperHandler) GetShoppingLists(c *gin.Context) {
	q := h.orgDB(c)
	if c.Query("status") == models.ShoppingListClosed {
		q = q.Where("status = ?", models.ShoppingListClosed).Order("closed_at DESC").Limit(50)
	} else if isHelperRole(c) {
		q = q.Where("status = ? OR (status = ? AND created_by_user_id = ?)",
			models.ShoppingListOpen, models.ShoppingListDraft, currentUserID(c)).
			Order("list_date DESC, id DESC")
	} else {
		q = q.Where("status = ?", models.ShoppingListOpen).Order("list_date DESC, id DESC")
	}
	var lists []models.ShoppingList
	if err := q.Find(&lists).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.listViews(lists))
}

type newListItem struct {
	Item     string `json:"item"`
	Quantity string `json:"quantity"`
}

// POST /shopping-lists {list_date?, items?}
// Помощник создаёт черновик; наниматель — сразу открытый список.
func (h *HelperHandler) CreateShoppingList(c *gin.Context) {
	var body struct {
		ListDate string        `json:"list_date"`
		Items    []newListItem `json:"items"`
	}
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.ListDate == "" {
		body.ListDate = clock.OrgToday(h.db, middleware.MustMembership(c).OrganizationID).Format("2006-01-02")
	}
	if !validListDate(body.ListDate) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "list_date must be YYYY-MM-DD"})
		return
	}
	now := time.Now()
	l := models.ShoppingList{
		OrganizationID:  h.orgID(c),
		ListDate:        body.ListDate,
		Status:          models.ShoppingListDraft,
		CreatedByUserID: currentUserID(c),
	}
	if !isHelperRole(c) {
		l.Status = models.ShoppingListOpen
		l.SentAt = &now
		l.ViewedAt = &now
	}
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&l).Error; err != nil {
			return err
		}
		pos := 0
		for _, it := range body.Items {
			text := strings.TrimSpace(it.Item)
			if text == "" {
				continue
			}
			pos++
			if err := tx.Create(h.newItem(c, l.ID, text, it.Quantity, pos)).Error; err != nil {
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

func (h *HelperHandler) newItem(c *gin.Context, listID uint, text, qty string, pos int) *models.ShoppingListItem {
	uid := currentUserID(c)
	addedBy := "admin"
	if isHelperRole(c) {
		addedBy = "helper"
	}
	return &models.ShoppingListItem{
		OrganizationID: h.orgID(c),
		ListID:         &listID,
		Item:           text,
		Quantity:       strings.TrimSpace(qty),
		AddedBy:        addedBy,
		AddedByUserID:  &uid,
		Position:       pos,
	}
}

// GET /shopping-lists/:id
func (h *HelperHandler) GetShoppingListByID(c *gin.Context) {
	l, ok := h.loadList(c)
	if !ok {
		return
	}
	h.writeList(c, http.StatusOK, l.ID)
}

// POST /shopping-lists/:id/send — черновик уходит нанимателю во «Входящие».
func (h *HelperHandler) SendShoppingList(c *gin.Context) {
	l, ok := h.loadList(c)
	if !ok {
		return
	}
	if l.Status == models.ShoppingListDraft {
		now := time.Now()
		res := h.db.Model(&models.ShoppingList{}).Where("id = ? AND status = ?", l.ID, models.ShoppingListDraft).
			Updates(map[string]any{"status": models.ShoppingListOpen, "sent_at": now})
		if res.Error == nil && res.RowsAffected > 0 {
			notify.ListSent(h.db, currentUserID(c), l)
		}
	}
	h.writeList(c, http.StatusOK, l.ID)
}

// POST /shopping-lists/:id/viewed — наниматель открыл список (снимает «новое»).
func (h *HelperHandler) MarkShoppingListViewed(c *gin.Context) {
	l, ok := h.loadList(c)
	if !ok {
		return
	}
	if !isHelperRole(c) && l.ViewedAt == nil && l.Status == models.ShoppingListOpen {
		h.db.Model(l).Update("viewed_at", time.Now())
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func rejectClosed(c *gin.Context, l *models.ShoppingList) bool {
	if l.Status == models.ShoppingListClosed {
		c.JSON(http.StatusConflict, gin.H{"error": "list is closed"})
		return true
	}
	return false
}

// POST /shopping-lists/:id/items {item, quantity}
func (h *HelperHandler) AddShoppingListEntry(c *gin.Context) {
	l, ok := h.loadList(c)
	if !ok || rejectClosed(c, l) {
		return
	}
	var body newListItem
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Item) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "item is required"})
		return
	}
	var maxPos int
	h.db.Model(&models.ShoppingListItem{}).Where("list_id = ?", l.ID).
		Select("COALESCE(MAX(position), 0)").Scan(&maxPos)
	item := h.newItem(c, l.ID, strings.TrimSpace(body.Item), body.Quantity, maxPos+1)
	if err := h.db.Create(item).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Помощник дописал в уже отправленный список — нанимателю (копится 5 минут).
	if l.Status == models.ShoppingListOpen && isHelperRole(c) {
		notify.ListItemAdded(h.db, currentUserID(c), l, item.Item)
	}
	c.JSON(http.StatusCreated, item)
}

func (h *HelperHandler) loadEntry(c *gin.Context, l *models.ShoppingList) (*models.ShoppingListItem, bool) {
	var item models.ShoppingListItem
	if err := h.db.Where("list_id = ?", l.ID).First(&item, c.Param("itemId")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "item not found"})
		return nil, false
	}
	return &item, true
}

// PATCH /shopping-lists/:id/items/:itemId {item?, quantity?, purchased?}
func (h *HelperHandler) UpdateShoppingListEntry(c *gin.Context) {
	l, ok := h.loadList(c)
	if !ok || rejectClosed(c, l) {
		return
	}
	item, ok := h.loadEntry(c, l)
	if !ok {
		return
	}
	var body struct {
		Item      *string  `json:"item"`
		Quantity  *string  `json:"quantity"`
		Purchased *bool    `json:"purchased"`
		BuyAmount *float64 `json:"buy_amount"` // пункт из меню: сколько берём (в единицах пункта)
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Item != nil {
		t := strings.TrimSpace(*body.Item)
		if t == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "item is required"})
			return
		}
		item.Item = t
	}
	if body.Quantity != nil {
		item.Quantity = strings.TrimSpace(*body.Quantity)
	}
	if body.BuyAmount != nil {
		if *body.BuyAmount < 0 || *body.BuyAmount > 1e6 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "buy_amount out of range"})
			return
		}
		item.BuyAmount = *body.BuyAmount
		item.Quantity = menu.Format(item.BuyAmount, item.Unit)
	}
	if body.Purchased != nil && *body.Purchased != item.Purchased {
		item.Purchased = *body.Purchased
		if item.Purchased {
			now := time.Now()
			item.PurchasedAt = &now
		} else {
			item.PurchasedAt = nil
		}
	}
	if err := h.db.Save(item).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, item)
}

// DELETE /shopping-lists/:id/items/:itemId
func (h *HelperHandler) DeleteShoppingListEntry(c *gin.Context) {
	l, ok := h.loadList(c)
	if !ok || rejectClosed(c, l) {
		return
	}
	item, ok := h.loadEntry(c, l)
	if !ok {
		return
	}
	if err := h.db.Delete(item).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /shopping-lists/:id/close — закрыть список. Некупленные пункты копируются
// в новый открытый список с той же датой; в закрытом остаются для истории.
func (h *HelperHandler) CloseShoppingList(c *gin.Context) {
	l, ok := h.loadList(c)
	if !ok || rejectClosed(c, l) {
		return
	}
	if l.Status == models.ShoppingListDraft {
		c.JSON(http.StatusConflict, gin.H{"error": "draft cannot be closed"})
		return
	}
	uid := currentUserID(c)
	now := time.Now()
	var next *models.ShoppingList
	err := h.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&models.ShoppingList{}).
			Where("id = ? AND status = ?", l.ID, models.ShoppingListOpen).
			Updates(map[string]any{"status": models.ShoppingListClosed, "closed_at": now, "closed_by_user_id": uid})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errAlreadyClosed
		}
		var left []models.ShoppingListItem
		if err := tx.Where("list_id = ? AND purchased = ?", l.ID, false).
			Order("position ASC, id ASC").Find(&left).Error; err != nil {
			return err
		}
		if len(left) == 0 {
			return nil
		}
		parent := l.ID
		n := models.ShoppingList{
			OrganizationID:  l.OrganizationID,
			ListDate:        l.ListDate,
			Status:          models.ShoppingListOpen,
			CreatedByUserID: l.CreatedByUserID,
			SentAt:          &now,
			ViewedAt:        &now,
			ParentListID:    &parent,
			MenuFrom:        l.MenuFrom,
			MenuTo:          l.MenuTo,
		}
		if err := tx.Create(&n).Error; err != nil {
			return err
		}
		for i, it := range left {
			copyItem := models.ShoppingListItem{
				OrganizationID: it.OrganizationID,
				ListID:         &n.ID,
				Item:           it.Item,
				Quantity:       it.Quantity,
				Category:       it.Category,
				AddedBy:        it.AddedBy,
				AddedByUserID:  it.AddedByUserID,
				Position:       i + 1,
				FromMenu:       it.FromMenu,
				NeedAmount:     it.NeedAmount,
				BuyAmount:      it.BuyAmount,
				Unit:           it.Unit,
				Sources:        it.Sources,
			}
			if err := tx.Create(&copyItem).Error; err != nil {
				return err
			}
		}
		next = &n
		return nil
	})
	if errors.Is(err, errAlreadyClosed) {
		c.JSON(http.StatusConflict, gin.H{"error": "list is closed"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resp := gin.H{"closed_id": l.ID}
	var nextID *uint
	if next != nil {
		resp["next_id"] = next.ID
		nextID = &next.ID
	}
	var total, bought int64
	h.db.Model(&models.ShoppingListItem{}).Where("list_id = ?", l.ID).Count(&total)
	h.db.Model(&models.ShoppingListItem{}).Where("list_id = ? AND purchased = ?", l.ID, true).Count(&bought)
	notify.ListClosed(h.db, uid, l, int(bought), int(total), nextID)
	c.JSON(http.StatusOK, resp)
}

// DELETE /shopping-lists/:id — удалить черновик (автор) или любой список (наниматель).
func (h *HelperHandler) DeleteShoppingList(c *gin.Context) {
	l, ok := h.loadList(c)
	if !ok {
		return
	}
	if isHelperRole(c) && l.Status != models.ShoppingListDraft {
		c.JSON(http.StatusForbidden, gin.H{"error": "only drafts can be deleted"})
		return
	}
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("list_id = ?", l.ID).Delete(&models.ShoppingListItem{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.ShoppingList{}, l.ID).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

var errAlreadyClosed = errors.New("already closed")

// GET /shopping-lists/suggestions — «Часто берём»: самые частые пункты организации
// за последние 90 дней (по тексту без учёта регистра).
func (h *HelperHandler) GetShoppingSuggestions(c *gin.Context) {
	type row struct {
		Item string
		N    int
	}
	var rows []row
	since := time.Now().AddDate(0, 0, -90)
	if err := h.db.Model(&models.ShoppingListItem{}).
		Select("MIN(item) AS item, COUNT(*) AS n").
		Where("organization_id = ? AND list_id IS NOT NULL AND created_at >= ?", h.orgID(c), since).
		Group("LOWER(TRIM(item))").Order("n DESC, item ASC").Limit(12).
		Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Item)
	}
	c.JSON(http.StatusOK, out)
}
