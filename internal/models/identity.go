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
	Phone        string    `json:"phone,omitempty"` // задел под P1 (магик-ссылка/OTP)
	AvatarURL    string    `json:"avatar_url,omitempty"`
	Locale       string    `gorm:"default:'ru'" json:"locale"`  // ru|en|el
	TokenVersion int       `gorm:"not null;default:0" json:"-"` // +1 при смене пароля — старые access-токены недействительны
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Organization — арендатор. Корень изоляции данных (organization_id во всех доменных моделях).
type Organization struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"not null" json:"name"`
	OwnerUserID uint   `gorm:"index" json:"owner_user_id"`
	// Сколько задач высокой важности можно поставить на один день
	HighPriorityLimit int `gorm:"default:3" json:"high_priority_limit"`
	// Часовой пояс дома (IANA): по нему считаются «сегодня», план дня, напоминания и тихие часы
	Timezone  string    `gorm:"default:'Europe/Nicosia'" json:"timezone"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	// Выходные и праздники (настраивает наниматель в карточке сотрудника)
	DaysOff             string    `gorm:"default:'7'" json:"days_off"`                // ISO-дни недели через запятую: 1 = Пн … 7 = Вс
	HolidayCountry      string    `gorm:"size:4;default:'CY'" json:"holiday_country"` // ISO-код страны праздников; "-" — без праздников
	VacationDaysPerYear int       `gorm:"default:24" json:"vacation_days_per_year"`   // рабочих дней отпуска в году
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
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

// PasswordReset — одноразовая ссылка «задать новый пароль» (хранится хэш, живёт 1 час).
type PasswordReset struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	UserID    uint       `gorm:"index;not null" json:"user_id"`
	TokenHash string     `gorm:"uniqueIndex;not null" json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// EmailChange — запрос на смену почты для входа. Почта меняется только после перехода по ссылке
// из письма на новый адрес (TokenHash). На старый адрес уходит уведомление со ссылкой отмены
// (CancelTokenHash). Токены хранятся хешем, ссылка живёт 24 часа, новый запрос гасит прежний.
type EmailChange struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	UserID          uint       `gorm:"index;not null" json:"user_id"`
	NewEmail        string     `gorm:"not null" json:"new_email"`
	TokenHash       string     `gorm:"uniqueIndex;not null" json:"-"`
	CancelTokenHash string     `gorm:"uniqueIndex;not null" json:"-"`
	ExpiresAt       time.Time  `json:"expires_at"`
	SentAt          time.Time  `json:"sent_at"`
	ConfirmedAt     *time.Time `json:"confirmed_at,omitempty"`
	CanceledAt      *time.Time `json:"canceled_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}
