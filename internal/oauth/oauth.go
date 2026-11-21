// Package oauth — сервер авторизации OAuth 2.1 для коннектора Helper's Day в Claude (MCP).
//
// Поток: Claude находит нас по /.well-known/*, регистрируется сам (RFC 7591), открывает /oauth/authorize →
// мы отправляем человека на экран согласия в приложении (/app/oauth-consent) → он выбирает дом и жмёт «Разрешить» →
// Claude меняет код на токены (PKCE S256, RFC 8707 resource) → ходит в /mcp с Bearer-токеном.
// Подключить может только тот, у кого в доме manage_recipes; права проверяются заново на каждом вызове.
// Коды и токены хранятся только хэшем. docs/work/recipes-mcp/PRD.md во фронте.
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"podlevskikh/awesomeProject/internal/middleware"
	"podlevskikh/awesomeProject/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	// Scope — доступ к рецептам дома. offline_access — Claude просит его, чтобы получить токен обновления.
	Scope          = "recipes"
	ScopeOffline   = "offline_access"
	AccessTTL      = time.Hour
	RefreshTTL     = 60 * 24 * time.Hour
	CodeTTL        = 60 * time.Second
	RequestTTL     = 10 * time.Minute
	ClaudeRedirect = "https://claude.ai/api/mcp/auth_callback"
	// DefaultIssuer — адрес сервера на проде.
	DefaultIssuer = "https://www.helperday.app"
	// ClientName — клиент с адресом возврата claude.ai.
	ClientName = "Claude"
	// LocalClientName — клиент на этом компьютере (Claude Code, MCP Inspector).
	LocalClientName = "Claude Code (localhost)"
	// RefreshGrace — повтор того же токена обновления в эти секунды (потерянный ответ, повтор запроса)
	// не считаем утечкой: выдаём новую пару, но не чаще одного раза.
	RefreshGrace = 30 * time.Second
)

// Server — обработчики OAuth.
type Server struct {
	DB *gorm.DB
}

// New — сервер OAuth над базой.
func New(db *gorm.DB) *Server { return &Server{DB: db} }

// Issuer — наш адрес: OAUTH_ISSUER, для localhost — адрес запроса (локальные проверки), иначе прод.
// По Host/X-Forwarded-* строим адрес только для localhost: их подставляет кто угодно.
func Issuer(r *http.Request) string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("OAUTH_ISSUER")), "/"); v != "" {
		return v
	}
	if r != nil && isLoopbackHost(r.Host) {
		return "http://" + r.Host
	}
	return DefaultIssuer
}

// MCPURL — адрес MCP-сервера (он же resource в токенах).
func MCPURL(r *http.Request) string { return Issuer(r) + "/mcp" }

// ResourceMetadataURL — куда отправляет WWW-Authenticate при 401 на /mcp.
func ResourceMetadataURL(r *http.Request) string {
	return Issuer(r) + "/.well-known/oauth-protected-resource/mcp"
}

func consentURL(r *http.Request) string {
	if v := strings.TrimSpace(os.Getenv("OAUTH_CONSENT_URL")); v != "" {
		return v
	}
	return Issuer(r) + "/app/oauth-consent"
}

func isLoopbackHost(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.Trim(strings.ToLower(h), "[]")
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// Register вешает маршруты OAuth на роутер. authMw — вход в приложение (для экрана согласия и «Подключений»).
func (s *Server) Register(router *gin.Engine, authMw gin.HandlerFunc, registerLimit, authorizeLimit, tokenLimit gin.HandlerFunc) {
	router.GET("/.well-known/oauth-protected-resource", s.protectedResource)
	router.GET("/.well-known/oauth-protected-resource/mcp", s.protectedResource)
	router.GET("/.well-known/oauth-authorization-server", s.serverMetadata)
	router.POST("/oauth/register", registerLimit, s.register)
	router.GET("/oauth/authorize", authorizeLimit, s.authorize)
	router.POST("/oauth/token", tokenLimit, s.token)
	router.GET("/oauth/requests/:id", authMw, s.getRequest)
	router.POST("/oauth/requests/:id/decision", authMw, s.decide)
	router.GET("/me/connections", authMw, s.listConnections)
	router.DELETE("/me/connections/:id", authMw, s.revokeConnection)
}

func (s *Server) protectedResource(c *gin.Context) {
	c.Header("Access-Control-Allow-Origin", "*")
	c.JSON(http.StatusOK, gin.H{
		"resource":                 MCPURL(c.Request),
		"authorization_servers":    []string{Issuer(c.Request)},
		"scopes_supported":         []string{Scope},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Helper's Day",
	})
}

func (s *Server) serverMetadata(c *gin.Context) {
	iss := Issuer(c.Request)
	c.Header("Access-Control-Allow-Origin", "*")
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                iss,
		"authorization_endpoint":                iss + "/oauth/authorize",
		"token_endpoint":                        iss + "/oauth/token",
		"registration_endpoint":                 iss + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{Scope, ScopeOffline},
	})
}

