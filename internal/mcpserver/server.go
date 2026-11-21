// Package mcpserver — MCP-сервер Helper's Day для Claude (/mcp, Streamable HTTP, без сессий).
// Вход — OAuth (internal/oauth): токен → человек + дом; права (manage_recipes) проверяются на каждом запросе.
// Инструменты — только рецепты дома; запись — через internal/recipestore, как в приложении.
// docs/work/recipes-mcp/PRD.md во фронте.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/models"
	"podlevskikh/awesomeProject/internal/oauth"
	"podlevskikh/awesomeProject/internal/recipeimport"
	"podlevskikh/awesomeProject/internal/recipestore"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"
)

// Via — пометка в «Изменено: …» у рецептов, которые добавил или поменял Claude.
const Via = "Claude"

type srv struct {
	db *gorm.DB
}

// Handler — /mcp: проверка Bearer-токена (401 с WWW-Authenticate → Claude идёт в OAuth) и MCP-сервер.
func Handler(db *gorm.DB, o *oauth.Server) http.Handler {
	s := &srv{db: db}
	server := mcp.NewServer(&mcp.Implementation{Name: "helperday", Title: "Helper's Day", Version: "1.0.0"},
		&mcp.ServerOptions{Instructions: instructions})
	s.addTools(server)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true})

	verify := func(_ context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
		a, err := o.Verify(r, token)
		if err != nil {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{Scopes: a.Scopes, Expiration: a.ExpiresAt, UserID: strconv.FormatUint(uint64(a.UserID), 10),
			Extra: map[string]any{"user_id": a.UserID, "org_id": a.OrgID, "grant_id": a.GrantID}}, nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mw := auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{
			ResourceMetadataURL: oauth.ResourceMetadataURL(r),
			Scopes:              []string{oauth.Scope},
		})
		mw(h).ServeHTTP(w, r)
	})
}

const instructions = `Helper's Day keeps the home's recipes: one serving each, one dish type per recipe, ingredients one per line ("Product — 170 g").
To add recipes: call list_dish_types and find_recipes first, format recipes with get_recipe_template, check them with preview_recipe_import, show the user a short summary (new, duplicates, errors) and call import_recipes only after the user agrees. Duplicates (same name) are skipped unless the user asks to replace them.
Keep the recipe language as the user writes it. Recalculate quantities for 1 serving.`

// who — человек и дом из токена.
type who struct {
	userID, orgID uint
	name          string
}

func (s *srv) who(req *mcp.CallToolRequest) (*who, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return nil, errors.New("not authorized")
	}
	x := req.Extra.TokenInfo.Extra
	uid, _ := x["user_id"].(uint)
	org, _ := x["org_id"].(uint)
	if uid == 0 || org == 0 {
		return nil, errors.New("not authorized")
	}
	var u models.User
	s.db.Select("id", "name").First(&u, uid)
	return &who{userID: uid, orgID: org, name: recipestore.ActorName(u.Name, Via)}, nil
}

func jsonResult(v any) *mcp.CallToolResult {
	b, _ := json.MarshalIndent(v, "", " ")
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

func errResult(format string, a ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}}}
}

func storeErr(e *recipestore.Error) *mcp.CallToolResult {
	if len(e.Extra) > 0 {
		b, _ := json.Marshal(e.Extra)
		return errResult("%s (%s) %s", e.Msg, e.Code, string(b))
	}
	return errResult("%s (%s)", e.Msg, e.Code)
}

func ptr[T any](v T) *T { return &v }

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: ptr(false)}
}

func writes(title string, destructive bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: ptr(destructive), OpenWorldHint: ptr(false)}
}

// ── Входы инструментов ──────────────────────────────────────────────────────

type noInput struct{}

type findInput struct {
	Query      string `json:"query,omitempty" jsonschema:"part of the recipe name or an ingredient; empty — all recipes"`
	DishTypeID uint   `json:"dish_type_id,omitempty" jsonschema:"only recipes of this dish type (id from list_dish_types)"`
}

