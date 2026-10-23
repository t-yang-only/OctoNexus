import io
import subprocess
import sys

P = r'internal/op/sub2api.go'


def run(cmd):
    r = subprocess.run(cmd, shell=True, capture_output=True, text=True, encoding='utf-8', errors='replace')
    return r.returncode, (r.stdout or '') + (r.stderr or '')


s = io.open(P, encoding='utf-8').read()

# 变异 1: R5-F1 去锁
orig_lock = '\tsyncLock := syncModelsLockFor(account.ChannelID)\n\tsyncLock.Lock()\n\tdefer syncLock.Unlock()\n'
assert orig_lock in s, 'lock anchor not found'
mut = s.replace(orig_lock, '\t_ = syncModelsLockFor(account.ChannelID) // MUTATION-R5F1\n', 1)
io.open(P, 'w', encoding='utf-8', newline='').write(mut)
code, out = run('cd /d "D:\\奇怪的软件\\octopus" && go test ./internal/op/ -run "TestSub2APISyncModelsConcurrentKeepsManualModels" 2>&1 | findstr /C:"--- FAIL" /C:"--- PASS"')
print('MUTATION-R5F1 exit=%d' % code)
print([l for l in out.splitlines() if 'FAIL' in l or 'PASS' in l][:2])

# 还原
io.open(P, 'w', encoding='utf-8', newline='').write(s)
code, out = run('cd /d "D:\\奇怪的软件\\octopus" && go test ./internal/op/ -run "TestSub2APISyncModelsConcurrentKeepsManualModels" 2>&1 | findstr /C:"--- FAIL" /C:"--- PASS"')
print('RESTORE exit=%d' % code)
print([l for l in out.splitlines() if 'FAIL' in l or 'PASS' in l][:2])
