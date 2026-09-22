package op

import (
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// T-usability-002 分组名拼错时给出候选。
//
// ## 解决什么问题
//
// 客户端报 "model not found" 是最常见的失败之一，而它**不告诉用户任何有用信息**：
// 是分组名拼错了？还是分组根本不存在？用户只能自己翻面板一个个比对。
//
// 分组名在这里扮演「虚拟模型名」的角色，而它常常是手输的
// （`claude-sonnet-4` 打成 `claude-sonet-4`、大小写写错、少个横杠），
// 拼错一个字符的代价是整条请求失败且原因不明。
//
// 所以这里做一件事：**在报错时把最像的几个分组名一并给出来**。
// 用户看到「相近：claude-sonnet-4」就知道该怎么改，不必再去翻面板。
//
// ## 为什么不用复杂的相似度算法
//
// 拼错的实际形态集中在三类：大小写差异、多/少字符、位置写错。
// 前两类用忽略大小写的包含判断就能覆盖，第三类用编辑距离。
// 更复杂的算法（拼音、分词、语义）在这里没有收益 —— 分组名是短标识符，不是自然语言。
const (
	// groupSuggestLimit 默认最多给几个候选：给太多等于没给（用户还是要一个个看）。
	groupSuggestLimit = 3
	// groupSuggestMaxDistance 编辑距离上限：超过它就不算「像」。
	// 取 2 是因为实测的分组名多在 10-30 字符，距离 2 已能覆盖常见的单处笔误；
	// 放宽到 3 会让毫不相干的短名字互相命中（如 "gpt-4o" 与 "gpt-4.1"）。
	groupSuggestMaxDistance = 2
)

// GroupSuggestSimilar 找出与 name 最像的若干分组名（按相似度排序，最多 limit 个）。
//
// 只在失败路径上调用（拼错才查），因此遍历全部分组是可接受的成本。
// limit <= 0 时用默认值。
func GroupSuggestSimilar(name string, limit int) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if limit <= 0 {
		limit = groupSuggestLimit
	}

	conn := db.GetDB()
	if conn == nil {
		return nil
	}
	var groups []model.Group
	if err := conn.Select("name").Find(&groups).Error; err != nil {
		// 候选只是锦上添花，查不到就不给 —— 不能让它把「分组不存在」这个主错误顶掉。
		return nil
	}

	lower := strings.ToLower(name)
	type scored struct {
		name  string
		score int
	}
	candidates := make([]scored, 0, 8)
	for _, g := range groups {
		other := strings.TrimSpace(g.Name)
		if other == "" {
			continue
		}
		otherLower := strings.ToLower(other)

		// 完全相等（忽略大小写）优先：说明只是大小写写错了，这比任何近似都更该先提示。
		if otherLower == lower {
			candidates = append(candidates, scored{other, 0})
			continue
		}
		// 包含关系次之：多打了后缀、少打了前缀这类。
		if strings.Contains(otherLower, lower) || strings.Contains(lower, otherLower) {
			candidates = append(candidates, scored{other, 1})
			continue
		}
		if d := editDistanceWithin(lower, otherLower, groupSuggestMaxDistance); d >= 0 {
			candidates = append(candidates, scored{other, 10 + d})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}
		return candidates[i].name < candidates[j].name
	})

	out := make([]string, 0, limit)
	for _, c := range candidates {
		if len(out) >= limit {
			break
		}
		out = append(out, c.name)
	}
	return out
}

// editDistanceWithin 计算两串的编辑距离，超过 max 时返回 -1（提前放弃）。
//
// 提前放弃的意义：绝大多数候选在头几列就能判出「差太远」，
// 算完整张表纯属浪费。分组名虽短，但 408 个分组逐个全算也没必要。
func editDistanceWithin(a, b string, max int) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la == 0 {
		if lb <= max {
			return lb
		}
		return -1
	}
	if lb == 0 {
		if la <= max {
			return la
		}
		return -1
	}
	// 长度差本身就超过 max 时不可能达标。
	if diff := la - lb; diff > max || -diff > max {
		return -1
	}

	// 按字节算：分组名以 ASCII 为主，中文名（如 "53HK生图"）按字节算会偏大，
	// 但那类名字不会走到「拼错一个字符」的场景，误判成「不像」只是少给一个候选，不影响正确性。
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			m := prev[j-1] + cost
			if v := prev[j] + 1; v < m {
				m = v
			}
			if v := curr[j-1] + 1; v < m {
				m = v
			}
			curr[j] = m
			if m < rowMin {
				rowMin = m
			}
		}
		if rowMin > max {
			return -1
		}
		prev, curr = curr, prev
	}
	if prev[lb] > max {
		return -1
	}
	return prev[lb]
}
