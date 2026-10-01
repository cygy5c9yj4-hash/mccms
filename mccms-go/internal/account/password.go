package account

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// 口令散列使用 PBKDF2-HMAC-SHA256（crypto/pbkdf2 自 Go 1.24 起进入标准库，
// 因此这里不需要引入任何第三方依赖）。
//
// 存储格式（各段以 $ 分隔）：
//
//	pbkdf2_sha256$<iterations>$<salt-b64>$<hash-b64>
//
// 前缀标明算法，便于将来平滑升级到更强的 KDF：升级时新增算法分支，
// 旧记录仍可校验，用户下次登录成功后再重新散列即可。
const (
	// passwordAlgo 当前算法标识。
	passwordAlgo = "pbkdf2_sha256"
	// passwordIterations PBKDF2 迭代次数。
	passwordIterations = 210_000
	// passwordSaltLen 盐长度（字节）。
	passwordSaltLen = 16
	// passwordKeyLen 派生密钥长度（字节）。
	passwordKeyLen = 32

	// MinPasswordLen 口令最小长度。
	MinPasswordLen = 8
	// MaxPasswordLen 口令最大长度（避免超长输入拖垮 KDF）。
	MaxPasswordLen = 200
)

// ErrBadPassword 表示口令不符合强度要求。
var ErrBadPassword = errors.New("口令强度不足")

// HashPassword 生成口令散列。返回的字符串可直接存入 User.PasswordHash。
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLen {
		return "", fmt.Errorf("%w：至少 %d 个字符", ErrBadPassword, MinPasswordLen)
	}
	if len(password) > MaxPasswordLen {
		return "", fmt.Errorf("%w：最多 %d 个字符", ErrBadPassword, MaxPasswordLen)
	}

	salt := make([]byte, passwordSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成盐失败: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, passwordKeyLen)
	if err != nil {
		return "", fmt.Errorf("派生密钥失败: %w", err)
	}

	return strings.Join([]string{
		passwordAlgo,
		strconv.Itoa(passwordIterations),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	}, "$"), nil
}

// VerifyPassword 校验明文口令与散列是否匹配。
//
// 比较使用常量时间实现，避免通过响应时间侧信道推断散列内容。
// 散列格式非法时返回 (false, err)；不匹配时返回 (false, nil)。
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 {
		return false, errors.New("口令散列格式非法")
	}
	if parts[0] != passwordAlgo {
		return false, fmt.Errorf("不支持的口令算法 %q", parts[0])
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false, errors.New("口令散列迭代次数非法")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, errors.New("口令散列盐非法")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, errors.New("口令散列内容非法")
	}

	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false, fmt.Errorf("派生密钥失败: %w", err)
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