type idInput struct {
	ID uint `json:"id" jsonschema:"recipe id from find_recipes"`
}

type previewInput struct {
	Text              string `json:"text" jsonschema:"recipes in the template format (see get_recipe_template); up to 50 recipes"`
	DefaultDishTypeID uint   `json:"default_dish_type_id,omitempty" jsonschema:"dish type for recipes without a Type line"`
}

type decisionInput struct {
	Index       int    `json:"index" jsonschema:"recipe number from preview_recipe_import (1, 2, …)"`
	Action      string `json:"action" jsonschema:"create (new recipe), replace (overwrite the existing recipe with the same name) or skip"`
	DishTypeID  uint   `json:"dish_type_id,omitempty" jsonschema:"dish type id for this recipe"`
	NewDishType string `json:"new_dish_type,omitempty" jsonschema:"create a new dish type with this name for this recipe (only if the user agreed)"`
}

type importInput struct {
	Text              string          `json:"text" jsonschema:"the same text that was checked with preview_recipe_import"`
	DefaultDishTypeID uint            `json:"default_dish_type_id,omitempty" jsonschema:"dish type for recipes without a Type line"`
	Decisions         []decisionInput `json:"decisions,omitempty" jsonschema:"what to do with each recipe; empty — new recipes are created, duplicates and recipes with errors are skipped"`
}

type undoInput struct {
	ImportID uint `json:"import_id" jsonschema:"import_id returned by import_recipes"`
}

type updateInput struct {
	ID           uint      `json:"id" jsonschema:"recipe id"`
	Name         *string   `json:"name,omitempty"`
	Description  *string   `json:"description,omitempty"`
	Ingredients  *[]string `json:"ingredients,omitempty" jsonschema:"the full new ingredient list for 1 serving, one per item: Product — 170 g"`
	Instructions *string   `json:"instructions,omitempty" jsonschema:"the full new How to cook text"`
	PrepTime     *int      `json:"prep_time,omitempty" jsonschema:"minutes"`
	CookTime     *int      `json:"cook_time,omitempty" jsonschema:"minutes"`
	Servings     *int      `json:"servings,omitempty" jsonschema:"set to 1 after recalculating quantities for one serving"`
	Tags         *[]string `json:"tags,omitempty"`
	VideoURL     *string   `json:"video_url,omitempty" jsonschema:"http(s) link to a video; empty string removes it"`
	SourceURL    *string   `json:"source_url,omitempty" jsonschema:"http(s) link to the recipe page; empty string removes it"`
	DishTypeID   *uint     `json:"dish_type_id,omitempty" jsonschema:"dish type id; 0 — no type"`
	IsActive     *bool     `json:"is_active,omitempty" jsonschema:"whether the recipe is in the menu rotation (cooking now)"`
}

func optID(v uint) *uint {
	if v == 0 {
		return nil
	}
	return &v
}

// ── Инструменты ─────────────────────────────────────────────────────────────