// ── Регистрация клиента (RFC 7591) ──────────────────────────────────────────

// allowedRedirect — куда можно вернуть код: Claude (claude.ai) и локальный адрес (Claude Code, MCP Inspector).
func allowedRedirect(raw string) bool {
	if raw == ClaudeRedirect {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" {
		return false
	}
	return isLoopbackHost(u.Host)
}

// displayName — как назвать клиента на экране согласия и в «Подключениях». Имя, которое клиент прислал сам,
// не показываем: его выбирает кто угодно («Helper's Day официальный»). Называем по адресу возврата.
func displayName(redirect string) string {
	if redirect == ClaudeRedirect {
		return ClientName
	}
	return LocalClientName
}

// sameRedirect — адрес из запроса совпадает с зарегистрированным; у локальных адресов порт не важен (RFC 8252).
func sameRedirect(registered, got string) bool {
	if registered == got {
		return true
	}
	a, err1 := url.Parse(registered)
	b, err2 := url.Parse(got)
	if err1 != nil || err2 != nil || a.Scheme != "http" || b.Scheme != "http" {
		return false
	}
	return isLoopbackHost(a.Host) && isLoopbackHost(b.Host) && a.Hostname() == b.Hostname() && a.Path == b.Path && a.RawQuery == b.RawQuery
}

func (s *Server) register(c *gin.Context) {
	var in struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_client_metadata", "error_description": "bad JSON"})
		return
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 5 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_redirect_uri", "error_description": "1–5 redirect_uris required"})
		return
	}
	for _, u := range in.RedirectURIs {
		if len(u) > 500 || !allowedRedirect(u) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_redirect_uri", "error_description": "redirect_uri not allowed: " + u})
			return
		}
	}
	if m := in.TokenEndpointAuthMethod; m != "" && m != "none" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_client_metadata", "error_description": "only public clients (token_endpoint_auth_method=none)"})
		return
	}
	name := strings.TrimSpace(in.ClientName)
	if r := []rune(name); len(r) > 60 {
		name = string(r[:60])
	}
	if name == "" {
		name = ClientName
	}
	s.cleanup(time.Now())
	uris, _ := json.Marshal(in.RedirectURIs)
	cl := models.OAuthClient{ClientID: "hd_" + randomHex(16), Name: name, RedirectURIs: string(uris)}
	if err := s.DB.Create(&cl).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"client_id":                  cl.ClientID,
		"client_id_issued_at":        cl.CreatedAt.Unix(),
		"client_name":                cl.Name,
		"redirect_uris":              in.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

func (s *Server) client(clientID string) (*models.OAuthClient, []string) {
	var cl models.OAuthClient
	if clientID == "" || s.DB.Where("client_id = ?", clientID).First(&cl).Error != nil {
		return nil, nil
	}
	var uris []string
	_ = json.Unmarshal([]byte(cl.RedirectURIs), &uris)
	return &cl, uris
}

// ── Авторизация ─────────────────────────────────────────────────────────────

var challengeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43,128}$`)

// scopeOK — запрошенные права: только наши; пусто — recipes.
func scopeOK(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return Scope, true
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range strings.Fields(raw) {
		if s != Scope && s != ScopeOffline {
			return "", false
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if !seen[Scope] {
		out = append([]string{Scope}, out...)
	}
	return strings.Join(out, " "), true
}

// resourceOK — RFC 8707: токен выдаём только для нашего MCP-сервера.
func resourceOK(r *http.Request, res string) bool {
	if res == "" {
		return true
	}
	res = strings.TrimRight(res, "/")
	return res == MCPURL(r) || res == Issuer(r)
}

func redirectWith(base string, params url.Values) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, vs := range params {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Server) authorize(c *gin.Context) {
	q := c.Request.URL.Query()
	cl, uris := s.client(q.Get("client_id"))
	redirect := q.Get("redirect_uri")
	if cl == nil {
		c.String(http.StatusBadRequest, "Unknown client. Remove the connector in Claude and add it again.")
		return
	}
	okRedirect := false
	for _, u := range uris {
		okRedirect = okRedirect || sameRedirect(u, redirect)
	}
	if redirect == "" || !okRedirect {
		c.String(http.StatusBadRequest, "redirect_uri does not match the registered one.")
		return
	}
	// Дальше ошибки — обратно клиенту, как велит OAuth.
	state := q.Get("state")
	back := func(code, desc string) {
		c.Redirect(http.StatusFound, redirectWith(redirect, url.Values{"error": {code}, "error_description": {desc}, "state": {state}}))
	}
	if q.Get("response_type") != "code" {
		back("unsupported_response_type", "response_type must be code")
		return
	}
	if q.Get("code_challenge_method") != "S256" || !challengeRe.MatchString(q.Get("code_challenge")) {
		back("invalid_request", "PKCE with S256 is required")
		return
	}
	scope, ok := scopeOK(q.Get("scope"))
	if !ok {
		back("invalid_scope", "unknown scope")
		return
	}
	if !resourceOK(c.Request, q.Get("resource")) {
		back("invalid_target", "unknown resource")
		return
	}
	if len(state) > 500 {
		back("invalid_request", "state is too long")
		return
	}
	now := time.Now()
	s.cleanup(now)
	req := models.OAuthRequest{ID: randomToken(24), ClientID: cl.ClientID, RedirectURI: redirect, State: state,
		CodeChallenge: q.Get("code_challenge"), Scope: scope, Resource: MCPURL(c.Request), ExpiresAt: now.Add(RequestTTL)}
	if err := s.DB.Create(&req).Error; err != nil {
		back("server_error", "try again")
		return
	}
	c.Redirect(http.StatusFound, consentURL(c.Request)+"?req="+url.QueryEscape(req.ID))
}

// cleanup — убрать отработавшее, чтобы таблицы не росли: истёкшие запросы и коды, истёкшие и отозванные токены,
// давно отозванные подключения, клиентов без подключений старше недели. Не чаще раза в 10 минут.
func (s *Server) cleanup(now time.Time) {
	if !cleanupGate.allow(now) {
		return
	}
	day := now.Add(-24 * time.Hour)
	s.DB.Where("expires_at < ?", now).Delete(&models.OAuthRequest{})
	s.DB.Where("expires_at < ?", day).Delete(&models.OAuthCode{})
	s.DB.Where("expires_at < ? OR revoked_at < ?", day, day).Delete(&models.OAuthToken{})
	s.DB.Where("revoked_at < ?", now.Add(-30*24*time.Hour)).Delete(&models.OAuthGrant{})
	s.DB.Where("created_at < ? AND client_id NOT IN (?)", now.Add(-7*24*time.Hour),
		s.DB.Model(&models.OAuthGrant{}).Select("client_id")).Delete(&models.OAuthClient{})
}

type gate struct {
	mu   sync.Mutex
	last time.Time
}

func (g *gate) allow(now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Sub(g.last) < 10*time.Minute {
		return false
	}
	g.last = now
	return true
}

var cleanupGate = &gate{}

// pending — запрос на доступ, который открыл этот человек (или ещё никто).
func (s *Server) pending(c *gin.Context) (*models.OAuthRequest, uint, bool) {
	uid := c.GetUint(middleware.ContextKeyUserID)
	var req models.OAuthRequest
	if err := s.DB.Where("id = ? AND expires_at > ?", c.Param("id"), time.Now()).First(&req).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "request not found or expired", "code": "expired"})
		return nil, 0, false
	}
	if req.UserID != nil && *req.UserID != uid {
		c.JSON(http.StatusNotFound, gin.H{"error": "request not found or expired", "code": "expired"})
		return nil, 0, false
	}
	if req.UserID == nil {
		// Первый, кто открыл, — владелец запроса; гонку закрываем условием.
		res := s.DB.Model(&models.OAuthRequest{}).Where("id = ? AND user_id IS NULL", req.ID).Update("user_id", uid)
		if res.RowsAffected == 0 {
			s.DB.First(&req, "id = ?", req.ID)
			if req.UserID == nil || *req.UserID != uid {
				c.JSON(http.StatusNotFound, gin.H{"error": "request not found or expired", "code": "expired"})
				return nil, 0, false
			}
		}
		req.UserID = &uid
	}
	return &req, uid, true
}

// homes — дома, где человек может подключить коннектор (manage_recipes).
func (s *Server) homes(uid uint) []gin.H {
	var ms []models.Membership
	s.DB.Where("user_id = ? AND status = ?", uid, models.MembershipActive).Find(&ms)
	out := []gin.H{}
	for i := range ms {
		if !middleware.Can(&ms[i], middleware.CapManageRecipes) {
			continue
		}
		var org models.Organization
		if s.DB.Select("id", "name").First(&org, ms[i].OrganizationID).Error != nil {
			continue
		}
		out = append(out, gin.H{"org_id": org.ID, "name": org.Name, "role": ms[i].Role})
	}
	return out
}

func (s *Server) getRequest(c *gin.Context) {
	req, uid, ok := s.pending(c)
	if !ok {
		return
	}
	name := displayName(req.RedirectURI)
	c.JSON(http.StatusOK, gin.H{"id": req.ID, "client_name": name, "local": name != ClientName, "scope": req.Scope, "homes": s.homes(uid),
		"expires_at": req.ExpiresAt})
}

func (s *Server) decide(c *gin.Context) {
	var in struct {
		Approve bool `json:"approve"`
		OrgID   uint `json:"org_id"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req, uid, ok := s.pending(c)
	if !ok {
		return
	}
	backErr := func(code string) {
		s.DB.Delete(&models.OAuthRequest{}, "id = ?", req.ID)
		c.JSON(http.StatusOK, gin.H{"redirect": redirectWith(req.RedirectURI, url.Values{"error": {code}, "state": {req.State}})})
	}
	if !in.Approve {
		backErr("access_denied")
		return
	}
	var m models.Membership
	if s.DB.Where("user_id = ? AND organization_id = ? AND status = ?", uid, in.OrgID, models.MembershipActive).First(&m).Error != nil ||
		!middleware.Can(&m, middleware.CapManageRecipes) {
		c.JSON(http.StatusForbidden, gin.H{"error": "only those who run the home can connect Claude", "code": "forbidden"})
		return
	}
	name := displayName(req.RedirectURI)
	code := randomToken(32)
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		// Запрос одноразовый: удалили — значит решение принято один раз.
		if res := tx.Delete(&models.OAuthRequest{}, "id = ?", req.ID); res.Error != nil || res.RowsAffected == 0 {
			return errors.New("request already used")
		}
		g := models.OAuthGrant{ClientID: req.ClientID, ClientName: name, UserID: uid, OrgID: in.OrgID, Scope: req.Scope, Resource: req.Resource}
		if err := tx.Create(&g).Error; err != nil {
			return err
		}
		return tx.Create(&models.OAuthCode{CodeHash: hash(code), ClientID: req.ClientID, RedirectURI: req.RedirectURI,
			CodeChallenge: req.CodeChallenge, Scope: req.Scope, Resource: req.Resource, UserID: uid, OrgID: in.OrgID,
			GrantID: g.ID, ExpiresAt: time.Now().Add(CodeTTL)}).Error
	})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "request not found or expired", "code": "expired"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"redirect": redirectWith(req.RedirectURI, url.Values{"code": {code}, "state": {req.State},
		"iss": {Issuer(c.Request)}})})
}

