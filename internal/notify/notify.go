// Package notify — пуш-уведомления: очередь (models.Notification), настройки,
// события из хендлеров, пуши по времени и доставка через Expo Push API.
//
// Хендлеры только кладут строки в очередь (Enqueue / события ниже); доставкой,
// тихими часами и склейкой занимается Worker.
package notify

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
	_ "time/tzdata" // часовые пояса домов и в контейнере без системной tzdata

	"podlevskikh/awesomeProject/internal/clock"
	"podlevskikh/awesomeProject/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Kind string

const (
	KindMorning       Kind = "morning_plan"     // П1 помощнику: план на день
	KindReminder      Kind = "task_reminder"    // П2 помощнику: напоминание о задаче со временем
	KindTaskNew       Kind = "task_new"         // П3 помощнику: новая задача на сегодня/завтра
	KindTaskMoved     Kind = "task_moved"       // П4 помощнику: задачу перенесли
	KindTaskCancelled Kind = "task_cancelled"   // П5 помощнику: задачу убрали
	KindListClosed    Kind = "list_closed"      // П6 помощнику: покупки привезли
	KindListSent      Kind = "list_sent"        // Н1 нанимателю: прислан список покупок
	KindListAdded     Kind = "list_added"       // Н2 нанимателю: дописано в список
	KindDigest        Kind = "day_digest"       // Н3 нанимателю: итог дня
	KindDayOff        Kind = "day_off_tomorrow" // вместе с итогом дня: завтра помощник отдыхает — его регулярные задачи на вас
	KindImportantDone Kind = "important_done"   // Н4 нанимателю: важная задача выполнена
	KindMemberJoined  Kind = "member_joined"    // Н5 нанимателю: новый человек в доме
	KindQuietSummary  Kind = "quiet_summary"    // склейка накопленного за тихие часы
	KindTest          Kind = "test"
)

// prefKey — какой переключатель в «Настройках → Уведомления» управляет видом.
// Пустая строка — не выключается.
func prefKey(k Kind) string {
	switch k {
	case KindMorning:
		return "morning"
	case KindReminder:
		return "remind"
	case KindTaskNew, KindTaskMoved, KindTaskCancelled:
		return "changes"
	case KindListClosed:
		return "bought"
	case KindListSent, KindListAdded:
		return "lists"
	case KindDigest, KindDayOff:
		return "digest"
	case KindImportantDone:
		return "important"
	}
	return ""
}

// PrefKeys — все ключи переключателей (для проверки ввода).
var PrefKeys = []string{"morning", "remind", "changes", "bought", "lists", "digest", "important"}

// channel — канал Android (на iOS — группа в Центре уведомлений).
func channel(k Kind) string {
	switch k {
	case KindListSent, KindListAdded, KindListClosed:
		return "shopping"
	case KindDigest, KindQuietSummary, KindDayOff:
		return "digest"
	case KindMemberJoined:
		return "team"
	}
	return "tasks"
}

// invalidate — какие данные приложению стоит перечитать, если пуш пришёл при открытом экране.
func invalidate(k Kind) []string {
	switch k {
	case KindListSent, KindListAdded, KindListClosed:
		return []string{"shopping"}
	case KindMemberJoined:
		return []string{"members"}
	case KindTest, KindQuietSummary:
		return nil
	}
	return []string{"schedule"}
}

// Now — текущее время (подменяется в тестах).
var Now = time.Now

// Item — уведомление для постановки в очередь.
type Item struct {
	OrgID    uint
	UserID   uint
	Kind     Kind
	DedupKey string // повтор с тем же ключом игнорируется
	Params   Params
	URL      string
	At       time.Time // когда отправить; ноль — сразу
}

// Enqueue кладёт уведомление в очередь. Повтор DedupKey молча пропускается.
func Enqueue(db *gorm.DB, it Item) {
	if it.UserID == 0 {
		return
	}
	at := it.At
	if at.IsZero() {
		at = Now()
	}
	key := it.DedupKey
	if key == "" {
		key = fmt.Sprintf("%s:%d:%d", it.Kind, it.UserID, Now().UnixNano())
	}
	raw, _ := json.Marshal(it.Params)
	n := models.Notification{
		OrganizationID: it.OrgID,
		UserID:         it.UserID,
		Kind:           string(it.Kind),
		DedupKey:       key,
		Params:         string(raw),
		URL:            it.URL,
		ScheduledFor:   at,
		Status:         models.NotificationQueued,
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&n).Error; err != nil {
		log.Printf("notify: enqueue %s for user %d: %v", it.Kind, it.UserID, err)
	}
}