func (s *srv) addTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "list_dish_types", Annotations: readOnly("Dish types"),
		Description: "List the home's dish types (id and name). Every recipe has one type; recurring meals pick dishes by type."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
			w, err := s.who(req)
			if err != nil {
				return errResult("%v", err), nil, nil
			}
			var ts []models.DishType
			s.db.Where("organization_id = ?", w.orgID).Order("position, id").Find(&ts)
			out := make([]map[string]any, 0, len(ts))
			for _, t := range ts {
				out = append(out, map[string]any{"id": t.ID, "name": t.Name})
			}
			return jsonResult(out), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "find_recipes", Annotations: readOnly("Find recipes"),
		Description: "Find the home's recipes by name or ingredient (up to 200). Use it before adding recipes to avoid duplicates."},
		func(ctx context.Context, req *mcp.CallToolRequest, in findInput) (*mcp.CallToolResult, any, error) {
			w, err := s.who(req)
			if err != nil {
				return errResult("%v", err), nil, nil
			}
			q := s.db.Where("organization_id = ?", w.orgID)
			if in.DishTypeID != 0 {
				q = q.Where("dish_type_id = ?", in.DishTypeID)
			}
			if t := strings.TrimSpace(in.Query); t != "" {
				like := "%" + strings.ToLower(t) + "%"
				q = q.Where("LOWER(name) LIKE ? OR LOWER(ingredients) LIKE ?", like, like)
			}
			var rs []models.Recipe
			q.Order("name").Limit(200).Find(&rs)
			types := s.typeNames(w.orgID)
			out := make([]map[string]any, 0, len(rs))
			for _, r := range rs {
				out = append(out, map[string]any{"id": r.ID, "name": r.Name, "dish_type": typeName(types, r.DishTypeID),
					"cooking_now": r.IsActive, "servings": r.Servings, "minutes": r.PrepTime + r.CookTime})
			}
			return jsonResult(out), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "get_recipe", Annotations: readOnly("Get a recipe"),
		Description: "Get one recipe of the home: ingredients for 1 serving (one per line), how to cook, times, tags, links."},
		func(ctx context.Context, req *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, any, error) {
			w, err := s.who(req)
			if err != nil {
				return errResult("%v", err), nil, nil
			}
			var r models.Recipe
			if s.db.Where("organization_id = ?", w.orgID).First(&r, in.ID).Error != nil {
				return errResult("recipe %d not found", in.ID), nil, nil
			}
			return jsonResult(recipeView(r, s.typeNames(w.orgID))), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "get_recipe_template", Annotations: readOnly("Recipe template"),
		Description: "The text format for preview_recipe_import and import_recipes, with rules and an example."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: template}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "preview_recipe_import", Annotations: readOnly("Check recipes before saving"),
		Description: "Parse recipes written in the template format WITHOUT saving: for each recipe — status (new, duplicate of an existing recipe, error), dish type, main ingredient, errors and warnings. Fix errors in the text and check again, then show the user a summary."},
		func(ctx context.Context, req *mcp.CallToolRequest, in previewInput) (*mcp.CallToolResult, any, error) {
			w, err := s.who(req)
			if err != nil {
				return errResult("%v", err), nil, nil
			}
			p, e := recipestore.PreviewImport(s.db, w.orgID, recipestore.ImportInput{Text: in.Text, DefaultDishTypeID: optID(in.DefaultDishTypeID)})
			if e != nil {
				return storeErr(e), nil, nil
			}
			return jsonResult(p), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "import_recipes", Annotations: writes("Save recipes to the home", false),
		Description: "Save recipes (template format) to the home. Call only after preview_recipe_import and the user's agreement. New recipes go straight to the menu rotation. Returns import_id: the import can be undone within 24 hours (undo_recipe_import or in the app)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in importInput) (*mcp.CallToolResult, any, error) {
			w, err := s.who(req)
			if err != nil {
				return errResult("%v", err), nil, nil
			}
			ii := recipestore.ImportInput{Text: in.Text, DefaultDishTypeID: optID(in.DefaultDishTypeID)}
			for _, d := range in.Decisions {
				dd := recipestore.Decision{Index: d.Index, Action: d.Action, NewDishType: d.NewDishType}
				if d.DishTypeID != 0 {
					dd.DishTypeID = optID(d.DishTypeID)
				}
				ii.Decisions = append(ii.Decisions, dd)
			}
			if len(ii.Decisions) == 0 {
				p, e := recipestore.PreviewImport(s.db, w.orgID, ii)
				if e != nil {
					return storeErr(e), nil, nil
				}
				ii.Decisions = recipestore.DefaultDecisions(p)
			}
			r, e := recipestore.SaveImport(s.db, w.orgID, recipestore.Actor{UserID: w.userID, Name: w.name}, ii, recipestore.SourceMCP)
			if e != nil {
				return storeErr(e), nil, nil
			}
			return jsonResult(r), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "undo_recipe_import", Annotations: writes("Undo a recipe import", true),
		Description: "Delete the recipes created by an import (within 24 hours). Recipes that the import replaced stay as they are now."},
		func(ctx context.Context, req *mcp.CallToolRequest, in undoInput) (*mcp.CallToolResult, any, error) {
			w, err := s.who(req)
			if err != nil {
				return errResult("%v", err), nil, nil
			}
			deleted, replaced, e := recipestore.UndoImport(s.db, w.orgID, in.ImportID)
			if e != nil {
				return storeErr(e), nil, nil
			}
			return jsonResult(map[string]any{"deleted": deleted, "replaced_kept": replaced}), nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "update_recipe", Annotations: writes("Edit a recipe", true),
		Description: "Edit one recipe of the home. Only the fields you pass change; ingredients and instructions replace the whole list/text. Read it with get_recipe first and tell the user what you will change."},
		func(ctx context.Context, req *mcp.CallToolRequest, in updateInput) (*mcp.CallToolResult, any, error) {
			w, err := s.who(req)
			if err != nil {
				return errResult("%v", err), nil, nil
			}
			p := recipestore.Patch{Name: in.Name, Description: in.Description, Ingredients: in.Ingredients,
				Instructions: in.Instructions, PrepTime: in.PrepTime, CookTime: in.CookTime, Servings: in.Servings,
				Tags: in.Tags, VideoURL: in.VideoURL, SourceURL: in.SourceURL, DishTypeID: in.DishTypeID, IsActive: in.IsActive}
			r, e := recipestore.Update(s.db, w.orgID, in.ID, recipestore.Actor{UserID: w.userID, Name: w.name}, p)
			if e != nil {
				return storeErr(e), nil, nil
			}
			return jsonResult(recipeView(*r, s.typeNames(w.orgID))), nil, nil
		})
}

