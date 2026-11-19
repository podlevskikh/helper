// seal — шифрует значения открытым ключом сервера и записывает их в secrets.sealed.json.
// Значения читаются из stdin строками NAME=value (чтобы пароль не попадал в историю команд).
// Обычно запускается через scripts/seal-mail.sh.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"golang.org/x/crypto/nacl/box"
)

func main() {
	server := flag.String("server", "https://www.helperday.app", "адрес сервера")
	out := flag.String("out", "secrets.sealed.json", "файл с запечатанными значениями")
	flag.Parse()

	resp, err := http.Get(strings.TrimRight(*server, "/") + "/system/sealing-key")
	if err != nil {
		fail("не удалось получить ключ сервера: %v", err)
	}
	defer resp.Body.Close()
	var key struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&key); err != nil || key.PublicKey == "" {
		fail("сервер не отдал ключ (обновлён ли бэкенд?)")
	}
	kb, err := base64.StdEncoding.DecodeString(key.PublicKey)
	if err != nil || len(kb) != 32 {
		fail("ключ сервера испорчен")
	}
	var pub [32]byte
	copy(pub[:], kb)

	file := struct {
		PublicKey string            `json:"public_key"`
		Values    map[string]string `json:"values"`
	}{Values: map[string]string{}}
	if raw, err := os.ReadFile(*out); err == nil {
		_ = json.Unmarshal(raw, &file)
		if file.PublicKey != key.PublicKey { // ключ сервера сменился — старые значения бесполезны
			file.Values = map[string]string{}
		}
	}
	file.PublicKey = key.PublicKey

	sc := bufio.NewScanner(os.Stdin)
	n := 0
	for sc.Scan() {
		name, value, ok := strings.Cut(sc.Text(), "=")
		if !ok || strings.TrimSpace(name) == "" {
			continue
		}
		ct, err := box.SealAnonymous(nil, []byte(value), &pub, rand.Reader)
		if err != nil {
			fail("шифрование: %v", err)
		}
		file.Values[strings.TrimSpace(name)] = base64.StdEncoding.EncodeToString(ct)
		n++
	}
	raw, _ := json.MarshalIndent(file, "", "  ")
	if err := os.WriteFile(*out, append(raw, '\n'), 0o644); err != nil {
		fail("запись %s: %v", *out, err)
	}
	fmt.Printf("Запечатано значений: %d → %s\n", n, *out)
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(1)
}
