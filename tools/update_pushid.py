import io

p = 'ITERATION_STATE.md'
s = io.open(p, encoding='utf-8').read()
s = s.replace('(第四周期 Round 40 达成，已推 Server酱)', '(第四周期 R31-R40 已全部达成，Server酱推送成功 pushid:63786139)', 1)
io.open(p, 'w', encoding='utf-8', newline='').write(s)
print('DOC_PUSH_UPDATED')
