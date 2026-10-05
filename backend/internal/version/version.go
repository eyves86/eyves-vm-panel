package version

// Repo 是发行版 / 更新的默认来源仓库。
// 使用「平台前缀 + owner/name」形式，支持 github / codeberg / gitee / gitlab
// （见 cli.resolveRepoSource）。当前官方仓库托管在 GitHub。
var (
	Version = "2.2.3"
	Repo    = "github:eyves86/eyves-vm-panel"
)

func Current() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
