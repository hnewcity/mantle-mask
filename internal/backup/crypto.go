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
	"io"
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
	return EncryptStream(func(w io.Writer) error {
		_, err := w.Write(plain)
		return err
	}, password)
}

const headerSize = len(magic) + saltSize
const maxPlain = MaxSize - headerSize - 28

// EncryptStream encrypts the document produced by write. write is called
// twice and must produce the same number of bytes each time: the first pass
// only measures, the second fills one exactly sized buffer that is then
// encrypted in place. The plaintext therefore exists once in memory, inside
// the archive buffer itself, instead of as a growing buffer plus a copy.
func EncryptStream(write func(io.Writer) error, password string) ([]byte, error) {
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	size := &countWriter{}
	if err := write(size); err != nil {
		return nil, err
	}
	// 12-byte random nonce and 16-byte tag around the ciphertext.
	fill := &fillWriter{buf: make([]byte, headerSize, headerSize+size.n+28)}
	if err := write(fill); err != nil {
		return nil, err
	}
	if len(fill.buf) != headerSize+size.n {
		return nil, errors.New("backup content changed while it was being encrypted")
	}
	buf := fill.buf
	copy(buf, magic)
	if _, err := rand.Read(buf[len(magic):headerSize]); err != nil {
		return nil, err
	}
	cipher, err := aead(password, buf[len(magic):headerSize])
	if err != nil {
		return nil, err
	}
	header := buf[:headerSize:headerSize]
	return cipher.Seal(header, nil, buf[headerSize:], header), nil
}

type countWriter struct{ n int }

func (c *countWriter) Write(p []byte) (int, error) {
	if c.n += len(p); c.n > maxPlain {
		return 0, ErrTooLarge
	}
	return len(p), nil
}

type fillWriter struct{ buf []byte }

func (f *fillWriter) Write(p []byte) (int, error) {
	if len(f.buf)+len(p) > cap(f.buf)-28 {
		return 0, errors.New("backup content changed while it was being encrypted")
	}
	f.buf = append(f.buf, p...)
	return len(p), nil
}

func Decrypt(data []byte, password string) ([]byte, error) {
	return DecryptInPlace(bytes.Clone(data), password)
}

// DecryptInPlace reuses data for the plaintext instead of allocating a second
// archive-sized buffer. data is overwritten whether or not decryption succeeds.
func DecryptInPlace(data []byte, password string) ([]byte, error) {
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
	plain, err := cipher.Open(data[n:n], nil, data[n:], data[:n])
	if err != nil {
		return nil, errors.New("备份密码错误或文件已损坏")
	}
	return plain, nil
}
