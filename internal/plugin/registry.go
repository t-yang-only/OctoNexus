package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/secret"
	"gorm.io/gorm"
)

// Dir 返回插件根目录（与业务库同目录下的 plugins/）。
//
// 与内核目录同一口径：数据库路径在配置里通常是相对的（data/data.db），这里必须返回**绝对路径**，
// 否则插件进程的 cwd 一变（插件自己的目录）就变成"找不到路径"，且报错内容与真实原因完全对不上。
func Dir() string {
	base := filepath.Dir(conf.AppConfig.Database.Path)
	if base == "" || base == "." {
		base = "data"
	}
	if abs, err := filepath.Abs(base); err == nil {
		base = abs
	}
	return filepath.Join(base, "plugins")
}

// PluginDir 返回单个插件的目录（根目录 + slug）。
func PluginDir(slug string) string { return filepath.Join(Dir(), slug) }

// Scan 扫描插件根目录，把每个合法清单同步进库，并回报解析失败的目录。
//
// 同步语义（与用户"把工具丢进目录就能接进来"的预期一致）：
//   - 新目录：入库，Enabled 默认可用、AutoChannel 默认开（默认值在入口层给，不靠 gorm default 标签）；
//   - 已存在：结构字段以**文件**为准（玩家改了清单，重启/重新扫描后生效），
//     而运行期选定项（Enabled / AutoStart / EgressNodeID / Port / Status / ChannelID）以**库**为准 ——
//     否则每次扫描都会把用户在面板上的选择抹掉。
func Scan(ctx context.Context) ([]model.Plugin, map[string]string, error) {
	conn := db.GetDB()
	if conn == nil {
		return nil, nil, fmt.Errorf("数据库未初始化")
	}
	root := Dir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, nil, fmt.Errorf("创建插件目录失败：%w", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, fmt.Errorf("读取插件目录失败：%w", err)
	}

	failures := map[string]string{}
	found := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		parsed, err := Load(dir)
		if err != nil {
			failures[entry.Name()] = err.Error()
			continue
		}
		if err := upsertManifest(ctx, conn, parsed); err != nil {
			failures[entry.Name()] = err.Error()
			continue
		}
		found = append(found, parsed.Slug)
	}

	// 目录已消失的插件：只标记不删行（删了会把用户的选择与渠道绑定一起丢掉，且无法回滚）。
	if len(found) > 0 {
		if err := conn.WithContext(ctx).Model(&model.Plugin{}).
			Where("slug NOT IN ?", found).Update("status", StatusStopped).Error; err != nil {
			return nil, failures, fmt.Errorf("清理失效插件状态失败：%w", err)
		}
	}
	list, err := List(ctx)
	if err != nil {
		return nil, failures, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Slug < list[j].Slug })
	return list, failures, nil
}

// upsertManifest 把清单写进库（新建或更新结构字段），保留运行期选定项。
func upsertManifest(ctx context.Context, conn *gorm.DB, parsed *Parsed) error {
	argsBytes, err := marshalArgs(parsed.Args)
	if err != nil {
		return err
	}
	autoChannel := true
	if parsed.AutoChannel != nil {
		autoChannel = *parsed.AutoChannel
	}

	var existing model.Plugin
	err = conn.WithContext(ctx).Where("slug = ?", parsed.Slug).First(&existing).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return fmt.Errorf("查询插件记录失败：%w", err)
	}
	if err == gorm.ErrRecordNotFound {
		row := model.Plugin{
			Slug: parsed.Slug, Name: parsed.Name, Version: parsed.Version,
			Runtime: parsed.Runtime, Entry: parsed.Entry, Args: argsBytes, PortEnv: parsed.PortEnv,
			Protocol: parsed.Protocol, BasePath: parsed.BasePath, HealthPath: parsed.HealthPath,
			EgressMode: parsed.EgressMode, Enabled: true, AutoStart: parsed.AutoStart,
			AutoChannel: autoChannel, Status: StatusStopped,
		}
		return conn.WithContext(ctx).Create(&row).Error
	}

	updates := map[string]any{
		"name": parsed.Name, "version": parsed.Version, "runtime": parsed.Runtime,
		"entry": parsed.Entry, "args": argsBytes, "port_env": parsed.PortEnv,
		"protocol": parsed.Protocol, "base_path": parsed.BasePath, "health_path": parsed.HealthPath,
		"egress_mode": parsed.EgressMode, "auto_channel": autoChannel,
	}
	// runtime/http 形态变化会让已分配的端口与出站口径失效：端口回收，交由下次启动重新分配。
	if existing.Runtime != parsed.Runtime || existing.Entry != parsed.Entry || existing.PortEnv != parsed.PortEnv {
		updates["port"] = 0
	}
	return conn.WithContext(ctx).Model(&model.Plugin{}).Where("slug = ?", parsed.Slug).Updates(updates).Error
}

