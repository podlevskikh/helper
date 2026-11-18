package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Message — одно сообщение Expo Push API (https://docs.expo.dev/push-notifications/sending-notifications/).
type Message struct {
	To         string         `json:"to"`
	Title      string         `json:"title,omitempty"`
	Body       string         `json:"body,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Sound      string         `json:"sound,omitempty"`
	ChannelID  string         `json:"channelId,omitempty"`
	Priority   string         `json:"priority,omitempty"`
	CollapseID string         `json:"collapseId,omitempty"`
	Tag        string         `json:"tag,omitempty"`
	ThreadID   string         `json:"threadId,omitempty"`
}

// Ticket — ответ Expo на одно сообщение (в том же порядке, что и сообщения).
type Ticket struct {
	Status  string `json:"status"` // ok | error
	ID      string `json:"id"`
	Message string `json:"message"`
	Details struct {
		Error string `json:"error"`
	} `json:"details"`
}

// Receipt — итог доставки по квитанции.
type Receipt = Ticket

// Sender отправляет сообщения. В проде — Expo, в тестах — подмена.
type Sender interface {
	Send(msgs []Message) ([]Ticket, error)
	Receipts(ids []string) (map[string]Receipt, error)
}

// ExpoClient — клиент Expo Push API. Токен доступа (EXPO_ACCESS_TOKEN) нужен,
// если в проекте Expo включена «Enhanced push security».
type ExpoClient struct {
	BaseURL     string
	AccessToken string
	HTTP        *http.Client
}

func NewExpoClientFromEnv() *ExpoClient {
	base := os.Getenv("EXPO_PUSH_API")
	if base == "" {
		base = "https://exp.host/--/api/v2/push"
	}
	return &ExpoClient{BaseURL: base, AccessToken: os.Getenv("EXPO_ACCESS_TOKEN"), HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// post шлёт запрос и повторяет его с паузой 2, 4, 8 с при 429, 5xx и сетевых ошибках.
func (c *ExpoClient) post(path string, payload any, out any) error {
	raw, _ := json.Marshal(payload)
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
		var retry bool
		retry, err = c.do(path, raw, out)
		if !retry {
			return err
		}
	}
	return err
}

func (c *ExpoClient) do(path string, raw []byte, out any) (retry bool, err error) {
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return true, fmt.Errorf("expo: HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Errors []struct{ Code, Message string } `json:"errors"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return false, fmt.Errorf("expo: HTTP %d %v", resp.StatusCode, e.Errors)
	}
	return false, json.NewDecoder(resp.Body).Decode(out)
}

// Send отправляет до 100 сообщений за раз (лимит Expo); больше — несколькими запросами.
func (c *ExpoClient) Send(msgs []Message) ([]Ticket, error) {
	var all []Ticket
	for start := 0; start < len(msgs); start += 100 {
		end := start + 100
		if end > len(msgs) {
			end = len(msgs)
		}
		var out struct {
			Data []Ticket `json:"data"`
		}
		if err := c.post("/send", msgs[start:end], &out); err != nil {
			return all, err
		}
		all = append(all, out.Data...)
	}
	return all, nil
}

// Receipts — итоги доставки по id квитанций (до 1000 за запрос).
func (c *ExpoClient) Receipts(ids []string) (map[string]Receipt, error) {
	all := map[string]Receipt{}
	for start := 0; start < len(ids); start += 1000 {
		end := start + 1000
		if end > len(ids) {
			end = len(ids)
		}
		var out struct {
			Data map[string]Receipt `json:"data"`
		}
		if err := c.post("/getReceipts", map[string]any{"ids": ids[start:end]}, &out); err != nil {
			return all, err
		}
		for k, v := range out.Data {
			all[k] = v
		}
	}
	return all, nil
}

// LogSender — «сухой» режим (PUSH_DRY_RUN=1): пишет пуши в лог вместо отправки.
type LogSender struct{ Printf func(string, ...any) }

func (l LogSender) Send(msgs []Message) ([]Ticket, error) {
	out := make([]Ticket, len(msgs))
	for i, m := range msgs {
		l.Printf("push (dry run) → %s | %s | %s | %v", m.To, m.Title, m.Body, m.Data["url"])
		out[i] = Ticket{Status: "ok", ID: fmt.Sprintf("dry-%d-%d", time.Now().UnixNano(), i)}
	}
	return out, nil
}

func (l LogSender) Receipts(ids []string) (map[string]Receipt, error) {
	out := map[string]Receipt{}
	for _, id := range ids {
		out[id] = Receipt{Status: "ok"}
	}
	return out, nil
}
