package models

import "time"

// Role — роль участника в организации.
type Role string

const (
	RoleOwner   Role = "owner"
	RoleAdmin   Role = "admin"
	RoleManager Role = "manager"
	RoleHelper  Role = "helper"
)

// MembershipStatus — состояние членства.
type MembershipStatus string

const (
	MembershipInvited  MembershipStatus = "invited"
	MembershipActive   MembershipStatus = "active"
	MembershipDisabled MembershipStatus = "disabled"
)

// InviteStatus — состояние приглашения.
type InviteStatus string

const (
	InvitePending  InviteStatus = "pending"
	InviteAccepted InviteStatus = "accepted"
	InviteExpired  InviteStatus = "expired"
	InviteRevoked  InviteStatus = "revoked"
)

// User — глобальный аккаунт. Может состоять в нескольких организациях через Membership.
type User struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Email        string    `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string    `gorm:"not null" json:"-"` // bcrypt; никогда не отдаём наружу
	Name         string    `json:"name"`
	Phone        string    `json:"phone,omitempty"`      // задел под P1 (магик-ссылка/OTP)
	AvatarURL    string    `json:"avatar_url,omitempty"`
	Locale       string    `gorm:"default:'ru'" json:"locale"` // ru|en|el
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Organization — арендатор. Корень изоляции данных (organization_id во всех доменных моделях).
type Organization struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"not null" json:"name"`
	OwnerUserID uint      `gorm:"index" json:"owner_user_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Membership — связь User ↔ Organization с ролью. Через неё скоупятся все данные и проверяются права.
type Membership struct {
	ID             uint             `gorm:"primaryKey" json:"id"`
	UserID         uint             `gorm:"uniqueIndex:idx_user_org;not null" json:"user_id"`
	OrganizationID uint             `gorm:"uniqueIndex:idx_user_org;index;not null" json:"organization_id"`
	Role           Role             `gorm:"not null" json:"role"`
	Permissions    string           `gorm:"type:text" json:"permissions,omitempty"` // JSON-массив ключей разделов; в MVP пусто (права от роли)
	Status         MembershipStatus `gorm:"default:'active'" json:"status"`
	InvitedBy      *uint            `json:"invited_by,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

// Invite — приглашение в организацию (копируемая ссылка с токеном).
type Invite struct {
	ID             uint         `gorm:"primaryKey" json:"id"`
	OrganizationID uint         `gorm:"index;not null" json:"organization_id"`
	Email          string       `gorm:"index" json:"email"`
	Role           Role         `gorm:"not null" json:"role"`
	Token          string       `gorm:"uniqueIndex;not null" json:"token"`
	Status         InviteStatus `gorm:"default:'pending'" json:"status"`
	ExpiresAt      time.Time    `json:"expires_at"` // TTL 7 дней
	InvitedBy      uint         `json:"invited_by"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// RefreshToken — серверный refresh-токен (хранится хэш) с ротацией и отзывом.
type RefreshToken struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	UserID    uint       `gorm:"index;not null" json:"user_id"`
	TokenHash string     `gorm:"uniqueIndex;not null" json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}
