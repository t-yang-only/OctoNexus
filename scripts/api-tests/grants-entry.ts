// 渠道授权收敛逻辑的自检入口：把界面上真正跑的两条链路——可用性判定（grants.ts）与
// 保存前裁剪/提交载荷（state.ts）——合成一个模块，交给 esbuild 打成单个 cjs，
// 由 check_grants.js require。打成单文件是为了让产物没有运行期依赖，node 直接跑即可。
export * from '../../web/src/components/modules/channel/grants';
export * from '../../web/src/components/modules/channel/state';
