/**
 * 版本比较的判据（T-identity-001 修"把降级当升级"）。
 *
 * 为什么用脚本而不是前端单测框架：本项目前端没有测试框架，为一个纯函数引入 vitest
 * 会把 devDependencies 与构建链路一起改掉，代价远大于收益。这里直接 import 被测的
 * TS 源文件（Node 25 原生支持 --experimental-strip-types），判据一样是"输入→期望输出"，
 * 且测的就是线上会跑的那份代码，不是抄一遍的副本。
 *
 * 用法：node --experimental-strip-types tools/check_version_compare.mjs
 */
import { parseVersion, compareVersions, hasNewerVersion } from '../web/src/lib/version.ts';

let pass = 0;
let fail = 0;
function check(name, got, want) {
    const ok = JSON.stringify(got) === JSON.stringify(want);
    if (ok) {
        pass++;
    } else {
        fail++;
        console.log(`  FAIL ${name}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
    }
}

// --- 这个 bug 的核心场景：线上比 release 新，不能提示升级 ---
// 线上 v0.79.0，仓库里有 fork 遗留的 v0.14.0 release。旧实现按字符串不等判定，
// 会把降级当成升级提示，用户一点"立即更新"就装回上游旧版。
check('v0.14.0 对 v0.79.0 不提示', hasNewerVersion('v0.14.0', 'v0.79.0'), false);
check('v0.79.0 对 v0.14.0 提示', hasNewerVersion('v0.79.0', 'v0.14.0'), true);
check('同版本不提示', hasNewerVersion('v0.79.0', 'v0.79.0'), false);

// --- 字典序陷阱：0.9 在字典序上"大于" 0.10，数值上却更小 ---
check('v0.9.0 对 v0.10.0 不提示（字典序陷阱）', hasNewerVersion('v0.9.0', 'v0.10.0'), false);
check('v0.10.0 对 v0.9.0 提示', hasNewerVersion('v0.10.0', 'v0.9.0'), true);
check('v1.0.0 对 v0.99.99 提示', hasNewerVersion('v1.0.0', 'v0.99.99'), true);
check('v2.0.0 对 v10.0.0 不提示', hasNewerVersion('v2.0.0', 'v10.0.0'), false);

// --- 解析宽容度 ---
check('无 v 前缀', parseVersion('1.2.3').version, { major: 1, minor: 2, patch: 3 });
check('refs/tags/ 前缀', parseVersion('refs/tags/v1.2.3').version, { major: 1, minor: 2, patch: 3 });
check('两段补 0', parseVersion('1.2').version, { major: 1, minor: 2, patch: 0 });
check('空串不支持', parseVersion('').ok, false);
check('缺段不支持', parseVersion('v1').ok, false);
check('四段不支持', parseVersion('v1.2.3.4').ok, false);
check('带后缀不支持', parseVersion('v1.2.3-rc1').ok, false);
check('非数字不支持', parseVersion('v1.x.3').ok, false);
check('空 latest 不提示', hasNewerVersion('', 'v0.79.0'), false);
check('空 current 不提示', hasNewerVersion('v0.80.0', ''), false);
check('认不出形状一律不提示', hasNewerVersion('latest', 'v0.79.0'), false);

// --- 比较函数本身 ---
check('同版本返回 0', compareVersions({ major: 1, minor: 2, patch: 3 }, { major: 1, minor: 2, patch: 3 }), 0);
check('major 优先', compareVersions({ major: 2, minor: 0, patch: 0 }, { major: 1, minor: 9, patch: 9 }), 1);
check('minor 优先于 patch', compareVersions({ major: 1, minor: 3, patch: 0 }, { major: 1, minor: 2, patch: 9 }), 1);

// --- 以下三条是变异检查补出来的隔离用例 ---
// 少了它们，patch 比较写反、v/V 前缀只认一半都测不出来：
// 上面的用例要么 major/minor 就已分出胜负（走不到 patch 分支），
// 要么用的是小写 v（走不到 V 分支）。守卫被别的守卫掩盖，正是这个项目的经典陷阱。
check('patch 级差异：仅第三段不同', compareVersions({ major: 1, minor: 2, patch: 4 }, { major: 1, minor: 2, patch: 3 }), 1);
check('patch 级差异：反方向', compareVersions({ major: 1, minor: 2, patch: 3 }, { major: 1, minor: 2, patch: 4 }), -1);
check('patch 决定升级判定', hasNewerVersion('v1.2.4', 'v1.2.3'), true);
check('patch 不误报升级', hasNewerVersion('v1.2.3', 'v1.2.4'), false);
check('大写 V 前缀', parseVersion('V1.2.3').version, { major: 1, minor: 2, patch: 3 });
check('大写 V 参与升级判定', hasNewerVersion('V2.0.0', 'v1.9.9'), true);

console.log(`\n通过 ${pass} / 失败 ${fail}`);
process.exit(fail === 0 ? 0 : 1);
