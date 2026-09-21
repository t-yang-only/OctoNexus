package update

import (
	"strings"
	"testing"
)

// 更新源归属判据（T-identity-001）。
//
// 这是"项目身份"里唯一会被用户直接踩到的一处：更新源指错，产品里的"检查更新"会把**上游原版**二进制
// 装进本项目 —— 那不是升级，而是把本项目上百个提交的能力整套抹掉。所以判据写成双向的：
// 必须指向本项目仓库，且**不得**出现上游仓库坐标（否定式对照：只写"等于某串"会在改错人时也可能过）。
func TestUpdateSourcePointsAtThisProject(t *testing.T) {
	if updateOwner != "t-yang-only" || updateRepo != "OctoNexus" {
		t.Fatalf("更新源仓库坐标 = %s/%s, want t-yang-only/OctoNexus", updateOwner, updateRepo)
	}

	for _, url := range []string{updateUrl, updateApiUrl} {
		if !strings.Contains(url, updateOwner+"/"+updateRepo) {
			t.Fatalf("更新源 %q 未指向本项目仓库 %s/%s", url, updateOwner, updateRepo)
		}
		if strings.Contains(url, "bestruirui/octopus") {
			t.Fatalf("更新源 %q 仍指向上游仓库: 会把上游原版二进制装进本项目", url)
		}
		if !strings.HasPrefix(url, "https://") {
			t.Fatalf("更新源 %q 必须是 https（更新包要能被校验来源域名）", url)
		}
	}

	// 两个 URL 必须是同一个仓库（一个查版本、一个下包, 指到不同仓库会出现"查到有新版但下的是别人的包"）。
	if !strings.HasPrefix(updateUrl, "https://github.com/"+updateOwner+"/"+updateRepo+"/") {
		t.Fatalf("下载地址前缀异常: %q", updateUrl)
	}
	if !strings.HasPrefix(updateApiUrl, "https://api.github.com/repos/"+updateOwner+"/"+updateRepo+"/") {
		t.Fatalf("版本查询地址前缀异常: %q", updateApiUrl)
	}
}
