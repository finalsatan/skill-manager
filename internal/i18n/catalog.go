package i18n

// Placeholders are fmt verbs; both languages must use the same ones in the
// same order.
var en = map[string]string{
	"tab.skills":  "Skills",
	"tab.updates": "Updates",
	"tab.repos":   "Repos",
	"tab.doctor":  "Doctor",

	"scope.project": "project",
	"scope.global":  "global",
	"scope.all":     "all",
	"scope.label":   "Scope: %s",

	"loading":      "Loading…",
	"locked":       "The store is busy (another skillet process holds the lock); try again in a moment.",
	"err.load":     "Could not load: %v",
	"help.tabs":    "tabs",
	"help.scope":   "scope",
	"help.refresh": "refresh",
	"help.help":    "help",
	"help.quit":    "quit",
	"repos.soon":   "Repository management is coming soon.",
	"updates.soon": "Update review is coming soon.",
	"doctor.soon":  "Health checks and fixes are coming soon.",
}

var zh = map[string]string{
	"tab.skills":  "技能",
	"tab.updates": "更新",
	"tab.repos":   "仓库",
	"tab.doctor":  "体检",

	"scope.project": "项目",
	"scope.global":  "全局",
	"scope.all":     "全部",
	"scope.label":   "范围：%s",

	"loading":      "加载中…",
	"locked":       "存储正忙（另一个 skillet 进程持有锁），请稍后再试。",
	"err.load":     "加载失败：%v",
	"help.tabs":    "切换标签",
	"help.scope":   "切换范围",
	"help.refresh": "刷新",
	"help.help":    "帮助",
	"help.quit":    "退出",
	"repos.soon":   "仓库管理即将推出。",
	"updates.soon": "更新审阅即将推出。",
	"doctor.soon":  "体检与修复即将推出。",
}
