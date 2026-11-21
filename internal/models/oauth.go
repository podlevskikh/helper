package models

import (
	"time"

	"gorm.io/gorm"
)

// OAuth для коннектора Helper's Day в Claude (docs/work/recipes-mcp/PRD.md во фронте).
// Секреты (коды, токены) хранятся только хэшем SHA-256.

// OAuthClient — зарегистрированный клиент (Claude регистрируется сам, RFC 7591).
type OAuthClient struct {
	ID           uint      `gorm:"primaryKey" json:"-"`
	ClientID     string    `gorm:"uniqueIndex;not null" json:"client_id"`
	Name         string    `json:"client_name"`
	RedirectURIs string    `gorm:"type:text" json:"-"` // JSON-массив
	CreatedAt    time.Time `json:"created_at"`
}

// OAuthRequest — запрос на доступ, ждёт решения человека на экране согласия (10 минут).
type OAuthRequest struct {
	ID            string    `gorm:"primaryKey;size:64" json:"id"`
	ClientID      string    `gorm:"index;not null" json:"client_id"`
	RedirectURI   string    `gorm:"type:text;not null" json:"-"`
	State         string    `gorm:"type:text" json:"-"`
	CodeChallenge string    `gorm:"not null" json:"-"`
	Scope         string    `json:"scope"`
	Resource      string    `gorm:"type:text" json:"-"`
	UserID        *uint     `json:"-"` // кто открыл запрос — после этого чужой не примет решение
	ExpiresAt     time.Time `json:"expires_at"`
	CreatedAt     time.Time `json:"-"`
}

// OAuthCode — одноразовый код авторизации (60 секунд), выдан после «Разрешить».
type OAuthCode struct {
	ID            uint   `gorm:"primaryKey"`
	CodeHash      string `gorm:"uniqueIndex;not null"`
	ClientID      string `gorm:"not null"`
	RedirectURI   string `gorm:"type:text;not null"`
	CodeChallenge string `gorm:"not null"`
	Scope         string
	Resource      string    `gorm:"type:text"`
	UserID        uint      `gorm:"not null"`
	OrgID         uint      `gorm:"not null"`
	GrantID       uint      `gorm:"not null"`
	ExpiresAt     time.Time `gorm:"not null"`
	UsedAt        *time.Time
	CreatedAt     time.Time
}

// OAuthGrant — подключение: человек разрешил клиенту доступ к дому. «Отключить» отзывает его и все токены.
type OAuthGrant struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	ClientID   string     `gorm:"index;not null" json:"-"`
	ClientName string     `json:"client_name"`
	UserID     uint       `gorm:"index;not null" json:"-"`
	OrgID      uint       `gorm:"index;not null" json:"org_id"`
	Scope      string     `json:"scope"`
	Resource   string     `gorm:"type:text" json:"-"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Виды токенов.
const (
	OAuthAccess  = "access"
	OAuthRefresh = "refresh"
)

// OAuthToken — токен доступа (1 час) или обновления (60 дней, одноразовый: при обновлении выдаётся новый).
type OAuthToken struct {
	ID        uint       `gorm:"primaryKey"`
	TokenHash string     `gorm:"uniqueIndex;not null"`
	Kind      string     `gorm:"not null"` // access | refresh
	GrantID   uint       `gorm:"index;not null"`
	ExpiresAt time.Time  `gorm:"not null"`
	UsedAt    *time.Time // refresh: когда обменян на новый (повтор — отзыв подключения)
	RevokedAt *time.Time
	CreatedAt time.Time
}

// RevokeOAuthGrants — отозвать подключения человека к дому (ушёл, убрали, отняли права):
// если права потом вернут, старый токен не заработает снова — нужно новое согласие.
func RevokeOAuthGrants(tx *gorm.DB, userID, orgID uint) error {
	var ids []uint
	if err := tx.Model(&OAuthGrant{}).Where("user_id = ? AND org_id = ? AND revoked_at IS NULL", userID, orgID).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	if err := tx.Model(&OAuthGrant{}).Where("id IN ?", ids).Update("revoked_at", now).Error; err != nil {
		return err
	}
	return tx.Model(&OAuthToken{}).Where("grant_id IN ? AND revoked_at IS NULL", ids).Update("revoked_at", now).Error
}
