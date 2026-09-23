package authchallenges

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/identity"
	"github.com/hcai-chat/hcai-chat/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestChallengeDigestIsBoundToRequestPurposeAndKey(t *testing.T) {
	s := NewService(nil, bytes.Repeat([]byte{1}, 32), "local_file", "")
	other := NewService(nil, bytes.Repeat([]byte{2}, 32), "local_file", "")
	id := uuid.New()
	code := "123456"
	digest := s.codeDigest(id, RegistrationCode, code)
	for _, otherDigest := range []string{
		hashCode(code), s.codeDigest(uuid.New(), RegistrationCode, code),
		s.codeDigest(id, LoginCode, code), other.codeDigest(id, RegistrationCode, code),
	} {
		if digest == otherDigest {
			t.Error("challenge digest can be reused across requests, purposes, keys or the plain six-digit code space")
		}
	}
}

// Fix the digits, not production entropy: use the issuance digest/encryption
// operations and the actual schema, delivery and consumption paths.
func insertCodeFixture(t *testing.T, pool *pgxpool.Pool, s *Service, email, purpose, code string, legacy bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	nonce, ciphertext, err := s.encrypt(id, purpose, code)
	if err != nil {
		t.Fatal(err)
	}
	digest := s.codeDigest(id, purpose, code)
	if legacy {
		digest = hashCode(code)
	}
	_, err = pool.Exec(context.Background(), `INSERT INTO identity_auth_challenges(id,email_snapshot,purpose,locale,code_hash,code_nonce,code_ciphertext,expires_at) VALUES($1,$2,$3,'en-US',$4,$5,$6,$7)`,
		id, email, purpose, digest, nonce, ciphertext, time.Now().Add(challengeLifetime))
	if err != nil {
		t.Fatalf("independent challenge could not store its code: %v", err)
	}
	return id
}

