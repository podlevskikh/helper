package auth

import "golang.org/x/crypto/bcrypt"

const bcryptCost = bcrypt.DefaultCost

// HashPassword хэширует пароль bcrypt.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	return string(b), err
}

// CheckPassword проверяет пароль против bcrypt-хэша.
func CheckPassword(plain, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}

// dummyHash — для выравнивания времени ответа, когда пользователя нет: иначе по скорости
// «неверный пароль» отличается от «нет такого email».
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing"), bcryptCost)

// CheckPasswordTimingSafe сравнивает пароль; при пустом hash всё равно тратит время bcrypt и возвращает ошибку.
func CheckPasswordTimingSafe(plain, hash string) error {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(plain))
		return bcrypt.ErrMismatchedHashAndPassword
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}