// ── Токены ──────────────────────────────────────────────────────────────────

func tokenErr(c *gin.Context, code, desc string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusBadRequest, gin.H{"error": code, "error_description": desc})
}

func (s *Server) token(c *gin.Context) {
	if err := c.Request.ParseForm(); err != nil {
		tokenErr(c, "invalid_request", "form expected")
		return
	}
	f := c.Request.PostForm
	switch f.Get("grant_type") {
	case "authorization_code":
		s.exchangeCode(c, f)
	case "refresh_token":
		s.refresh(c, f)
	default:
		tokenErr(c, "unsupported_grant_type", "authorization_code or refresh_token")
	}
}

// activeGrant — подключение не отозвано, и у человека в доме всё ещё есть manage_recipes.
func (s *Server) activeGrant(id uint) (*models.OAuthGrant, bool) {
	var g models.OAuthGrant
	if s.DB.First(&g, id).Error != nil || g.RevokedAt != nil {
		return nil, false
	}
	var m models.Membership
	if s.DB.Where("user_id = ? AND organization_id = ? AND status = ?", g.UserID, g.OrgID, models.MembershipActive).First(&m).Error != nil ||
		!middleware.Can(&m, middleware.CapManageRecipes) {
		return nil, false
	}
	return &g, true
}

func (s *Server) exchangeCode(c *gin.Context, f url.Values) {
	var code models.OAuthCode
	if s.DB.Where("code_hash = ?", hash(f.Get("code"))).First(&code).Error != nil {
		tokenErr(c, "invalid_grant", "unknown code")
		return
	}
	if code.UsedAt != nil {
		// Код показали дважды — кто-то перехватил: отзываем подключение.
		s.revokeGrant(code.GrantID)
		tokenErr(c, "invalid_grant", "code already used")
		return
	}
	now := time.Now()
	if res := s.DB.Model(&models.OAuthCode{}).Where("id = ? AND used_at IS NULL", code.ID).Update("used_at", now); res.RowsAffected == 0 {
		s.revokeGrant(code.GrantID)
		tokenErr(c, "invalid_grant", "code already used")
		return
	}
	if now.After(code.ExpiresAt) {
		tokenErr(c, "invalid_grant", "code expired")
		return
	}
	if f.Get("client_id") != code.ClientID || f.Get("redirect_uri") != code.RedirectURI {
		tokenErr(c, "invalid_grant", "client_id or redirect_uri mismatch")
		return
	}
	if !pkceOK(f.Get("code_verifier"), code.CodeChallenge) {
		tokenErr(c, "invalid_grant", "PKCE verification failed")
		return
	}
	if !resourceOK(c.Request, f.Get("resource")) {
		tokenErr(c, "invalid_target", "unknown resource")
		return
	}
	g, ok := s.activeGrant(code.GrantID)
	if !ok {
		tokenErr(c, "invalid_grant", "access revoked")
		return
	}
	s.issue(c, g)
}

