// EMPTY_LIST 是一个**引用稳定**的空数组，专供「数据还没到就退回空列表」的场景。
//
// ## 为什么不能就地写 `?? []`
//
// 数组字面量每次渲染都会新建一个引用。当它落进 useMemo / useEffect 的依赖数组里时，
// 依赖每一轮都判为「变了」—— memo 永远重算、effect 永远重跑，等于白写。
// 界面看着完全正常，只是在无谓地重复计算，所以这类问题不会被用户发现，只能靠 lint 抓：
// `react-hooks/exhaustive-deps` 报的
// 「The 'x' logical expression could make the dependencies ... change on every render」
// 就是它。
//
// ## 为什么类型是 never[]
//
// `T[] | never[]` 会被 TS 收窄回 `T[]`，所以各调用点不需要额外断言或类型体操，
// 直接把 `?? []` 换成 `?? EMPTY_LIST` 即可。
//
// ## 为什么不怕被改
//
// `never[]` 的 push 需要传一个 never 类型的值 —— 现实中构造不出来，所以它实际上只读。
export const EMPTY_LIST: never[] = [];
