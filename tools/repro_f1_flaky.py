import subprocess

CMD = 'cd /d "D:\\奇怪的软件\\octopus" && go test ./internal/op/ -run "TestSub2APISyncModelsConcurrentKeepsManualModels" -count=1 2>&1'
for i in range(5):
    r = subprocess.run(CMD, shell=True, capture_output=True, text=True, encoding='utf-8', errors='replace')
    out = (r.stdout or '') + (r.stderr or '')
    lines = [l for l in out.splitlines() if '.go:' in l]
    print('run %d: %s' % (i, lines[0] if lines else 'PASS'))