func (s *Server) refresh(c *gin.Context, f url.Values) {
	var t models.OAuthToken
	if s.DB.Where("token_hash = ? AND kind = ?", hash(f.Get("refresh_token")), models.OAuthRefresh).First(&t).Error != nil {
		tokenErr(c, "invalid_grant", "unknown refresh token")
		return
	}
	now := time.Now()
	if t.UsedAt != nil {
		// Повтор сразу после обмена (ответ потерялся) — ещё одна пара, один раз; позже — утечка: отзываем подключение.
		if now.Sub(*t.UsedAt) <= RefreshGrace && t.RevokedAt == nil {
			if res := s.DB.Model(&models.OAuthToken{}).Where("id = ? AND revoked_at IS NULL", t.ID).Update("revoked_at", now); res.RowsAffected == 1 {
				if g, ok := s.activeGrant(t.GrantID); ok {
					s.issue(c, g)
					return
				}
			}
		}
		s.revokeGrant(t.GrantID)
		tokenErr(c, "invalid_grant", "refresh token already used")
		return
	}
	if t.RevokedAt != nil || now.After(t.ExpiresAt) {
		tokenErr(c, "invalid_grant", "refresh token expired")
		return
	}
	if res := s.DB.Model(&models.OAuthToken{}).Where("id = ? AND used_at IS NULL", t.ID).Update("used_at", now); res.RowsAffected == 0 {
		s.revokeGrant(t.GrantID)
		tokenErr(c, "invalid_grant", "refresh token already used")
		return
	}
	g, ok := s.activeGrant(t.GrantID)
	if !ok {
		tokenErr(c, "invalid_grant", "access revoked")
		return
	}
	if cid := f.Get("client_id"); cid != "" && cid != g.ClientID {
		tokenErr(c, "invalid_grant", "client_id mismatch")
		return
	}
	if !resourceOK(c.Request, f.Get("resource")) {
		tokenErr(c, "invalid_target", "unknown resource")
		return
	}
	s.issue(c, g)
}

