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
