// Package securitykey manages the project's OpenPGP disclosure key material.
package securitykey

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

const keyLifetime = 2 * 365 * 24 * time.Hour

// KeyPair contains armored public and private OpenPGP material and its v4 fingerprint.
type KeyPair struct {
	Public      []byte
	Private     []byte
	Fingerprint string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// Generate creates a v4 Ed25519 signing key with a Curve25519 encryption subkey.
func Generate(name, email string, now time.Time) (KeyPair, error) {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	if name == "" || email == "" {
		return KeyPair{}, errors.New("OpenPGP identity name and email are required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	created := now.UTC().Truncate(time.Second)
	config := &packet.Config{
		Algorithm:       packet.PubKeyAlgoEd25519,
		Curve:           packet.Curve25519,
		KeyLifetimeSecs: uint32(keyLifetime.Seconds()),
		Time:            func() time.Time { return created },
	}
	entity, err := openpgp.NewEntity(name, "", email, config)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate OpenPGP keypair: %w", err)
	}
	public, err := armorEntity(entity, openpgp.PublicKeyType, false)
	if err != nil {
		return KeyPair{}, err
	}
	private, err := armorEntity(entity, openpgp.PrivateKeyType, true)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{
		Public:      public,
		Private:     private,
		Fingerprint: strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint[:])),
		CreatedAt:   created,
		ExpiresAt:   created.Add(keyLifetime),
	}, nil
}

func armorEntity(entity *openpgp.Entity, armorType string, includePrivate bool) ([]byte, error) {
	var output bytes.Buffer
	writer, err := armor.Encode(&output, armorType, nil)
	if err != nil {
		return nil, fmt.Errorf("armor OpenPGP key: %w", err)
	}
	if includePrivate {
		err = entity.SerializePrivate(writer, nil)
	} else {
		err = entity.Serialize(writer)
	}
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("serialize OpenPGP key: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish armored OpenPGP key: %w", err)
	}
	return output.Bytes(), nil
}

// SealPrivate encrypts an armored private key using a key derived from installationSecret.
func SealPrivate(installationSecret, private []byte) ([]byte, error) {
	if len(installationSecret) < 32 || len(private) == 0 {
		return nil, errors.New("installation secret and private key are required")
	}
	key := deriveWrappingKey(installationSecret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize private-key encryption: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize private-key encryption: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("create private-key nonce: %w", err)
	}
	return append(nonce, gcm.Seal(nil, nonce, private, []byte("countries-pgp-private-v1"))...), nil
}

// OpenPrivate decrypts the private-key file and validates its armored keyring.
func OpenPrivate(installationSecret, encrypted []byte) ([]byte, error) {
	if len(installationSecret) < 32 {
		return nil, errors.New("installation secret is unavailable")
	}
	key := deriveWrappingKey(installationSecret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize private-key decryption: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize private-key decryption: %w", err)
	}
	if len(encrypted) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("encrypted private key is truncated")
	}
	nonce, ciphertext := encrypted[:gcm.NonceSize()], encrypted[gcm.NonceSize():]
	private, err := gcm.Open(nil, nonce, ciphertext, []byte("countries-pgp-private-v1"))
	if err != nil {
		return nil, errors.New("could not decrypt private key with installation secret")
	}
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(private))
	if err != nil || len(entities) == 0 || entities[0] == nil || entities[0].PrivateKey == nil {
		return nil, errors.New("decrypted private key is invalid")
	}
	return private, nil
}

// Fingerprint returns the uppercase, separator-free OpenPGP v4 fingerprint.
func Fingerprint(armoredKey []byte) (string, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(armoredKey))
	if err != nil || len(entities) == 0 || entities[0] == nil || entities[0].PrimaryKey == nil {
		return "", errors.New("invalid armored OpenPGP key")
	}
	if entities[0].PrimaryKey.Version != 4 {
		return "", errors.New("OpenPGP key must use version 4")
	}
	return strings.ToUpper(hex.EncodeToString(entities[0].PrimaryKey.Fingerprint[:])), nil
}

// EncryptMessage encrypts plaintext to the recipients in an armored public keyring.
func EncryptMessage(armoredPublic, plaintext []byte) ([]byte, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(armoredPublic))
	if err != nil || len(entities) == 0 {
		return nil, errors.New("invalid armored OpenPGP public key")
	}
	var ciphertext bytes.Buffer
	writer, err := openpgp.Encrypt(&ciphertext, entities, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("initialize OpenPGP encryption: %w", err)
	}
	if _, err := writer.Write(plaintext); err != nil {
		return nil, fmt.Errorf("encrypt OpenPGP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish OpenPGP message: %w", err)
	}
	return ciphertext.Bytes(), nil
}

// DecryptMessage decrypts an OpenPGP message using an armored private keyring.
func DecryptMessage(armoredPrivate, ciphertext []byte) ([]byte, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(armoredPrivate))
	if err != nil || len(entities) == 0 || entities[0].PrivateKey == nil {
		return nil, errors.New("invalid armored OpenPGP private key")
	}
	message, err := openpgp.ReadMessage(bytes.NewReader(ciphertext), entities, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt OpenPGP message: %w", err)
	}
	plaintext, err := io.ReadAll(message.UnverifiedBody)
	if err != nil {
		return nil, fmt.Errorf("read decrypted OpenPGP message: %w", err)
	}
	if message.SignatureError != nil {
		return nil, fmt.Errorf("verify OpenPGP message: %w", message.SignatureError)
	}
	return plaintext, nil
}

// SignPublicKey creates an armored detached signature over the new public key.
func SignPublicKey(oldPrivate, newPublic []byte) ([]byte, error) {
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(oldPrivate))
	if err != nil || len(entities) == 0 || entities[0].PrivateKey == nil {
		return nil, errors.New("invalid armored OpenPGP private key")
	}
	var signature bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&signature, entities[0], bytes.NewReader(newPublic), nil); err != nil {
		return nil, fmt.Errorf("sign rotated OpenPGP public key: %w", err)
	}
	return signature.Bytes(), nil
}

func deriveWrappingKey(secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("countries/pgp/private-key/v1"))
	return mac.Sum(nil)
}
