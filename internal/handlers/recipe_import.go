package handlers

import (
	"net/http"
	"strconv"

	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/recipestore"

	"github.com/gin-gonic/gin"
)

// Загрузка рецептов списком (docs/work/recipes-import/PRD.md во фронте). Логика — internal/recipestore,
// её же зовёт MCP-коннектор для Claude.

// UndoWindow — сколько после загрузки работает «Отменить загрузку».
const UndoWindow = recipestore.UndoWindow

func storeError(c *gin.Context, e *recipestore.Error) {
	body := gin.H{"error": e.Msg, "code": e.Code}
	for k, v := range e.Extra {
		body[k] = v
	}
	c.JSON(e.Status, body)
}

// actor — кто делает действие в приложении.
func (h *AdminHandler) actor(c *gin.Context) recipestore.Actor {
	m := middleware.MustMembership(c)
	var u models.User
	h.db.Select("id", "name").First(&u, m.UserID)
	return recipestore.Actor{UserID: m.UserID, Name: u.Name}
}

// ImportRecipes — POST /admin/api/recipe-imports: с dry_run — предпросмотр, без — сохранить по решениям.
func (h *AdminHandler) ImportRecipes(c *gin.Context) {
	var in recipestore.ImportInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if in.DryRun {
		p, e := recipestore.PreviewImport(h.db, h.orgID(c), in)
		if e != nil {
			storeError(c, e)
			return
		}
		c.JSON(http.StatusOK, p)
		return
	}
	r, e := recipestore.SaveImport(h.db, h.orgID(c), h.actor(c), in, recipestore.SourceText)
	if e != nil {
		storeError(c, e)
		return
	}
	c.JSON(http.StatusCreated, r)
}

// RecentRecipeImports — GET /admin/api/recipe-imports: загрузки за сутки, которые ещё можно отменить
// (в том числе сделанные из чата с Claude).
func (h *AdminHandler) RecentRecipeImports(c *gin.Context) {
	list, err := recipestore.RecentImports(h.db, h.orgID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, i := range list {
		out = append(out, gin.H{"id": i.ID, "source": i.Source, "user_name": i.UserName, "created": i.Created,
			"replaced": i.Replaced, "created_at": i.CreatedAt, "undo_until": i.CreatedAt.Add(UndoWindow)})
	}
	c.JSON(http.StatusOK, out)
}

// UndoRecipeImport — DELETE /admin/api/recipe-imports/:id: удалить рецепты, созданные загрузкой (сутки).
func (h *AdminHandler) UndoRecipeImport(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Import not found"})
		return
	}
	deleted, replaced, e := recipestore.UndoImport(h.db, h.orgID(c), uint(id))
	if e != nil {
		storeError(c, e)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted, "replaced_kept": replaced})
}
