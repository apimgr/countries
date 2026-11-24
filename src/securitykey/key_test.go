package securitykey

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

func TestGenerateAndProtectOpenPGPKeypair(t *testing.T) {
	now := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	pair, err := Generate("Countries Security", "security@example.test", now)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if pair.CreatedAt != now || !pair.ExpiresAt.Equal(now.Add(keyLifetime)) {
		t.Fatalf("key validity = (%s, %s), want (%s, %s)", pair.CreatedAt, pair.ExpiresAt, now, now.Add(keyLifetime))
	}
	fingerprint, err := Fingerprint(pair.Public)
	if err != nil {
		t.Fatalf("Fingerprint() error = %v", err)
	}
	if fingerprint != pair.Fingerprint || len(fingerprint) != 40 || fingerprint != strings.ToUpper(fingerprint) {
		t.Fatalf("fingerprint = %q, pair fingerprint = %q", fingerprint, pair.Fingerprint)
	}
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(pair.Public))
	if err != nil || len(entities) != 1 || entities[0].PrimaryKey.PubKeyAlgo != packet.PubKeyAlgoEd25519 {
		t.Fatalf("generated public key primary algorithm = %v, error = %v; want Ed25519", entities, err)
	}
	encryptionKey, ok := entities[0].EncryptionKey(now)
	if !ok {
		t.Fatal("generated key has no valid encryption subkey")
	}
	curve, err := encryptionKey.PublicKey.Curve()
	if err != nil || encryptionKey.PublicKey.PubKeyAlgo != packet.PubKeyAlgoX25519 || curve != packet.Curve25519 {
		t.Fatalf("encryption subkey algorithm and curve = (%v, %v, %v), want X25519 Curve25519", encryptionKey.PublicKey.PubKeyAlgo, curve, err)
	}
	secret := bytes.Repeat([]byte{0x5a}, 32)
	encrypted, err := SealPrivate(secret, pair.Private)
	if err != nil {
		t.Fatalf("SealPrivate() error = %v", err)
	}
	opened, err := OpenPrivate(secret, encrypted)
	if err != nil {
		t.Fatalf("OpenPrivate() error = %v", err)
	}
	if !bytes.Equal(opened, pair.Private) {
		t.Fatal("OpenPrivate() did not return the original private key")
	}
	if _, err := OpenPrivate(bytes.Repeat([]byte{0x6b}, 32), encrypted); err == nil {
		t.Fatal("OpenPrivate() accepted the wrong installation secret")
	}
}

func TestOpenPrivateRejectsMalformedInputs(t *testing.T) {
	if _, err := SealPrivate([]byte("short"), []byte("private")); err == nil {
		t.Fatal("SealPrivate() accepted a short installation secret")
	}
	if _, err := OpenPrivate(bytes.Repeat([]byte{1}, 32), []byte("bad")); err == nil {
		t.Fatal("OpenPrivate() accepted truncated ciphertext")
	}
	if _, err := Fingerprint([]byte("not a key")); err == nil {
		t.Fatal("Fingerprint() accepted malformed armored data")
	}
}

func TestEncryptDecryptAndSignRotatedKey(t *testing.T) {
	now := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	oldPair, err := Generate("Countries Security", "security@example.test", now)
	if err != nil {
		t.Fatal(err)
	}
	newPair, err := Generate("Countries Security", "security@example.test", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("retained security report")
	ciphertext, err := EncryptMessage(oldPair.Public, plaintext)
	if err != nil {
		t.Fatalf("EncryptMessage() error = %v", err)
	}
	decrypted, err := DecryptMessage(oldPair.Private, ciphertext)
	if err != nil || !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("DecryptMessage() = (%q, %v), want (%q, nil)", decrypted, err, plaintext)
	}
	newCiphertext, err := EncryptMessage(newPair.Public, decrypted)
	if err != nil {
		t.Fatalf("EncryptMessage(new key) error = %v", err)
	}
	decrypted, err = DecryptMessage(newPair.Private, newCiphertext)
	if err != nil || !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("DecryptMessage(new key) = (%q, %v), want (%q, nil)", decrypted, err, plaintext)
	}
	signature, err := SignPublicKey(oldPair.Private, newPair.Public)
	if err != nil {
		t.Fatalf("SignPublicKey() error = %v", err)
	}
	if !bytes.HasPrefix(signature, []byte("-----BEGIN PGP SIGNATURE-----")) {
		t.Fatalf("SignPublicKey() returned non-armored signature: %q", signature[:min(len(signature), 40)])
	}
	oldEntities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(oldPair.Public))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openpgp.CheckArmoredDetachedSignature(oldEntities, bytes.NewReader(newPair.Public), bytes.NewReader(signature), nil); err != nil {
		t.Fatalf("old key could not verify rotation signature: %v", err)
	}
}