func (s *srv) typeNames(orgID uint) map[uint]string {
	var ts []models.DishType
	s.db.Where("organization_id = ?", orgID).Find(&ts)
	m := map[uint]string{}
	for _, t := range ts {
		m[t.ID] = t.Name
	}
	return m
}

func typeName(m map[uint]string, id *uint) string {
	if id == nil {
		return ""
	}
	return m[*id]
}

func recipeView(r models.Recipe, types map[uint]string) map[string]any {
	var ings []string
	for _, l := range strings.Split(r.Ingredients, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ings = append(ings, l)
		}
	}
	tags := []string{}
	for _, t := range strings.Split(r.Tags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	sort.Strings(tags)
	return map[string]any{"id": r.ID, "name": r.Name, "description": r.Description, "dish_type": typeName(types, r.DishTypeID),
		"dish_type_id": r.DishTypeID, "servings": r.Servings, "ingredients": ings, "instructions": r.Instructions,
		"prep_time": r.PrepTime, "cook_time": r.CookTime, "tags": tags, "video_url": r.VideoURL, "source_url": r.SourceURL,
		"cooking_now": r.IsActive, "updated_by": r.UpdatedByName, "updated_at": r.UpdatedAt.Format(time.RFC3339)}
}

var template = fmt.Sprintf(`Recipe text format for preview_recipe_import / import_recipes (up to %d recipes, %d KB):

## Chicken pilaf
Type: Lunch
Description: Fluffy, not greasy.
Prep: 15 min · Cooking: 45 min
Tags: rice, chicken
Link: https://example.com/pilaf

Ingredients:
Chicken thigh — 150 g
Long-grain rice — 70 g
Carrot — ½ pc

How to cook:
1. Cut the chicken…

Rules:
- Every recipe starts with "## Name". Quantities are for 1 serving — recalculate if the source is for more (or write "Servings: 4" and the app will ask to recalculate).
- One ingredient per line: "Product — amount". Salt, pepper, water may go without an amount.
- Type is one of the home's dish types (list_dish_types); an unknown type is offered as a new one.
- Labels may be in English or Russian (Тип, Описание, Подготовка, Готовка, Теги, Ссылка, Ингредиенты, Как готовить). Write the recipe itself in the user's language.
- Link to a video (YouTube etc.) is saved as the video, any other link — as the recipe's source.
- Times in minutes ("1 h 30 min" works too).`, recipeimport.MaxRecipes, recipeimport.MaxBytes/1024)