// ── Кто получает ────────────────────────────────────────────────────────────

func activeMembers(db *gorm.DB, orgID uint, roles ...models.Role) []models.Membership {
	var ms []models.Membership
	db.Where("organization_id = ? AND status = ? AND role IN ?", orgID, models.MembershipActive, roles).Find(&ms)
	return ms
}

// employers — наниматели дома: owner, admin, manager.
func employers(db *gorm.DB, orgID uint) []uint {
	var ids []uint
	for _, m := range activeMembers(db, orgID, models.RoleOwner, models.RoleAdmin, models.RoleManager) {
		ids = append(ids, m.UserID)
	}
	return ids
}

func helpers(db *gorm.DB, orgID uint) []uint {
	var ids []uint
	for _, m := range activeMembers(db, orgID, models.RoleHelper) {
		ids = append(ids, m.UserID)
	}
	return ids
}

// taskAssignees — кому относится задача: исполнитель, а если его нет — все помощники дома.
func taskAssignees(db *gorm.DB, orgID uint, assigned *uint) []uint {
	if assigned != nil && *assigned != 0 {
		return []uint{*assigned}
	}
	return helpers(db, orgID)
}

func without(ids []uint, actor uint) []uint {
	out := ids[:0:0]
	for _, id := range ids {
		if id != actor {
			out = append(out, id)
		}
	}
	return out
}

// UserName — имя для текста: имя пользователя или часть email до @.
func UserName(db *gorm.DB, id uint) string {
	var u models.User
	if id == 0 || db.First(&u, id).Error != nil {
		return ""
	}
	if n := strings.TrimSpace(u.Name); n != "" {
		return n
	}
	return strings.Split(u.Email, "@")[0]
}

// ── Время дома ──────────────────────────────────────────────────────────────

const defaultTZ = "Europe/Nicosia"

// OrgLocation — часовой пояс дома.
func OrgLocation(db *gorm.DB, orgID uint) *time.Location {
	return clock.OrgLocation(db, orgID)
}

// CalendarDay — календарная дата daily_schedules.date. В базе встречается и полночь UTC,
// и полночь пояса сервера; сдвиг на 12 часов приводит обе к одному дню.
func CalendarDay(t time.Time) string {
	return t.UTC().Add(12 * time.Hour).Format("2006-01-02")
}

// dayWindow — интервал поиска daily_schedules.date для календарного дня.
func dayWindow(day string) (time.Time, time.Time) {
	d, _ := time.Parse("2006-01-02", day)
	return d.Add(-12 * time.Hour), d.Add(12 * time.Hour)
}

// relativeDay — today | tomorrow | "" (другой день) в часовом поясе дома.
func relativeDay(day string, loc *time.Location) string {
	now := Now().In(loc)
	switch day {
	case now.Format("2006-01-02"):
		return "today"
	case now.AddDate(0, 0, 1).Format("2006-01-02"):
		return "tomorrow"
	}
	return ""
}

func taskDay(db *gorm.DB, t *models.ScheduleTask) string {
	var s models.DailySchedule
	if db.Select("id", "date").First(&s, t.ScheduleID).Error != nil {
		return ""
	}
	return CalendarDay(s.Date)
}

// EffectivePriority — важность задачи: своя или категории.
func EffectivePriority(db *gorm.DB, t *models.ScheduleTask) string {
	switch t.Priority {
	case "high", "mid", "normal":
		return t.Priority
	}
	if t.TaskCategory != nil {
		return t.TaskCategory.Priority
	}
	if t.TaskCategoryID != nil {
		var cat models.TaskCategory
		if db.Select("id", "priority").First(&cat, *t.TaskCategoryID).Error == nil {
			return cat.Priority
		}
	}
	return "normal"
}

// taskURL — экран задачи; дата нужна, чтобы найти задачу в расписании дня.
func taskURL(id uint, day string) string {
	if day == "" {
		return fmt.Sprintf("/task/%d", id)
	}
	return fmt.Sprintf("/task/%d?date=%s", id, day)
}

