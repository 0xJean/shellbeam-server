package accounts

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"golang.org/x/crypto/argon2"
)

func TestMain(m *testing.M) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:database_accounts_test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	os.Exit(m.Run())
}

func storedPasswd(t *testing.T, username string) string {
	t.Helper()
	var user models.User
	if err := dbcore.GetDBInstance().First(&user, "username = ?", username).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	return user.Passwd
}

func TestNewPasswordsUseArgon2id(t *testing.T) {
	const password = "Correct-horse-1"
	username := "new-" + uuid.NewString()
	if _, err := CreateAccount(username, password); err != nil {
		t.Fatalf("create account: %v", err)
	}
	stored := storedPasswd(t, username)
	if !strings.HasPrefix(stored, argon2Prefix) || len(stored) > 255 {
		t.Fatalf("unexpected stored hash format, length %d", len(stored))
	}
	if stored == hashPasswd(password) {
		t.Fatal("hashes must use a random salt")
	}
	if _, ok := CheckPassword(username, password); !ok {
		t.Fatal("correct password rejected")
	}
	if _, ok := CheckPassword(username, "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	if storedPasswd(t, username) != stored {
		t.Fatal("a current hash must not be rewritten on login")
	}
}

func TestLegacyHashIsVerifiedAndUpgradedOnLogin(t *testing.T) {
	const password = "Legacy-password-1"
	username := "legacy-" + uuid.NewString()
	user := models.User{UUID: uuid.NewString(), Username: username, Passwd: legacyHashPasswd(password)}
	db := dbcore.GetDBInstance()
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create legacy user: %v", err)
	}
	session, err := CreateSession(user.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, ok := CheckPassword(username, "wrong"); ok {
		t.Fatal("wrong password accepted for legacy hash")
	}
	if storedPasswd(t, username) != user.Passwd {
		t.Fatal("failed login changed the stored hash")
	}

	uuidValue, ok := CheckPassword(username, password)
	if !ok || uuidValue != user.UUID {
		t.Fatal("legacy password rejected")
	}
	upgraded := storedPasswd(t, username)
	if !strings.HasPrefix(upgraded, argon2Prefix) {
		t.Fatal("legacy hash was not upgraded")
	}
	if _, ok := CheckPassword(username, password); !ok {
		t.Fatal("password rejected after upgrade")
	}
	if _, err := GetSession(session); err != nil {
		t.Fatalf("upgrade must not end existing sessions: %v", err)
	}
}

func TestPasswordSettersStoreArgon2id(t *testing.T) {
	username := "setter-" + uuid.NewString()
	user, err := CreateAccount(username, "Initial-password-1")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := ForceResetPassword(username, "Reset-password-1"); err != nil {
		t.Fatalf("force reset: %v", err)
	}
	if !strings.HasPrefix(storedPasswd(t, username), argon2Prefix) {
		t.Fatal("ForceResetPassword stored a non-argon2id hash")
	}
	if _, ok := CheckPassword(username, "Reset-password-1"); !ok {
		t.Fatal("reset password rejected")
	}
	updated := "Updated-password-1"
	if err := UpdateUser(user.UUID, nil, &updated, nil); err != nil {
		t.Fatalf("update user: %v", err)
	}
	if !strings.HasPrefix(storedPasswd(t, username), argon2Prefix) {
		t.Fatal("UpdateUser stored a non-argon2id hash")
	}
	if _, ok := CheckPassword(username, updated); !ok {
		t.Fatal("updated password rejected")
	}
}

func TestVerifyPasswdRejectsMalformedHashes(t *testing.T) {
	for _, stored := range []string{
		"",
		argon2Prefix,
		argon2Prefix + "v=19$m=0,t=2,p=1$AAAA$AAAA",
		argon2Prefix + "v=19$m=19456,t=0,p=1$AAAA$AAAA",
		argon2Prefix + "v=19$m=19456,t=2,p=0$AAAA$AAAA",
		argon2Prefix + "v=19$m=4294967295,t=2,p=1$AAAA$AAAA",
		argon2Prefix + "v=1$m=19456,t=2,p=1$AAAA$AAAA",
		argon2Prefix + "v=19$m=19456,t=2,p=1$!!$AAAA",
		argon2Prefix + "v=19$m=19456,t=2,p=1$AAAA$",
	} {
		if valid, _ := verifyPasswd(stored, "password"); valid {
			t.Errorf("malformed hash %q accepted", stored)
		}
	}
}

func TestVerifyPasswdRequestsUpgradeOfOtherParameters(t *testing.T) {
	stored := hashPasswd("password")
	if valid, rehash := verifyPasswd(stored, "password"); !valid || rehash {
		t.Fatalf("current hash: valid=%v rehash=%v", valid, rehash)
	}
	// The derived key depends on the parameters, so editing them alone fails.
	if valid, _ := verifyPasswd(strings.Replace(stored, "t=2", "t=1", 1), "password"); valid {
		t.Fatal("altered parameters must not verify")
	}
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte("password"), salt, 1, argon2Memory, argon2Threads, argon2KeyLen)
	older := fmt.Sprintf("%sv=%d$m=%d,t=1,p=%d$%s$%s", argon2Prefix, argon2.Version, argon2Memory, argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	if valid, rehash := verifyPasswd(older, "password"); !valid || !rehash {
		t.Fatalf("older hash: valid=%v rehash=%v", valid, rehash)
	}
}
