package op

import (
	"fmt"
	"sync"
	"time"
)

// R13-F1: 登录失败节流 —— 管理面暴露在公网, 无限次试密等于把爆破门槛降为零。
// 单二进制部署下进程内节流即覆盖全部登录入口; 按用户名分别计数, 避免互相牵连。
const (
	maxLoginFailures  = 5
	loginLockDuration = 5 * time.Minute
	minPasswordLength = 8
)

type loginFailState struct {
	count int
	until time.Time // 锁定截止; 零值表示未锁定
}

var loginThrottle struct {
	sync.Mutex
	states map[string]*loginFailState
}

func init() {
	loginThrottle.states = make(map[string]*loginFailState)
}

// LoginThrottleCheck 返回锁定剩余时间错误; 未锁定返回 nil。
func LoginThrottleCheck(username string) error {
	loginThrottle.Lock()
	defer loginThrottle.Unlock()
	st, ok := loginThrottle.states[username]
	if !ok || st.until.IsZero() {
		return nil
	}
	if wait := time.Until(st.until); wait > 0 {
		return fmt.Errorf("登录失败次数过多，账号已锁定，请 %.0f 秒后重试", wait.Seconds())
	}
	// 锁定期已过: 清零计数放行
	st.count = 0
	st.until = time.Time{}
	return nil
}

// LoginThrottleRecordFailure 记录一次失败; 达到上限即进入锁定。
func LoginThrottleRecordFailure(username string) {
	loginThrottle.Lock()
	defer loginThrottle.Unlock()
	st, ok := loginThrottle.states[username]
	if !ok {
		st = &loginFailState{}
		loginThrottle.states[username] = st
	}
	st.count++
	if st.count >= maxLoginFailures {
		st.until = time.Now().Add(loginLockDuration)
	}
}

// LoginThrottleReset 登录成功后清零。
func LoginThrottleReset(username string) {
	loginThrottle.Lock()
	defer loginThrottle.Unlock()
	delete(loginThrottle.states, username)
}

// UserLoginVerify 带节流的登录校验 (R13-F1): 锁定期间即使口令正确也拒绝。
func UserLoginVerify(username, password string) error {
	if err := LoginThrottleCheck(username); err != nil {
		return err
	}
	if err := UserVerify(username, password); err != nil {
		LoginThrottleRecordFailure(username)
		return err
	}
	LoginThrottleReset(username)
	return nil
}
