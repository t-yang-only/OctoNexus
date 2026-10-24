import io
import subprocess

P = r'internal/op/login_throttle.go'
ANCHOR = '''	if st.count >= maxLoginFailures {
		st.until = time.Now().Add(loginLockDuration)
	}
'''
MUT = '''	if false && st.count >= maxLoginFailures {
		st.until = time.Now().Add(loginLockDuration)
	}
'''
CMD = 'cd /d "D:\\奇怪的软件\\octopus" && go test ./internal/op/ -run "TestLoginThrottleLocksAfterMaxFailures" -count=1 2>&1'


def run(cmd):
    r = subprocess.run(cmd, shell=True, capture_output=True, text=True, encoding='utf-8', errors='replace')
    return (r.stdout or '') + (r.stderr or '')


s = io.open(P, encoding='utf-8').read()
assert ANCHOR in s, 'anchor missing'
io.open(P, 'w', encoding='utf-8', newline='').write(s.replace(ANCHOR, MUT, 1))
out = run(CMD)
print('MUTATION-F1:', [l.strip()[:90] for l in out.splitlines() if '--- ' in l][:1])

io.open(P, 'w', encoding='utf-8', newline='').write(s)
out = run(CMD)
print('RESTORE-F1:', [l.strip()[:90] for l in out.splitlines() if '--- ' in l][:1])

# F2 变异: 去掉长度校验
P2 = r'internal/op/user.go'
A2 = '\tif len([]rune(newPassword)) < minPasswordLength {\n\t\treturn fmt.Errorf("新密码长度不足 %d 位", minPasswordLength)\n\t}\n'
M2 = '\tif false && len([]rune(newPassword)) < minPasswordLength {\n\t\treturn fmt.Errorf("新密码长度不足 %d 位", minPasswordLength)\n\t}\n'
CMD2 = 'cd /d "D:\\奇怪的软件\\octopus" && go test ./internal/op/ -run "TestUserChangePasswordStrength" -count=1 2>&1'
s2 = io.open(P2, encoding='utf-8').read()
assert A2 in s2, 'F2 anchor missing'
io.open(P2, 'w', encoding='utf-8', newline='').write(s2.replace(A2, M2, 1))
out = run(CMD2)
print('MUTATION-F2:', [l.strip()[:90] for l in out.splitlines() if '--- ' in l][:1])
io.open(P2, 'w', encoding='utf-8', newline='').write(s2)
out = run(CMD2)
print('RESTORE-F2:', [l.strip()[:90] for l in out.splitlines() if '--- ' in l][:1])
