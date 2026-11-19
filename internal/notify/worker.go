package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"podlevskikh/awesomeProject/internal/calendar"
	"podlevskikh/awesomeProject/internal/models"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Worker — фоновый отправщик: раз в 15 с доставляет очередь, раз в минуту
// ставит пуши по времени (план дня, напоминания, итог дня), раз в 15 мин
// проверяет квитанции Expo.
type Worker struct {
	DB     *gorm.DB
	Sender Sender
	// EnsureDay достраивает день дома (повторяющиеся задачи), прежде чем считать план и напоминания.
	EnsureDay func(orgID uint, day time.Time)
}

func (w *Worker) Run(ctx context.Context) {
	// Тики идут постоянно — их SQL в лог не пишем (только медленные запросы и ошибки).
	w.DB = w.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Warn)})
	send := time.NewTicker(15 * time.Second)
	timed := time.NewTicker(time.Minute)
	receipts := time.NewTicker(15 * time.Minute)
	defer send.Stop()
	defer timed.Stop()
	defer receipts.Stop()
	safe := func(name string, f func()) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("notify: %s panic: %v", name, r)
			}
		}()
		f()
	}
	safe("timed", w.ScheduleTimed)
	safe("send", w.SendDue)
	for {
		select {
		case <-ctx.Done():
			return
		case <-send.C:
			safe("send", w.SendDue)
		case <-timed.C:
			safe("timed", w.ScheduleTimed)
		case <-receipts.C:
			safe("receipts", w.CheckReceipts)
		}
	}
}

// ── Доставка ────────────────────────────────────────────────────────────────

type outgoing struct {
	notifs   []models.Notification
	deviceID uint
	msg      Message
}

func mark(db *gorm.DB, ids []uint, status, errText string) {
	if len(ids) == 0 {
		return
	}
	upd := map[string]any{"status": status, "error": errText}
	if status == models.NotificationSent {
		upd["sent_at"] = Now()
	}
	db.Model(&models.Notification{}).Where("id IN ?", ids).Updates(upd)
}

func idsOf(ns []models.Notification) []uint {
	out := make([]uint, len(ns))
	for i, n := range ns {
		out[i] = n.ID
	}
	return out
}

func paramsOf(n models.Notification) Params {
	var p Params
	_ = json.Unmarshal([]byte(n.Params), &p)
	return p
}