func TestIdenticalChallengeDigitsRemainIndependent(t *testing.T) {
	pool, cleanup := challengeTestPool(t)
	defer cleanup()
	ctx := context.Background()
	sender := &captureSender{}
	s := NewService(pool, bytes.Repeat([]byte{3}, 32), "smtp", t.TempDir(), sender)
	const code = "123456"
	ids := []uuid.UUID{
		insertCodeFixture(t, pool, s, "same-code-0@example.test", RegistrationCode, code, false),
		insertCodeFixture(t, pool, s, "same-code-1@example.test", RegistrationCode, code, false),
		insertCodeFixture(t, pool, s, "same-code-2@example.test", RegistrationCode, code, true),
	}
	for i, id := range ids {
		if err := s.HandleDeliveryJob(ctx, jobs.Job{Payload: []byte(fmt.Sprintf(`{"challengeId":%q}`, id)), Attempts: 1, MaxAttempts: 5}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(sender.body, []byte("Registration code: "+code)) {
			t.Fatal("delivery did not contain the original digits")
		}
		input := identity.RegisterInput{Email: fmt.Sprintf("same-code-%d@example.test", i), Password: "independent-code-password", Handle: fmt.Sprintf("same_code_%d", i), DisplayName: "Code Test", Locale: "en-US", Timezone: "UTC"}
		if _, _, err := s.CompleteRegistration(ctx, id, input, "654321", identity.ClientInfo{}); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("incorrect digits were accepted: %v", err)
		}
		user, token, err := s.CompleteRegistration(ctx, id, input, code, identity.ClientInfo{})
		if err != nil || token == "" || user.Email != input.Email || !user.EmailVerified {
			t.Fatalf("valid independent registration failed: %v", err)
		}
		if _, _, err := s.CompleteRegistration(ctx, id, input, code, identity.ClientInfo{}); !errors.Is(err, ErrConflict) {
			t.Fatalf("consumed code was reused: %v", err)
		}
	}
	// Reuse the same six digits for two login challenges, neither of which
	// depends on the other user's registration or login state.
	ids = []uuid.UUID{
		insertCodeFixture(t, pool, s, "same-code-0@example.test", LoginCode, code, false),
		insertCodeFixture(t, pool, s, "same-code-1@example.test", LoginCode, code, false),
		insertCodeFixture(t, pool, s, "same-code-2@example.test", LoginCode, code, true),
	}
	for i, id := range ids {
		email := fmt.Sprintf("same-code-%d@example.test", i)
		if _, _, err := s.ConfirmLogin(ctx, id, "outsider@example.test", code, identity.ClientInfo{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("challenge email binding failed: %v", err)
		}
		user, token, err := s.ConfirmLogin(ctx, id, email, code, identity.ClientInfo{})
		if err != nil || token == "" || user.Email != email {
			t.Fatalf("valid independent login failed: %v", err)
		}
		if _, _, err := s.ConfirmLogin(ctx, id, email, code, identity.ClientInfo{}); !errors.Is(err, ErrConflict) {
			t.Fatalf("consumed login code was reused: %v", err)
		}
	}
}

func TestIssuedChallengeUsesProtectedDigest(t *testing.T) {
	pool, cleanup := challengeTestPool(t)
	defer cleanup()
	ctx := context.Background()
	s := NewService(pool, bytes.Repeat([]byte{4}, 32), "local_file", t.TempDir())
	challenge, err := s.Start(ctx, "protected-code@example.test", RegistrationCode, "en-US", "protected-code")
	if err != nil {
		t.Fatal(err)
	}
	var digest string
	var nonce, ciphertext []byte
	if err := pool.QueryRow(ctx, `SELECT code_hash,code_nonce,code_ciphertext FROM identity_auth_challenges WHERE id=$1`, challenge.ID).Scan(&digest, &nonce, &ciphertext); err != nil {
		t.Fatal(err)
	}
	code, err := s.decrypt(challenge.ID, RegistrationCode, nonce, ciphertext)
	if err != nil || !validCode(code) {
		t.Fatalf("issued code ciphertext is invalid: %v", err)
	}
	if digest == hashCode(code) || !s.matchesCode(challenge.ID, RegistrationCode, code, digest) {
		t.Fatal("new request did not store the protected verification digest")
	}
	if s.matchesCode(uuid.New(), RegistrationCode, code, digest) || s.matchesCode(challenge.ID, LoginCode, code, digest) {
		t.Fatal("stored digest could be transferred to another challenge or purpose")
	}
	for _, key := range [][]byte{nil, bytes.Repeat([]byte{5}, 32)} {
		if NewService(nil, key, "local_file", "").matchesCode(challenge.ID, RegistrationCode, code, digest) {
			t.Fatal("new digest was accepted without the original deployment key")
		}
	}
}

func TestCurrentAndLegacyCodesKeepExpiryAndAttemptLimits(t *testing.T) {
	pool, cleanup := challengeTestPool(t)
	defer cleanup()
	ctx := context.Background()
	s := NewService(pool, bytes.Repeat([]byte{6}, 32), "local_file", t.TempDir())
	index := 0
	for _, purpose := range []string{RegistrationCode, LoginCode} {
		for _, legacy := range []bool{false, true} {
			for _, expired := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/legacy_%t/expired_%t", purpose, legacy, expired), func(t *testing.T) {
					index++
					code := fmt.Sprintf("%06d", index)
					email := fmt.Sprintf("code-limit-%d@example.test", index)
					id := insertCodeFixture(t, pool, s, email, purpose, code, legacy)
					if expired {
						if _, err := pool.Exec(ctx, `UPDATE identity_auth_challenges SET created_at=now()-interval '20 minutes',expires_at=now()-interval '1 second' WHERE id=$1`, id); err != nil {
							t.Fatal(err)
						}
					} else {
						// Exercise persisted failed attempts rather than seeding
						// the counter; both formats share the same guessing cap.
						for range maxVerifyAttempts {
							var err error
							if purpose == LoginCode {
								_, _, err = s.ConfirmLogin(ctx, id, email, "999999", identity.ClientInfo{})
							} else {
								_, _, err = s.CompleteRegistration(ctx, id, identity.RegisterInput{Email: email}, "999999", identity.ClientInfo{})
							}
							if !errors.Is(err, ErrInvalidCode) {
								t.Fatalf("incorrect attempt was not counted: %v", err)
							}
						}
					}
					var token string
					var err error
					if purpose == LoginCode {
						_, token, err = s.ConfirmLogin(ctx, id, email, code, identity.ClientInfo{})
					} else {
						_, token, err = s.CompleteRegistration(ctx, id, identity.RegisterInput{Email: email}, code, identity.ClientInfo{})
					}
					want := ErrRateLimited
					if expired {
						want = ErrExpired
					}
					if !errors.Is(err, want) || token != "" {
						t.Fatalf("code limit bypassed: %v", err)
					}
				})
			}
		}
	}
}
