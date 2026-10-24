import io
import subprocess

P = r'internal/op/sub2api.go'
MUTS = [
    ('probe', '\tif !account.Enabled {\n\t\treturn "", fmt.Errorf("该账号已停用，请先在面板启用后再探测")\n\t}',
     '\tif false && !account.Enabled {\n\t\treturn "", fmt.Errorf("该账号已停用，请先在面板启用后再探测")\n\t}'),
    ('sync', '\tif !account.Enabled {\n\t\treturn nil, fmt.Errorf("该账号已停用，请先在面板启用后再同步模型")\n\t}',
     '\tif false && !account.Enabled {\n\t\treturn nil, fmt.Errorf("该账号已停用，请先在面板启用后再同步模型")\n\t}'),
    ('clean', '\tif !account.Enabled {\n\t\treturn nil, fmt.Errorf("该账号已停用，请先在面板启用后再清理")\n\t}',
     '\tif false && !account.Enabled {\n\t\treturn nil, fmt.Errorf("该账号已停用，请先在面板启用后再清理")\n\t}'),
]
CMD = 'cd /d "D:\\奇怪的软件\\octopus" && go test ./internal/op/ -run "TestSub2APIDisabledAccountGuardsAllPaths" -count=1 2>&1'


def run(cmd):
    r = subprocess.run(cmd, shell=True, capture_output=True, text=True, encoding='utf-8', errors='replace')
    return (r.stdout or '') + (r.stderr or '')


orig = io.open(P, encoding='utf-8').read()
for name, anchor, mut in MUTS:
    assert anchor in orig, name + ' anchor missing'
    io.open(P, 'w', encoding='utf-8', newline='').write(orig.replace(anchor, mut, 1))
    out = run(CMD)
    verdict = [l.strip()[:90] for l in out.splitlines() if '--- ' in l]
    print('MUTATION-%s: %s' % (name, verdict[:1]))

io.open(P, 'w', encoding='utf-8', newline='').write(orig)
out = run(CMD)
print('RESTORE:', [l.strip()[:90] for l in out.splitlines() if '--- ' in l][:1])
