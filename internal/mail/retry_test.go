package mail

import (
	"errors"
	"net/textproto"
	"testing"
	"time"
)

// MAIL-01: временный сбой почты — письмо уходит со следующей попытки; адрес отклонён (5xx) — без повторов;
// итоговый сбой сообщается (OnFailure), а остановка сервера дожидается отправки (Wait).
func TestSendRetries(t *testing.T) {
	oldDelays, oldSend, oldFail := retryDelays, sendFunc, OnFailure
	t.Cleanup(func() { retryDelays, sendFunc, OnFailure = oldDelays, oldSend, oldFail })
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond}

	calls := 0
	sendFunc = func(Message) error {
		calls++
		if calls < 3 {
			return errors.New("dial tcp: i/o timeout")
		}
		return nil
	}
	failed := 0
	OnFailure = func(string, error) { failed++ }
	SendAsync(Message{To: "a@b.c", Subject: "s"})
	if !Wait(time.Second) {
		t.Fatal("Wait не дождался отправки")
	}
	if calls != 3 || failed != 0 {
		t.Fatalf("временный сбой: попыток %d (ждали 3), провалов %d", calls, failed)
	}

	calls = 0
	sendFunc = func(Message) error { calls++; return &textproto.Error{Code: 550, Msg: "no such user"} }
	SendAsync(Message{To: "a@b.c", Subject: "s"})
	Wait(time.Second)
	if calls != 1 || failed != 1 {
		t.Fatalf("постоянная ошибка: попыток %d (ждали 1), провалов %d (ждали 1)", calls, failed)
	}

	calls = 0
	sendFunc = func(Message) error { calls++; return errors.New("connection refused") }
	SendAsync(Message{To: "a@b.c", Subject: "s"})
	Wait(time.Second)
	if calls != 3 || failed != 2 {
		t.Fatalf("сервис лежит: попыток %d (ждали 3), провалов %d (ждали 2)", calls, failed)
	}
}
