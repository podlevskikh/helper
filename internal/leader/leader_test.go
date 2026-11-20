package leader

import (
	"context"
	"database/sql"
	"os"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// OPS-07: фоновые задачи на двух серверах (выкатка) — работает только один; ведущий остановился —
// задачи подхватывает второй.
func TestOnlyOneLeader(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB") == "1" {
			t.Fatal("TEST_DATABASE_URL не задан, а REQUIRE_DB=1")
		}
		t.Skip("TEST_DATABASE_URL не задан")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	RetryEvery, CheckEvery = 20*time.Millisecond, 20*time.Millisecond
	key := time.Now().UnixNano() % 1_000_000_000

	var running, maxRunning, startedA, startedB int32
	work := func(started *int32) func(ctx context.Context) {
		return func(ctx context.Context) {
			atomic.AddInt32(started, 1)
			n := atomic.AddInt32(&running, 1)
			for {
				m := atomic.LoadInt32(&maxRunning)
				if n <= m || atomic.CompareAndSwapInt32(&maxRunning, m, n) {
					break
				}
			}
			<-ctx.Done()
			atomic.AddInt32(&running, -1)
		}
	}
	ctxA, stopA := context.WithCancel(context.Background())
	ctxB, stopB := context.WithCancel(context.Background())
	defer stopB()
	doneA := make(chan struct{})
	go func() { Run(ctxA, db, key, work(&startedA)); close(doneA) }()
	time.Sleep(100 * time.Millisecond) // A успел стать ведущим
	go Run(ctxB, db, key, work(&startedB))
	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&startedA) != 1 || atomic.LoadInt32(&startedB) != 0 || atomic.LoadInt32(&maxRunning) != 1 {
		t.Fatalf("пока A ведущий: A=%d B=%d, одновременно %d", startedA, startedB, maxRunning)
	}
	stopA() // старую версию остановили
	<-doneA
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&startedB) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&startedB) != 1 || atomic.LoadInt32(&maxRunning) != 1 {
		t.Fatalf("после остановки A: B=%d, одновременно максимум %d", startedB, maxRunning)
	}
}
