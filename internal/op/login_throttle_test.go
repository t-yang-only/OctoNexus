package op

import (
	"strings"
	"testing"
	"time"

	"github.com/t-yang-only/OctoNexus/internal/model"
)

// R13-F1: 登录防爆破 —— 连续失败达到上限即锁定, 锁定期内即使口令正确也拒绝;
// 到期清零放行; 登录成功即清零。装置直接驱动节流器, 不依赖真实口令。
func TestLoginThrottleLocksAfterMaxFailures(t *testing.T) {
	const user = "throttle-probe"

	// 前 maxLoginFailures-1 次失败不锁定
	for i := 0; i < maxLoginFailures-1; i++ {
		if err := LoginThrottleCheck(user); err != nil {
			t.Fatalf("第 %d 次失败前不应锁定: %v", i, err)
		}
		LoginThrottleRecordFailure(user)
	}

	// 第 maxLoginFailures 次失败后进入锁定
	LoginThrottleRecordFailure(user)
	err := LoginThrottleCheck(user)
	if err == nil || !strings.Contains(err.Error(), "已锁定") {
		t.Fatalf("达到失败上限应锁定, 实际: %v", err)
	}

	// 锁定期内: 即使"口令正确"(成功路径)也必须先被锁定拦住 —— 这正是
	// UserLoginVerify 的语义: 锁定优先于口令校验
	if err := UserLoginVerify(user, "whatever-password"); err == nil || !strings.Contains(err.Error(), "已锁定") {
		t.Fatalf("锁定期内 UserLoginVerify 应拒绝, 实际: %v", err)
	}

	// 成功登录清零: 先让锁定到期(拨 until), 再走一次成功路径
	loginThrottle.Lock()
	st := loginThrottle.states[user]
	st.until = time.Now().Add(-time.Second) // 拨到已过期
	loginThrottle.Unlock()
	// 过期的锁定在下一次 Check 时清零
	if err := LoginThrottleCheck(user); err != nil {
		t.Fatalf("锁定到期后应放行: %v", err)
	}
	// 此时 UserVerify 会因用户名不存在而失败并重新计数 —— 用成功路径不可行,
	// 直接验证 Reset 语义
	LoginThrottleRecordFailure(user)
	LoginThrottleReset(user)
	if err := LoginThrottleCheck(user); err != nil {
		t.Fatalf("Reset 后应无锁定: %v", err)
	}
}

// R13-F1 变异检查: 去掉"达到上限即锁定"一行, 本用例必须红。
// R13-F2: 密码强度下限在 op 层收口 (handler 之外的第二道入口, 如 CLI/未来 RPC)。
func TestUserChangePasswordStrength(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	// 在临时库造一个已知口令的用户行(UserChangePassword 的 Update 需要主键)
	userCache = model.User{Username: "pw-strength"}
	userCache.Password = "oldpassword123"
	if err := userCache.HashPassword(); err != nil {
		t.Fatalf("哈希失败: %v", err)
	}
	// users 表由 AutoMigrate 建; 测试助手未含该表则先补建
	if !conn.Migrator().HasTable("users") {
		if err := conn.AutoMigrate(&model.User{}); err != nil {
			t.Fatalf("补建 users 表失败: %v", err)
		}
	}
	if err := conn.Create(&userCache).Error; err != nil {
		t.Fatalf("造用户失败: %v", err)
	}

	// 短密码拒绝
	if err := UserChangePassword("oldpassword123", "short"); err == nil || !strings.Contains(err.Error(), "长度不足") {
		t.Fatalf("短密码应被拒绝, 实际: %v", err)
	}

	// 新旧相同拒绝
	if err := UserChangePassword("oldpassword123", "oldpassword123"); err == nil || !strings.Contains(err.Error(), "不能与旧密码相同") {
		t.Fatalf("新旧相同应被拒绝, 实际: %v", err)
	}

	// 合法密码通过(注意会写库, 但测试 DB 是临时的)
	if err := UserChangePassword("oldpassword123", "newpassword456"); err != nil {
		t.Fatalf("合法密码应通过: %v", err)
	}
}