// SendDue доставляет всё, чему пришло время: с учётом выключенных видов и тихих часов.
func (w *Worker) SendDue() {
	now := Now()
	var due []models.Notification
	w.DB.Where("status = ? AND scheduled_for <= ?", models.NotificationQueued, now).
		Order("id ASC").Limit(500).Find(&due)
	if len(due) == 0 {
		return
	}
	type key struct{ user, org uint }
	groups := map[key][]models.Notification{}
	var order []key
	for _, n := range due {
		k := key{n.UserID, n.OrganizationID}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], n)
	}

	var out []outgoing
	for _, k := range order {
		list := groups[k]
		prefs := LoadPrefs(w.DB, k.user, k.org)
		local := now.In(OrgLocation(w.DB, k.org))

		var ready, off []models.Notification
		for _, n := range list {
			if prefs.Enabled(Kind(n.Kind)) {
				ready = append(ready, n)
			} else {
				off = append(off, n)
			}
		}
		mark(w.DB, idsOf(off), models.NotificationSkipped, "disabled")

		if prefs.inQuiet(local) {
			end := prefs.quietEnd(local)
			for _, n := range ready {
				switch Kind(n.Kind) {
				case KindTest:
					continue
				case KindReminder:
					mark(w.DB, []uint{n.ID}, models.NotificationSkipped, "quiet hours")
				default:
					w.DB.Model(&n).Updates(map[string]any{"scheduled_for": end, "deferred": true})
				}
			}
			var tests []models.Notification
			for _, n := range ready {
				if Kind(n.Kind) == KindTest {
					tests = append(tests, n)
				}
			}
			ready = tests
			if len(ready) == 0 {
				continue
			}
		}

		var devices []models.PushDevice
		w.DB.Where("user_id = ? AND disabled_at IS NULL", k.user).Find(&devices)
		if len(devices) == 0 {
			mark(w.DB, idsOf(ready), models.NotificationSkipped, "no devices")
			continue
		}
		var user models.User
		w.DB.Select("id", "locale").First(&user, k.user)

		// Накопленное за тихие часы — одним пушем.
		var deferred, single []models.Notification
		for _, n := range ready {
			if n.Deferred {
				deferred = append(deferred, n)
			} else {
				single = append(single, n)
			}
		}
		if len(deferred) == 1 {
			single = append(single, deferred...)
			deferred = nil
		}
		units := [][]models.Notification{}
		for _, n := range single {
			units = append(units, []models.Notification{n})
		}
		if len(deferred) > 0 {
			units = append(units, deferred)
		}

		for _, unit := range units {
			msg := w.message(unit, user.Locale, k.org)
			for _, d := range devices {
				m := msg
				m.To = d.Token
				out = append(out, outgoing{notifs: unit, deviceID: d.ID, msg: m})
			}
		}
	}
	if len(out) == 0 {
		return
	}

	msgs := make([]Message, len(out))
	for i, o := range out {
		msgs[i] = o.msg
	}
	tickets, err := w.Sender.Send(msgs)
	if err != nil {
		log.Printf("notify: send: %v", err)
	}
	okByNotif := map[uint]bool{}
	errByNotif := map[uint]string{}
	for i, o := range out {
		if i >= len(tickets) {
			break // запрос не прошёл — останутся в очереди до следующей попытки
		}
		t := tickets[i]
		for _, n := range o.notifs {
			if t.Status == "ok" {
				okByNotif[n.ID] = true
			} else {
				errByNotif[n.ID] = strings.TrimSpace(t.Details.Error + " " + t.Message)
			}
		}
		if t.Status == "ok" && t.ID != "" {
			w.DB.Create(&models.PushTicket{TicketID: t.ID, NotificationID: o.notifs[0].ID, DeviceID: o.deviceID})
		}
		if t.Details.Error == "DeviceNotRegistered" {
			w.disableDevice(o.deviceID)
		}
	}
	var sent, failed []uint
	for id := range okByNotif {
		sent = append(sent, id)
	}
	for id, e := range errByNotif {
		if !okByNotif[id] {
			failed = append(failed, id)
			mark(w.DB, []uint{id}, models.NotificationFailed, e)
		}
	}
	mark(w.DB, sent, models.NotificationSent, "")
	// Не ушедшее из-за сети спустя 6 часов уже не актуально.
	w.DB.Model(&models.Notification{}).
		Where("status = ? AND scheduled_for < ?", models.NotificationQueued, now.Add(-6*time.Hour)).
		Updates(map[string]any{"status": models.NotificationFailed, "error": "expired"})
}

func (w *Worker) disableDevice(id uint) {
	now := Now()
	w.DB.Model(&models.PushDevice{}).Where("id = ?", id).Update("disabled_at", &now)
}

// message собирает текст на языке получателя и данные для перехода по тапу.
func (w *Worker) message(unit []models.Notification, locale string, orgID uint) Message {
	n := unit[0]
	kind := Kind(n.Kind)
	var title, body, url string
	if len(unit) > 1 {
		kind = KindQuietSummary
		var titles []string
		for _, x := range unit {
			titles = append(titles, SummaryLine(Kind(x.Kind), locale, paramsOf(x)))
		}
		title, body = Render(KindQuietSummary, locale, Params{Titles: titles})
		url = ""
	} else {
		title, body = Render(kind, locale, paramsOf(n))
		url = n.URL
	}
	data := map[string]any{"kind": string(kind), "org_id": orgID, "notification_id": n.ID}
	if url != "" {
		data["url"] = url
	}
	if inv := invalidate(kind); len(inv) > 0 {
		data["invalidate"] = inv
	}
	ch := channel(kind)
	m := Message{Title: title, Body: body, Data: data, Sound: "default", ChannelID: ch, ThreadID: ch, Priority: "default"}
	if ch == "tasks" {
		m.Priority = "high"
	}
	if n.GroupKey != "" && len(unit) == 1 {
		m.CollapseID = n.GroupKey
		m.Tag = n.GroupKey
	}
	return m
}

