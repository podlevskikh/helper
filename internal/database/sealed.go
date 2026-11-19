package database

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"os"

	"golang.org/x/crypto/nacl/box"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Запечатанные секреты: прод-переменные окружения задать нельзя, а репозиторий публичный.
// Сервер хранит в базе пару ключей; открытый ключ отдаёт GET /system/sealing-key.
// Значение шифруется этим ключом (scripts/seal-mail.sh) и коммитится в secrets.sealed.json —
// расшифровать его может только этот сервер. Переменная из окружения важнее запечатанной.

// SealedFile — где лежат запечатанные значения (рядом с бинарником в образе).
const SealedFile = "secrets.sealed.json"

var sealingPublic, sealingPrivate *[32]byte

func secretValue(db *gorm.DB, name string) string {
	var v string
	db.Raw(`SELECT value FROM app_secrets WHERE name = ?`, name).Scan(&v)
	return v
}

func decodeKey(s string) (*[32]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, errors.New("bad key")
	}
	var k [32]byte
	copy(k[:], b)
	return &k, nil
}

// EnsureSealingKeys создаёт (один раз) и загружает пару ключей для запечатанных секретов.
func EnsureSealingKeys(db *gorm.DB) {
	db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}) // ключи — не в лог
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS app_secrets (name TEXT PRIMARY KEY, value TEXT NOT NULL)`).Error; err != nil {
		log.Printf("sealed: таблица секретов недоступна: %v", err)
		return
	}
	if secretValue(db, "sealing_private") == "" {
		pub, priv, err := box.GenerateKey(rand.Reader)
		if err != nil {
			log.Printf("sealed: не удалось создать ключи: %v", err)
			return
		}
		db.Transaction(func(tx *gorm.DB) error {
			tx.Exec(`INSERT INTO app_secrets (name, value) VALUES ('sealing_private', ?) ON CONFLICT (name) DO NOTHING`, base64.StdEncoding.EncodeToString(priv[:]))
			return tx.Exec(`INSERT INTO app_secrets (name, value) VALUES ('sealing_public', ?) ON CONFLICT (name) DO NOTHING`, base64.StdEncoding.EncodeToString(pub[:])).Error
		})
	}
	pub, err1 := decodeKey(secretValue(db, "sealing_public"))
	priv, err2 := decodeKey(secretValue(db, "sealing_private"))
	if err1 != nil || err2 != nil {
		log.Printf("sealed: ключи в базе повреждены")
		return
	}
	sealingPublic, sealingPrivate = pub, priv
}

// SealingPublicKey — открытый ключ (base64) для шифрования значений; "" если ключей нет.
func SealingPublicKey() string {
	if sealingPublic == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(sealingPublic[:])
}

// LoadSealedSecrets расшифровывает secrets.sealed.json и кладёт значения в окружение
// (если такая переменная там ещё не задана). Чужие или испорченные значения пропускаются.
func LoadSealedSecrets(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return // файла нет — нечего загружать
	}
	if sealingPrivate == nil {
		log.Printf("sealed: есть %s, но нет ключей для расшифровки", path)
		return
	}
	var file struct {
		PublicKey string            `json:"public_key"`
		Values    map[string]string `json:"values"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		log.Printf("sealed: %s не читается: %v", path, err)
		return
	}
	if len(file.Values) == 0 {
		return
	}
	if file.PublicKey != SealingPublicKey() {
		log.Printf("sealed: %s зашифрован ключом другого сервера — пропускаем", path)
		return
	}
	loaded := 0
	for name, enc := range file.Values {
		if os.Getenv(name) != "" {
			continue
		}
		ct, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			continue
		}
		plain, ok := box.OpenAnonymous(nil, ct, sealingPublic, sealingPrivate)
		if !ok {
			log.Printf("sealed: %s не расшифровывается", name)
			continue
		}
		os.Setenv(name, string(plain))
		loaded++
	}
	log.Printf("sealed: загружено значений: %d", loaded)
}
