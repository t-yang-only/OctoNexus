import io
import subprocess

P = r'internal/op/sub2api.go'
ANCHOR = '\tif account.Credentials == "" {\n\t\treturn "", fmt.Errorf("该账号未配置凭据，请先编辑账号补全 API Key 后再探测")\n\t}\n'
MUT = '\tif false && account.Credentials == "" { // MUT-R10\n\t\treturn "", fmt.Errorf("该账号未配置凭据，请先编辑账号补全 API Key 后再探测")\n\t}\n'
CMD = 'cd /d "D:\\奇怪的软件\\octopus" && go test ./internal/op/ -run "TestSub2APIProbeMissingCredentialsGuard" -count=1 2>&1'


def run(cmd):
    r = subprocess.run(cmd, shell=True, capture_output=True, text=True, encoding='utf-8', errors='replace')
    return (r.stdout or '') + (r.stderr or '')


s = io.open(P, encoding='utf-8').read()
assert ANCHOR in s, 'anchor missing'

io.open(P, 'w', encoding='utf-8', newline='').write(s.replace(ANCHOR, MUT, 1))
out = run(CMD)
print('MUTATION:', [l.strip()[:110] for l in out.splitlines() if '--- ' in l][:1])

io.open(P, 'w', encoding='utf-8', newline='').write(s)
out = run(CMD)
print('RESTORE:', [l.strip()[:110] for l in out.splitlines() if '--- ' in l][:1])
