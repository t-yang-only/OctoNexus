import io
import subprocess

P = r'internal/op/sub2api.go'
ANCHOR = '\t\t\t"last_sync_summary": string(summaryPayload),\n'
MUT = '\t\t\t"last_sync_summary": string(summaryPayload[:0]), // MUT-R9: 摘要值为空但保留引用\n'
CMD = 'cd /d "D:\\奇怪的软件\\octopus" && go test ./internal/op/ -run "TestSub2APISyncModelsPersistsSummary" -count=1 2>&1'


def run(cmd):
    r = subprocess.run(cmd, shell=True, capture_output=True, text=True, encoding='utf-8', errors='replace')
    return (r.stdout or '') + (r.stderr or '')


s = io.open(P, encoding='utf-8').read()
assert ANCHOR in s, 'anchor missing'

io.open(P, 'w', encoding='utf-8', newline='').write(s.replace(ANCHOR, MUT, 1))
out = run(CMD)
print('MUTATION:', [l.strip()[:110] for l in out.splitlines() if '--- ' in l or 'FAIL' in l][:2])

io.open(P, 'w', encoding='utf-8', newline='').write(s)
out = run(CMD)
print('RESTORE:', [l.strip()[:110] for l in out.splitlines() if '--- ' in l or 'FAIL' in l][:2])
