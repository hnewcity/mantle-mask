// Package backup implements the versioned, password-encrypted migration file.
package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"unicode/utf8"
)

const MaxSize = 256 << 20
const magic = "MANTLE-MASK-BACKUP-1\n"
const saltSize = 16

var ErrTooLarge = errors.New("备份超过 256 MiB 限制，请使用停机数据目录迁移")

func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < 12 || len(password) > 1024 {
		return errors.New("备份密码至少需要 12 个字符，且不能超过 1024 字节")
	}
	return nil
}

func aead(password string, salt []byte) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func Encrypt(plain []byte, password string) ([]byte, error) {
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	if len(plain) > MaxSize-len(magic)-saltSize-28 {
		return nil, ErrTooLarge
	}
	header := make([]byte, len(magic)+saltSize)
	copy(header, magic)
	if _, err := rand.Read(header[len(magic):]); err != nil {
		return nil, err
	}
	cipher, err := aead(password, header[len(magic):])
	if err != nil {
		return nil, err
	}
	sealed := cipher.Seal(nil, nil, plain, header)
	return append(header, sealed...), nil
}

func Decrypt(data []byte, password string) ([]byte, error) {
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	n := len(magic) + saltSize
	if len(data) < n+28 || len(data) > MaxSize || !bytes.HasPrefix(data, []byte(magic)) {
		return nil, errors.New("备份格式无效、版本不受支持或文件超过 256 MiB")
	}
	cipher, err := aead(password, data[len(magic):n])
	if err != nil {
		return nil, err
	}
	plain, err := cipher.Open(nil, nil, data[n:], data[:n])
	if err != nil {
		return nil, errors.New("备份密码错误或文件已损坏")
	}
	return plain, nil
}
