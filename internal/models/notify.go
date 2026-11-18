package models

import "time"

// PushDevice — телефон, на который можно слать пуши (Expo push token).
// Один токен = одно устройство; при входе другим пользователем токен переезжает к нему.
type PushDevice struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	UserID     uint       `gorm:"index;not null" json:"user_id"`
	Token      string     `gorm:"uniqueIndex;not null" json:"token"`
	Platform   string     `json:"platform"` // ios | android
	DeviceName string     `json:"device_name,omitempty"`
	Locale     string     `json:"locale,omitempty"`
	Timezone   string     `json:"timezone,omitempty"`
	AppVersion string     `json:"app_version,omitempty"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"` // Expo ответил DeviceNotRegistered
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// NotificationPref — настройки уведомлений пользователя в конкретном доме.
// Строки нет → действуют значения по умолчанию (notify.DefaultPrefs).
type NotificationPref struct {
	ID              uint      `gorm:"primaryKey" json:"-"`
	UserID          uint      `gorm:"uniqueIndex:idx_notif_pref_user_org;not null" json:"-"`
	OrganizationID  uint      `gorm:"uniqueIndex:idx_notif_pref_user_org;not null" json:"-"`
	Disabled        string    `gorm:"type:text" json:"-"` // ключи выключенных видов через запятую
	MorningTime     string    `gorm:"type:varchar(5)" json:"morning_time"`
	DigestTime      string    `gorm:"type:varchar(5)" json:"digest_time"`
	ReminderLeadMin int       `json:"reminder_lead_min"`
	QuietOff        bool      `json:"-"` // тихие часы выключены (по умолчанию включены)
	QuietFrom       string    `gorm:"type:varchar(5)" json:"quiet_from"`
	QuietTo         string    `gorm:"type:varchar(5)" json:"quiet_to"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Notification — очередь и журнал уведомлений (outbox). Хендлер кладёт строку,
// фоновый отправщик (notify.Worker) доставляет её в Expo.
type Notification struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	OrganizationID uint       `gorm:"index;not null" json:"organization_id"`
	UserID         uint       `gorm:"index;not null" json:"user_id"`
	Kind           string     `gorm:"type:varchar(32);index;not null" json:"kind"`
	DedupKey       string     `gorm:"uniqueIndex;not null" json:"dedup_key"`
	GroupKey       string     `gorm:"index" json:"group_key,omitempty"` // склейка («дописано в список»)
	Params         string     `gorm:"type:text" json:"params"`          // JSON для текста; текст собирается при отправке на языке получателя
	URL            string     `json:"url,omitempty"`                    // куда вести по тапу
	ScheduledFor   time.Time  `gorm:"index;not null" json:"scheduled_for"`
	Status         string     `gorm:"type:varchar(16);index;not null;default:'queued'" json:"status"` // queued|sent|skipped|failed
	Deferred       bool       `json:"deferred"`                                                       // отложено тихими часами
	SentAt         *time.Time `json:"sent_at,omitempty"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// PushTicket — квитанция Expo на одно сообщение (уведомление × устройство);
// через ~15 минут по ней проверяется доставка.
type PushTicket struct {
	ID             uint       `gorm:"primaryKey"`
	TicketID       string     `gorm:"uniqueIndex;not null"`
	NotificationID uint       `gorm:"index"`
	DeviceID       uint       `gorm:"index"`
	CheckedAt      *time.Time `gorm:"index"`
	CreatedAt      time.Time  `gorm:"index"`
}

const (
	NotificationQueued  = "queued"
	NotificationSent    = "sent"
	NotificationSkipped = "skipped"
	NotificationFailed  = "failed"
)
