package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// onboxSource is the source name for the push-based on-box ciphertext
// provider, the recommended mechanism (SPEC: Secret resolution).
const onboxSource = "onbox"

// onboxKeyEnv overrides the on-box ciphertext store directory.
const onboxKeyEnv = "SERVITOR_SECRET_DIR"

// OnBoxDir returns the on-box ciphertext store directory: the
// SERVITOR_SECRET_DIR override, or `.servitor/secrets` in the working
// directory.
func OnBoxDir() string {
	if d := os.Getenv(onboxKeyEnv); d != "" {
		return d
	}
	return filepath.Join(".servitor", "secrets")
}

// OnBoxProvider resolves secrets from a push-delivered on-box ciphertext store
// (SPEC: Secret resolution): each value is sealed to the box as AES-GCM
// ciphertext, never plaintext on disk, and unlocked with a local key. This is
// the non-TPM unlock tier (the non-exportable-holder: TPM/KMS sealing of the
// key is a future tier); the key lives in a 0600 file in the store, so the
// stored values are ciphertext, not plaintext. Sealing is the push path
// (CI/CD, or the operator, calls SealOnBox on the box).
type OnBoxProvider struct {
	dir string
}

// NewOnBoxProvider returns a provider over the on-box store at dir ("" uses
// OnBoxDir).
func NewOnBoxProvider(dir string) *OnBoxProvider {
	if dir == "" {
		dir = OnBoxDir()
	}
	return &OnBoxProvider{dir: dir}
}

// Resolve reads secretName's sealed value from the store and decrypts it with
// the local key. A missing file is ErrSecretMissing; a missing or unreadable
// key or a store that does not exist is ErrSourceUnreachable.
func (p *OnBoxProvider) Resolve(_ context.Context, _, secretName string) (string, error) {
	key, err := loadKey(p.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: on-box store not initialized at %s", ErrSourceUnreachable, p.dir)
		}
		return "", fmt.Errorf("%w: %v", ErrSourceUnreachable, err)
	}
	raw, err := os.ReadFile(filepath.Join(p.dir, secretName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrSecretMissing, secretName)
		}
		return "", fmt.Errorf("%w: %v", ErrSourceUnreachable, err)
	}
	v, err := decrypt(key, raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrStale, err)
	}
	return v, nil
}

// SealOnBox seals value for secretName into the on-box store, creating the
// store and its key if needed. This is the push path: CI/CD or the operator
// calls it on the box so the value is never plaintext on disk (SPEC: Secret
// resolution).
func SealOnBox(dir, secretName, value string) error {
	if dir == "" {
		dir = OnBoxDir()
	}
	key, err := loadOrCreateKey(dir)
	if err != nil {
		return err
	}
	sealed, err := encrypt(key, value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("onbox: create store: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, secretName), sealed, 0o600); err != nil {
		return fmt.Errorf("onbox: write %s: %w", secretName, err)
	}
	return nil
}

// keyPath is the local unlock key file (the non-TPM tier).
func keyPath(dir string) string { return filepath.Join(dir, ".key") }

func loadKey(dir string) ([]byte, error) {
	return os.ReadFile(keyPath(dir))
}

func loadOrCreateKey(dir string) ([]byte, error) {
	key, err := loadKey(dir)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("onbox: read key: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("onbox: create store: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("onbox: generate key: %w", err)
	}
	if err := os.WriteFile(keyPath(dir), key, 0o600); err != nil {
		return nil, fmt.Errorf("onbox: write key: %w", err)
	}
	return key, nil
}

// encrypt seals plaintext as base64(nonce || AES-GCM ciphertext).
func encrypt(key []byte, plaintext string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("onbox: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("onbox: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("onbox: nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return []byte(base64.StdEncoding.EncodeToString(sealed)), nil
}

func decrypt(key, raw []byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("sealed value too short")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(pt), nil
}