// ── События задач ───────────────────────────────────────────────────────────

// TaskCreated — наниматель поставил задачу (П3). Пуш — только если она на сегодня или завтра.
func TaskCreated(db *gorm.DB, actorID uint, t *models.ScheduleTask) {
	loc := OrgLocation(db, t.OrganizationID)
	day := taskDay(db, t)
	rel := relativeDay(day, loc)
	if rel == "" {
		return
	}
	actor := UserName(db, actorID)
	for _, uid := range without(taskAssignees(db, t.OrganizationID, t.AssignedToUserID), actorID) {
		Enqueue(db, Item{
			OrgID: t.OrganizationID, UserID: uid, Kind: KindTaskNew,
			DedupKey: fmt.Sprintf("task-new:%d:%d", t.ID, uid),
			Params:   Params{Actor: actor, Title: t.Title, TaskType: t.TaskType, Time: t.Time, Day: rel},
			URL:      taskURL(t.ID, day),
		})
	}
}

// TaskUpdated — наниматель изменил задачу (П4, а при смене исполнителя П3/П5).
// Правки текста пушем не присылаем.
func TaskUpdated(db *gorm.DB, actorID uint, before, after *models.ScheduleTask) {
	loc := OrgLocation(db, after.OrganizationID)
	day := taskDay(db, after)
	rel := relativeDay(day, loc)
	if rel == "" || after.Completed {
		return
	}
	actor := UserName(db, actorID)
	oldTo := without(taskAssignees(db, before.OrganizationID, before.AssignedToUserID), actorID)
	newTo := without(taskAssignees(db, after.OrganizationID, after.AssignedToUserID), actorID)
	stamp := Now().Unix()
	in := func(ids []uint, id uint) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	}
	p := Params{Actor: actor, Title: after.Title, TaskType: after.TaskType, Time: after.Time, Day: rel}
	for _, uid := range newTo {
		if !in(oldTo, uid) {
			Enqueue(db, Item{OrgID: after.OrganizationID, UserID: uid, Kind: KindTaskNew,
				DedupKey: fmt.Sprintf("task-new:%d:%d:%d", after.ID, uid, stamp), Params: p, URL: taskURL(after.ID, day)})
		} else if before.Time != after.Time {
			Enqueue(db, Item{OrgID: after.OrganizationID, UserID: uid, Kind: KindTaskMoved,
				DedupKey: fmt.Sprintf("task-moved:%d:%d:%s", after.ID, uid, after.Time+"@"+fmt.Sprint(stamp)), Params: p, URL: taskURL(after.ID, day)})
		}
	}
	for _, uid := range oldTo {
		if !in(newTo, uid) {
			Enqueue(db, Item{OrgID: after.OrganizationID, UserID: uid, Kind: KindTaskCancelled,
				DedupKey: fmt.Sprintf("task-cancel:%d:%d:%d", after.ID, uid, stamp),
				Params:   Params{Title: before.Title, TaskType: before.TaskType, Day: rel}, URL: "/today"})
		}
	}
}

// TaskDeleted — задачу убрали из дня (П5). Вызывать до удаления строки.
func TaskDeleted(db *gorm.DB, actorID uint, t *models.ScheduleTask) {
	if t.Completed {
		return
	}
	loc := OrgLocation(db, t.OrganizationID)
	rel := relativeDay(taskDay(db, t), loc)
	if rel == "" {
		return
	}
	for _, uid := range without(taskAssignees(db, t.OrganizationID, t.AssignedToUserID), actorID) {
		Enqueue(db, Item{OrgID: t.OrganizationID, UserID: uid, Kind: KindTaskCancelled,
			DedupKey: fmt.Sprintf("task-cancel:%d:%d", t.ID, uid),
			Params:   Params{Title: t.Title, TaskType: t.TaskType, Day: rel}, URL: "/today"})
	}
}

