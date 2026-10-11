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

	"soon":       "Coming soon.",
	"loading":    "Loading…",
	"locked":     "The store is busy (another skillet process holds the lock); try again in a moment.",
	"err.load":   "Could not load: %v",
	"no.project": "Not inside a project directory.",
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

	"soon":       "敬请期待。",
	"loading":    "加载中…",
	"locked":     "存储正忙（另一个 skillet 进程持有锁），请稍后再试。",
	"err.load":   "加载失败：%v",
	"no.project": "当前不在项目目录中。",
}
