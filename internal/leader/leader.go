// Package leader — фоновые задачи (сборка дней, очередь пушей) выполняет один сервер.
//
// Во время выкатки Railway держит старую и новую версию одновременно; без ведущего обе собирали бы
// дни и слали одни и те же пуши. Ведущий — тот, кто держит «аренду» — строку в таблице leader_leases
// со сроком: продлевает её каждые RenewEvery, а не продлил за TTL — её забирает другой сервер.
//
// Аренда в таблице, а не advisory-lock сессии: база на Neon, и через пулер соединений (PgBouncer в
// режиме транзакций) сессионная блокировка живёт на чужом серверном соединении. Строка работает при
// любом способе подключения; время — только по часам базы (now()), часы серверов не важны.
package leader

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"
)

// Name — имя аренды фоновых задач.
const Name = "background-jobs"

var (
	TTL        = 30 * time.Second // аренда действует столько после последнего продления
	RenewEvery = 10 * time.Second // продлеваем (и пробуем захватить) так часто
)

// EnsureTable создаёт таблицу аренды (если её нет).
func EnsureTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS leader_leases (
		name TEXT PRIMARY KEY, holder TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL)`)
	return err
}

func newHolder() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	host, _ := os.Hostname()
	return fmt.Sprintf("%s/%d/%s", host, os.Getpid(), hex.EncodeToString(b))
}

// acquire захватывает или продлевает аренду: удаётся, если она свободна, истекла или уже наша.
func acquire(ctx context.Context, db *sql.DB, name, holder string) bool {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var got string
	err := db.QueryRowContext(c, `INSERT INTO leader_leases (name, holder, expires_at)
		VALUES ($1, $2, now() + $3 * interval '1 millisecond')
		ON CONFLICT (name) DO UPDATE SET holder = EXCLUDED.holder, expires_at = EXCLUDED.expires_at
		WHERE leader_leases.holder = EXCLUDED.holder OR leader_leases.expires_at < now()
		RETURNING holder`, name, holder, TTL.Milliseconds()).Scan(&got)
	return err == nil && got == holder
}

func release(db *sql.DB, name, holder string) {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = db.ExecContext(c, `UPDATE leader_leases SET expires_at = now() WHERE name = $1 AND holder = $2`, name, holder)
}

// Run пытается стать ведущим и, пока им остаётся, выполняет work(ctx). Возвращается, когда ctx отменён
// и work завершилась. Не продлил аренду — work останавливается (её отменят раньше, чем аренда истечёт
// и её заберёт другой: RenewEvery намного меньше TTL).
func Run(ctx context.Context, db *sql.DB, name string, work func(ctx context.Context)) {
	if err := EnsureTable(ctx, db); err != nil {
		log.Printf("фоновые задачи: таблица аренды: %v", err)
	}
	holder := newHolder()
	for ctx.Err() == nil {
		if acquire(ctx, db, name, holder) {
			lead(ctx, db, name, holder, work)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(RenewEvery):
		}
	}
}

func lead(ctx context.Context, db *sql.DB, name, holder string, work func(ctx context.Context)) {
	log.Println("фоновые задачи: этот сервер ведущий")
	jobCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); work(jobCtx) }()
	t := time.NewTicker(RenewEvery)
	defer t.Stop()
watch:
	for {
		select {
		case <-ctx.Done():
			break watch
		case <-done:
			break watch
		case <-t.C:
			if !acquire(ctx, db, name, holder) {
				log.Println("фоновые задачи: аренда не продлилась — перестаю быть ведущим")
				break watch
			}
		}
	}
	cancel()
	<-done
	release(db, name, holder)
}
