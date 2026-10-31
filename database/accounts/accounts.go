package accounts

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	logger "github.com/komari-monitor/komari/utils/log"
	"golang.org/x/crypto/argon2"
	"gorm.io/gorm"
)

const constantSalt = "06Wm4Jv1Hkxx"

// argon2id parameters follow the OWASP minimum so login stays affordable on
// small hosts. They are stored in each hash and may be raised later; older
// hashes are upgraded on the next successful login.
const (
	argon2Memory  uint32 = 19 * 1024
	argon2Time    uint32 = 2
	argon2Threads uint8  = 1
	argon2SaltLen        = 16
	argon2KeyLen  uint32 = 32

	argon2MaxMemory uint32 = 4 * 1024 * 1024
	argon2MaxTime   uint32 = 64
	argon2Prefix           = "$argon2id$"
)

// dummyPasswordHash equalizes login timing for unknown usernames.
var dummyPasswordHash = sync.OnceValue(func() string { return hashPasswd(uuid.NewString()) })

// CheckPassword 检查密码是否正确
//
// 如果密码正确，返回用户的 UUID 和 true；否则返回空字符串和 false
func CheckPassword(username, passwd string) (uuid string, success bool) {
	db := dbcore.GetDBInstance()
	var user models.User
	result := db.Where("username = ?", username).First(&user)
	if result.Error != nil {
		// 静默处理错误，不显示日志
		verifyPasswd(dummyPasswordHash(), passwd)
		return "", false
	}
	valid, needsRehash := verifyPasswd(user.Passwd, passwd)
	if !valid {
		return "", false
	}
	if needsRehash {
		rehashPasswd(db, user, passwd)
	}
	return user.UUID, true
}

// rehashPasswd upgrades a stored hash after a successful login. A failure only
// delays the upgrade to the next login. The old hash in the WHERE clause keeps a
// concurrent password change from being overwritten.
func rehashPasswd(db *gorm.DB, user models.User, passwd string) {
	err := db.Model(&models.User{}).
		Where("uuid = ? AND passwd = ?", user.UUID, user.Passwd).
		UpdateColumn("passwd", hashPasswd(passwd)).Error
	if err != nil {
		logger.Errorf("accounts", "Failed to upgrade password hash for user %s: %v", user.UUID, err)
	}
}

// ForceResetPassword 强制重置用户密码
func ForceResetPassword(username, passwd string) (err error) {
	db := dbcore.GetDBInstance()
	result := db.Model(&models.User{}).Where("username = ?", username).Update("passwd", hashPasswd(passwd))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("无法找到用户名")
	}
	return nil
}

// hashPasswd 使用 argon2id 和随机盐生成 PHC 格式的密码哈希
func hashPasswd(passwd string) string {
	salt := make([]byte, argon2SaltLen)
	rand.Read(salt) // crypto/rand.Read never returns an error
	key := argon2.IDKey([]byte(passwd), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	return fmt.Sprintf("%sv=%d$m=%d,t=%d,p=%d$%s$%s", argon2Prefix, argon2.Version, argon2Memory, argon2Time, argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// verifyPasswd checks a password against a stored argon2id or legacy hash.
// needsRehash is true when a valid password should be stored in the current format.
func verifyPasswd(stored, passwd string) (valid, needsRehash bool) {
	if !strings.HasPrefix(stored, argon2Prefix) {
		return subtle.ConstantTimeCompare([]byte(legacyHashPasswd(passwd)), []byte(stored)) == 1, true
	}
	parts := strings.Split(stored, "$")
	if len(parts) != 6 {
		return false, false
	}
	var version int
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, false
	}
	if memory == 0 || memory > argon2MaxMemory || iterations == 0 || iterations > argon2MaxTime || threads == 0 {
		return false, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, false
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return false, false
	}
	computed := argon2.IDKey([]byte(passwd), salt, iterations, memory, threads, uint32(len(key)))
	if subtle.ConstantTimeCompare(computed, key) != 1 {
		return false, false
	}
	return true, memory != argon2Memory || iterations != argon2Time || threads != argon2Threads || uint32(len(key)) != argon2KeyLen
}

// legacyHashPasswd reproduces the old salted SHA-256 format. It is used only to
// verify hashes created before argon2id; nothing new is stored in this format.
func legacyHashPasswd(passwd string) string {
	saltedPassword := passwd + constantSalt
	hash := sha256.New()
	hash.Write([]byte(saltedPassword))
	hashedPassword := base64.StdEncoding.EncodeToString(hash.Sum(nil))
	return hashedPassword
}

func CreateAccount(username, passwd string) (user models.User, err error) {
	return CreateAccountWithDB(dbcore.GetDBInstance(), username, passwd)
}

func CreateAccountWithDB(db *gorm.DB, username, passwd string) (user models.User, err error) {
	hashedPassword := hashPasswd(passwd)
	user = models.User{
		UUID:     uuid.New().String(),
		Username: username,
		Passwd:   hashedPassword,
	}
	err = db.Create(&user).Error
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

func DeleteAccountByUsername(username string) (err error) {
	return DeleteAccountByUsernameWithDB(dbcore.GetDBInstance(), username)
}

func DeleteAccountByUsernameWithDB(db *gorm.DB, username string) (err error) {
	err = db.Where("username = ?", username).Delete(&models.User{}).Error
	if err != nil {
		return err
	}
	return nil
}

func GetUserByUUID(uuid string) (user models.User, err error) {
	db := dbcore.GetDBInstance()
	err = db.Where("uuid = ?", uuid).First(&user).Error
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

// 通过 SSO 信息获取用户
func GetUserBySSO(ssoID string) (user models.User, err error) {
	db := dbcore.GetDBInstance()

	// 首先尝试查找已存在的用户
	err = db.Where("sso_id = ?", ssoID).First(&user).Error
	if err == nil {
		return user, nil
	}

	// 如果找不到用户，返回明确的错误信息
	return models.User{}, fmt.Errorf("用户不存在：%s", ssoID)
}

func BindingExternalAccount(uuid string, sso_id string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Update("sso_id", sso_id).Error
	if err != nil {
		return err
	}
	return nil
}

func UnbindExternalAccount(uuid string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Update("sso_id", "").Error
	if err != nil {
		return err
	}
	return nil
}

func UpdateUser(uuid string, name, password, sso_type *string) error {
	db := dbcore.GetDBInstance()
	// Check if user exists
	var existingUser models.User
	result := db.Where("uuid = ?", uuid).First(&existingUser)
	if result.Error != nil {
		return fmt.Errorf("user not found: %s", uuid)
	}
	updates := make(map[string]interface{})
	if name != nil {
		updates["username"] = *name
	}
	if password != nil {
		updates["passwd"] = hashPasswd(*password)
	}
	if sso_type != nil {
		updates["sso_type"] = *sso_type
	}
	updates["updated_at"] = time.Now().UTC()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Updates(updates).Error
	if err != nil {
		return err
	}
	if password != nil {
		DeleteAllSessions()
	}
	return nil
}
