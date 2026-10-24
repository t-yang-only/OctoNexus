import io
import subprocess

P = r'internal/server/middleware/auth.go'
ANCHOR = '\tc.SetCookie("auth", "", -1, "/", "", CookieSecure(c), true)\n'
MUT = '\tc.SetCookie("auth", "", -1, "/", "", CookieSecure(c), false) // MUT-R14: 去掉 HttpOnly\n'
CMD = 'cd /d "D:\\奇怪的软件\\octopus" && go test ./internal/server/middleware/ -run "TestAuthCookieFlags" -count=1 2>&1'


def run(cmd):
    r = subprocess.run(cmd, shell=True, capture_output=True, text=True, encoding='utf-8', errors='replace')
    return (r.stdout or '') + (r.stderr or '')


s = io.open(P, encoding='utf-8').read()
assert ANCHOR in s, 'anchor missing'
io.open(P, 'w', encoding='utf-8', newline='').write(s.replace(ANCHOR, MUT, 1))
out = run(CMD)
print('MUTATION-R14:', [l.strip()[:90] for l in out.splitlines() if '--- ' in l][:1])

io.open(P, 'w', encoding='utf-8', newline='').write(s)
out = run(CMD)
print('RESTORE:', [l.strip()[:90] for l in out.splitlines() if '--- ' in l][:1])
