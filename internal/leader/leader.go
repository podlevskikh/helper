// Package leader — фоновые задачи (сборка дней, очередь пушей) выполняет один сервер.
//
// Во время выкатки Railway держит старую и новую версию одновременно; без ведущего обе собирали бы
// дни и слали одни и те же пуши. Ведущий — тот, кто держит advisory-lock в Postgres на отдельном
// соединении. Сервер остановили или соединение порвалось — блокировка снимается сама, и её берёт другой.
package leader

import (
	"context"
	"database/sql"
	"log"
	"time"
)

// Key — номер блокировки ведущего (одно число на всё приложение).
const Key int64 = 7_423_001

// RetryEvery — как часто не-ведущий пробует стать ведущим; CheckEvery — как часто ведущий проверяет соединение.
var (
	RetryEvery = 10 * time.Second
	CheckEvery = 30 * time.Second
)

// Run пытается стать ведущим и, пока им остаётся, выполняет work(ctx). Возвращается, когда ctx отменён
// и work завершилась.
func Run(ctx context.Context, db *sql.DB, key int64, work func(ctx context.Context)) {
	for ctx.Err() == nil {
		lead(ctx, db, key, work)
		select {
		case <-ctx.Done():
			return
		case <-time.After(RetryEvery):
		}
	}
}

func lead(ctx context.Context, db *sql.DB, key int64, work func(ctx context.Context)) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return
	}
	defer conn.Close()
	var ok bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil || !ok {
		return
	}
	log.Println("фоновые задачи: этот сервер ведущий")
	jobCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); work(jobCtx) }()

	t := time.NewTicker(CheckEvery)
	defer t.Stop()
watch:
	for {
		select {
		case <-ctx.Done():
			break watch
		case <-done:
			break watch
		case <-t.C:
			if err := conn.PingContext(ctx); err != nil {
				log.Printf("фоновые задачи: соединение с базой потеряно (%v) — перестаю быть ведущим", err)
				break watch
			}
		}
	}
	cancel()
	<-done
	unlockCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	_, _ = conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", key)
}