// CheckReceipts — квитанции Expo старше 15 минут: недоступные устройства отключаем.
func (w *Worker) CheckReceipts() {
	now := Now()
	var ts []models.PushTicket
	w.DB.Where("checked_at IS NULL AND created_at < ? AND created_at > ?", now.Add(-15*time.Minute), now.Add(-24*time.Hour)).
		Limit(1000).Find(&ts)
	if len(ts) == 0 {
		return
	}
	ids := make([]string, len(ts))
	for i, t := range ts {
		ids[i] = t.TicketID
	}
	rs, err := w.Sender.Receipts(ids)
	if err != nil {
		log.Printf("notify: receipts: %v", err)
		return
	}
	for _, t := range ts {
		r, ok := rs[t.TicketID]
		if !ok {
			continue
		}
		if r.Status == "error" {
			log.Printf("notify: receipt %s: %s %s", t.TicketID, r.Details.Error, r.Message)
			if r.Details.Error == "DeviceNotRegistered" {
				w.disableDevice(t.DeviceID)
			}
		}
		w.DB.Model(&models.PushTicket{}).Where("id = ?", t.ID).Update("checked_at", now)
	}
}

// ── Пуши по времени ─────────────────────────────────────────────────────────

// within — местное время в окне [hhmm, hhmm+width). Окно, а не точная минута:
// тик может опоздать, а повтор отсекает DedupKey.
func within(local time.Time, hhmm string, width int) bool {
	t := minutesOf(hhmm)
	m := local.Hour()*60 + local.Minute()
	return t >= 0 && m >= t && m < t+width
}

func dayTasks(db *gorm.DB, orgID uint, day string) []models.ScheduleTask {
	from, to := dayWindow(day)
	var s models.DailySchedule
	if db.Where("organization_id = ? AND date >= ? AND date < ?", orgID, from, to).First(&s).Error != nil {
		return nil
	}
	var tasks []models.ScheduleTask
	db.Preload("Recipes").Preload("TaskCategory").Where("schedule_id = ?", s.ID).Find(&tasks)
	return tasks
}

// tasksOf — задачи помощника: его и без исполнителя.
func tasksOf(tasks []models.ScheduleTask, uid uint) []models.ScheduleTask {
	var out []models.ScheduleTask
	for _, t := range tasks {
		if t.AssignedToUserID == nil || *t.AssignedToUserID == uid {
			out = append(out, t)
		}
	}
	return out
}

func sortByTime(ts []models.ScheduleTask) {
	sort.SliceStable(ts, func(i, j int) bool {
		a, b := ts[i].Time, ts[j].Time
		if (a == "") != (b == "") {
			return a != ""
		}
		return a < b
	})
}

