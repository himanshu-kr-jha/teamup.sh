package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/hkdf"
)

// signingKeyLockKey serialises first-boot key creation across replicas. Like
// the wizard's setup lock it is an arbitrary constant; it only has to be stable
// and not collide with the other advisory locks in this database.
const signingKeyLockKey int64 = 8264113570002

const (
	signingKeyAlgorithm = "RS256"
	signingKeyBits      = 2048

	// How the stored key material is protected. 'none' is a bare PKCS#8 DER
	// key; 'aes-256-gcm' is that key sealed under TEAMUP_AUTH_SECRET.
	wrapNone   = "none"
	wrapAESGCM = "aes-256-gcm"
)

// loadOrCreateSigningKey returns the key this deployment signs access tokens
// with, generating and storing one the first time it is asked.
//
// The key lives in the database rather than in the process because every
// replica has to reach the same conclusion: a key minted per process means a
// token issued by one pod is a forgery to the next, and a restart signs
// everybody out. Deriving it from TEAMUP_AUTH_SECRET instead is not an option
// for RSA — crypto/rsa deliberately consumes a random amount of entropy, so
// the same seed does not reproduce the same key even within one process.
//
// Simultaneous first boots are settled by an advisory lock plus a re-read
// inside the transaction, the same shape wizard.FinalizeSetup uses: the
// replicas that lose the race read the winner's key instead of storing a
// second one.
func loadOrCreateSigningKey(ctx context.Context, db *pgxpool.Pool, secret string) (*rsa.PrivateKey, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin signing key transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, signingKeyLockKey); err != nil {
		return nil, fmt.Errorf("acquire signing key lock: %w", err)
	}

	var (
		kid       uuid.UUID
		algorithm string
		wrapping  string
		material  []byte
	)
	err = tx.QueryRow(ctx, `
		SELECT kid, algorithm, wrapping, private_key
		FROM signing_keys ORDER BY created_at LIMIT 1
	`).Scan(&kid, &algorithm, &wrapping, &material)

	switch {
	case err == nil:
		key, err := unwrapSigningKey(algorithm, wrapping, material, secret)
		if err != nil {
			return nil, err
		}
		// Setting the secret on an instance that started without one should
		// protect the key that is already in use rather than demand a new one.
		if wrapping == wrapNone && secret != "" {
			sealed, err := sealSigningKey(key, secret)
			if err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE signing_keys SET wrapping = $2, private_key = $3 WHERE kid = $1
			`, kid, wrapAESGCM, sealed); err != nil {
				return nil, fmt.Errorf("encrypt the stored signing key: %w", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit signing key transaction: %w", err)
		}
		return key, nil

	case errors.Is(err, pgx.ErrNoRows):
		// First boot. The error from GenerateKey used to be discarded, which
		// would have left a nil key and failed every login at signing time.
		key, err := rsa.GenerateKey(rand.Reader, signingKeyBits)
		if err != nil {
			return nil, fmt.Errorf("generate signing key: %w", err)
		}
		wrapping := wrapNone
		material, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("encode signing key: %w", err)
		}
		if secret != "" {
			wrapping = wrapAESGCM
			if material, err = sealSigningKey(key, secret); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO signing_keys (kid, algorithm, wrapping, private_key)
			VALUES ($1, $2, $3, $4)
		`, uuid.New(), signingKeyAlgorithm, wrapping, material); err != nil {
			return nil, fmt.Errorf("store signing key: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit signing key transaction: %w", err)
		}
		return key, nil

	default:
		return nil, fmt.Errorf("load signing key: %w", err)
	}
}

// unwrapSigningKey turns a stored row back into a usable key, or explains why
// it cannot. Every failure here is fatal on purpose: carrying on with a freshly
// generated key would silently sign out everybody who is signed in, and would
// hide the fact that the deployment has lost its key material.
func unwrapSigningKey(algorithm, wrapping string, material []byte, secret string) (*rsa.PrivateKey, error) {
	if algorithm != signingKeyAlgorithm {
		return nil, fmt.Errorf("stored signing key uses %s, which this version does not know how to use", algorithm)
	}

	switch wrapping {
	case wrapNone:
	case wrapAESGCM:
		if secret == "" {
			return nil, errors.New("the stored signing key is encrypted with TEAMUP_AUTH_SECRET, which is not set: " +
				"restore the secret this deployment was started with")
		}
		opened, err := openSigningKey(material, secret)
		if err != nil {
			return nil, fmt.Errorf("TEAMUP_AUTH_SECRET does not match the one the stored signing key was encrypted with: "+
				"restore it, or delete the row in signing_keys to start over (which signs everybody out): %w", err)
		}
		material = opened
	default:
		return nil, fmt.Errorf("stored signing key uses unknown wrapping %q", wrapping)
	}

	parsed, err := x509.ParsePKCS8PrivateKey(material)
	if err != nil {
		return nil, fmt.Errorf("decode stored signing key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("stored signing key is a %T, not an RSA key", parsed)
	}
	return key, nil
}

// secretKey stretches TEAMUP_AUTH_SECRET into an AES key. The secret is an
// operator-chosen string of unknown length, so it cannot be used as key
// material directly.
func secretKey(secret string) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(secret), nil, []byte("teamup signing key")), key); err != nil {
		return nil, fmt.Errorf("derive key from TEAMUP_AUTH_SECRET: %w", err)
	}
	return key, nil
}

func secretAEAD(secret string) (cipher.AEAD, error) {
	key, err := secretKey(secret)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// sealSigningKey encodes the key and encrypts it under the secret, nonce first.
func sealSigningKey(key *rsa.PrivateKey, secret string) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode signing key: %w", err)
	}
	aead, err := secretAEAD(secret)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce for signing key: %w", err)
	}
	return aead.Seal(nonce, nonce, der, nil), nil
}

func openSigningKey(material []byte, secret string) ([]byte, error) {
	aead, err := secretAEAD(secret)
	if err != nil {
		return nil, err
	}
	if len(material) < aead.NonceSize() {
		return nil, errors.New("stored signing key is too short to be encrypted material")
	}
	nonce, sealed := material[:aead.NonceSize()], material[aead.NonceSize():]
	return aead.Open(nil, nonce, sealed, nil)
}
