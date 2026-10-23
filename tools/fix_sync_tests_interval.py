import io

# 批量把「连续 sync」用例里的 LastSyncAt 拨到 11 秒前，使 R8 最小间隔不影响 R4/R5/R6 既有判据。
DIAL = '\tif err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).\n\t\tUpdate("last_sync_at", time.Now().Add(-11*time.Second)).Error; err != nil {\n\t\tt.Fatalf("拨动 LastSyncAt 失败: %v", err)\n\t}\n'


def patch(path, edits, need_conn_func=None, add_time=True):
    s = io.open(path, encoding='utf-8').read()
    for old, new in edits:
        assert old in s, '%s: anchor not found: %r' % (path, old[:60])
        s = s.replace(old, new, 1)
    if need_conn_func and need_conn_func in s:
        s = s.replace(need_conn_func + ' {\n\t_, restore := openSub2APITestDB(t)',
                      need_conn_func + ' {\n\tconn, restore := openSub2APITestDB(t)', 1)
    if add_time and '\t"time"\n' not in s:
        s = s.replace('\t"testing"\n', '\t"testing"\n\t"time"\n', 1)
    io.open(path, 'w', encoding='utf-8', newline='').write(s)
    print('PATCHED', path)


# 1) R5-F1 并发用例: 拨动移到每轮基线之前(goroutine 内成功的 sync 会写 LastSyncAt=now)
patch(r'internal/op/sub2api_r5_test.go', [
    ('\tfor round := 0; round < 20; round++ {\n\t\tif _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil {\n\t\t\tt.Fatalf("第 %d 轮基线同步失败: %v", round, err)\n\t\t}\n',
     '\tfor round := 0; round < 20; round++ {\n' + DIAL + '\t\tif _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil {\n\t\t\tt.Fatalf("第 %d 轮基线同步失败: %v", round, err)\n\t\t}\n'),
    # 2) R5-F2 改名用例: 上游扩容前拨动
    ('\t// 上游新增 model-gamma, sync 必须成功且新授权挂到 custom-key\n\tserveExtra = true\n',
     '\t// 上游新增 model-gamma, sync 必须成功且新授权挂到 custom-key(先拨开最小间隔)\n' + DIAL + '\tserveExtra = true\n'),
], add_time=False)

# 3) R5-F1 的 conn 变量已在该用例内声明; GrantFollows 用例也共用同文件的 DIAL, 需 conn:
s = io.open(r'internal/op/sub2api_r5_test.go', encoding='utf-8').read()
old = 'func TestSub2APISyncModelsGrantFollowsRenamedKey(t *testing.T) {\n\t_, restore := openSub2APITestDB(t)'
assert old in s
s = s.replace(old, 'func TestSub2APISyncModelsGrantFollowsRenamedKey(t *testing.T) {\n\tconn, restore := openSub2APITestDB(t)', 1)
io.open(r'internal/op/sub2api_r5_test.go', 'w', encoding='utf-8', newline='').write(s)
print('CONN for GrantFollows')

# 4) R6 用例: 第二、三次 sync 前拨动
patch(r'internal/op/sub2api_r6_test.go', [
    ('\t// 上游扩容: Added 只报新增的那一个, 不得把旧模型算成新增\n\tserve = ',
     '\t// 上游扩容: Added 只报新增的那一个, 不得把旧模型算成新增(先拨开最小间隔)\n' + DIAL + '\tserve = '),
    ('\t// 上游缩容: Stale 报出下架模型, 且这些模型必须仍留在渠道里(只报告不删除)\n\tserve = ',
     '\t// 上游缩容: Stale 报出下架模型, 且这些模型必须仍留在渠道里(只报告不删除, 先拨开最小间隔)\n' + DIAL + '\tserve = '),
], need_conn_func='func TestSub2APISyncModelsReportsAddedAndStale(t *testing.T)')

# 5) R4 幂等用例: 二次同步前拨动
patch(r'internal/op/sub2api_test.go', [
    ('\t// 幂等：再次同步不得重复追加\n\tagain, err := Sub2APIAccountSyncModels(ctx, created.ID)',
     '\t// 幂等：再次同步不得重复追加(先拨开 R8 最小间隔)\n' + DIAL + '\tagain, err := Sub2APIAccountSyncModels(ctx, created.ID)'),
], need_conn_func='func TestSub2APISyncModelsDynamicDiscovery(t *testing.T)')