// StartAt — «начать готовить примерно в»: время блюда минус самое долгое приготовление.
func StartAt(t *models.ScheduleTask) string {
	if t.TaskType != "meal" || t.Time == "" || t.EndTime != "" {
		return ""
	}
	prep := 0
	for _, r := range t.Recipes {
		if v := r.PrepTime + r.CookTime; v > prep {
			prep = v
		}
	}
	if prep == 0 {
		return ""
	}
	m := minutesOf(t.Time) - prep
	if m < 0 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

func (w *Worker) ScheduleTimed() {
	var orgs []models.Organization
	w.DB.Find(&orgs)
	for _, org := range orgs {
		w.timedForOrg(org.ID)
	}
}

func (w *Worker) timedForOrg(orgID uint) {
	loc := OrgLocation(w.DB, orgID)
	local := Now().In(loc)
	day := local.Format("2006-01-02")
	helperIDs := helpers(w.DB, orgID)
	employerIDs := employers(w.DB, orgID)
	if len(helperIDs) == 0 {
		return
	}
	var devCount int64
	w.DB.Model(&models.PushDevice{}).Where("user_id IN ? AND disabled_at IS NULL", append(append([]uint{}, helperIDs...), employerIDs...)).Count(&devCount)
	if devCount == 0 {
		return // в доме никто не поставил приложение — считать нечего
	}
	if w.EnsureDay != nil {
		d, _ := time.Parse("2006-01-02", day)
		w.EnsureDay(orgID, d)
	}
	tasks := dayTasks(w.DB, orgID, day)

	// П1 — план на день.
	d, _ := time.Parse("2006-01-02", day)
	for _, uid := range helperIDs {
		p := LoadPrefs(w.DB, uid, orgID)
		// у отдыхающего (выходной, праздник, отпуск) утреннего плана нет
		if !calendar.IsWorking(w.DB, orgID, uid, d) || !p.Enabled(KindMorning) || !within(local, p.MorningTime, 10) {
			continue
		}
		mine := tasksOf(tasks, uid)
		if len(mine) == 0 {
			continue
		}
		sortByTime(mine)
		high := 0
		for i := range mine {
			if EffectivePriority(w.DB, &mine[i]) == "high" {
				high++
			}
		}
		first := mine[0]
		Enqueue(w.DB, Item{OrgID: orgID, UserID: uid, Kind: KindMorning,
			DedupKey: fmt.Sprintf("morning:%d:%d:%s", orgID, uid, day),
			Params: Params{Name: UserName(w.DB, uid), Count: len(mine), HighCount: high,
				Title: first.Title, TaskType: first.TaskType, FirstTime: first.Time},
			URL: "/today"})
	}

	// П2 — напоминания о задачах со временем.
	now := local.Hour()*60 + local.Minute()
	for i := range tasks {
		t := &tasks[i]
		if t.Completed || t.Time == "" {
			continue
		}
		start := StartAt(t)
		base := t.Time
		if start != "" {
			base = start
		}
		for _, uid := range taskAssignees(w.DB, orgID, t.AssignedToUserID) {
			p := LoadPrefs(w.DB, uid, orgID)
			if !p.Enabled(KindReminder) {
				continue
			}
			fire := minutesOf(base) - p.ReminderLeadMin
			if now < fire || now >= fire+5 || now >= minutesOf(base) {
				continue
			}
			Enqueue(w.DB, Item{OrgID: orgID, UserID: uid, Kind: KindReminder,
				DedupKey: fmt.Sprintf("remind:%d:%d:%s:%s", t.ID, uid, day, base),
				Params: Params{Title: t.Title, TaskType: t.TaskType, Time: t.Time, StartAt: start,
					Lead: p.ReminderLeadMin, Important: EffectivePriority(w.DB, t) == "high"},
				URL: taskURL(t.ID, day)})
		}
	}

	// Н3 — итог дня по каждому помощнику.
	for _, eid := range employerIDs {
		p := LoadPrefs(w.DB, eid, orgID)
		if !p.Enabled(KindDigest) || !within(local, p.DigestTime, 10) {
			continue
		}
		for _, hid := range helperIDs {
			if hid == eid {
				continue
			}
			mine := tasksOf(tasks, hid)
			if len(mine) == 0 {
				continue
			}
			done := 0
			var notDone, notDoneTypes []string
			sort.SliceStable(mine, func(i, j int) bool {
				return prioRank(EffectivePriority(w.DB, &mine[i])) < prioRank(EffectivePriority(w.DB, &mine[j]))
			})
			for _, t := range mine {
				if t.Completed {
					done++
				} else {
					notDone = append(notDone, t.Title)
					notDoneTypes = append(notDoneTypes, t.TaskType)
				}
			}
			Enqueue(w.DB, Item{OrgID: orgID, UserID: eid, Kind: KindDigest,
				DedupKey: fmt.Sprintf("digest:%d:%d:%d:%s", orgID, eid, hid, day),
				Params:   Params{Name: UserName(w.DB, hid), Done: done, Total: len(mine), Items: notDone, ItemTypes: notDoneTypes},
				URL:      "/home"})
		}
	}
}

func prioRank(p string) int {
	switch p {
	case "high":
		return 0
	case "mid":
		return 1
	}
	return 2
}