// List 返回库内全部插件（按 slug 排序）。
func List(ctx context.Context) ([]model.Plugin, error) {
	conn := db.GetDB()
	if conn == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	var rows []model.Plugin
	if err := conn.WithContext(ctx).Order("slug asc").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询插件失败：%w", err)
	}
	return rows, nil
}

// Get 按 slug 取一条记录。
func Get(ctx context.Context, slug string) (model.Plugin, error) {
	conn := db.GetDB()
	if conn == nil {
		return model.Plugin{}, fmt.Errorf("数据库未初始化")
	}
	var row model.Plugin
	if err := conn.WithContext(ctx).Where("slug = ?", slug).First(&row).Error; err != nil {
		return model.Plugin{}, fmt.Errorf("插件不存在：%s", slug)
	}
	return row, nil
}

// Update 更新运行期选定项（结构字段以清单文件为准）。
func Update(ctx context.Context, slug string, req model.PluginUpdateRequest) (model.Plugin, error) {
	conn := db.GetDB()
	if conn == nil {
		return model.Plugin{}, fmt.Errorf("数据库未初始化")
	}
	if _, err := Get(ctx, slug); err != nil {
		return model.Plugin{}, err
	}
	updates := map[string]any{}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.AutoStart != nil {
		updates["auto_start"] = *req.AutoStart
	}
	if req.EgressNodeID != nil {
		if *req.EgressNodeID < 0 {
			return model.Plugin{}, fmt.Errorf("egress_node_id 不能为负")
		}
		// 绑定前先验证节点可用：绑一个不存在的节点，下次启动才会报错的体验很差。
		if *req.EgressNodeID > 0 {
			if _, err := op.ProxyNodeEndpoint(*req.EgressNodeID); err != nil {
				return model.Plugin{}, err
			}
		}
		updates["egress_node_id"] = *req.EgressNodeID
	}
	if len(updates) > 0 {
		if err := conn.WithContext(ctx).Model(&model.Plugin{}).Where("slug = ?", slug).Updates(updates).Error; err != nil {
			return model.Plugin{}, fmt.Errorf("更新插件失败：%w", err)
		}
	}
	return Get(ctx, slug)
}

// Remove 删除插件记录（不删目录、不停进程：目录里的东西是玩家自己的，删行只是为了重新扫描）。
func Remove(ctx context.Context, slug string) error {
	conn := db.GetDB()
	if conn == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if running, _ := IsRunning(slug); running {
		return fmt.Errorf("插件 %s 正在运行，请先停止再删除", slug)
	}
	return conn.WithContext(ctx).Where("slug = ?", slug).Delete(&model.Plugin{}).Error
}

// tokenFor 取（必要时生成）插件令牌：同一把明文既作渠道凭据落库、也在启动时注入插件进程。
//
// 令牌是"octopus↔插件"之间的共享凭据：插件据此校验请求确实来自 octopus。
// 与渠道凭据同一套加密（internal/secret），AAD 用 "plugin:token" 做用途隔离。
func tokenFor(ctx context.Context, conn *gorm.DB, row model.Plugin) (string, error) {
	if strings.TrimSpace(row.TokenCipher) != "" {
		plain, err := secret.OpenWith(tokenAAD, row.TokenCipher)
		if err != nil {
			return "", fmt.Errorf("插件令牌解不开（换过 credential.key？）：%w", err)
		}
		return plain, nil
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	sealed, err := secret.SealWith(tokenAAD, token)
	if err != nil {
		return "", fmt.Errorf("插件令牌加密失败（缺少 credential.key？）：%w", err)
	}
	if err := conn.WithContext(ctx).Model(&model.Plugin{}).Where("id = ?", row.ID).
		Update("token_cipher", sealed).Error; err != nil {
		return "", fmt.Errorf("保存插件令牌失败：%w", err)
	}
	return token, nil
}

const tokenAAD = "plugin:token"
