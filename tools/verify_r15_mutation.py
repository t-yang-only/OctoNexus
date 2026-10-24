import io
import subprocess

CMD = 'go test ./internal/op/ -run "TestDBImportWarnsOnForeignSub2APICredentials|TestDBImportSealsPlaintextSub2APICredentials|TestDBDumpRoundTripsSub2APILastSyncModels" -count=1'

def run():
    r = subprocess.run(CMD, shell=True, capture_output=True, text=True, encoding="utf-8", errors="replace")
    out = (r.stdout or "") + (r.stderr or "")
    fails = [l.strip() for l in out.splitlines() if "--- FAIL" in l]
    return fails

# 1. Mutation F1 (suppress warning with n > 999999)
p_warn = "internal/op/backup_warnings.go"
s_warn = io.open(p_warn, encoding="utf-8").read()
a1 = "sub2apiSecretAAD); n > 0 {"
m1 = "sub2apiSecretAAD); n > 999999 {"
assert a1 in s_warn
io.open(p_warn, "w", encoding="utf-8", newline="").write(s_warn.replace(a1, m1, 1))
f1 = run()
io.open(p_warn, "w", encoding="utf-8", newline="").write(s_warn)
print("MUTATION-F1:", f1)

# 2. Mutation F2 (skip sealing on import)
p_bk = "internal/op/backup.go"
s_bk = io.open(p_bk, encoding="utf-8").read()
a2 = "acct.Credentials = sealSub2APICredentialsForStore(row.CredentialsOut)"
m2 = "acct.Credentials = row.CredentialsOut // MUT-F2"
assert a2 in s_bk
io.open(p_bk, "w", encoding="utf-8", newline="").write(s_bk.replace(a2, m2, 1))
f2 = run()
io.open(p_bk, "w", encoding="utf-8", newline="").write(s_bk)
print("MUTATION-F2:", f2)

# 3. Mutation F3 (skip restoring LastSyncModels)
a3 = "acct.LastSyncModels = row.LastSyncModelsOut"
m3 = "_ = row.LastSyncModelsOut // MUT-F3"
assert a3 in s_bk
io.open(p_bk, "w", encoding="utf-8", newline="").write(s_bk.replace(a3, m3, 1))
f3 = run()
io.open(p_bk, "w", encoding="utf-8", newline="").write(s_bk)
print("MUTATION-F3:", f3)

clean = run()
print("CLEAN RESTORE:", clean)