func (s *Server) issue(c *gin.Context, g *models.OAuthGrant) {
	access, refresh := "hd_at_"+randomToken(32), "hd_rt_"+randomToken(32)
	now := time.Now()
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&models.OAuthToken{TokenHash: hash(access), Kind: models.OAuthAccess, GrantID: g.ID, ExpiresAt: now.Add(AccessTTL)}).Error; err != nil {
			return err
		}
		return tx.Create(&models.OAuthToken{TokenHash: hash(refresh), Kind: models.OAuthRefresh, GrantID: g.ID, ExpiresAt: now.Add(RefreshTTL)}).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"access_token": access, "token_type": "Bearer", "expires_in": int(AccessTTL.Seconds()),
		"refresh_token": refresh, "scope": g.Scope})
}

func (s *Server) revokeGrant(id uint) {
	now := time.Now()
	s.DB.Model(&models.OAuthGrant{}).Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", now)
	s.DB.Model(&models.OAuthToken{}).Where("grant_id = ? AND revoked_at IS NULL", id).Update("revoked_at", now)
}

// Access — кто стоит за токеном доступа на /mcp.
type Access struct {
	UserID    uint
	OrgID     uint
	GrantID   uint
	Scopes    []string
	ExpiresAt time.Time
}

// ErrInvalid — токен не годится (нет, истёк, отозван, прав больше нет).
var ErrInvalid = errors.New("invalid token")

// Verify — проверка токена доступа для /mcp: не истёк, не отозван, выдан для нашего MCP-адреса,
// у человека в доме всё ещё manage_recipes.
func (s *Server) Verify(r *http.Request, token string) (*Access, error) {
	if !strings.HasPrefix(token, "hd_at_") {
		return nil, ErrInvalid
	}
	var t models.OAuthToken
	if s.DB.Where("token_hash = ? AND kind = ?", hash(token), models.OAuthAccess).First(&t).Error != nil {
		return nil, ErrInvalid
	}
	now := time.Now()
	if t.RevokedAt != nil || now.After(t.ExpiresAt) {
		return nil, ErrInvalid
	}
	g, ok := s.activeGrant(t.GrantID)
	if !ok || (g.Resource != "" && g.Resource != MCPURL(r)) {
		return nil, ErrInvalid
	}
	if g.LastUsedAt == nil || now.Sub(*g.LastUsedAt) > time.Minute {
		s.DB.Model(&models.OAuthGrant{}).Where("id = ?", g.ID).Update("last_used_at", now)
	}
	return &Access{UserID: g.UserID, OrgID: g.OrgID, GrantID: g.ID, Scopes: strings.Fields(g.Scope), ExpiresAt: t.ExpiresAt}, nil
}

// ── Подключения ─────────────────────────────────────────────────────────────

func (s *Server) listConnections(c *gin.Context) {
	uid := c.GetUint(middleware.ContextKeyUserID)
	var gs []models.OAuthGrant
	s.DB.Where("user_id = ? AND revoked_at IS NULL", uid).Order("created_at DESC").Find(&gs)
	out := []gin.H{}
	for _, g := range gs {
		if _, ok := s.activeGrant(g.ID); !ok {
			continue // ушли из дома или нет прав — подключение не работает
		}
		// Подключение без живого токена обновления (Claude так и не обменял код или срок вышел) не показываем.
		var n int64
		s.DB.Model(&models.OAuthToken{}).Where("grant_id = ? AND kind = ? AND revoked_at IS NULL AND expires_at > ?",
			g.ID, models.OAuthRefresh, time.Now()).Count(&n)
		if n == 0 {
			continue
		}
		var org models.Organization
		s.DB.Select("id", "name").First(&org, g.OrgID)
		out = append(out, gin.H{"id": g.ID, "client_name": g.ClientName, "org_id": g.OrgID, "org_name": org.Name,
			"scope": g.Scope, "created_at": g.CreatedAt, "last_used_at": g.LastUsedAt})
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) revokeConnection(c *gin.Context) {
	uid := c.GetUint(middleware.ContextKeyUserID)
	var g models.OAuthGrant
	if s.DB.Where("id = ? AND user_id = ? AND revoked_at IS NULL", c.Param("id"), uid).First(&g).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "connection not found"})
		return
	}
	s.revokeGrant(g.ID)
	c.JSON(http.StatusOK, gin.H{"revoked": true})
}

// ── Криптография ────────────────────────────────────────────────────────────

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func randomToken(n int) string { return base64.RawURLEncoding.EncodeToString(randomBytes(n)) }
func randomHex(n int) string   { return hex.EncodeToString(randomBytes(n)) }

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func pkceOK(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}
