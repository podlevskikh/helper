package leader

import (
	"context"
	"database/sql"
	"fmt"
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
	TTL, RenewEvery = 300*time.Millisecond, 50*time.Millisecond
	key := fmt.Sprintf("test-%d", time.Now().UnixNano())

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

// OPS-07: ведущий упал, не отдав аренду, — после её срока задачи подхватывает другой сервер.
func TestLeaseTakenAfterCrash(t *testing.T) {
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
	TTL, RenewEvery = 300*time.Millisecond, 50*time.Millisecond
	name := fmt.Sprintf("crash-%d", time.Now().UnixNano())
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	if err := EnsureTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO leader_leases VALUES ($1, 'упавший сервер', now() + interval '400 milliseconds')`, name); err != nil {
		t.Fatal(err)
	}
	started := make(chan time.Time, 1)
	t0 := time.Now()
	go Run(ctx, db, name, func(c context.Context) { started <- time.Now(); <-c.Done() })
	select {
	case at := <-started:
		if at.Sub(t0) < 300*time.Millisecond {
			t.Fatalf("аренду забрали раньше срока: через %v", at.Sub(t0))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("после срока аренду никто не забрал")
	}
}
