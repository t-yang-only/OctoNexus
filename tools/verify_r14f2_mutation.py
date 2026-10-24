import io
import subprocess

P = r'internal/server/handlers/user.go'
FIXED = '''	// 登出单独成组: 不挂 RequireJSON(登出无 body, 线上验证曾因挂在 JSON 组返回 415,
	// 前端清不掉 HttpOnly cookie)、不挂 Auth(过期后也要能登出)。
	router.NewGroupRouter("/api/v1/user").
		ServeOn(router.ServerAdmin).
		AddRoute(
			router.NewRoute("/logout", http.MethodPost).
				Handle(logout),
		)
'''
MUT = ''
CMD = 'cd /d "D:\\奇怪的软件\\octopus" && go test ./internal/server/handlers/ -run "TestLogoutClearsCookieWithoutJSONBody" -count=1 2>&1'


def run(cmd):
    r = subprocess.run(cmd, shell=True, capture_output=True, text=True, encoding='utf-8', errors='replace')
    return (r.stdout or '') + (r.stderr or '')


s = io.open(P, encoding='utf-8').read()
assert FIXED in s, 'anchor missing'

# 变异: 删除独立组(登出回到 login 组的 RequireJSON 管辖范围由注册表决定 ——
# 删掉独立组后 logout 不再注册, 但变异目标是"挂回 JSON 组", 故改为把独立组的
# Use(RequireJSON) 加上, 模拟"挂在 JSON 组"的缺陷形态)
MUTATED = FIXED.replace(
    '\t\tServeOn(router.ServerAdmin).\n\t\tAddRoute(\n\t\t\trouter.NewRoute("/logout", http.MethodPost).\n\t\t\t\tHandle(logout),\n\t\t)\n',
    '\t\tServeOn(router.ServerAdmin).\n\t\tUse(middleware.RequireJSON()).\n\t\tAddRoute(\n\t\t\trouter.NewRoute("/logout", http.MethodPost).\n\t\t\t\tHandle(logout),\n\t\t)\n',
)
assert MUTATED != FIXED, 'mutation anchor missing'

io.open(P, 'w', encoding='utf-8', newline='').write(s.replace(FIXED, MUTATED, 1))
out = run(CMD)
print('MUTATION-R14F2:', [l.strip()[:110] for l in out.splitlines() if '--- ' in l][:1])

io.open(P, 'w', encoding='utf-8', newline='').write(s)
out = run(CMD)
print('RESTORE:', [l.strip()[:110] for l in out.splitlines() if '--- ' in l][:1])