// TaskCompleted — помощник отметил задачу (Н4, только ★ высокая важность).
func TaskCompleted(db *gorm.DB, actorID uint, t *models.ScheduleTask) {
	if EffectivePriority(db, t) != "high" {
		return
	}
	loc := OrgLocation(db, t.OrganizationID)
	at := Now().In(loc)
	if t.CompletedAt != nil {
		at = t.CompletedAt.In(loc)
	}
	actor := UserName(db, actorID)
	for _, uid := range without(employers(db, t.OrganizationID), actorID) {
		Enqueue(db, Item{OrgID: t.OrganizationID, UserID: uid, Kind: KindImportantDone,
			DedupKey: fmt.Sprintf("important-done:%d:%d:%s", t.ID, uid, CalendarDay(at)),
			Params:   Params{Actor: actor, Title: t.Title, TaskType: t.TaskType, At: at.Format("15:04")},
			URL:      taskURL(t.ID, taskDay(db, t))})
	}
}

// ── Покупки ─────────────────────────────────────────────────────────────────

func listItems(db *gorm.DB, listID uint) []models.ShoppingListItem {
	var items []models.ShoppingListItem
	db.Where("list_id = ?", listID).Order("position ASC, id ASC").Find(&items)
	return items
}

func listURL(id uint) string { return fmt.Sprintf("/shopping/%d", id) }

// ListSent — помощник отправил список (Н1).
func ListSent(db *gorm.DB, actorID uint, l *models.ShoppingList) {
	items := listItems(db, l.ID)
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Item)
	}
	actor := UserName(db, actorID)
	for _, uid := range without(employers(db, l.OrganizationID), actorID) {
		Enqueue(db, Item{OrgID: l.OrganizationID, UserID: uid, Kind: KindListSent,
			DedupKey: fmt.Sprintf("list-sent:%d:%d", l.ID, uid),
			Params:   Params{Actor: actor, Count: len(names), Items: names}, URL: listURL(l.ID)})
	}
}

// listAddDelay — сколько копим дописанные пункты, прежде чем прислать один пуш.
const listAddDelay = 5 * time.Minute

// ListItemAdded — помощник дописал пункт в уже отправленный список (Н2).
// Пункты копятся 5 минут в одной строке очереди.
func ListItemAdded(db *gorm.DB, actorID uint, l *models.ShoppingList, item string) {
	actor := UserName(db, actorID)
	for _, uid := range without(employers(db, l.OrganizationID), actorID) {
		group := fmt.Sprintf("list-add:%d:%d", l.ID, uid)
		var n models.Notification
		err := db.Where("group_key = ? AND status = ?", group, models.NotificationQueued).First(&n).Error
		if err == nil {
			var p Params
			_ = json.Unmarshal([]byte(n.Params), &p)
			p.Items = append(p.Items, item)
			raw, _ := json.Marshal(p)
			db.Model(&n).Update("params", string(raw))
			continue
		}
		raw, _ := json.Marshal(Params{Actor: actor, Items: []string{item}})
		db.Create(&models.Notification{
			OrganizationID: l.OrganizationID, UserID: uid, Kind: string(KindListAdded),
			DedupKey: fmt.Sprintf("%s:%d", group, Now().UnixNano()), GroupKey: group,
			Params: string(raw), URL: listURL(l.ID), ScheduledFor: Now().Add(listAddDelay),
			Status: models.NotificationQueued,
		})
	}
}

// ListClosed — наниматель закрыл список (П6): помощникам дома.
func ListClosed(db *gorm.DB, actorID uint, l *models.ShoppingList, bought, total int, nextID *uint) {
	url := "/inbox"
	if nextID != nil {
		url = listURL(*nextID)
	}
	for _, uid := range without(helpers(db, l.OrganizationID), actorID) {
		Enqueue(db, Item{OrgID: l.OrganizationID, UserID: uid, Kind: KindListClosed,
			DedupKey: fmt.Sprintf("list-closed:%d:%d", l.ID, uid),
			Params:   Params{Done: bought, Total: total, Left: total - bought}, URL: url})
	}
}

// ── Команда ─────────────────────────────────────────────────────────────────

// MemberJoined — по приглашению вошёл новый человек (Н5).
func MemberJoined(db *gorm.DB, orgID, userID uint) {
	name := UserName(db, userID)
	for _, uid := range without(employers(db, orgID), userID) {
		Enqueue(db, Item{OrgID: orgID, UserID: uid, Kind: KindMemberJoined,
			DedupKey: fmt.Sprintf("joined:%d:%d:%d", orgID, userID, uid),
			Params:   Params{Name: name}, URL: "/members"})
	}
}
